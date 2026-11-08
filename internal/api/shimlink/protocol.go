package shimlink

import (
	"errors"
	"fmt"
)

// The protocol's versions. A shim outlives an upgrade of the worker that started it, so
// a worker must speak to one older than itself (D21).
const (
	// Version is what this build speaks.
	Version = 1
	// OldestVersion is the oldest a worker accepts.
	OldestVersion = 1
)

// The operations of the protocol. Each connection carries one request.
const (
	OpHello      = "hello"
	OpPing       = "ping"
	OpAttach     = "attach"
	OpInput      = "input"
	OpSignal     = "signal"
	OpCloseStdin = "close_stdin"
	OpShutdown   = "shutdown"
)

// The signals a client may ask for. A shim sends nothing else, and only to its child.
const (
	SignalInterrupt = "INT"
	SignalTerminate = "TERM"
	SignalKill      = "KILL"
)

// The refusals of the protocol.
const (
	CodeBadVersion  = "bad_version"
	CodeBadRequest  = "bad_request"
	CodeBadLine     = "bad_line"
	CodeStdinClosed = "stdin_closed"
	CodeBadSignal   = "bad_signal"
	CodeBusy        = "busy"
)

// Request is one line a client sends.
type Request struct {
	Version int    `json:"v"`
	Op      string `json:"op"`
	// Nonce travels through the event loop on a ping, so an answer shows the loop runs.
	Nonce string `json:"nonce,omitempty"`
	// Since is the last sequence number a client took.
	Since int64 `json:"since,omitempty"`
	// ID makes an input at-most-once.
	ID string `json:"id,omitempty"`
	// Line is one stream-json message for the child.
	Line string `json:"line,omitempty"`
	// Signal names a signal for the child.
	Signal string `json:"signal,omitempty"`
}

// Reply is the one line that answers a request. An attach streams records after it.
type Reply struct {
	OK      bool   `json:"ok"`
	Version int    `json:"v"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Nonce   string `json:"nonce,omitempty"`
	// Duplicate says this input ID arrived before, so nothing was written.
	Duplicate bool `json:"duplicate,omitempty"`
	// PID answers a hello, so a worker can tell the shim it started from one already
	// holding the socket.
	PID int `json:"pid,omitempty"`
}

// Exit says how the child ended.
type Exit struct {
	Code   int    `json:"code"`
	Signal string `json:"signal,omitempty"`
	// Error is what stopped the shim from reading how it ended.
	Error string `json:"error,omitempty"`
}

// Record is one line of the journal. Exactly one of the fields after Seq is set.
type Record struct {
	Seq int64 `json:"seq"`
	// Line is one line the child wrote, as it stands.
	Line string `json:"line,omitempty"`
	// Input is the ID of an input written to the child, in stream order.
	Input string `json:"input,omitempty"`
	// Exit is the child's end, which is always the last record.
	Exit *Exit `json:"exit,omitempty"`
	// Gap is the last sequence number rotation took from a reader. It appears only in
	// what a reader receives, and never on disk.
	Gap int64 `json:"gap,omitempty"`
}

// ErrUnreachable means nothing answered, so a caller learned nothing about the session.
var ErrUnreachable = errors.New("no shim answered")

// RefusedError is a refusal a shim explained.
type RefusedError struct {
	Code    string
	Message string
}

func (e *RefusedError) Error() string { return e.Code + ": " + e.Message }

// Negotiate returns the version two sides speak. A worker reaches back to an older shim,
// and an older worker speaks its own version to a newer one.
func Negotiate(ours, theirs int) (int, error) {
	speak := min(ours, theirs)
	if speak < OldestVersion {
		return 0, fmt.Errorf("shim protocol %d is older than the oldest this worker speaks, %d", theirs, OldestVersion)
	}
	return speak, nil
}
