// Package fake holds an in-memory fake for every port (NFR-TEST-1). Each fake
// declares itself with a blank assertion, which TestEveryPortHasAFake reads.
package fake

import (
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/ports"
)

// Clock is a clock that moves only when a test moves it.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a clock stopped at start.
func NewClock(start time.Time) *Clock { return &Clock{now: start} }

// Now returns the stopped time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var _ ports.Clock = (*Clock)(nil)
