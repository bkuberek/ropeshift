package ports

import (
	"context"

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
}
