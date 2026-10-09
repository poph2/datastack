package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stuckStore wraps fakeStore and reports every PAID order as stuck.
type stuckStore struct{ *fakeStore }

func (s stuckStore) StuckPaid(context.Context, time.Duration, int) ([]Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stuck []Order
	for _, o := range s.orders {
		if o.Status == StatusPaid {
			stuck = append(stuck, o)
		}
	}
	return stuck, nil
}

func TestNewReconcilerValidatesConfig(t *testing.T) {
	store := stuckStore{newFakeStore()}
	checkout := NewCheckout(store, newFakePeers().asPeers(), discardLogger)

	for name, config := range map[string]ReconcileConfig{
		"negative interval": {Interval: -time.Second, After: time.Minute},
		"zero after":        {Interval: time.Second, After: 0},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewReconciler(store, checkout, newFakePeers(), config, discardLogger)
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
	reconciler, err := NewReconciler(store, checkout, peers, ReconcileConfig{Interval: time.Second, After: time.Second}, discardLogger)
	require.NoError(t, err)

	reconciler.reconcileOnce(context.Background())

	got, err := store.Get(context.Background(), result.Order.ID)
	require.NoError(t, err)
	assert.Equal(t, StatusShipped, got.Status)
	assert.Equal(t, 2, peers.shipped, "the original attempt plus the retry")
	assert.Equal(t, 1, peers.charged, "the reconciler never charges again")
}

func TestReconcilerRunStopsOnCancel(t *testing.T) {
	store := stuckStore{newFakeStore()}
	peers := newFakePeers()
	reconciler, err := NewReconciler(store, NewCheckout(store, peers.asPeers(), discardLogger), peers,
		ReconcileConfig{Interval: 5 * time.Millisecond, After: time.Second}, discardLogger)
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
