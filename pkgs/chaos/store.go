package chaos

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// LatencyKnob is the knob every service has: a delay added before each API
// request is handled.
const LatencyKnob = "latency_ms"

// maxLatencyMS is long enough to trip any of the Order Service's peer
// timeouts, which is the most a demo needs.
const maxLatencyMS = 5000

// ErrInvalidChange means a requested change named an unknown knob or a value
// the knob doesn't accept. Nothing was applied.
var ErrInvalidChange = errors.New("invalid chaos change")

// State is a knob as the admin API reports it.
type State struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Value       float64 `json:"value"`
	Default     float64 `json:"default"`
	Min         float64 `json:"min"`
	Max         float64 `json:"max"`
}

// Store is one service's set of knobs. Changes are all-or-nothing and
// serialised, and each one is logged and exported as a metric.
type Store struct {
	service string
	logger  *slog.Logger
	latency *Knob
	// knobs is in report order: latency first, then the service's own.
	knobs  []*Knob
	byName map[string]*Knob
	// mu serialises changes, so a concurrent PUT and DELETE can't interleave
	// and every logged old value is the true previous one.
	mu sync.Mutex
}

// NewStore returns the knobs for service: the shared latency knob followed
// by the service's own knobs, in the order given.
func NewStore(service string, logger *slog.Logger, knobs ...*Knob) (*Store, error) {
	latency, err := NewKnob(Spec{
		Name:        LatencyKnob,
		Description: "Delay in milliseconds added before every API request is handled (admin endpoints excluded).",
		Default:     0,
		Min:         0,
		Max:         maxLatencyMS,
		Integer:     true,
	})
	if err != nil {
		return nil, err
	}

	s := &Store{
		service: service,
		logger:  logger,
		latency: latency,
		knobs:   append([]*Knob{latency}, knobs...),
		byName:  map[string]*Knob{},
	}
	for _, knob := range s.knobs {
		if _, duplicate := s.byName[knob.Name()]; duplicate {
			return nil, fmt.Errorf("chaos knob %s registered twice", knob.Name())
		}
		s.byName[knob.Name()] = knob
		knobValue.WithLabelValues(knob.Name()).Set(knob.Value())
	}
	return s, nil
}

// Latency returns the delay currently injected before each API request.
func (s *Store) Latency() time.Duration {
	return time.Duration(s.latency.Int()) * time.Millisecond
}

// States returns every knob's current state, latency first.
func (s *Store) States() []State {
	states := make([]State, 0, len(s.knobs))
	for _, knob := range s.knobs {
		states = append(states, State{
			Name:        knob.spec.Name,
			Description: knob.spec.Description,
			Value:       knob.Value(),
			Default:     knob.spec.Default,
			Min:         knob.spec.Min,
			Max:         knob.spec.Max,
		})
	}
	return states
}

// Apply sets the knobs named in changes. Every change is validated first: if
// any names an unknown knob or an out-of-range value, the returned error
// wraps ErrInvalidChange, lists every problem, and nothing is applied.
func (s *Store) Apply(ctx context.Context, changes map[string]float64) error {
	if err := s.validate(changes); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, knob := range s.knobs {
		if value, ok := changes[knob.Name()]; ok {
			s.set(ctx, knob, value)
		}
	}
	return nil
}

// Reset puts every knob back to its default.
func (s *Store) Reset(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, knob := range s.knobs {
		s.set(ctx, knob, knob.spec.Default)
	}
}

func (s *Store) validate(changes map[string]float64) error {
	names := make([]string, 0, len(changes))
	for name := range changes {
		names = append(names, name)
	}
	sort.Strings(names) // a stable message for the same request

	var problems []error
	for _, name := range names {
		knob, ok := s.byName[name]
		if !ok {
			problems = append(problems, fmt.Errorf("unknown knob %q", name))
			continue
		}
		if err := knob.validate(changes[name]); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %w", ErrInvalidChange, errors.Join(problems...))
	}
	return nil
}

// set stores one validated value. The caller holds mu.
func (s *Store) set(ctx context.Context, knob *Knob, value float64) {
	old := knob.Value()
	if old == value {
		return
	}
	knob.store(value)
	knobValue.WithLabelValues(knob.Name()).Set(value)
	s.logger.InfoContext(ctx, "chaos knob changed", "knob", knob.Name(), "old", old, "new", value)
}
