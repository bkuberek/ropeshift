package ledger

import "errors"

// WorkspaceID scopes every record (FR-LEDGER-2).
type WorkspaceID string

// Scope errors. A Store refuses a call that has no valid workspace, so a query
// cannot omit one.
var (
	ErrNoWorkspace      = errors.New("no workspace in scope")
	ErrUnknownWorkspace = errors.New("unknown workspace")
)

// Validate refuses an empty scope.
func (id WorkspaceID) Validate() error {
	if id == "" {
		return ErrNoWorkspace
	}
	return nil
}

// FleetMode is the one control for a whole fleet (FR-LANE-16).
type FleetMode string

// The fleet modes.
const (
	FleetRun   FleetMode = "run"
	FleetPause FleetMode = "pause"
	FleetDrain FleetMode = "drain"
)

// Workspace is one workspace record. Rev is the commit-ordered revision of its
// event log.
type Workspace struct {
	ID            WorkspaceID
	Name          string
	FleetMode     FleetMode
	Rev           int64
	PolicyVersion int64
}
