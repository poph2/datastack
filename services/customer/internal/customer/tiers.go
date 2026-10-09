package customer

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// TierPicker assigns a loyalty tier to new customers who don't specify one,
// following configured weights, so the demo segments have realistic sizes.
type TierPicker struct {
	tiers   []LoyaltyTier
	weights []int
	total   int
	// intN returns a random int in [0, n). Tests replace it.
	intN func(n int) int
}

// ParseTierPicker builds a picker from a spec such as
// "BRONZE=70,SILVER=22,GOLD=8". Weights are relative and need not sum to 100.
func ParseTierPicker(spec string) (*TierPicker, error) {
	picker := &TierPicker{intN: rand.IntN}

	for entry := range strings.SplitSeq(spec, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok {
			return nil, fmt.Errorf("tier weight %q: want TIER=WEIGHT", entry)
		}

		tier := LoyaltyTier(strings.ToUpper(strings.TrimSpace(name)))
		if !isKnownTier(tier) {
			return nil, fmt.Errorf("tier weight %q: unknown tier %q", entry, name)
		}

		weight, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || weight < 0 {
			return nil, fmt.Errorf("tier weight %q: weight must be a non-negative integer", entry)
		}

		picker.tiers = append(picker.tiers, tier)
		picker.weights = append(picker.weights, weight)
		picker.total += weight
	}

	if picker.total == 0 {
		return nil, fmt.Errorf("tier weights %q: at least one weight must be positive", spec)
	}
	return picker, nil
}

// Pick returns a random tier according to the weights.
func (p *TierPicker) Pick() LoyaltyTier {
	n := p.intN(p.total)
	for i, weight := range p.weights {
		if n < weight {
			return p.tiers[i]
		}
		n -= weight
	}
	// Unreachable while intN honours its contract.
	return p.tiers[len(p.tiers)-1]
}

func isKnownTier(tier LoyaltyTier) bool {
	switch tier {
	case TierBronze, TierSilver, TierGold:
		return true
	}
	return false
}
