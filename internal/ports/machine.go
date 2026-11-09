package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
)

// Machine reads what this machine has free, for the caps only a worker can see.
type Machine interface {
	// Reading says what is free now. An error is an incomplete read, never a full machine.
	Reading(ctx context.Context) (lanes.MachineReading, error)
}
