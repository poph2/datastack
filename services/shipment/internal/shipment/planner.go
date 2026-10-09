package shipment

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

const (
	// loyaltyTierGold is the only tier that changes the plan: GOLD orders are
	// packed faster, so the VIP SLA dashboard has a difference to show.
	loyaltyTierGold = "GOLD"
	// domesticCountry is where UPS and FedEx dominate. Everywhere else DHL does.
	domesticCountry = "US"

	upsTrackingPrefix = "1Z"
	upsTrackingLength = 16
	fedExTrackingLen  = 12
	dhlTrackingLen    = 10

	digits       = "0123456789"
	alphanumeric = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
)

// carrierWeight is one carrier's relative share of volume.
type carrierWeight struct {
	carrier Carrier
	weight  int
}

// Carrier mixes by destination, so the carrier-distribution dashboard differs
// between domestic and international volume.
var (
	domesticCarriers = []carrierWeight{
		{CarrierUPS, 45},
		{CarrierFedEx, 40},
		{CarrierDHL, 15},
	}
	internationalCarriers = []carrierWeight{
		{CarrierDHL, 60},
		{CarrierFedEx, 25},
		{CarrierUPS, 15},
	}
)

// PlanConfig controls how long packing takes.
type PlanConfig struct {
	// PackingDelayMin and PackingDelayMax bound the uniformly random delay.
	PackingDelayMin time.Duration
	PackingDelayMax time.Duration
	// GoldDelayFactor multiplies the delay for GOLD customers. In (0, 1].
	GoldDelayFactor float64
}

// Plan is what the planner decides for a new shipment.
type Plan struct {
	Carrier        Carrier
	TrackingNumber string
	PackingDelayMS int
}

// Planner picks the carrier, tracking number and packing delay for new
// shipments.
type Planner struct {
	config PlanConfig
	// intN returns a random int in [0, n). Tests replace it.
	intN func(n int) int
}

// NewPlanner validates config and returns a Planner.
func NewPlanner(config PlanConfig) (*Planner, error) {
	if config.PackingDelayMin < 0 {
		return nil, fmt.Errorf("packing delay min must not be negative, got %s", config.PackingDelayMin)
	}
	if config.PackingDelayMax < config.PackingDelayMin {
		return nil, fmt.Errorf("packing delay max (%s) must be at least the min (%s)",
			config.PackingDelayMax, config.PackingDelayMin)
	}
	if config.GoldDelayFactor <= 0 || config.GoldDelayFactor > 1 {
		return nil, fmt.Errorf("gold delay factor must be in (0, 1], got %g", config.GoldDelayFactor)
	}
	return &Planner{config: config, intN: rand.IntN}, nil
}

// Plan decides how an order travels to destinationCountry. loyaltyTier is
// optional and only shortens the packing delay for GOLD.
func (p *Planner) Plan(destinationCountry, loyaltyTier string) Plan {
	carrier := p.pickCarrier(destinationCountry)
	return Plan{
		Carrier:        carrier,
		TrackingNumber: p.trackingNumber(carrier),
		PackingDelayMS: p.packingDelayMS(loyaltyTier),
	}
}

func (p *Planner) pickCarrier(destinationCountry string) Carrier {
	weights := internationalCarriers
	if destinationCountry == domesticCountry {
		weights = domesticCarriers
	}

	total := 0
	for _, w := range weights {
		total += w.weight
	}

	n := p.intN(total)
	for _, w := range weights {
		if n < w.weight {
			return w.carrier
		}
		n -= w.weight
	}
	// Unreachable while intN honours its contract.
	return weights[len(weights)-1].carrier
}

// trackingNumber returns a number in the carrier's real-world shape.
func (p *Planner) trackingNumber(carrier Carrier) string {
	switch carrier {
	case CarrierUPS:
		return upsTrackingPrefix + p.randomString(alphanumeric, upsTrackingLength)
	case CarrierFedEx:
		return p.randomString(digits, fedExTrackingLen)
	default:
		return p.randomString(digits, dhlTrackingLen)
	}
}

func (p *Planner) randomString(alphabet string, length int) string {
	var b strings.Builder
	b.Grow(length)
	for range length {
		b.WriteByte(alphabet[p.intN(len(alphabet))])
	}
	return b.String()
}

// packingDelayMS draws a delay uniformly from [min, max], in milliseconds.
func (p *Planner) packingDelayMS(loyaltyTier string) int {
	minMS := int(p.config.PackingDelayMin.Milliseconds())
	maxMS := int(p.config.PackingDelayMax.Milliseconds())

	delay := minMS + p.intN(maxMS-minMS+1)
	if loyaltyTier == loyaltyTierGold {
		delay = int(float64(delay) * p.config.GoldDelayFactor)
	}
	return delay
}
