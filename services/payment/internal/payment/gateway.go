package payment

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/poph2/datastack/pkgs/chaos"
)

// declineReasons are drawn uniformly when the gateway declines a charge.
var declineReasons = []DeclineReason{ReasonCardDeclined, ReasonInsufficientFunds, ReasonFraudSuspected}

// GatewayConfig controls the simulated card gateway.
type GatewayConfig struct {
	// FailureRate is the share of charges declined, between 0 and 1. It is
	// the default of the decline_rate chaos knob, which can change it live.
	FailureRate float64
	// LatencyMin and LatencyMax bound the uniformly random delay before each
	// decision, so the payment step is visible in traces. Both zero disables it.
	LatencyMin time.Duration
	LatencyMax time.Duration
}

// Decision is the gateway's verdict on one charge.
type Decision struct {
	Status Status
	// DeclineReason is set only when Status is DECLINED.
	DeclineReason *DeclineReason
}

// Gateway stands in for a third-party processor such as Stripe: it waits a
// little, then approves or declines.
type Gateway struct {
	config GatewayConfig
	// declineRate is the live failure rate, read on every charge.
	declineRate *chaos.Knob
	// float64 returns a random number in [0, 1). Tests replace it.
	float64 func() float64
	// int64N returns a random number in [0, n). Tests replace it.
	int64N func(n int64) int64
}

// NewGateway validates config and returns a Gateway.
func NewGateway(config GatewayConfig) (*Gateway, error) {
	if config.LatencyMin < 0 {
		return nil, fmt.Errorf("latency min must not be negative, got %s", config.LatencyMin)
	}
	if config.LatencyMin > config.LatencyMax {
		return nil, fmt.Errorf("latency min (%s) must not exceed latency max (%s)", config.LatencyMin, config.LatencyMax)
	}
	declineRate, err := chaos.NewKnob(chaos.Spec{
		Name:        "decline_rate",
		Description: "Share of charges the simulated gateway declines, between 0 and 1.",
		Default:     config.FailureRate,
		Min:         0,
		Max:         1,
	})
	if err != nil {
		return nil, fmt.Errorf("failure rate: %w", err)
	}
	return &Gateway{config: config, declineRate: declineRate, float64: rand.Float64, int64N: rand.Int64N}, nil
}

// ChaosKnobs returns the gateway's runtime knobs, for the chaos store.
func (g *Gateway) ChaosKnobs() []*chaos.Knob {
	return []*chaos.Knob{g.declineRate}
}

// Authorize simulates the processor's round trip and returns its decision. It
// returns early with ctx's error if ctx is cancelled while waiting.
func (g *Gateway) Authorize(ctx context.Context) (Decision, error) {
	if err := g.simulateLatency(ctx); err != nil {
		return Decision{}, err
	}
	return g.decide(), nil
}

func (g *Gateway) simulateLatency(ctx context.Context) error {
	if g.config.LatencyMax == 0 {
		return nil
	}

	spread := int64(g.config.LatencyMax - g.config.LatencyMin)
	delay := g.config.LatencyMin + time.Duration(g.int64N(spread+1))

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for simulated gateway: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}

func (g *Gateway) decide() Decision {
	if g.float64() >= g.declineRate.Value() {
		return Decision{Status: StatusSuccess}
	}
	reason := declineReasons[g.int64N(int64(len(declineReasons)))]
	return Decision{Status: StatusDeclined, DeclineReason: &reason}
}
