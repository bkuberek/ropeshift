// Package clock adapts the operating system's clock to the Clock port.
package clock

import (
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// System reads the host clock.
type System struct{}

// Now returns the current time.
func (System) Now() time.Time { return time.Now() }

var _ ports.Clock = System{}
