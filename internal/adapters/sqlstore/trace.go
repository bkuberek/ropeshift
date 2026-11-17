package sqlstore

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Mode says what kind of transaction a statement ran in.
type Mode string

// The transaction modes.
const (
	ModeWrite   Mode = "write"
	ModeRead    Mode = "read"
	ModeMigrate Mode = "migrate"
)

// TraceEntry is one statement as it ran. A trace shows what no result can: the
// order of statements inside a transaction.
type TraceEntry struct {
	Tx        uint64
	Mode      Mode
	Op        string
	Statement string
}

// Options configures a Store.
type Options struct {
	// Trace receives every statement, in order, when it is set.
	Trace func(TraceEntry)
	// Installation holds the records every project shares (ADR-0021): members and their sessions,
	// workers with their join grants, autonomy, and run windows, orchestrators, and the autonomy policy.
	// A project's transaction reaches them there, under the installation's own scope. With none, the
	// store keeps them in its own database under each transaction's scope, as a single ledger does.
	Installation *Installation
	// Schema is the schema every transaction of the store runs in, on a server that holds several
	// projects and the installation as schemas of one database. Empty uses the connection's own.
	Schema string
	// Warn receives what a migration goes on past that a person may want to know, such as migrations the
	// database applied that this build does not ship. Nil says nothing.
	Warn func(string)
	// AfterFailedWrite is called when a write fails after the repository index committed its part and
	// before the project committed its own: the project's commit failed, or its outcome is unknown. The
	// index may then disagree with the project's records, and the function may bring it back, which a
	// store whose database does not rebuild the index while it runs needs. It is called with the write's
	// scope, on a context that the write's cancellation does not end, and the write returns its own error
	// after it. A failure of the write at any other point does not call it. Nil does nothing.
	AfterFailedWrite func(ctx context.Context, project ledger.WorkspaceID)
}

// Names a trace check needs.
const (
	StatementTakeRevision    = "workspace.take_revision"
	StatementCreateWorkspace = "workspace.create"
	StatementReadClock       = "clock.read"
	OpRenew                  = "lease.renew"
	OpWakeNextEval           = "wake.next_eval"
	OpSaveReport             = "worker.save_report"
	OpBeat                   = "worker.beat"
	OpFold                   = "delivery.fold"
	OpSharePool              = "delivery.share_pool"
	OpForecast               = "delivery.forecast"
)
