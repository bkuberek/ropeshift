package sqlstore

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
}

// Names a trace check needs.
const (
	StatementTakeRevision    = "workspace.take_revision"
	StatementCreateWorkspace = "workspace.create"
	StatementReadClock       = "clock.read"
	OpRenew                  = "lease.renew"
	OpWakeNextEval           = "wake.next_eval"
)
