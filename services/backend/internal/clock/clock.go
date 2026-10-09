package clock
// Package clock provides the process-wide simulated clock used for every database timestamp.
package clock

import (
	"sync"
	"time"
)

// SimulatedDayPerSecond advances simulated time by one day for every real second.
const SimulatedDayPerSecond = 24 * 60 * 60

// Clock maps real elapsed time onto a simulated timeline. It never runs ahead of real time.
type Clock struct {
	mu       sync.RWMutex
	simBase  time.Time
	realBase time.Time
	speed    float64
}

var global = &Clock{}

// Now returns the current simulated time. Before the clock is ever started it is plain wall-clock time.
func Now() time.Time { return global.Now() }

// Start runs the global clock forward from the given simulated time.
func Start(from time.Time) { global.Start(from) }

// Freeze pauses the global clock at its current simulated time.
func Freeze() { global.Freeze() }

// CaughtUp reports whether the global clock has reached real time.
func CaughtUp() bool { return global.CaughtUp() }

// Now returns the current simulated time, capped at real time.
func (c *Clock) Now() time.Time {
	realNow := time.Now()
	sim, started := c.uncapped(realNow)
	if !started || sim.After(realNow) {
		return realNow
	}
	return sim
}

// Start runs the clock forward from the given simulated time at one day per second.
func (c *Clock) Start(from time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.simBase = from
	c.realBase = time.Now()
	c.speed = SimulatedDayPerSecond
}

// Freeze pauses the clock at its current simulated time.
func (c *Clock) Freeze() {
	now := c.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.simBase = now
	c.realBase = time.Now()
	c.speed = 0
}

// CaughtUp reports whether simulated time has reached real time.
func (c *Clock) CaughtUp() bool {
	realNow := time.Now()
	sim, started := c.uncapped(realNow)
	return started && !sim.Before(realNow)
}

func (c *Clock) uncapped(realNow time.Time) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.simBase.IsZero() {
		return time.Time{}, false
	}
	elapsed := realNow.Sub(c.realBase)
	return c.simBase.Add(time.Duration(float64(elapsed) * c.speed)), true
}
