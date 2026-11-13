package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
)

// Machine reads what this machine has free, for the caps only a worker can see, and what it and each
// session's processes use, for what a worker reports (FR-OBS-8).
type Machine interface {
	// Reading says what is free now. An error is an incomplete read, never a full machine.
	Reading(ctx context.Context) (lanes.MachineReading, error)
	// Sample reads the machine as a whole, and the filesystem of each volume. A measure the system does
	// not provide stays nil, and a volume it cannot read is left out. An error is a read that failed
	// outright.
	Sample(ctx context.Context, volumes []lanes.Volume) (lanes.MachineSample, error)
	// ProcessTrees reads the process tree under each root: the root and every process descended from it. A
	// root that is gone is absent from the answer. An error says this machine cannot read processes.
	ProcessTrees(ctx context.Context, roots []int) (map[int]lanes.ProcessUse, error)
}
