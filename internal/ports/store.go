package ports

import (
	"context"
	"errors"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/planning"
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
	// Renew extends a lease. It is the one write that takes no revision and
	// appends no event: one guarded statement on one row, because every heartbeat
	// would otherwise wait for the workspace lock. A rejection reads the row in
	// the same transaction and returns ErrExpired, ErrLapsed, or a FencedError.
	Renew(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64) error
	// Close releases the database.
	Close() error
}

// ReadTx is one consistent view of a workspace.
type ReadTx interface {
	// Workspace reads the workspace record.
	Workspace() (ledger.Workspace, error)
	// Events returns up to limit events after the revision, in order.
	Events(after int64, limit int) ([]ledger.Event, error)
	// Now reads database time.
	Now() (int64, error)
	// WorkOrders reads the work order projection.
	WorkOrders() WorkOrderReader
	// Claims reads claims.
	Claims() ClaimReader
	// Lanes reads lanes.
	Lanes() LaneReader
	// Planning reads plans, contracts, links, dependencies, and bindings.
	Planning() PlanningReader
	// Workers reads registered workers and join grants.
	Workers() WorkerReader
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
	// Event returns the event with a client-generated ID, or ErrNotFound. It is how
	// a retry finds its first attempt.
	Event(id string) (ledger.Event, error)
	// Append adds one event, which takes its own revision.
	Append(ledger.EventDraft) (ledger.Event, error)
	// SaveFleet replaces a workspace's fleet mode and caps.
	SaveFleet(mode ledger.FleetMode, caps ledger.Caps) error
	// WorkOrders reads and writes the work order projection.
	WorkOrders() WorkOrderWriter
	// Leases runs the guarded lease statements.
	Leases() LeaseWriter
	// Claims reads and writes claims.
	Claims() ClaimWriter
	// Lanes reads and writes lanes.
	Lanes() LaneWriter
	// Planning reads and writes plans, contracts, links, dependencies, and
	// bindings.
	Planning() PlanningWriter
	// Workers reads and writes registered workers and join grants.
	Workers() WorkerWriter
}

// WorkOrderReader reads the work order projection.
type WorkOrderReader interface {
	// Get returns one work order, or ErrNotFound.
	Get(id string) (workorders.WorkOrder, error)
	// List returns every work order in the workspace, ordered by ID.
	List() ([]workorders.WorkOrder, error)
	// HeldBy returns the work orders a worker holds a lease on.
	HeldBy(worker string) ([]workorders.WorkOrder, error)
	// Expired lists the leases past their deadline at a time. A tick reads this
	// with no lock, so each item must be checked again by a guarded statement.
	Expired(now int64) ([]string, error)
}

// WorkOrderWriter writes the work order projection.
type WorkOrderWriter interface {
	WorkOrderReader
	// Insert adds a new work order.
	Insert(workorders.WorkOrder) error
	// ReplaceAll swaps the whole projection for rows rebuilt from events.
	ReplaceAll([]workorders.WorkOrder) error
	// Move is a compare-and-set on the state of an unleased work order. It
	// reports whether the row matched.
	Move(id string, from, to workorders.State) (bool, error)
	// Reviewed records who passed a work order's review.
	Reviewed(id, member string) error
}

// LeaseWriter runs the statements that decide who holds a work order. Each is one
// guarded statement and reports whether it matched a row. A row count of 0 is an
// answer, not an error, and the caller reads the row to learn which answer.
type LeaseWriter interface {
	// WorkInProgress counts the work orders a holder occupies.
	WorkInProgress() (int64, error)
	// InRepository counts the work orders a holder occupies in one repository.
	InRepository(repository string) (int64, error)
	// HeldByWorker counts the work orders one worker occupies.
	HeldByWorker(worker string) (int64, error)
	// Grant is the claim statement.
	Grant(request workorders.ClaimRequest) (bool, error)
	// Start moves a claimed work order to running under its lease.
	Start(lease workorders.Lease) (bool, error)
	// Guard is the fenced write statement.
	Guard(workOrder string, fence int64) (bool, error)
	// Lapse is the expiry statement, which repeats the expiry predicate.
	Lapse(workOrder string) (bool, error)
	// Return restores a lapsed lease to its holder.
	Return(lease workorders.Lease, restored workorders.State, ttlMs int64) (bool, error)
	// Revoke ends a lapsed holder's ownership and moves the fence.
	Revoke(workOrder string, fence int64) (bool, error)
	// Release gives back a claim before its lane starts, and moves the fence.
	Release(lease workorders.Lease) (bool, error)
	// End closes a running lease by a lane's outcome, and moves the fence.
	End(lease workorders.Lease, to workorders.State, attempts int64) (bool, error)
}

// ClaimReader reads claims.
type ClaimReader interface {
	// Find returns one claim, or ErrNotFound.
	Find(id string) (workorders.Claim, error)
	// List returns every claim, by work order and then by fence. The fence orders
	// grants, because two grants can share a millisecond.
	List() ([]workorders.Claim, error)
}

// ClaimWriter writes claims.
type ClaimWriter interface {
	ClaimReader
	// Record inserts a new claim.
	Record(workorders.Claim) error
	// End closes an open claim.
	End(id, reason string) error
	// Dispatch names the lane a claim should run.
	Dispatch(id, lane string) error
}

// LaneReader reads lanes.
type LaneReader interface {
	// Generation returns a lane's current generation, or ErrNotFound.
	Generation(lane string) (int64, error)
	// StartedUnder reports whether a lane started under this fence.
	StartedUnder(workOrder string, fence int64) (bool, error)
	// Row returns one lane's status, or ErrNotFound.
	Row(lane string) (ledger.LaneRow, error)
	// Rows returns every lane's status, by ID.
	Rows() ([]ledger.LaneRow, error)
	// Resets returns how many times a lane's breaker was reset, or ErrNotFound.
	Resets(lane string) (int64, error)
	// Artifacts lists what a lane left, oldest first.
	Artifacts(lane string) ([]ledger.Artifact, error)
	// Archiving lists the lanes a worker should take away.
	Archiving(worker string) ([]ledger.DesiredArchive, error)
}

// LaneWriter writes lanes.
type LaneWriter interface {
	LaneReader
	// Start records a lane's first generation under a fence.
	Start(lane, workOrder string, fence int64) error
	// NextGeneration moves a lane to its next generation and returns it.
	NextGeneration(lane string) (int64, error)
	// Report stores what a worker says about a lane. An ended lane stays ended.
	Report(row ledger.LaneRow) error
	// MarkEnded records a lane's end, which the ledger knows for itself.
	MarkEnded(lane string) error
	// Reset counts one human reset of a lane's breaker and returns the count.
	Reset(lane string) (int64, error)
	// Guarded keeps how a stop left a worktree. Work no remote holds withdraws a request
	// to remove it, unless a member forced that request.
	Guarded(lane string, unpushed bool) error
	// AddArtifact keeps what a lane left, once per ID.
	AddArtifact(artifact ledger.Artifact) error
	// RequestArchive asks a worker to take a lane's worktree away.
	RequestArchive(lane string, forced bool) error
	// Archived records that a lane's worktree went.
	Archived(lane string) error
}

// PlanningReader reads what intake recorded.
type PlanningReader interface {
	// Contract returns one contract version, or ErrNotFound.
	Contract(id string, version int64) (planning.Contract, error)
	// LatestContract returns a contract's newest version, or ErrNotFound.
	LatestContract(id string) (planning.Contract, error)
	// Contracts returns every version, by contract and then by version.
	Contracts() ([]planning.Contract, error)
	// Plans returns every plan.
	Plans() ([]planning.Plan, error)
	// Link returns the record of one external key, or ErrNotFound.
	Link(recordKind string, key ledger.ExternalKey) (ledger.ExternalLink, error)
	// Links returns every link.
	Links() ([]ledger.ExternalLink, error)
	// Dependencies returns what one work order depends on.
	Dependencies(workOrder string) ([]planning.Dependency, error)
	// AllDependencies returns every dependency.
	AllDependencies() ([]planning.Dependency, error)
	// Binding returns one source binding, or ErrNotFound.
	Binding(id string) (planning.SourceBinding, error)
	// Bindings returns every source binding.
	Bindings() ([]planning.SourceBinding, error)
}

// PlanningWriter records what intake read.
type PlanningWriter interface {
	PlanningReader
	// InsertPlan adds a plan.
	InsertPlan(planning.Plan) error
	// InsertContract adds one contract version.
	InsertContract(planning.Contract) error
	// InsertLink adds one external key.
	InsertLink(ledger.ExternalLink) error
	// InsertDependency adds one edge.
	InsertDependency(planning.Dependency) error
	// SaveBinding creates or replaces a binding.
	SaveBinding(planning.SourceBinding) error
}

// WorkerReader reads the worker plane's records.
type WorkerReader interface {
	// Find returns one worker and its proof digest, or ErrNotFound.
	Find(id string) (ledger.WorkerRecord, error)
	// List returns every worker.
	List() ([]ledger.Worker, error)
	// JoinGrant returns one join grant, or ErrNotFound.
	JoinGrant(id string) (ledger.JoinGrant, error)
}

// WorkerWriter writes the worker plane's records.
type WorkerWriter interface {
	WorkerReader
	// InsertJoinGrant records a join token by its digest.
	InsertJoinGrant(ledger.JoinGrant) error
	// SpendJoinGrant marks a grant used by a worker.
	SpendJoinGrant(id, worker string) error
	// Insert adds a worker with no session yet.
	Insert(ledger.WorkerRecord) error
	// ReplaceProof swaps a worker's proof digest.
	ReplaceProof(id, digest string) error
	// OpenEpoch moves a worker to its next epoch and returns it.
	OpenEpoch(worker ledger.Worker) (int64, error)
}
