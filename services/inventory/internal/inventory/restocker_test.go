package inventory

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/chaos"
)

type fakeRestockStore struct {
	calls     int
	threshold int
	quantity  int
	err       error
}

func (s *fakeRestockStore) RestockBelow(_ context.Context, threshold, quantity int) (int64, error) {
	s.calls++
	s.threshold, s.quantity = threshold, quantity
	return 3, s.err
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestNewRestockerValidatesConfig(t *testing.T) {
	cases := map[string]RestockConfig{
		"negative interval":        {Interval: -time.Second, Threshold: 10, Quantity: 50},
		"zero threshold":           {Interval: time.Minute, Threshold: 0, Quantity: 50},
		"quantity below threshold": {Interval: time.Minute, Threshold: 10, Quantity: 5},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewRestocker(&fakeRestockStore{}, config, discardLogger)
			assert.Error(t, err)
		})
	}

	_, err := NewRestocker(&fakeRestockStore{}, RestockConfig{Interval: 0, Threshold: 10, Quantity: 10}, discardLogger)
	assert.NoError(t, err, "interval 0 is valid and disables the loop")
}

func TestRestockOncePassesConfig(t *testing.T) {
	store := &fakeRestockStore{}
	restocker, err := NewRestocker(store, RestockConfig{Interval: time.Minute, Threshold: 10, Quantity: 50}, discardLogger)
	require.NoError(t, err)

	restocker.restockOnce(context.Background())

	assert.Equal(t, 1, store.calls)
	assert.Equal(t, 10, store.threshold)
	assert.Equal(t, 50, store.quantity)
}

func TestRestockOnceSkipsWhilePaused(t *testing.T) {
	store := &fakeRestockStore{}
	restocker, err := NewRestocker(store, RestockConfig{Interval: time.Minute, Threshold: 10, Quantity: 50}, discardLogger)
	require.NoError(t, err)
	knobs, err := chaos.NewStore("inventory-service-test", discardLogger, restocker.ChaosKnobs()...)
	require.NoError(t, err)
	ctx := context.Background()

	require.NoError(t, knobs.Apply(ctx, map[string]float64{"restock_paused": 1}))
	restocker.restockOnce(ctx)
	assert.Zero(t, store.calls, "a paused restocker doesn't touch stock")

	require.NoError(t, knobs.Apply(ctx, map[string]float64{"restock_paused": 0}))
	restocker.restockOnce(ctx)
	assert.Equal(t, 1, store.calls)

	assert.ErrorIs(t, knobs.Apply(ctx, map[string]float64{"restock_paused": 0.5}), chaos.ErrInvalidChange)
}

func TestRestockOnceSurvivesErrors(t *testing.T) {
	store := &fakeRestockStore{err: errors.New("database down")}
	restocker, err := NewRestocker(store, RestockConfig{Interval: time.Minute, Threshold: 10, Quantity: 50}, discardLogger)
	require.NoError(t, err)

	assert.NotPanics(t, func() { restocker.restockOnce(context.Background()) })
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	store := &fakeRestockStore{}
	restocker, err := NewRestocker(store, RestockConfig{Interval: 5 * time.Millisecond, Threshold: 10, Quantity: 50}, discardLogger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		restocker.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	assert.Positive(t, store.calls)
}
