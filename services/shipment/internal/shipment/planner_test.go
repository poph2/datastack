package shipment

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/poph2/datastack/pkgs/chaos"
)

var defaultPlanConfig = PlanConfig{
	PackingDelayMin: 2 * time.Second,
	PackingDelayMax: 30 * time.Second,
	GoldDelayFactor: 0.5,
}

func newTestPlanner(t *testing.T, intN func(n int) int) *Planner {
	t.Helper()
	planner, err := NewPlanner(defaultPlanConfig)
	require.NoError(t, err)
	planner.intN = intN
	return planner
}

func TestNewPlannerValidatesConfig(t *testing.T) {
	cases := map[string]PlanConfig{
		"negative min":     {PackingDelayMin: -time.Second, PackingDelayMax: time.Second, GoldDelayFactor: 0.5},
		"max below min":    {PackingDelayMin: 5 * time.Second, PackingDelayMax: time.Second, GoldDelayFactor: 0.5},
		"zero factor":      {PackingDelayMin: time.Second, PackingDelayMax: 2 * time.Second, GoldDelayFactor: 0},
		"factor above one": {PackingDelayMin: time.Second, PackingDelayMax: 2 * time.Second, GoldDelayFactor: 1.5},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewPlanner(config)
			assert.Error(t, err)
		})
	}

	_, err := NewPlanner(PlanConfig{GoldDelayFactor: 1})
	assert.NoError(t, err, "zero delays and a factor of 1 are valid")
}

func TestPickCarrierFollowsDestinationWeights(t *testing.T) {
	// Each draw maps onto the cumulative ranges of the weight tables.
	cases := []struct {
		country string
		draw    int
		want    Carrier
	}{
		{"US", 0, CarrierUPS},
		{"US", 44, CarrierUPS},
		{"US", 45, CarrierFedEx},
		{"US", 84, CarrierFedEx},
		{"US", 85, CarrierDHL},
		{"US", 99, CarrierDHL},
		{"DE", 0, CarrierDHL},
		{"DE", 59, CarrierDHL},
		{"DE", 60, CarrierFedEx},
		{"DE", 85, CarrierUPS},
		{"JP", 99, CarrierUPS},
	}
	for _, tc := range cases {
		planner := newTestPlanner(t, func(n int) int {
			require.Equal(t, 100, n)
			return tc.draw
		})
		assert.Equal(t, tc.want, planner.pickCarrier(tc.country), "%s draw %d", tc.country, tc.draw)
	}
}

func TestTrackingNumberShapes(t *testing.T) {
	planner, err := NewPlanner(defaultPlanConfig)
	require.NoError(t, err)

	shapes := map[Carrier]*regexp.Regexp{
		CarrierUPS:   regexp.MustCompile(`^1Z[0-9A-Z]{16}$`),
		CarrierFedEx: regexp.MustCompile(`^[0-9]{12}$`),
		CarrierDHL:   regexp.MustCompile(`^[0-9]{10}$`),
	}
	for carrier, shape := range shapes {
		for range 50 {
			number := planner.trackingNumber(carrier)
			assert.Regexp(t, shape, number, carrier)
		}
	}
}

func TestPackingDelay(t *testing.T) {
	lowest := newTestPlanner(t, func(int) int { return 0 })
	highest := newTestPlanner(t, func(n int) int { return n - 1 })

	assert.Equal(t, 2000, lowest.packingDelayMS(""))
	assert.Equal(t, 30000, highest.packingDelayMS("BRONZE"))
	assert.Equal(t, 30000, highest.packingDelayMS("SILVER"))
	assert.Equal(t, 15000, highest.packingDelayMS("GOLD"), "GOLD is packed in half the time")
	assert.Equal(t, 1000, lowest.packingDelayMS("GOLD"))
}

func TestPackingDelayFollowsLiveChaosFactor(t *testing.T) {
	planner := newTestPlanner(t, func(int) int { return 0 })
	store, err := chaos.NewStore("shipment-service-test", discardLogger, planner.ChaosKnobs()...)
	require.NoError(t, err)

	require.NoError(t, store.Apply(context.Background(), map[string]float64{"packing_delay_factor": 2.5}))
	assert.Equal(t, 5000, planner.packingDelayMS(""))
	assert.Equal(t, 2500, planner.packingDelayMS("GOLD"), "the factor stacks with the GOLD discount")

	store.Reset(context.Background())
	assert.Equal(t, 2000, planner.packingDelayMS(""))

	err = store.Apply(context.Background(), map[string]float64{"packing_delay_factor": 0.5})
	assert.ErrorIs(t, err, chaos.ErrInvalidChange, "the factor never speeds packing up")
}

func TestPackingDelayStaysInRange(t *testing.T) {
	planner, err := NewPlanner(defaultPlanConfig)
	require.NoError(t, err)

	for range 1000 {
		delay := planner.packingDelayMS("")
		require.GreaterOrEqual(t, delay, 2000)
		require.LessOrEqual(t, delay, 30000)
	}
}

func TestPlan(t *testing.T) {
	planner := newTestPlanner(t, func(int) int { return 0 })

	plan := planner.Plan("FR", "GOLD")

	assert.Equal(t, Plan{Carrier: CarrierDHL, TrackingNumber: "0000000000", PackingDelayMS: 1000}, plan)
}
