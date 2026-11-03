package ports

import (
	"context"
	"io"
)

// Signal names a signal, so the port carries no system type.
type Signal string

// The signals a worker sends.
const (
	SignalInterrupt Signal = "SIGINT"
	SignalTerminate Signal = "SIGTERM"
	SignalKill      Signal = "SIGKILL"
)

// ProcessSpec is one spawn. Path is absolute and Env is the whole environment, so
// nothing is inherited by default and no shell chooses what runs (FR-DRV-10).
type ProcessSpec struct {
	Path string
	Args []string
	Dir  string
	Env  []string

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// Detached starts the process in a session of its own, so a signal to the group of
	// whatever started it does not reach it, and it has no terminal to lose.
	Detached bool
	// Outlive says the process must outlive a stop of the service that runs the worker, which
	// on Linux ends the service's whole control group (D20). A runner that knows a service
	// manager starts it outside that group, and every other runner treats it as Detached.
	Outlive bool
}

// ExitStatus says how a process ended. Signal is empty for a normal exit.
type ExitStatus struct {
	Code   int
	Signal Signal
}

// Process is one running process.
type Process interface {
	// PID identifies the process, for a signal sent only to one recorded ID.
	PID() int
	// Signal sends a signal to this process.
	Signal(Signal) error
	// Wait returns how the process ended. It can be called more than once.
	Wait() (ExitStatus, error)
}

// ProcessRunner spawns and signals processes with an explicit environment.
type ProcessRunner interface {
	// Start spawns a process. A process does not end with ctx, because a session
	// outlives whatever started it.
	Start(ctx context.Context, spec ProcessSpec) (Process, error)
}
