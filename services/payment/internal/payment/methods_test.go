package payment

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMethodPickerRejectsInvalidSpecs(t *testing.T) {
	cases := map[string]string{
		"missing weight":   "CREDIT_CARD",
		"unknown method":   "BITCOIN=5",
		"negative weight":  "PAYPAL=-1",
		"non-numeric":      "PAYPAL=lots",
		"all weights zero": "CREDIT_CARD=0,PAYPAL=0",
		"empty":            "",
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMethodPicker(spec)
			assert.Error(t, err)
		})
	}
}

func TestMethodPickerFollowsWeights(t *testing.T) {
	picker, err := ParseMethodPicker(" credit_card=75, PAYPAL=25 ")
	require.NoError(t, err)

	// Each draw maps onto the cumulative ranges [0,75) [75,100).
	expected := map[int]Method{
		0:  MethodCreditCard,
		74: MethodCreditCard,
		75: MethodPayPal,
		99: MethodPayPal,
	}
	for draw, want := range expected {
		picker.intN = func(n int) int {
			require.Equal(t, 100, n)
			return draw
		}
		assert.Equal(t, want, picker.Pick(), "draw %d", draw)
	}
}

func TestMethodPickerSkipsZeroWeightMethods(t *testing.T) {
	picker, err := ParseMethodPicker("CREDIT_CARD=0,PAYPAL=1")
	require.NoError(t, err)

	picker.intN = func(int) int { return 0 }
	assert.Equal(t, MethodPayPal, picker.Pick())
}
