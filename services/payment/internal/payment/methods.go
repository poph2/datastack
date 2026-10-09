package payment

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
)

// MethodPicker assigns a payment method to charges that don't specify one,
// following configured weights, so the demo's method mix looks realistic.
type MethodPicker struct {
	methods []Method
	weights []int
	total   int
	// intN returns a random int in [0, n). Tests replace it.
	intN func(n int) int
}

// ParseMethodPicker builds a picker from a spec such as
// "CREDIT_CARD=75,PAYPAL=25". Weights are relative and need not sum to 100.
func ParseMethodPicker(spec string) (*MethodPicker, error) {
	picker := &MethodPicker{intN: rand.IntN}

	for entry := range strings.SplitSeq(spec, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(entry), "=")
		if !ok {
			return nil, fmt.Errorf("method weight %q: want METHOD=WEIGHT", entry)
		}

		method := Method(strings.ToUpper(strings.TrimSpace(name)))
		if !isKnownMethod(method) {
			return nil, fmt.Errorf("method weight %q: unknown method %q", entry, name)
		}

		weight, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || weight < 0 {
			return nil, fmt.Errorf("method weight %q: weight must be a non-negative integer", entry)
		}

		picker.methods = append(picker.methods, method)
		picker.weights = append(picker.weights, weight)
		picker.total += weight
	}

	if picker.total == 0 {
		return nil, fmt.Errorf("method weights %q: at least one weight must be positive", spec)
	}
	return picker, nil
}

// Pick returns a random method according to the weights.
func (p *MethodPicker) Pick() Method {
	n := p.intN(p.total)
	for i, weight := range p.weights {
		if n < weight {
			return p.methods[i]
		}
		n -= weight
	}
	// Unreachable while intN honours its contract.
	return p.methods[len(p.methods)-1]
}

func isKnownMethod(method Method) bool {
	switch method {
	case MethodCreditCard, MethodPayPal:
		return true
	}
	return false
}
