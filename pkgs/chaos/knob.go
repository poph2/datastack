// Package chaos holds the runtime failure-injection knobs that every service
// exposes on /admin/chaos for live demos: extra latency on every API request,
// plus knobs a service adds for its own behaviour (decline rate, shipping
// capacity, ...).
//
// Knob values live in memory in each pod. A restart resets every knob to its
// default, and with several replicas each pod has its own values.
package chaos

import (
	"fmt"
	"math"
	"sync/atomic"
)

// Spec defines a knob: what it means and which values it accepts.
type Spec struct {
	// Name is the JSON key, such as "decline_rate".
	Name string
	// Description tells an operator what the knob does, in one sentence.
	Description string
	// Default is the value at startup and after a reset.
	Default float64
	// Min and Max bound the accepted values, inclusive.
	Min, Max float64
	// Integer rejects values with a fractional part.
	Integer bool
}

// Knob is one live, adjustable value. Reads are lock-free, so hot paths such
// as the payment gateway can read it on every request. It changes only
// through a Store, which validates and logs every change.
type Knob struct {
	spec Spec
	// bits holds the float64 value (math.Float64bits), for atomic access.
	bits atomic.Uint64
}

// NewKnob validates spec and returns a knob holding its default.
func NewKnob(spec Spec) (*Knob, error) {
	if spec.Name == "" {
		return nil, fmt.Errorf("chaos knob needs a name")
	}
	if spec.Min > spec.Max {
		return nil, fmt.Errorf("chaos knob %s: min %g exceeds max %g", spec.Name, spec.Min, spec.Max)
	}
	if spec.Integer && (!isWhole(spec.Min) || !isWhole(spec.Max)) {
		return nil, fmt.Errorf("chaos knob %s: integer knob needs whole bounds", spec.Name)
	}
	knob := &Knob{spec: spec}
	if err := knob.validate(spec.Default); err != nil {
		return nil, fmt.Errorf("default: %w", err)
	}
	knob.store(spec.Default)
	return knob, nil
}

// Name returns the knob's name.
func (k *Knob) Name() string {
	return k.spec.Name
}

// Value returns the current value.
func (k *Knob) Value() float64 {
	return math.Float64frombits(k.bits.Load())
}

// Int returns the current value of an integer knob.
func (k *Knob) Int() int {
	return int(k.Value())
}

func (k *Knob) store(value float64) {
	k.bits.Store(math.Float64bits(value))
}

// validate reports why value is not acceptable for this knob, or nil.
func (k *Knob) validate(value float64) error {
	switch {
	case math.IsNaN(value) || value < k.spec.Min || value > k.spec.Max:
		return fmt.Errorf("%s must be between %g and %g, got %g", k.spec.Name, k.spec.Min, k.spec.Max, value)
	case k.spec.Integer && !isWhole(value):
		return fmt.Errorf("%s must be a whole number, got %g", k.spec.Name, value)
	}
	return nil
}

func isWhole(value float64) bool {
	return value == math.Trunc(value)
}
