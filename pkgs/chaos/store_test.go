package chaos

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func newRateKnob(t *testing.T) *Knob {
	t.Helper()
	knob, err := NewKnob(Spec{Name: "decline_rate", Description: "share declined", Default: 0.05, Min: 0, Max: 1})
	require.NoError(t, err)
	return knob
}

func newTestStore(t *testing.T, knobs ...*Knob) *Store {
	t.Helper()
	store, err := NewStore("test-service", discardLogger, knobs...)
	require.NoError(t, err)
	return store
}

func TestNewKnobValidatesSpec(t *testing.T) {
	cases := map[string]Spec{
		"no name":              {Min: 0, Max: 1},
		"min above max":        {Name: "k", Min: 2, Max: 1},
		"default below min":    {Name: "k", Default: -1, Min: 0, Max: 1},
		"default above max":    {Name: "k", Default: 2, Min: 0, Max: 1},
		"fractional integer":   {Name: "k", Default: 0.5, Min: 0, Max: 1, Integer: true},
		"fractional int bound": {Name: "k", Default: 0, Min: 0, Max: 1.5, Integer: true},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewKnob(spec)
			assert.Error(t, err)
		})
	}

	knob, err := NewKnob(Spec{Name: "k", Default: 3, Min: 0, Max: 10, Integer: true})
	require.NoError(t, err)
	assert.Equal(t, 3, knob.Int())
}

func TestNewStoreListsLatencyFirst(t *testing.T) {
	store := newTestStore(t, newRateKnob(t))

	states := store.States()
	require.Len(t, states, 2)
	assert.Equal(t, State{Name: LatencyKnob, Description: states[0].Description, Value: 0, Default: 0, Min: 0, Max: 5000}, states[0])
	assert.NotEmpty(t, states[0].Description)
	assert.Equal(t, "decline_rate", states[1].Name)
	assert.InDelta(t, 0.05, states[1].Value, 0)
}

func TestNewStoreRejectsDuplicateKnobs(t *testing.T) {
	latency, err := NewKnob(Spec{Name: LatencyKnob, Max: 1})
	require.NoError(t, err)

	_, err = NewStore("test-service", discardLogger, latency)
	assert.Error(t, err)
}

func TestApplySetsValuesAndMetric(t *testing.T) {
	rate := newRateKnob(t)
	store := newTestStore(t, rate)

	require.NoError(t, store.Apply(context.Background(), map[string]float64{LatencyKnob: 250, "decline_rate": 0.9}))

	assert.Equal(t, 250*time.Millisecond, store.Latency())
	assert.InDelta(t, 0.9, rate.Value(), 0)
	assert.InDelta(t, 0.9, gaugeValue(t, "decline_rate"), 0)
	assert.InDelta(t, 250, gaugeValue(t, LatencyKnob), 0)
}

func gaugeValue(t *testing.T, knob string) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, knobValue.WithLabelValues(knob).Write(&m))
	return m.GetGauge().GetValue()
}

func TestApplyIsAllOrNothing(t *testing.T) {
	cases := map[string]map[string]float64{
		"unknown knob":     {LatencyKnob: 100, "nope": 1},
		"above max":        {LatencyKnob: 100, "decline_rate": 1.5},
		"below min":        {LatencyKnob: -1, "decline_rate": 0.5},
		"fractional value": {LatencyKnob: 10.5, "decline_rate": 0.5},
	}
	for name, changes := range cases {
		t.Run(name, func(t *testing.T) {
			rate := newRateKnob(t)
			store := newTestStore(t, rate)

			err := store.Apply(context.Background(), changes)

			require.ErrorIs(t, err, ErrInvalidChange)
			assert.Zero(t, store.Latency(), "no change is applied")
			assert.InDelta(t, 0.05, rate.Value(), 0, "no change is applied")
		})
	}
}

func TestApplyReportsEveryProblem(t *testing.T) {
	store := newTestStore(t, newRateKnob(t))

	err := store.Apply(context.Background(), map[string]float64{"nope": 1, "decline_rate": 7})

	require.ErrorIs(t, err, ErrInvalidChange)
	assert.Contains(t, err.Error(), `unknown knob "nope"`)
	assert.Contains(t, err.Error(), "decline_rate must be between 0 and 1")
}

func TestResetRestoresDefaults(t *testing.T) {
	rate := newRateKnob(t)
	store := newTestStore(t, rate)
	require.NoError(t, store.Apply(context.Background(), map[string]float64{LatencyKnob: 100, "decline_rate": 1}))

	store.Reset(context.Background())

	assert.Zero(t, store.Latency())
	assert.InDelta(t, 0.05, rate.Value(), 0)
}

// Run with -race: writers and readers on the same knobs must not race.
func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	rate := newRateKnob(t)
	store := newTestStore(t, rate)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if i%5 == 0 {
				store.Reset(ctx)
				return
			}
			assert.NoError(t, store.Apply(ctx, map[string]float64{LatencyKnob: float64(i), "decline_rate": float64(i) / 20}))
		})
		wg.Go(func() {
			_ = store.States()
			_ = store.Latency()
			_ = rate.Value()
		})
	}
	wg.Wait()

	value := rate.Value()
	assert.True(t, value >= 0 && value <= 1, "the value is always one that was set, got %g", value)
}
