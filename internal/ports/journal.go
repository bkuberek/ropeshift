package ports

import (
	"context"
	"errors"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// JournalResult is what the journal keeps about one event the ledger rejected.
type JournalResult = ledger.UploadResult

// Journal is the worker's local durable store, separate from the ledger in every
// mode, so a crash test in solo mode exercises the same recovery as group mode.
type Journal interface {
	// Migrate brings the schema to this build's version.
	Migrate(ctx context.Context) error
	// Close releases the store.
	Close() error
	// Identity returns what this worker keeps across restarts, or ErrNotFound.
	Identity(ctx context.Context) (ledger.WorkerIdentity, error)
	// SaveIdentity replaces it.
	SaveIdentity(ctx context.Context, identity ledger.WorkerIdentity) error
	// Append stores one event durably and assigns its contiguous sequence number.
	Append(ctx context.Context, event ledger.UploadEvent) (ledger.UploadEvent, error)
	// Pending returns up to limit events past the high-water mark, in order.
	Pending(ctx context.Context, limit int) ([]ledger.UploadEvent, error)
	// Through returns the high-water mark.
	Through(ctx context.Context) (int64, error)
	// Acknowledge moves the high-water mark and keeps each rejection.
	Acknowledge(ctx context.Context, through int64, rejected []JournalResult) error
	// Rejected lists the events the ledger refused.
	Rejected(ctx context.Context) ([]JournalResult, error)
	// Leases lists the leases this worker holds.
	Leases(ctx context.Context) ([]ledger.HeldLease, error)
	// SaveLeases replaces them.
	SaveLeases(ctx context.Context, leases []ledger.HeldLease) error
	// Desired returns the cached desired state, or ErrNotFound.
	Desired(ctx context.Context) (ledger.DesiredState, error)
	// SaveDesired replaces it.
	SaveDesired(ctx context.Context, state ledger.DesiredState) error
	// SaveLane creates or replaces one lane's record.
	SaveLane(ctx context.Context, lane lanes.Record) error
	// BeginLane stores a new lane and its first event in one transaction, so a crash
	// leaves a lane the worker can account for or nothing at all.
	BeginLane(ctx context.Context, lane lanes.Record, event ledger.UploadEvent) (ledger.UploadEvent, error)
	// EndLane marks a lane ended, expires its token, and stores its last event in one
	// transaction, so a lane ends once whatever a crash interrupts.
	EndLane(ctx context.Context, id string, event ledger.UploadEvent) (ledger.UploadEvent, error)
	// Lane returns one lane, or ErrNotFound.
	Lane(ctx context.Context, id string) (lanes.Record, error)
	// LaneByToken returns the lane a token digest belongs to, or ErrNotFound. A token
	// that expired has no lane.
	LaneByToken(ctx context.Context, digest string) (lanes.Record, error)
	// Lanes lists every lane.
	Lanes(ctx context.Context) ([]lanes.Record, error)
	// SaveReport keeps a lane's first report, and reports whether this was it.
	SaveReport(ctx context.Context, lane string, report []byte) (bool, error)
	// ExpireToken ends a lane's token, because its process ended.
	ExpireToken(ctx context.Context, lane string) error
	// MergeHook stores one hook event. A repeat of the same generation, event, and
	// subject adds to the first.
	MergeHook(ctx context.Context, event lanes.HookEvent) error
	// HookEvents lists one lane's merged events.
	HookEvents(ctx context.Context, lane string) ([]lanes.HookRecord, error)
	// BeginEffect records an effect as started, before it happens. A key that exists
	// changes nothing and reports false, so an effect has one record for good.
	BeginEffect(ctx context.Context, effect lanes.Effect) (bool, error)
	// Effect returns one effect, or ErrNotFound.
	Effect(ctx context.Context, key string) (lanes.Effect, error)
	// Effects lists every effect, in the order they began.
	Effects(ctx context.Context) ([]lanes.Effect, error)
	// FinishEffect records how an effect ended. It settles a started or an unknown
	// effect, and a settled one stays as it is.
	FinishEffect(ctx context.Context, key string, state lanes.EffectState, detail string) error
	// RetryEffect moves an unknown effect back to started and counts the attempt. It
	// reports false for any other state, so only an observed absence leads to a retry.
	RetryEffect(ctx context.Context, key string) (bool, error)
	// MarkUnknown turns every started effect into unknown_after_crash, and returns how
	// many. It runs once at a start, before this process begins an effect.
	MarkUnknown(ctx context.Context) (int, error)
	// BindGeneration records a lane's session and its event together, once, and reports
	// whether this was the first time.
	BindGeneration(ctx context.Context, lane, sessionID string, event ledger.UploadEvent) (bool, error)
	// SaveProgress replaces how far this worker read a lane's session.
	SaveProgress(ctx context.Context, lane string, progress lanes.Progress) error
	// SaveActual replaces where a lane's state stands.
	SaveActual(ctx context.Context, lane string, state lanes.ActualState) error
	// BeginGeneration moves a lane to its next generation and stores that event, together,
	// so a crash leaves the lane on one generation or the other. The token of the last
	// generation ends here, because its process has.
	BeginGeneration(ctx context.Context, lane lanes.Record, event ledger.UploadEvent) (ledger.UploadEvent, error)
	// SaveHandoff replaces how far a lane's handoff got. Nil clears it.
	SaveHandoff(ctx context.Context, lane string, progress *lanes.HandoffProgress) error
	// StoreHandoff keeps a sealed handoff and the event that carries it to the ledger,
	// together, so a crash cannot store it twice or lose it.
	StoreHandoff(ctx context.Context, lane string, progress lanes.HandoffProgress, event ledger.UploadEvent) (ledger.UploadEvent, error)
	// ChargeRestart keeps one charge and reports whether this was the first of its key.
	ChargeRestart(ctx context.Context, lane string, charge lanes.Restart) (bool, error)
	// Restarts lists a lane's charges, oldest first.
	Restarts(ctx context.Context, lane string) ([]lanes.Restart, error)
	// ClearRestarts drops a lane's charges and keeps how many resets it has acted on.
	ClearRestarts(ctx context.Context, lane string, resets int64) error
	// SaveGuard keeps how the last stop left a lane's worktree.
	SaveGuard(ctx context.Context, lane string, result lanes.GuardResult) error
	// MarkArchived records that a lane's worktree went, and stores that event, together,
	// so a crash cannot leave a removal the ledger never hears of.
	MarkArchived(ctx context.Context, lane string, event ledger.UploadEvent) (ledger.UploadEvent, error)
	// Allocate gives a lane its resources, or returns what it already holds, so a retry
	// after a crash converges. A pool with no block left is ErrNoPorts.
	Allocate(ctx context.Context, request lanes.AllocationRequest) (lanes.Allocation, error)
	// Allocations lists what every lane holds.
	Allocations(ctx context.Context) ([]lanes.Allocation, error)
	// Release gives back what a lane holds. A lane that holds nothing is done.
	Release(ctx context.Context, lane string) error
}

// ErrNoPorts means a worker's pool has no block left.
var ErrNoPorts = errors.New("no ports left in the pool")
