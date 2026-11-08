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
	// WIPLimit caps the work orders in progress. Zero means no limit.
	WIPLimit int64
	// RepositoryLimit and WorkerLimit cap the work orders in progress per repository and
	// per worker. Zero means no limit.
	RepositoryLimit int64
	WorkerLimit     int64
}

// Valid reports a mode this build knows.
func (m FleetMode) Valid() bool { return m == FleetRun || m == FleetPause || m == FleetDrain }

// Caps is what a claim is held to (FR-LANE-16, FR-WO-7). Zero means no limit.
type Caps struct {
	WIPLimit        int64 `json:"wip_limit"`
	RepositoryLimit int64 `json:"lanes_per_repository"`
	WorkerLimit     int64 `json:"lanes_per_worker"`
}

// Caps reads a workspace's caps.
func (w Workspace) Caps() Caps {
	return Caps{WIPLimit: w.WIPLimit, RepositoryLimit: w.RepositoryLimit, WorkerLimit: w.WorkerLimit}
}
