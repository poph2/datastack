package customer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTierPickerRejectsInvalidSpecs(t *testing.T) {
	cases := map[string]string{
		"missing weight":   "BRONZE",
		"unknown tier":     "PLATINUM=5",
		"negative weight":  "BRONZE=-1",
		"non-numeric":      "BRONZE=lots",
		"all weights zero": "BRONZE=0,GOLD=0",
		"empty":            "",
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseTierPicker(spec)
			assert.Error(t, err)
		})
	}
}

func TestTierPickerFollowsWeights(t *testing.T) {
	picker, err := ParseTierPicker(" bronze=70, SILVER=22 ,GOLD=8")
	require.NoError(t, err)

	// Each draw maps onto the cumulative ranges [0,70) [70,92) [92,100).
	expected := map[int]LoyaltyTier{
		0:  TierBronze,
		69: TierBronze,
		70: TierSilver,
		91: TierSilver,
		92: TierGold,
		99: TierGold,
	}
	for draw, want := range expected {
		picker.intN = func(n int) int {
			require.Equal(t, 100, n)
			return draw
		}
		assert.Equal(t, want, picker.Pick(), "draw %d", draw)
	}
}

func TestTierPickerSkipsZeroWeightTiers(t *testing.T) {
	picker, err := ParseTierPicker("BRONZE=0,GOLD=1")
	require.NoError(t, err)

	picker.intN = func(int) int { return 0 }
	assert.Equal(t, TierGold, picker.Pick())
}
