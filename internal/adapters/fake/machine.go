package fake

import (
	"context"
	"sync"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Machine reports what a test tells it to.
type Machine struct {
	mu      sync.Mutex
	reading lanes.MachineReading
	err     error
}

// NewMachine returns a machine with this reading.
func NewMachine(reading lanes.MachineReading) *Machine { return &Machine{reading: reading} }

// Set replaces the reading.
func (m *Machine) Set(reading lanes.MachineReading) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reading = reading
}

// Fail makes every reading incomplete, until it is given nil.
func (m *Machine) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// Reading returns what a test set.
func (m *Machine) Reading(context.Context) (lanes.MachineReading, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reading, m.err
}

var _ ports.Machine = (*Machine)(nil)
