package shipment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/poph2/datastack/pkgs/chaos"
)

// maxShipCapacity bounds the ship_capacity chaos knob.
const maxShipCapacity = 1000

// PackStore is the persistence the packer needs. *Repository implements it.
type PackStore interface {
	ShipDue(ctx context.Context, limit int) ([]Shipment, error)
	CountPending(ctx context.Context) (int64, error)
}

// PackConfig controls the background packing loop.
type PackConfig struct {
	// Interval between runs. Zero disables the loop.
	Interval time.Duration
	// Capacity is the most shipments one run ships. A fixed capacity per tick
	// is what makes time-to-ship grow when a k6 burst outpaces the warehouse.
	// It is the default of the ship_capacity chaos knob, which can change it
	// live, down to 0 to pause shipping.
	Capacity int
}

// Packer periodically ships every shipment whose packing delay has passed,
// up to its capacity per run. All state lives in Postgres, so a restart
// loses nothing and any number of replicas can run it side by side.
type Packer struct {
	store    PackStore
	config   PackConfig
	capacity *chaos.Knob
	logger   *slog.Logger
}

// NewPacker validates config and returns a Packer.
func NewPacker(store PackStore, config PackConfig, logger *slog.Logger) (*Packer, error) {
	if config.Interval < 0 {
		return nil, fmt.Errorf("ship interval must not be negative, got %s", config.Interval)
	}
	if config.Capacity < 1 {
		return nil, fmt.Errorf("ship capacity must be at least 1, got %d", config.Capacity)
	}
	capacity, err := chaos.NewKnob(chaos.Spec{
		Name:        "ship_capacity",
		Description: "Most shipments shipped per packing run. 0 pauses shipping, so the backlog grows.",
		Default:     float64(config.Capacity),
		Min:         0,
		Max:         maxShipCapacity,
		Integer:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("ship capacity: %w", err)
	}
	return &Packer{store: store, config: config, capacity: capacity, logger: logger}, nil
}

// ChaosKnobs returns the packer's runtime knobs, for the chaos store.
func (p *Packer) ChaosKnobs() []*chaos.Knob {
	return []*chaos.Knob{p.capacity}
}

// Run packs on every tick until ctx is cancelled. A failed run is logged, and
// the loop carries on with the next tick.
func (p *Packer) Run(ctx context.Context) {
	if p.config.Interval == 0 {
		p.logger.Info("background packing disabled")
		return
	}

	p.logger.Info("background packing started",
		"interval", p.config.Interval.String(), "capacity", p.config.Capacity)

	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.packOnce(ctx)
		}
	}
}

// packOnce performs one run under its own root span. The HTTP request that
// created a shipment finished long ago, so there is no parent to attach to;
// each shipped box is a span event carrying its order.id instead.
func (p *Packer) packOnce(ctx context.Context) {
	ctx, span := otel.Tracer("shipment").Start(ctx, "shipment.pack")
	defer span.End()

	p.ship(ctx, span)
	p.measureBacklog(ctx, span)
}

func (p *Packer) ship(ctx context.Context, span trace.Span) {
	capacity := p.capacity.Int()
	span.SetAttributes(attribute.Int("shipment.capacity", capacity))
	if capacity == 0 {
		// Paused by the chaos knob. The backlog is still measured, so the
		// dashboards show it growing.
		return
	}

	shipped, err := p.store.ShipDue(ctx, capacity)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "shipping failed")
		p.logger.ErrorContext(ctx, "background packing failed", "error", err)
		return
	}

	span.SetAttributes(attribute.Int("shipment.shipped", len(shipped)))
	for _, s := range shipped {
		recordShipped(span, s)
	}
	if len(shipped) > 0 {
		p.logger.InfoContext(ctx, "shipments shipped", "count", len(shipped))
	}
}

func (p *Packer) measureBacklog(ctx context.Context, span trace.Span) {
	pending, err := p.store.CountPending(ctx)
	if err != nil {
		span.RecordError(err)
		p.logger.ErrorContext(ctx, "counting pending shipments failed", "error", err)
		return
	}

	shipmentsPending.Set(float64(pending))
	span.SetAttributes(attribute.Int64("shipment.pending", pending))
}

// recordShipped counts one shipped box. Both timestamps come from the
// database clock, so the difference is a true time-to-ship.
func recordShipped(span trace.Span, s Shipment) {
	shipmentsShippedTotal.WithLabelValues(string(s.Carrier)).Inc()
	if s.ShippedAt != nil {
		timeToShipSeconds.Observe(s.ShippedAt.Sub(s.CreatedAt).Seconds())
	}

	span.AddEvent("shipment.shipped", trace.WithAttributes(shipmentAttributes(s)...))
}

// shipmentAttributes describe a shipment on request spans and pack events.
func shipmentAttributes(s Shipment) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.Int64("order.id", int64(s.OrderID)),
		attribute.Int64("shipment.id", int64(s.ID)),
		attribute.String("shipment.carrier", string(s.Carrier)),
		attribute.Int("shipment.packing_delay_ms", s.PackingDelayMS),
	}
}
