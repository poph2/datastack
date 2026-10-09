package order

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// reconcileBatchSize bounds how many stuck orders one run retries.
const reconcileBatchSize = 50

// StuckStore finds orders stuck in PAID. *Repository implements it.
type StuckStore interface {
	StuckPaid(ctx context.Context, olderThan time.Duration, limit int) ([]Order, error)
}

// ReconcileConfig controls the background reconciler.
type ReconcileConfig struct {
	// Interval between runs. Zero disables the reconciler.
	Interval time.Duration
	// After is how long an order must have been PAID before it counts as stuck.
	// It must comfortably exceed a normal checkout, so in-flight checkouts are
	// never picked up.
	After time.Duration
}

// Reconciler retries the shipment for orders that were paid but whose
// shipment request failed. That is safe because the Shipment Service creates
// at most one shipment per order, and the PAID -> SHIPPED transition is
// guarded, so it is also safe with several replicas.
type Reconciler struct {
	stuck     StuckStore
	checkout  *Checkout
	customers Customers
	config    ReconcileConfig
	logger    *slog.Logger
}

// NewReconciler validates config and returns a Reconciler.
func NewReconciler(stuck StuckStore, checkout *Checkout, customers Customers, config ReconcileConfig, logger *slog.Logger) (*Reconciler, error) {
	if config.Interval < 0 {
		return nil, fmt.Errorf("reconcile interval must not be negative, got %s", config.Interval)
	}
	if config.After <= 0 {
		return nil, fmt.Errorf("reconcile-after must be positive, got %s", config.After)
	}
	return &Reconciler{stuck: stuck, checkout: checkout, customers: customers, config: config, logger: logger}, nil
}

// Run reconciles on every tick until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	if r.config.Interval == 0 {
		r.logger.Info("order reconciler disabled")
		return
	}

	ticker := time.NewTicker(r.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcileOnce(ctx)
		}
	}
}

// reconcileOnce performs one run under its own root span.
func (r *Reconciler) reconcileOnce(ctx context.Context) {
	ctx, span := otel.Tracer("order").Start(ctx, "order.reconcile")
	defer span.End()

	orders, err := r.stuck.StuckPaid(ctx, r.config.After, reconcileBatchSize)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "finding stuck orders failed")
		r.logger.ErrorContext(ctx, "order reconciler failed", "error", err)
		return
	}
	span.SetAttributes(attribute.Int("order.stuck", len(orders)))

	for _, order := range orders {
		r.reconcile(ctx, order)
	}
}

func (r *Reconciler) reconcile(ctx context.Context, order Order) {
	customer, err := r.customers.Get(ctx, order.UserID)
	if err != nil {
		reconciledTotal.WithLabelValues("failed").Inc()
		r.logger.WarnContext(ctx, "reconciler could not load customer", "order_id", order.ID, "error", err)
		return
	}

	result, err := r.checkout.ship(ctx, order, customer)
	switch {
	case errors.Is(err, ErrStatusChanged):
		// Another replica, or the original checkout, got there first.
		return
	case err != nil:
		reconciledTotal.WithLabelValues("failed").Inc()
		r.logger.ErrorContext(ctx, "reconciler could not update order", "order_id", order.ID, "error", err)
	case result.Order.Status == StatusShipped:
		reconciledTotal.WithLabelValues("shipped").Inc()
		r.logger.InfoContext(ctx, "reconciled stuck order", "order_id", order.ID)
	default:
		reconciledTotal.WithLabelValues("failed").Inc()
	}
}
