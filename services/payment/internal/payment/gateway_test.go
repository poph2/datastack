package payment

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGatewayValidatesConfig(t *testing.T) {
	cases := map[string]GatewayConfig{
		"negative failure rate": {FailureRate: -0.1},
		"failure rate above 1":  {FailureRate: 1.1},
		"negative latency min":  {LatencyMin: -time.Millisecond, LatencyMax: time.Millisecond},
		"min above max":         {LatencyMin: 200 * time.Millisecond, LatencyMax: 100 * time.Millisecond},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewGateway(config)
			assert.Error(t, err)
		})
	}

	for _, rate := range []float64{0, 1} {
		_, err := NewGateway(GatewayConfig{FailureRate: rate})
		assert.NoError(t, err, "failure rate %v is valid", rate)
	}
}

func TestGatewayDeclinesBelowFailureRate(t *testing.T) {
	gateway, err := NewGateway(GatewayConfig{FailureRate: 0.05})
	require.NoError(t, err)

	cases := map[float64]Status{
		0:      StatusDeclined,
		0.0499: StatusDeclined,
		0.05:   StatusSuccess,
		0.99:   StatusSuccess,
	}
	for draw, want := range cases {
		gateway.float64 = func() float64 { return draw }

		decision, err := gateway.Authorize(context.Background())

		require.NoError(t, err)
		assert.Equal(t, want, decision.Status, "draw %v", draw)
		assert.Equal(t, want == StatusDeclined, decision.DeclineReason != nil, "a reason is set only on declines")
	}
}

func TestGatewayPicksEachDeclineReason(t *testing.T) {
	gateway, err := NewGateway(GatewayConfig{FailureRate: 1})
	require.NoError(t, err)

	for i, want := range []DeclineReason{ReasonCardDeclined, ReasonInsufficientFunds, ReasonFraudSuspected} {
		gateway.int64N = func(n int64) int64 {
			require.EqualValues(t, 3, n)
			return int64(i)
		}

		decision, err := gateway.Authorize(context.Background())

		require.NoError(t, err)
		require.NotNil(t, decision.DeclineReason)
		assert.Equal(t, want, *decision.DeclineReason)
	}
}

func TestGatewayFailureRateExtremes(t *testing.T) {
	never, err := NewGateway(GatewayConfig{FailureRate: 0})
	require.NoError(t, err)
	always, err := NewGateway(GatewayConfig{FailureRate: 1})
	require.NoError(t, err)

	for range 100 {
		assert.Equal(t, StatusSuccess, never.decide().Status)
		assert.Equal(t, StatusDeclined, always.decide().Status)
	}
}

func TestGatewayLatencyStaysInRange(t *testing.T) {
	gateway, err := NewGateway(GatewayConfig{LatencyMin: 5 * time.Millisecond, LatencyMax: 10 * time.Millisecond})
	require.NoError(t, err)

	var spread int64
	gateway.int64N = func(n int64) int64 {
		spread = n
		return n - 1
	}

	start := time.Now()
	_, err = gateway.Authorize(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int64(5*time.Millisecond)+1, spread, "the draw covers [min, max] inclusive")
	assert.GreaterOrEqual(t, time.Since(start), 10*time.Millisecond)
}

func TestGatewayLatencyRespectsCancellation(t *testing.T) {
	gateway, err := NewGateway(GatewayConfig{LatencyMin: time.Minute, LatencyMax: time.Minute})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err = gateway.Authorize(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
}
