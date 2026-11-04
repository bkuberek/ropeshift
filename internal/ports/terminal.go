package ports

import "context"

// PaneSpec is one pane to open. Command runs directly, with no shell, and the terminal
// adds nothing a session can read but its own variables, so a command that needs an
// exact environment sets it itself.
type PaneSpec struct {
	Name    string
	Dir     string
	Command []string
	Width   int
	Height  int
}

// Pane is what a terminal knows about one pane. A dead pane stays until it is closed, so
// its end can be read after whoever watched it is gone.
type Pane struct {
	Name string
	// PID is the process the pane runs, for a signal sent only to one recorded ID.
	PID  int
	Dead bool
	// Exit says how a dead pane's process ended.
	Exit ExitStatus
}

// Terminal holds panes on a private server that outlives the worker. The screen is read
// only by the interactive start check, and everything after it goes through hooks and
// the messaging socket.
type Terminal interface {
	// Open starts a pane. A name already held is an error, so a retry cannot start a
	// second session.
	Open(ctx context.Context, spec PaneSpec) (Pane, error)
	// Pane reads one pane. ErrNotFound means the server answered and holds no such pane,
	// or that no server is left to hold one. Any other error is an incomplete read.
	Pane(ctx context.Context, name string) (Pane, error)
	// Capture returns the visible screen as text.
	Capture(ctx context.Context, name string) (string, error)
	// Keys sends named keys, such as Down or Enter.
	Keys(ctx context.Context, name string, keys ...string) error
	// Type sends literal text, with no key to submit it.
	Type(ctx context.Context, name, text string) error
	// Signal sends a signal to the process a pane runs now, and to nothing else.
	Signal(ctx context.Context, name string, signal Signal) error
	// Close removes a pane and ends its process. A pane that is not there is not an
	// error, because a stop is safe to repeat.
	Close(ctx context.Context, name string) error
}
