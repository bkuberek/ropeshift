package ports

import (
	"context"
	"errors"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// ErrNotFound means the record does not exist in this workspace.
var ErrNotFound = errors.New("not found")

// ErrNoEvent means a write transaction changed records and appended no event, so
// the change could not trace to its cause (FR-LEDGER-4). It rolled back.
var ErrNoEvent = errors.New("a change with no event")

// Store is the ledger's persistence port (ADR-0010). Every call takes a workspace
// scope, and everything inside a transaction is bound to it, so a query cannot
// omit one.
type Store interface {
	// Migrate brings the schema to this build's version. It is a no-op when the
	// schema is already there.
	Migrate(ctx context.Context) error
	// CreateWorkspace records a workspace and reports whether it was new. A
	// retry changes nothing and consumes no revision.
	CreateWorkspace(ctx context.Context, w ledger.Workspace, actor ledger.Actor) (bool, error)
	// Read runs fn on one consistent view.
	Read(ctx context.Context, scope ledger.WorkspaceID, fn func(ReadTx) error) error
	// Write runs fn in one write transaction. The first statement takes the
	// workspace revision, and database time is read after it, so every read in
	// fn decides the transaction. It commits only if fn returns nil and appended
	// at least one event: a transaction that changes nothing rolls back and
	// leaves no gap in the revisions. op names the transaction in traces and
	// crash steps.
	Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(WriteTx) error) error
	// Close releases the database.
	Close() error
}

// ReadTx is one consistent view of a workspace.
type ReadTx interface {
	// Workspace reads the workspace record.
	Workspace() (ledger.Workspace, error)
	// Events returns up to limit events after the revision, in order.
	Events(after int64, limit int) ([]ledger.Event, error)
	// WorkOrders reads the work order projection.
	WorkOrders() WorkOrderReader
}

// WriteTx is one write transaction, which holds the workspace revision.
type WriteTx interface {
	// Now returns database time in epoch milliseconds, as read inside this
	// transaction after its first statement. It is the only clock for leases
	// and timers.
	Now() int64
	// Workspace reads the workspace record.
	Workspace() (ledger.Workspace, error)
	// Events returns up to limit events after the revision, in order.
	Events(after int64, limit int) ([]ledger.Event, error)
	// Append adds one event, which takes its own revision.
	Append(ledger.EventDraft) (ledger.Event, error)
	// WorkOrders reads and writes the work order projection.
	WorkOrders() WorkOrderWriter
}

// WorkOrderReader reads the work order projection.
type WorkOrderReader interface {
	// Get returns one work order, or ErrNotFound.
	Get(id string) (workorders.WorkOrder, error)
	// List returns every work order in the workspace, ordered by ID.
	List() ([]workorders.WorkOrder, error)
}

// WorkOrderWriter writes the work order projection.
type WorkOrderWriter interface {
	WorkOrderReader
	// Insert adds a new work order.
	Insert(workorders.WorkOrder) error
	// ReplaceAll swaps the whole projection for rows rebuilt from events.
	ReplaceAll([]workorders.WorkOrder) error
}
