package inventory

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// RestockStore is the persistence the restocker needs. *Repository implements it.
type RestockStore interface {
	RestockBelow(ctx context.Context, threshold, quantity int) (int64, error)
}

// RestockConfig controls the background restock loop.
type RestockConfig struct {
	// Interval between runs. Zero disables the loop.
	Interval time.Duration
	// Threshold: products with fewer units than this are restocked.
	Threshold int
	// Quantity of units added to each restocked product.
	Quantity int
}

// Restocker periodically tops up products running low, so a long k6 run
// doesn't drain the whole catalog to zero.
type Restocker struct {
	store  RestockStore
	config RestockConfig
	logger *slog.Logger
}

// NewRestocker validates config and returns a Restocker.
func NewRestocker(store RestockStore, config RestockConfig, logger *slog.Logger) (*Restocker, error) {
	if config.Interval < 0 {
		return nil, fmt.Errorf("restock interval must not be negative, got %s", config.Interval)
	}
	if config.Threshold < 1 {
		return nil, fmt.Errorf("restock threshold must be at least 1, got %d", config.Threshold)
	}
	// One run then lifts every product it touches to at least the threshold,
	// so a concurrent run from another replica finds nothing left to restock.
	if config.Quantity < config.Threshold {
		return nil, fmt.Errorf("restock quantity (%d) must be at least the threshold (%d)", config.Quantity, config.Threshold)
	}
	return &Restocker{store: store, config: config, logger: logger}, nil
}

// Run restocks on every tick until ctx is cancelled. A failed run is logged,
// and the loop carries on with the next tick.
func (r *Restocker) Run(ctx context.Context) {
	if r.config.Interval == 0 {
		r.logger.Info("background restock disabled")
		return
	}

	r.logger.Info("background restock started",
		"interval", r.config.Interval.String(), "threshold", r.config.Threshold, "quantity", r.config.Quantity)

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.restockOnce(ctx)
		}
	}
}

// restockOnce performs one run under its own root span, so replenishment
// shows up in Tempo next to the checkout traces.
func (r *Restocker) restockOnce(ctx context.Context) {
	ctx, span := otel.Tracer("inventory").Start(ctx, "inventory.restock")
	defer span.End()

	restocked, err := r.store.RestockBelow(ctx, r.config.Threshold, r.config.Quantity)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "restock failed")
		r.logger.ErrorContext(ctx, "background restock failed", "error", err)
		return
	}

	span.SetAttributes(attribute.Int64("inventory.products_restocked", restocked))
	if restocked == 0 {
		return
	}

	units := restocked * int64(r.config.Quantity)
	unitsRestockedTotal.WithLabelValues(restockSourceAuto).Add(float64(units))
	r.logger.InfoContext(ctx, "background restock", "products", restocked, "units", units)
}
