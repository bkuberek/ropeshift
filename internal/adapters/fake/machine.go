package fake

import (
	"context"
	"errors"
	"sync"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// Machine reports what a test tells it to.
type Machine struct {
	mu      sync.Mutex
	reading lanes.MachineReading
	err     error
	sample  lanes.MachineSample
	trees   map[int]lanes.ProcessUse
	// treesErr says this machine cannot read processes.
	treesErr error
	// volumes is what each Sample was asked for, last first.
	volumes [][]lanes.Volume
}

// NewMachine returns a machine with this reading.
func NewMachine(reading lanes.MachineReading) *Machine { return &Machine{reading: reading} }

// Set replaces the reading.
func (m *Machine) Set(reading lanes.MachineReading) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reading = reading
}

// Fail makes every reading and every sample incomplete, until it is given nil.
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

// SetSample replaces what Sample returns. Its volumes are the ones a test can read: a volume Sample is
// asked for comes back only when one of these has its role and path.
func (m *Machine) SetSample(sample lanes.MachineSample) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sample = sample
}

// Sample returns what a test set, with only the volumes it was asked for.
func (m *Machine) Sample(_ context.Context, volumes []lanes.Volume) (lanes.MachineSample, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.volumes = append(m.volumes, volumes)
	if m.err != nil {
		return lanes.MachineSample{}, m.err
	}
	out := m.sample
	out.Volumes = nil
	for _, asked := range volumes {
		for _, known := range m.sample.Volumes {
			if known.Volume == asked {
				out.Volumes = append(out.Volumes, known)
			}
		}
	}
	return out, nil
}

// Asked returns the volumes the last Sample was asked for.
func (m *Machine) Asked() []lanes.Volume {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.volumes) == 0 {
		return nil
	}
	return m.volumes[len(m.volumes)-1]
}

// SetTree says what the process tree under one root has used. A root never set is gone.
func (m *Machine) SetTree(root int, use lanes.ProcessUse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.trees == nil {
		m.trees = map[int]lanes.ProcessUse{}
	}
	m.trees[root] = use
}

// NoProcesses makes this a machine that cannot read processes.
func (m *Machine) NoProcesses() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.treesErr = errors.New("this machine reads no processes")
}

// ProcessTrees returns the trees a test set, for the roots asked for.
func (m *Machine) ProcessTrees(_ context.Context, roots []int) (map[int]lanes.ProcessUse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.treesErr != nil {
		return nil, m.treesErr
	}
	out := map[int]lanes.ProcessUse{}
	for _, root := range roots {
		if use, ok := m.trees[root]; ok {
			out[root] = use
		}
	}
	return out, nil
}

var _ ports.Machine = (*Machine)(nil)
