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
	"go.opentelemetry.io/otel/trace"
)

// reconcileBatchSize bounds how many stuck orders of each kind one run
// handles.
const reconcileBatchSize = 50

// Reconciler outcomes, the "outcome" label of orders_reconciled_total.
const (
	// outcomeShipped: a PAID order's shipment went through.
	outcomeShipped = "shipped"
	// outcomePaid: a PENDING order had been charged, so it moved to PAID.
	outcomePaid = "paid"
	// outcomeReleased: a PENDING order's stock was released and it FAILED.
	outcomeReleased = "released"
	// outcomeCancelled: a PENDING order with nothing reserved FAILED.
	outcomeCancelled = "cancelled"
	// outcomeFailed: the attempt didn't succeed and the order is left for a
	// later run.
	outcomeFailed = "failed"
)

// errClaimLost means another replica, or a still-running checkout, changed
// a stranded order first, so this run leaves it alone.
var errClaimLost = errors.New("order changed since it was read")

// StuckStore finds and moves stuck orders. *Repository implements it.
type StuckStore interface {
	StuckPaid(ctx context.Context, olderThan time.Duration, limit int) ([]Order, error)
	StuckPending(ctx context.Context, olderThan time.Duration, limit int) ([]Order, error)
	Apply(ctx context.Context, id uint64, t Transition) (Order, error)
}

// ReconcileConfig controls the background reconciler.
type ReconcileConfig struct {
	// Interval between runs. Zero disables the reconciler.
	Interval time.Duration
	// After is how long an order must have been PAID before it counts as stuck.
	// It must comfortably exceed a normal checkout, so in-flight checkouts are
	// never picked up.
	After time.Duration
	// PendingAfter is how long an order must have been PENDING, untouched,
	// before it counts as stranded. It must exceed the longest possible
	// checkout (the sum of every peer timeout), so a live checkout is never
	// resolved under its feet.
	PendingAfter time.Duration
}

// Reconciler repairs orders a checkout left half-done:
//
//   - PAID orders whose shipment request failed: it retries the shipment.
//     That is safe because the Shipment Service creates at most one shipment
//     per order, and PAID -> SHIPPED is guarded.
//   - PENDING orders stranded by a crash mid-checkout: if the Payment Service
//     holds a successful charge it moves them to PAID, otherwise it releases
//     any reserved stock and fails them with UPSTREAM_ERROR.
//
// Every step is a guarded transition, so it is safe with several replicas.
type Reconciler struct {
	store    StuckStore
	checkout *Checkout
	peers    Peers
	config   ReconcileConfig
	logger   *slog.Logger
}

// NewReconciler validates config and returns a Reconciler. checkout ships
// PAID orders, and peers answer the PENDING questions: was it paid, and
// where does reserved stock go back to.
func NewReconciler(store StuckStore, checkout *Checkout, peers Peers, config ReconcileConfig, logger *slog.Logger) (*Reconciler, error) {
	if config.Interval < 0 {
		return nil, fmt.Errorf("reconcile interval must not be negative, got %s", config.Interval)
	}
	if config.After <= 0 {
		return nil, fmt.Errorf("reconcile-after must be positive, got %s", config.After)
	}
	if config.PendingAfter <= 0 {
		return nil, fmt.Errorf("pending-after must be positive, got %s", config.PendingAfter)
	}
	return &Reconciler{store: store, checkout: checkout, peers: peers, config: config, logger: logger}, nil
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

// reconcileOnce performs one run under its own root span. Stranded PENDING
// orders go first: one that turns out to be paid becomes PAID, and a later
// run ships it once it has been PAID for the After threshold.
func (r *Reconciler) reconcileOnce(ctx context.Context) {
	ctx, span := otel.Tracer("order").Start(ctx, "order.reconcile")
	defer span.End()

	pending, err := r.store.StuckPending(ctx, r.config.PendingAfter, reconcileBatchSize)
	if err != nil {
		r.recordRunError(ctx, span, "finding stranded PENDING orders failed", err)
	}
	span.SetAttributes(attribute.Int("order.stuck_pending", len(pending)))
	for _, order := range pending {
		r.resolvePending(ctx, span, order)
	}

	paid, err := r.store.StuckPaid(ctx, r.config.After, reconcileBatchSize)
	if err != nil {
		r.recordRunError(ctx, span, "finding stuck PAID orders failed", err)
	}
	span.SetAttributes(attribute.Int("order.stuck", len(paid)))
	for _, order := range paid {
		r.reconcilePaid(ctx, order)
	}
}

func (r *Reconciler) recordRunError(ctx context.Context, span trace.Span, message string, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, message)
	r.logger.ErrorContext(ctx, "order reconciler: "+message, "error", err)
}

func (r *Reconciler) reconcilePaid(ctx context.Context, order Order) {
	customer, err := r.peers.Customers.Get(ctx, order.UserID)
	if err != nil {
		reconciledTotal.WithLabelValues(outcomeFailed).Inc()
		r.logger.WarnContext(ctx, "reconciler could not load customer", "order_id", order.ID, "error", err)
		return
	}

	result, err := r.checkout.ship(ctx, order, customer)
	switch {
	case errors.Is(err, ErrStatusChanged):
		// Another replica, or the original checkout, got there first.
		return
	case err != nil:
		reconciledTotal.WithLabelValues(outcomeFailed).Inc()
		r.logger.ErrorContext(ctx, "reconciler could not update order", "order_id", order.ID, "error", err)
	case result.Order.Status == StatusShipped:
		reconciledTotal.WithLabelValues(outcomeShipped).Inc()
		r.logger.InfoContext(ctx, "reconciled stuck order", "order_id", order.ID)
	default:
		reconciledTotal.WithLabelValues(outcomeFailed).Inc()
	}
}

// resolvePending settles one stranded PENDING order and records the outcome
// on the run's span.
func (r *Reconciler) resolvePending(ctx context.Context, span trace.Span, order Order) {
	outcome, err := r.settlePending(ctx, order)
	if errors.Is(err, errClaimLost) {
		return
	}

	reconciledTotal.WithLabelValues(outcome).Inc()
	span.AddEvent("order.pending_resolved", trace.WithAttributes(
		attribute.Int64("order.id", int64(order.ID)),
		attribute.String("order.reconcile_outcome", outcome),
	))
	if err != nil {
		r.logger.WarnContext(ctx, "reconciler could not resolve PENDING order, retrying on a later run",
			"order_id", order.ID, "error", err)
		return
	}
	r.logger.InfoContext(ctx, "resolved stranded PENDING order", "order_id", order.ID, "outcome", outcome)
}

// settlePending decides a stranded order's fate and returns the outcome.
//
// It first claims the order: a guarded no-op transition that matches only if
// the order hasn't changed since it was read, and that moves updated_at. A
// second replica holding the same stale read then loses with
// errClaimLost, so stock is released at most once. If a later step
// fails, the order stays PENDING and becomes eligible again after
// PendingAfter.
func (r *Reconciler) settlePending(ctx context.Context, order Order) (string, error) {
	claimed, err := r.store.Apply(ctx, order.ID, Transition{
		From: StatusPending, To: StatusPending, UnchangedSince: &order.UpdatedAt,
	})
	if errors.Is(err, ErrStatusChanged) {
		return "", errClaimLost
	}
	if err != nil {
		return outcomeFailed, err
	}

	paid, err := r.peers.Payments.Paid(ctx, claimed.ID)
	if err != nil {
		return outcomeFailed, fmt.Errorf("looking up payments: %w", err)
	}
	if paid {
		return r.markPaid(ctx, claimed)
	}
	return r.failPending(ctx, claimed)
}

// markPaid moves an order the Payment Service has charged to PAID.
func (r *Reconciler) markPaid(ctx context.Context, order Order) (string, error) {
	paid, err := r.store.Apply(ctx, order.ID, Transition{From: StatusPending, To: StatusPaid})
	if err != nil {
		return outcomeFailed, err
	}
	if paid.TotalAmount != nil {
		revenueTotal.Add(paid.TotalAmount.InexactFloat64())
	}
	return outcomePaid, nil
}

// failPending releases the order's stock if it was reserved, then fails it.
// The checkout records unit_price right after a successful reservation, so a
// priced order is one holding stock.
func (r *Reconciler) failPending(ctx context.Context, order Order) (string, error) {
	outcome := outcomeCancelled
	if order.UnitPrice != nil {
		if err := r.peers.Inventory.Release(ctx, order.ProductID, order.Quantity); err != nil {
			return outcomeFailed, fmt.Errorf("releasing reserved stock: %w", err)
		}
		outcome = outcomeReleased
	}

	reason := ReasonUpstreamError
	_, err := r.store.Apply(ctx, order.ID, Transition{From: StatusPending, To: StatusFailed, FailureReason: &reason})
	if err != nil {
		return outcomeFailed, err
	}
	checkoutsTotal.WithLabelValues(string(StatusFailed), string(reason)).Inc()
	return outcome, nil
}
