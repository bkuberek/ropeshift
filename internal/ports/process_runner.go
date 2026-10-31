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
