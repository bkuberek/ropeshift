package ports

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
)

// ErrNotShared means a write went to the wrong side of an installation: a project transaction tried to
// write a record every project shares, or a shared transaction a record of one project.
var ErrNotShared = errors.New("not a record of this side of the installation")

// Installation holds the records every project of an installation shares (ADR-0021): members and their
// sessions, workers with their join grants, autonomy, and run windows, orchestrators, and the autonomy
// policy. A change to them commits in a transaction of its own, with its events in the installation's
// log, so no project transaction writes them and no project's log holds their events. A project
// transaction only reads them.
type Installation interface {
	// Read runs fn on one consistent view of the shared records.
	Read(ctx context.Context, fn func(SharedReadTx) error) error
	// Write runs fn in one write transaction of the shared records. It commits only if fn returns nil
	// and appended at least one event, as a project's Write does. op names it in traces and crash steps.
	Write(ctx context.Context, op string, fn func(SharedTx) error) error
	// SaveReport keeps what a worker measured, in place of what it measured before, dated by database
	// time less the report's age, and moves the worker's capacity intervals by it (FR-OBS-14): freshMs is
	// how long after a report the next one keeps an interval standing. Like a lease renewal it takes no
	// revision and appends no event: one short transaction on one worker's rows, guarded by its epoch, so a
	// heartbeat never waits for the shared records' lock. A report of an epoch that is no longer current
	// changes nothing.
	SaveReport(ctx context.Context, worker string, epoch int64, report ledger.ResourceReport, freshMs int64) error
	// Beat records that a worker's session beat, dated by database time, which is how the control plane
	// tells a connected worker from one that stopped. Like a lease renewal it takes no revision and appends
	// no event: one short transaction on one worker's row, guarded by its epoch. A beat of an epoch that is
	// no longer current changes nothing.
	Beat(ctx context.Context, worker string, epoch int64) error
	// SharePool replaces what a project shares in the installation's delivery pool with these records, each
	// holding only what delivery.Pooled keeps, and the digest of them; no digest takes the project's share out
	// (ADR-0026, FR-EST-13). policyVersion is the estimate policy version the caller read: a share under an
	// earlier version than the last share or take-out changes nothing, so a tick that read the policy before a
	// member stopped sharing cannot put the records back. A project the registry does not hold as active shares
	// nothing. The pool is derived from each project's delivery records, so like a report it takes no revision
	// and appends no event. A store with no installation has no other project to share with, and keeps nothing.
	SharePool(ctx context.Context, project ledger.WorkspaceID, policyVersion int64, digest string, records []delivery.Record) error
}

// SharedReadTx is one consistent view of the shared records.
type SharedReadTx interface {
	// Now reads database time.
	Now() (int64, error)
	// Revision is how many changes the shared records took, which only grows. A worker's desired state
	// reads them, so it counts toward the revision a worker watches. A store that keeps them itself counts
	// them in its own workspace's revision, and answers zero.
	Revision() (int64, error)
	// Events returns up to limit events of the installation's log after a revision, in order.
	Events(after int64, limit int) ([]ledger.Event, error)
	// Workers reads registered workers, their join grants, autonomy, run windows, and orchestrators.
	Workers() WorkerReader
	// Members reads who can sign in and who has.
	Members() MemberReader
	// Policies reads the autonomy policy, the one kind every project shares.
	Policies() PolicyReader
	// Roster reads the installation's layer of the agent roster.
	Roster() RosterReader
}

// SharedTx is one write transaction of the shared records, which holds the installation's revision.
type SharedTx interface {
	// Now returns database time, read after the transaction's first statement.
	Now() int64
	// Event returns the event of the installation's log with a client-generated ID, or ErrNotFound, so
	// a retry finds its first attempt.
	Event(id string) (ledger.Event, error)
	// Append adds one event to the installation's log.
	Append(ledger.EventDraft) (ledger.Event, error)
	// Workers reads and writes registered workers, their join grants, autonomy, run windows, and
	// orchestrators.
	Workers() WorkerWriter
	// Members reads and writes who can sign in and who has.
	Members() MemberWriter
	// Policies reads and writes the autonomy policy, and refuses any other kind.
	Policies() PolicyWriter
	// Roster reads and writes the installation's layer of the agent roster, and refuses a project's.
	Roster() RosterWriter
}
