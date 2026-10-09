package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stuckStore wraps fakeStore and reports every PAID or PENDING order as
// stuck, whatever its age.
type stuckStore struct{ *fakeStore }

func (s stuckStore) StuckPaid(context.Context, time.Duration, int) ([]Order, error) {
	return s.inStatus(StatusPaid), nil
}

func (s stuckStore) StuckPending(context.Context, time.Duration, int) ([]Order, error) {
	return s.inStatus(StatusPending), nil
}

func (s stuckStore) inStatus(status Status) []Order {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stuck []Order
	for _, o := range s.orders {
		if o.Status == status {
			stuck = append(stuck, o)
		}
	}
	return stuck
}

var testReconcileConfig = ReconcileConfig{Interval: time.Second, After: time.Second, PendingAfter: time.Minute}

func newTestReconciler(t *testing.T, store stuckStore, peers *fakePeers) *Reconciler {
	t.Helper()
	checkout := NewCheckout(store, peers.asPeers(), discardLogger)
	reconciler, err := NewReconciler(store, checkout, peers.asPeers(), testReconcileConfig, discardLogger)
	require.NoError(t, err)
	return reconciler
}

// strandedOrder creates an order stuck in PENDING, priced (stock reserved)
// or not, as a checkout that crashed halfway would leave it.
func strandedOrder(t *testing.T, store stuckStore, priced bool) Order {
	t.Helper()
	ctx := context.Background()
	o := Order{UserID: 1, ProductID: "PROD_0001", Quantity: 2, Status: StatusPending}
	require.NoError(t, store.Create(ctx, &o))
	if !priced {
		return o
	}

	price, total := decimal.RequireFromString("10.00"), decimal.RequireFromString("20.00")
	o, err := store.Apply(ctx, o.ID, Transition{From: StatusPending, To: StatusPending, UnitPrice: &price, TotalAmount: &total})
	require.NoError(t, err)
	return o
}

func reconciledCount(t *testing.T, outcome string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "orders_reconciled_total" {
			continue
		}
		for _, m := range family.GetMetric() {
			if m.GetLabel()[0].GetValue() == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func TestNewReconcilerValidatesConfig(t *testing.T) {
	store := stuckStore{newFakeStore()}
	peers := newFakePeers().asPeers()
	checkout := NewCheckout(store, peers, discardLogger)

	for name, config := range map[string]ReconcileConfig{
		"negative interval": {Interval: -time.Second, After: time.Minute, PendingAfter: time.Minute},
		"zero after":        {Interval: time.Second, After: 0, PendingAfter: time.Minute},
		"zero pending":      {Interval: time.Second, After: time.Minute, PendingAfter: 0},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewReconciler(store, checkout, peers, config, discardLogger)
			assert.Error(t, err)
		})
	}
}

func TestReconcilerShipsStuckOrders(t *testing.T) {
	peers := newFakePeers()
	peers.shipErr = errors.New("shipment down")
	store := stuckStore{newFakeStore()}
	checkout := NewCheckout(store, peers.asPeers(), discardLogger)

	result, err := checkout.Run(context.Background(), CheckoutRequest{UserID: 1, ProductID: "PROD_0001", Quantity: 1})
	require.NoError(t, err)
	require.Equal(t, StatusPaid, result.Order.Status)

	peers.shipErr = nil // the Shipment Service is back
	reconciler := newTestReconciler(t, store, peers)

	reconciler.reconcileOnce(context.Background())

	got, err := store.Get(context.Background(), result.Order.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusShipped, got.Status)
	assert.Equal(t, 2, peers.shipped, "the original attempt plus the retry")
	assert.Equal(t, 1, peers.charged, "the reconciler never charges again")
}

func TestReconcilerMovesChargedPendingOrderToPaid(t *testing.T) {
	peers := newFakePeers()
	peers.paid = true
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, true)
	reconciler := newTestReconciler(t, store, peers)

	outcome, err := reconciler.settlePending(context.Background(), stranded)

	require.NoError(t, err)
	assert.Equal(t, outcomePaid, outcome)
	got, err := store.Get(context.Background(), stranded.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPaid, got.Status)
	assert.Zero(t, peers.released, "a paid order keeps its stock")
	assert.Zero(t, peers.charged, "the reconciler never charges")
}

// A whole run resolves PENDING before PAID. The fake reports every PAID order
// as stuck regardless of age, so here the order is paid and shipped in one
// run; in Postgres the shipment waits until it has been PAID for After.
func TestReconcileOnceResolvesPendingThenShips(t *testing.T) {
	peers := newFakePeers()
	peers.paid = true
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, true)
	reconciler := newTestReconciler(t, store, peers)
	paidBefore := reconciledCount(t, outcomePaid)

	reconciler.reconcileOnce(context.Background())

	got, err := store.Get(context.Background(), stranded.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusShipped, got.Status)
	assert.InDelta(t, 1, reconciledCount(t, outcomePaid)-paidBefore, 0)
}

func TestReconcilerReleasesStockOfUnpaidPricedOrder(t *testing.T) {
	peers := newFakePeers()
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, true)
	reconciler := newTestReconciler(t, store, peers)
	before := reconciledCount(t, outcomeReleased)

	reconciler.reconcileOnce(context.Background())

	got, err := store.Get(context.Background(), stranded.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, got.Status)
	require.NotNil(t, got.FailureReason)
	assert.Equal(t, ReasonUpstreamError, *got.FailureReason)
	assert.Equal(t, 1, peers.released, "the reserved stock goes back")
	assert.InDelta(t, 1, reconciledCount(t, outcomeReleased)-before, 0)
}

func TestReconcilerFailsUnpricedOrderWithoutRelease(t *testing.T) {
	peers := newFakePeers()
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, false)
	reconciler := newTestReconciler(t, store, peers)

	outcome, err := reconciler.settlePending(context.Background(), stranded)

	require.NoError(t, err)
	assert.Equal(t, outcomeCancelled, outcome)
	got, err := store.Get(context.Background(), stranded.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, got.Status)
	assert.Zero(t, peers.released, "nothing was reserved, so nothing is released")
}

func TestReconcilerLeavesOrderPendingWhenReleaseFails(t *testing.T) {
	peers := newFakePeers()
	peers.releaseErr = errors.New("inventory down")
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, true)
	reconciler := newTestReconciler(t, store, peers)

	outcome, err := reconciler.settlePending(context.Background(), stranded)

	require.Error(t, err)
	assert.Equal(t, outcomeFailed, outcome)
	got, err := store.Get(context.Background(), stranded.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, got.Status, "left for a later run")

	peers.releaseErr = nil
	outcome, err = reconciler.settlePending(context.Background(), got)
	require.NoError(t, err)
	assert.Equal(t, outcomeReleased, outcome, "the next run finishes the job")
}

func TestReconcilerLeavesOrderPendingWhenPaymentLookupFails(t *testing.T) {
	peers := newFakePeers()
	peers.paidErr = errors.New("payment down")
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, true)
	reconciler := newTestReconciler(t, store, peers)

	reconciler.reconcileOnce(context.Background())

	got, err := store.Get(context.Background(), stranded.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, got.Status, "an unknown payment state is never guessed")
	assert.Zero(t, peers.released)
}

// Two replicas that read the same stranded order: only the first claim wins,
// so the stock is released once.
func TestReconcilerClaimsPendingOrderOnce(t *testing.T) {
	peers := newFakePeers()
	store := stuckStore{newFakeStore()}
	stranded := strandedOrder(t, store, true)
	first := newTestReconciler(t, store, peers)
	second := newTestReconciler(t, store, peers)

	_, err := first.settlePending(context.Background(), stranded)
	require.NoError(t, err)
	_, err = second.settlePending(context.Background(), stranded) // a stale read
	require.ErrorIs(t, err, errClaimLost)

	assert.Equal(t, 1, peers.released)
}

// An order a live checkout touches after the reconciler read it is left alone.
func TestReconcilerSkipsOrderChangedSinceRead(t *testing.T) {
	peers := newFakePeers()
	store := stuckStore{newFakeStore()}
	read := strandedOrder(t, store, false)
	price, total := decimal.RequireFromString("10.00"), decimal.RequireFromString("20.00")
	_, err := store.Apply(context.Background(), read.ID, Transition{From: StatusPending, To: StatusPending, UnitPrice: &price, TotalAmount: &total})
	require.NoError(t, err)
	reconciler := newTestReconciler(t, store, peers)

	_, err = reconciler.settlePending(context.Background(), read)

	require.ErrorIs(t, err, errClaimLost)
	got, err := store.Get(context.Background(), read.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, got.Status)
}

func TestReconcilerRunStopsOnCancel(t *testing.T) {
	store := stuckStore{newFakeStore()}
	peers := newFakePeers()
	reconciler, err := NewReconciler(store, NewCheckout(store, peers.asPeers(), discardLogger), peers.asPeers(),
		ReconcileConfig{Interval: 5 * time.Millisecond, After: time.Second, PendingAfter: time.Minute}, discardLogger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		reconciler.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
