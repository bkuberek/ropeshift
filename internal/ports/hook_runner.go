package ports

import (
	"context"
	"time"
)

// HookCommand is one command a profile names. Argv's first word is a path relative to Dir,
// an absolute path, or a name the environment's PATH finds. Env is the whole environment.
type HookCommand struct {
	Argv    []string
	Dir     string
	Env     []string
	Timeout time.Duration
}

// HookResult says how a command ended, with the end of what it printed.
type HookResult struct {
	Exit   ExitStatus
	Output []byte
}

// HookRunner runs the commands profiles name, on a lane's behalf. No shell reads them. A
// command that passes its timeout ends with a signal, and that is a result, not an error.
type HookRunner interface {
	Run(ctx context.Context, command HookCommand) (HookResult, error)
}
