package shipment

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakePackStore struct {
	shipCalls  int
	limit      int
	shipped    []Shipment
	pending    int64
	shipErr    error
	pendingErr error
}

func (s *fakePackStore) ShipDue(_ context.Context, limit int) ([]Shipment, error) {
	s.shipCalls++
	s.limit = limit
	return s.shipped, s.shipErr
}

func (s *fakePackStore) CountPending(context.Context) (int64, error) {
	return s.pending, s.pendingErr
}

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestNewPackerValidatesConfig(t *testing.T) {
	cases := map[string]PackConfig{
		"negative interval": {Interval: -time.Second, Capacity: 10},
		"zero capacity":     {Interval: time.Second, Capacity: 0},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewPacker(&fakePackStore{}, config, discardLogger)
			assert.Error(t, err)
		})
	}

	_, err := NewPacker(&fakePackStore{}, PackConfig{Interval: 0, Capacity: 1}, discardLogger)
	assert.NoError(t, err, "interval 0 is valid and disables the loop")
}

func TestPackOnceShipsAndMeasuresBacklog(t *testing.T) {
	created := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	shippedAt := created.Add(4 * time.Second)
	shipped := Shipment{Carrier: CarrierDHL, Status: StatusShipped, ShippedAt: &shippedAt}
	shipped.CreatedAt = created

	store := &fakePackStore{shipped: []Shipment{shipped}, pending: 42}
	packer, err := NewPacker(store, PackConfig{Interval: time.Second, Capacity: 7}, discardLogger)
	require.NoError(t, err)

	shippedBefore := metricValue(t, "shipments_shipped_total", "DHL")
	packer.packOnce(context.Background())

	assert.Equal(t, 7, store.limit, "the capacity caps every run")
	assert.InDelta(t, 1, metricValue(t, "shipments_shipped_total", "DHL")-shippedBefore, 0)
	assert.InDelta(t, 42, metricValue(t, "shipments_pending", ""), 0)
}

// metricValue reads a counter or gauge from the default registry. carrier
// selects the labelled series, or "" for an unlabelled metric. A series that
// doesn't exist yet reads as 0.
func metricValue(t *testing.T, name, carrier string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			if carrier != "" && (len(m.GetLabel()) != 1 || m.GetLabel()[0].GetValue() != carrier) {
				continue
			}
			return m.GetCounter().GetValue() + m.GetGauge().GetValue()
		}
	}
	return 0
}

func TestPackOnceSurvivesErrors(t *testing.T) {
	store := &fakePackStore{shipErr: errors.New("database down"), pendingErr: errors.New("database down")}
	packer, err := NewPacker(store, PackConfig{Interval: time.Second, Capacity: 10}, discardLogger)
	require.NoError(t, err)

	assert.NotPanics(t, func() { packer.packOnce(context.Background()) })
}

func TestRunStopsWhenContextIsCancelled(t *testing.T) {
	store := &fakePackStore{}
	packer, err := NewPacker(store, PackConfig{Interval: 5 * time.Millisecond, Capacity: 10}, discardLogger)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		packer.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	assert.Positive(t, store.shipCalls)
}

func TestRunReturnsImmediatelyWhenDisabled(t *testing.T) {
	store := &fakePackStore{}
	packer, err := NewPacker(store, PackConfig{Interval: 0, Capacity: 10}, discardLogger)
	require.NoError(t, err)

	packer.Run(context.Background())

	assert.Zero(t, store.shipCalls)
}
