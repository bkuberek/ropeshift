package ports

import (
	"context"
	"errors"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
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
	// ScheduleWake says when the tick next reads an open wake, and zero says only an event can
	// change its answer. Like Renew it takes no revision and appends no event: it runs after
	// the transaction that evaluated, on one row, and it only schedules (FR-PARK-5).
	ScheduleWake(ctx context.Context, scope ledger.WorkspaceID, id string, nextEvalAt int64) error
	// Fold runs fn in one short transaction that keeps the delivery read models (ADR-0026). It reads as Read
	// does and writes only rows derived from the log, so like Renew it takes no revision and appends no event.
	Fold(ctx context.Context, scope ledger.WorkspaceID, fn func(FoldTx) error) error
	// Installation is where the records every project shares change (ADR-0021): the installation's
	// database, or, for a store that keeps them itself, its own database under scope.
	Installation(scope ledger.WorkspaceID) Installation
	// Close releases the database.
	Close() error
}

// FoldTx is one transaction of the delivery fold: a view of the workspace, and its delivery read models.
type FoldTx interface {
	ReadTx
	Delivery() DeliveryWriter
}

// DeliveryReader reads the delivery read models (ADR-0026): the fold's state, each finished work order's
// timeline and each finished change's or rework's delivery record, and the spans in which the workspace's
// work-in-progress cap was full.
type DeliveryReader interface {
	// FoldState returns the fold's state, or ErrNotFound before its first run.
	FoldState() (delivery.FoldState, error)
	// WIPFull returns every span the work-in-progress cap was full, by start.
	WIPFull() ([]delivery.Span, error)
	// Timeline and Record return one work order's, or ErrNotFound.
	Timeline(workOrder string) (delivery.Timeline, error)
	Record(workOrder string) (delivery.Record, error)
	// Records returns every delivery record, by end.
	Records() ([]delivery.Record, error)
}

// DeliveryWriter writes the delivery read models. Every row is derived from the log, and Clear drops them
// all for a new derivation version.
type DeliveryWriter interface {
	DeliveryReader
	SaveFoldState(state delivery.FoldState) error
	AddWIPFull(span delivery.Span) error
	// Save keeps a finished work order's timeline, and its record, or none when record is nil.
	Save(timeline delivery.Timeline, record *delivery.Record) error
	// Forget drops a work order's timeline and record, because it left its terminal state.
	Forget(workOrder string) error
	Clear() error
}

// EstimateReader reads estimate sets and their estimates (ADR-0026).
type EstimateReader interface {
	// Set returns one set with its estimates, by work order, each saying whether it is still current, or
	// ErrNotFound.
	Set(id string) (delivery.EstimateSet, error)
	// Current returns a work order's current estimate, the one its newest set holds, or ErrNotFound.
	Current(workOrder string) (delivery.Estimate, error)
}

// EstimateWriter keeps estimate sets, which never change once kept.
type EstimateWriter interface {
	EstimateReader
	// Add keeps a sealed set, and each of its estimates becomes its work order's current estimate in place of
	// the one before.
	Add(set delivery.EstimateSet) error
}

// ReadTx is one consistent view of a workspace.
type ReadTx interface {
	// Workspace reads the workspace record.
	Workspace() (ledger.Workspace, error)
	// Events returns up to limit events after the revision, in order.
	Events(after int64, limit int) ([]ledger.Event, error)
	// EventsAbout returns every event about one subject, in order.
	EventsAbout(subject ledger.Subject) ([]ledger.Event, error)
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
	// InstallationEvents returns up to limit events of the installation's log after a revision, in
	// order. A store that keeps the shared records itself has their events in its own log, and none here.
	InstallationEvents(after int64, limit int) ([]ledger.Event, error)
	// Questions reads questions, decisions, and a workspace's added categories.
	Questions() QuestionReader
	// Members reads who can sign in and who has.
	Members() MemberReader
	// Policies reads policy versions.
	Policies() PolicyReader
	// Roster reads the versions of the agent roster: the installation's layer, which every project
	// resolves under its own, and this project's.
	Roster() RosterReader
	// Measures reads experience records.
	Measures() MeasureReader
	// Evidence reads what stays linked to a work order for its acceptance.
	Evidence() EvidenceReader
	// Wakes reads wake conditions and signals.
	Wakes() WakeReader
	// Messages reads messages to lanes.
	Messages() MessageReader
	// Conversations reads members' conversations with the assistant and their entries.
	Conversations() ConversationReader
	// Packs reads context packs.
	Packs() PackReader
	// Approvals reads approval requests.
	Approvals() ApprovalReader
	// Effects reads what waits for a person's word on an effect a crash left unknown.
	Effects() EffectReader
	// Repositories reads the record of each repository.
	Repositories() RepositoryReader
	// Forge reads forge requests, pull requests, and each worker's forge health.
	Forge() ForgeReader
	// Search returns lexical candidates, best first.
	Search(query SearchQuery) ([]SearchHit, error)
	// Deliveries reads the delivery read models the fold keeps (ADR-0026).
	Deliveries() DeliveryReader
	// Estimates reads estimate sets and each work order's current estimate.
	Estimates() EstimateReader
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
	// EventsAbout returns every event about one subject, in order.
	EventsAbout(subject ledger.Subject) ([]ledger.Event, error)
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
	// Workers reads registered workers and join grants. A project transaction never writes them: a change
	// to a record every project shares commits through the Installation.
	Workers() WorkerReader
	// Questions reads and writes questions, decisions, and added categories.
	Questions() QuestionWriter
	// Members reads who can sign in and who has, which only the Installation writes.
	Members() MemberReader
	// Policies reads and writes policy versions. It refuses a version of the autonomy policy, which the
	// Installation writes.
	Policies() PolicyWriter
	// Roster reads both layers of the agent roster and writes this project's. It refuses a version of the
	// installation's layer, which the Installation writes.
	Roster() RosterWriter
	// Measures reads and writes experience records.
	Measures() MeasureWriter
	// Evidence reads and writes what stays linked to a work order for its acceptance.
	Evidence() EvidenceWriter
	// Wakes reads and writes wake conditions and signals.
	Wakes() WakeWriter
	// Messages reads and writes messages to lanes.
	Messages() MessageWriter
	// Conversations reads and writes members' conversations with the assistant and their entries.
	Conversations() ConversationWriter
	// Packs reads and writes context packs.
	Packs() PackWriter
	// Approvals reads and writes approval requests.
	Approvals() ApprovalWriter
	// Effects reads and writes what waits for a person's word on an effect a crash left unknown.
	Effects() EffectWriter
	// Repositories reads and writes the record of each repository.
	Repositories() RepositoryWriter
	// Recoveries keeps the record of recovery acts.
	Recoveries() RecoveryWriter
	// Forge reads and writes forge requests, pull requests, and each worker's forge health.
	Forge() ForgeWriter
	// Search returns lexical candidates, best first.
	Search(query SearchQuery) ([]SearchHit, error)
	// Index keeps one record's text for search, replacing what it held.
	Index(kind, id, text string) error
	// Deliveries reads the delivery read models the fold keeps, which an estimate draws on.
	Deliveries() DeliveryReader
	// Estimates reads and keeps estimate sets.
	Estimates() EstimateWriter
}

// ForgeReader reads what the control plane asked workers to do on a forge, the pull request each work order
// lands through, and what each worker said of its forge sign-in (ADR-0020).
type ForgeReader interface {
	// Request returns one forge request, or ErrNotFound.
	Request(id string) (ledger.ForgeRequest, error)
	// RequestsFor lists what one worker has been asked and has not reported done, oldest first.
	RequestsFor(worker string) ([]ledger.ForgeRequest, error)
	// RequestsOf lists every request of one work order, oldest first.
	RequestsOf(workOrder string) ([]ledger.ForgeRequest, error)
	// PullRequest returns the pull request a work order lands through, or ErrNotFound.
	PullRequest(workOrder string) (ledger.WorkPullRequest, error)
	// PullRequests lists every work order's pull request, by work order.
	PullRequests() ([]ledger.WorkPullRequest, error)
	// Health lists what each worker last said of its forge sign-in, by repository and worker.
	Health() ([]ledger.ForgeHealth, error)
	// Integration returns one approval of a reviewed commit for merge, or ErrNotFound.
	Integration(id string) (ledger.IntegrationRecord, error)
	// Integrations lists a work order's approvals for merge, oldest first.
	Integrations(workOrder string) ([]ledger.IntegrationRecord, error)
}

// ForgeWriter writes them. Each write that answers a worker compares the attempt it answers, so a stale
// report changes nothing.
type ForgeWriter interface {
	ForgeReader
	// InsertRequest keeps a new request, and reports false for an ID that exists.
	InsertRequest(ledger.ForgeRequest) (bool, error)
	// FailRequest keeps why an attempt failed and offers the next one, if the request is still requested
	// at that attempt.
	FailRequest(id string, attempt int64, detail string) (bool, error)
	// FinishRequest marks a request done at an attempt, if it is still requested at that attempt.
	FinishRequest(id string, attempt int64, at int64) (bool, error)
	// FinishMerge marks a merge request done at an attempt with what the merge came to and why, if it is
	// still requested at that attempt.
	FinishMerge(id string, attempt, at int64, outcome ledger.MergeOutcome, detail string) (bool, error)
	// InsertIntegration keeps an approval for merge, and reports false for an ID that exists.
	InsertIntegration(ledger.IntegrationRecord) (bool, error)
	// RecordMerge keeps the commit an approved merge made, once: a record already merged keeps its own. It
	// also names the pull request the merge went through when the record names none yet.
	RecordMerge(id string, pullRequest int64, commit string, at int64) (bool, error)
	// SavePullRequest keeps what a worker read of a work order's pull request, replacing what it held.
	SavePullRequest(ledger.WorkPullRequest) error
	// SaveHealth keeps what a worker said of its sign-in for a repository when the state or its detail
	// changed, and reports whether it did. The same word again writes nothing.
	SaveHealth(ledger.ForgeHealth) (bool, error)
}

// RecoveryWriter keeps every recovery act that ran or was refused (ADR-0014).
type RecoveryWriter interface {
	// Record keeps one act.
	Record(authority.RecoveryRecord) error
	// Done counts the acts of one kind that principals of one kind took on a work order after a time, which
	// is how the policy bounds an orchestrator's breaker resets in a day.
	Done(workOrder string, act authority.RecoveryAct, by ledger.ActorKind, since int64) (int64, error)
}

// WorkOrderReader reads the work order projection.
type WorkOrderReader interface {
	// Get returns one work order, or ErrNotFound.
	Get(id string) (workorders.WorkOrder, error)
	// List returns every work order in the workspace, ordered by ID.
	List() ([]workorders.WorkOrder, error)
	// HeldBy returns the work orders a worker holds a lease on.
	HeldBy(worker string) ([]workorders.WorkOrder, error)
	// Woken returns the work orders a wake made ready, each with a lane to resume.
	Woken() ([]workorders.WorkOrder, error)
	// ReviewsOf returns the reviews of one work order, oldest attempt first.
	ReviewsOf(id string) ([]workorders.WorkOrder, error)
	// ByContract returns the work orders of one contract.
	ByContract(contract string) ([]workorders.WorkOrder, error)
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
	// Retry returns failed work to `ready` with the attempts it now allows, and reports whether the row
	// was still failed.
	Retry(id string, maxAttempts int64) (bool, error)
	// Reviewed records who passed a work order's review. An empty member voids it.
	Reviewed(id, member string) error
	// TakenIn records the commit intake read for an accepted `plan` work order.
	TakenIn(id, commit string) error
	// Pin moves unstarted work to a contract version, and voids its review.
	Pin(id string, version int64) error
	// ResolveRepository targets work at the repository that took the name its contract gave.
	ResolveRepository(id, repository string) error
	// KeepBranch has work keep a branch for its next attempts.
	KeepBranch(id, branch string) error
	// Hold records why ready work is not claimed, and nil takes the reason back.
	Hold(id string, hold *workorders.StartHold) error
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
	// Park releases a running lease with its process gone, and moves the fence.
	Park(lease workorders.Lease) (bool, error)
	// Cancel ends a work order from the state and fence read, and clears any holder. The fence
	// moves when the state held a lease.
	Cancel(workOrder string, from workorders.State, fence int64, held bool) (bool, error)
}

// ClaimReader reads claims.
type ClaimReader interface {
	// Find returns one claim, or ErrNotFound.
	Find(id string) (workorders.Claim, error)
	// List returns every claim, by work order and then by fence. The fence orders
	// grants, because two grants can share a millisecond.
	List() ([]workorders.Claim, error)
	// OfLane returns the last claim that ran a lane, or ErrNotFound.
	OfLane(lane string) (workorders.Claim, error)
}

// ClaimWriter writes claims.
type ClaimWriter interface {
	ClaimReader
	// Record inserts a new claim.
	Record(workorders.Claim) error
	// End closes an open claim.
	End(id, reason string) error
	// Dispatch names the lane a claim should run, and that lane's own profile layer.
	Dispatch(id, lane, overrides string) error
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
	// WindowResets returns how many times an orchestrator reset a lane's breaker window, or ErrNotFound.
	WindowResets(lane string) (int64, error)
	// Respawns returns how many times someone asked for a lane's next generation.
	Respawns(lane string) (int64, error)
	// Artifacts lists what a lane left, oldest first.
	Artifacts(lane string) ([]ledger.Artifact, error)
	// Archiving lists the lanes a worker should take away.
	Archiving(worker string) ([]ledger.DesiredArchive, error)
	// Generations lists a lane's generations, oldest first.
	Generations(lane string) ([]ledger.Generation, error)
	// Handoff returns what one generation left, or ErrNotFound.
	Handoff(lane string, generation int64) (ledger.HandoffRecord, error)
	// Checkpoint returns what one parked generation left, or ErrNotFound.
	Checkpoint(lane string, generation int64) (ledger.Checkpoint, error)
	// EasingDue lists the lanes whose preferred worker's first claim has run out at a time. A
	// tick reads this with no lock, so Ease decides each one.
	EasingDue(now int64) ([]string, error)
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
	// MarkParking records that a lane began to park. An ended lane stays ended.
	MarkParking(lane string) error
	// MarkParked records that a lane parked, which the ledger knows for itself, and where it
	// can resume.
	MarkParked(lane string, hold ledger.ResumeHold) error
	// SaveCheckpoint keeps what a parked generation left.
	SaveCheckpoint(checkpoint ledger.Checkpoint) error
	// Wakened starts the delay a preferred worker has, from a wake.
	Wakened(lane string, after int64) error
	// Ease ends a preferred worker's first claim, once its delay ran out, and reports whether
	// it matched.
	Ease(lane string, now int64) (bool, error)
	// Resume moves a lane to the fence of the claim that took it, and lets go of its hold.
	Resume(lane string, fence int64) error
	// Responded stamps a generation's last model response with database time.
	Responded(lane string, number int64) error
	// FellBack says a generation that meant to resume started from its handoff instead.
	FellBack(lane string, number int64, handoffIn string) error
	// WantPark says a policy asked a lane to park, and an empty reason takes that back.
	WantPark(lane, reason string) error
	// Reset counts one human reset of a lane's breaker and returns the count.
	Reset(lane string) (int64, error)
	// ResetWindow counts one orchestrator's reset of a lane's breaker window and returns the count.
	ResetWindow(lane string) (int64, error)
	// Respawn counts one request for a lane's next generation.
	Respawn(lane string) error
	// Guarded keeps how a stop left a worktree. Work no remote holds withdraws a request
	// to remove it, unless a member forced that request.
	Guarded(lane string, unpushed bool) error
	// AddArtifact keeps what a lane left, once per ID.
	AddArtifact(artifact ledger.Artifact) error
	// RequestArchive asks a worker to take a lane's worktree away.
	RequestArchive(lane string, forced bool) error
	// Archived records that a lane's worktree went.
	Archived(lane string) error
	// StartGeneration records a generation's beginning and why.
	StartGeneration(generation ledger.Generation) error
	// BindSession records what the vendor calls a generation's session.
	BindSession(lane string, number int64, session string) error
	// ReadPack records the context pack version a generation read.
	ReadPack(lane string, number int64, pack string, version int64) error
	// Baseline records what a generation held at its first call.
	Baseline(lane string, number, tokens int64) error
	// EndGeneration records a generation's end, once.
	EndGeneration(lane string, number int64, reason string) error
	// SaveHandoff keeps one generation's handoff once, and names it on that generation.
	SaveHandoff(record ledger.HandoffRecord) error
}

// WakeReader reads wake conditions and signals.
type WakeReader interface {
	// Get returns one wake, or ErrNotFound.
	Get(id string) (parking.Wake, error)
	// Open lists the wakes that have neither fired nor been retired. An event reads all of
	// them, because an expression is opaque text and nothing maps a record to its wakes.
	Open() ([]parking.Wake, error)
	// All lists every wake.
	All() ([]parking.Wake, error)
	// Due lists the open wakes whose time has come. A tick reads this with no lock, so the
	// guarded statements decide each one.
	Due(now int64) ([]string, error)
	// Signaled says a signal arrived after a revision.
	Signaled(kind parking.Kind, key string, after int64) (bool, error)
}

// WakeWriter writes wake conditions and signals. Fire and MoveToReady are the two guards of a
// firing, and each reports whether it matched a row.
type WakeWriter interface {
	WakeReader
	// Insert stores a wake and reports whether it was new.
	Insert(wake parking.Wake) (bool, error)
	// Fire is the compare-and-set on `fired_at`.
	Fire(id, cause string) (bool, error)
	// MoveToReady is the compare-and-set on `parked`, and it names the lane to resume.
	MoveToReady(workOrder, lane string) (bool, error)
	// Retire closes what a work order's lanes left open, with no firing.
	Retire(workOrder, cause string) error
	// Signal records a fact under its event's revision.
	Signal(signal parking.Signal, rev int64) error
}

// MessageReader reads messages to lanes. No read here stops at a page, because each one decides
// what happens to everything it returns.
type MessageReader interface {
	// Message returns one message, or ErrNotFound.
	Message(id string) (messages.Message, error)
	// All lists every message, oldest first.
	All() ([]messages.Message, error)
	// ToLane lists what was sent to a lane, oldest first.
	ToLane(lane string) ([]messages.Message, error)
	// Queued lists what no worker has acknowledged for a lane, oldest first.
	Queued(lane string) ([]messages.Message, error)
	// Unsettled lists what a clock or a generation's end can still move, for a lane's generations
	// up to one.
	Unsettled(lane string, through int64) ([]messages.Message, error)
	// Facts lists what a lane's next first prompt owes: facts no session took in, and messages
	// a generation up to `ended` took in and did not answer.
	Facts(lane string, ended int64) ([]messages.Message, error)
	// Due lists the messages past their window at a time. A tick reads this with no lock, so each
	// one is read again in its own transaction.
	Due(now int64) ([]string, error)
	// ToTarget lists what was sent to a target of any kind, by the target's ID, oldest first.
	ToTarget(kind messages.TargetKind, id string) ([]messages.Message, error)
	// QueuedTo lists what no worker has acknowledged for a target of any kind, oldest first.
	QueuedTo(kind messages.TargetKind, id string) ([]messages.Message, error)
}

// MessageWriter writes messages to lanes.
type MessageWriter interface {
	MessageReader
	// NextSeq is a sender's next number (FR-MSG-1).
	NextSeq(kind, id string) (int64, error)
	// Insert adds a new message.
	Insert(messages.Message) error
	// Save is the compare-and-set of the delivery table: it replaces what can change on a message
	// that still stands in the state its caller read, and reports whether it did.
	Save(message messages.Message, from messages.State) (bool, error)
	// DropTo removes every message to one target, and says how many it removed.
	DropTo(kind messages.TargetKind, id string) (int64, error)
}

// ConversationReader reads members' conversations with the assistant, and what each holds.
type ConversationReader interface {
	// Conversation returns one conversation, or ErrNotFound.
	Conversation(id string) (conversations.Conversation, error)
	// OfMember lists a member's conversations, newest activity first.
	OfMember(member string) ([]conversations.Conversation, error)
	// Running lists every conversation a member asked to run, oldest activity first.
	Running() ([]conversations.Conversation, error)
	// ByRunLane returns the conversation a run lane belongs to, or ErrNotFound.
	ByRunLane(lane string) (conversations.Conversation, error)
	// Entries lists a conversation's entries before a seq, newest first, up to limit. A zero seq reads from
	// the newest.
	Entries(conversation string, before int64, limit int) ([]conversations.Entry, error)
	// Entry returns one entry, or ErrNotFound.
	Entry(conversation, id string) (conversations.Entry, error)
	// NextSeq is a conversation's next entry position, from 1.
	NextSeq(conversation string) (int64, error)
	// Forgets lists the deleted conversations whose text a worker still holds, oldest first.
	Forgets(worker string) ([]conversations.Forget, error)
}

// ConversationWriter writes members' conversations with the assistant, and what each holds.
type ConversationWriter interface {
	ConversationReader
	// Insert adds a conversation.
	Insert(conversations.Conversation) error
	// Save replaces what can change on a conversation.
	Save(conversations.Conversation) error
	// Delete removes a conversation and every entry it holds.
	Delete(id string) error
	// AddEntry adds an entry at its seq.
	AddEntry(conversations.Entry) error
	// MarkBeforeClear marks every entry of a conversation up to a seq as earlier than a clear.
	MarkBeforeClear(conversation string, through int64) error
	// AddForget records what a worker still holds of a deleted conversation.
	AddForget(conversations.Forget) error
	// DropForget ends a worker's forget of a conversation, and says whether one stood.
	DropForget(conversation, worker string) (bool, error)
}

// PackReader reads context packs. No read here stops at a page, because each one decides who hears
// of a change.
type PackReader interface {
	// Pack returns one version, or ErrNotFound.
	Pack(id string, version int64) (contextpacks.Pack, error)
	// Latest returns a pack's newest version, or ErrNotFound.
	Latest(id string) (contextpacks.Pack, error)
	// All lists every version, by pack and then by version.
	All() ([]contextpacks.Pack, error)
	// Pinning names the packs whose newest version holds a record, through the item index.
	Pinning(kind contextpacks.Kind, record string) ([]string, error)
	// Lanes names the lanes a pack's claims ran.
	Lanes(pack string) ([]string, error)
	// Additions lists what was added to a work order's next pack, oldest first.
	Additions(workOrder string) ([]contextpacks.Addition, error)
	// AllAdditions lists what was added to every work order, oldest first.
	AllAdditions() ([]contextpacks.Addition, error)
	// Samples reads what fresh generations measured, for the ratio an estimate uses.
	Samples() ([]contextpacks.Sample, error)
}

// PackWriter writes context packs. A version never changes once it is written.
type PackWriter interface {
	PackReader
	// Insert adds a version and its items.
	Insert(contextpacks.Pack) error
	// Add keeps one addition, and reports whether it was new.
	Add(contextpacks.Addition) (bool, error)
}

// ApprovalReader reads approval requests. No read here stops at a page, because each decides what
// happens to everything it returns.
type ApprovalReader interface {
	// Approval returns one request, or ErrNotFound.
	Approval(id string) (authority.Approval, error)
	// OfLane lists what a lane raised, oldest first.
	OfLane(lane string) ([]authority.Approval, error)
	// OfWorkOrder lists what a work order's lanes raised, oldest first.
	OfWorkOrder(workOrder string) ([]authority.Approval, error)
	// All lists every request, oldest first.
	All() ([]authority.Approval, error)
	// Running lists the confirmed cards a member approved by a time whose call has recorded no outcome,
	// oldest first.
	Running(before int64) ([]string, error)
	// Due lists the live requests past their time. A tick reads this with no lock, so each is read
	// again in its own transaction.
	Due(now int64) ([]string, error)
}

// ApprovalWriter writes approval requests. The store itself refuses a decision by the principal
// that asked, one by a kind that decides nothing, and one that names any other digest.
type ApprovalWriter interface {
	ApprovalReader
	// Insert adds a request, and reports whether it was new.
	Insert(authority.Approval) (bool, error)
	// Save is the compare-and-set of a request's state: it replaces what can change on one that still
	// stands in the state its caller read, and reports whether it did.
	Save(approval authority.Approval, from authority.ApprovalState) (bool, error)
	// LinkPlan names the plan version a plan approval is about once the ledger holds it, for a request
	// that was raised for a branch nothing had read yet. It reports whether the request had none.
	LinkPlan(id, plan string, version int64) (bool, error)
}

// EffectReader reads what a person can decide of an effect a crash left unknown. No read here stops
// at a page, because each decides what a person or a worker hears.
type EffectReader interface {
	// Effect returns one, or ErrNotFound.
	Effect(id string) (ledger.UnknownEffect, error)
	// Effects lists every one, oldest first.
	Effects() ([]ledger.UnknownEffect, error)
	// DecidedFor lists what a person decided and one worker has not yet acted on, oldest first.
	DecidedFor(worker string) ([]ledger.UnknownEffect, error)
}

// EffectWriter writes them. The store itself refuses a decision with no member behind it.
type EffectWriter interface {
	EffectReader
	// Insert keeps one attempt at an effect, and reports whether it was new.
	Insert(ledger.UnknownEffect) (bool, error)
	// Decide keeps a decision on one nobody decided yet, and reports whether it did. Kind is the
	// kind of actor deciding.
	Decide(id string, decided ledger.EffectDecided, kind string) (bool, error)
	// Settle records that the worker acted on a decision, and reports whether this was the first time.
	Settle(id string, at int64) (bool, error)
}

// RepositoryReader reads the record of each repository. No read here stops at a page, because there are
// few and each decides where a lane starts.
type RepositoryReader interface {
	// Repository returns one record, or ErrNotFound.
	Repository(id string) (ledger.Repository, error)
	// Named returns the record with a name in the workspace, or ErrNotFound.
	Named(name string) (ledger.Repository, error)
	// Repositories lists every record, by ID.
	Repositories() ([]ledger.Repository, error)
}

// RepositoryWriter writes the record of each repository.
type RepositoryWriter interface {
	RepositoryReader
	// Insert adds a record and reports whether it was new.
	Insert(ledger.Repository) (bool, error)
	// Detected replaces the remote and the default branch git gave, and reports whether either moved.
	// A member's settings stay.
	Detected(id, remote, defaultBranch string) (bool, error)
	// Remove keeps a record as history, out of its project, and drops its identity from the installation's
	// repository index.
	Remove(id string) error
	// Restore brings a removed record back into its project and holds its identity in the repository
	// index again, which is refused when another project holds it.
	Restore(id string) error
	// Settings replaces the name and the settings a member gives. The store itself refuses a combination
	// that cannot hold, and a name another record of the workspace holds.
	Settings(ledger.Repository) error
}

// EvidenceReader reads what a work order's lanes left and what its checks said.
type EvidenceReader interface {
	// Attempt lists the check results of one attempt, oldest first.
	Attempt(workOrder string, fence int64) ([]workorders.Evidence, error)
	// Checks lists every attempt's check results, oldest first.
	Checks(workOrder string) ([]workorders.Evidence, error)
	// Artifacts lists what every lane of a work order left, oldest first.
	Artifacts(workOrder string) ([]ledger.Artifact, error)
	// Verdicts lists what every review of a work order said, oldest first and a review's by criterion. A
	// verdict stays on record when its attempt or its commit is no longer the work's, and the caller
	// chooses the ones that count.
	Verdicts(workOrder string) ([]workorders.Verdict, error)
	// Reworked returns the newest note on why a work order went back for another attempt, or ErrNotFound.
	Reworked(workOrder string) (workorders.ReworkNote, error)
	// Calibrations lists every record of a member's decision on work a review judged, oldest first.
	Calibrations() ([]workorders.Calibration, error)
}

// EvidenceWriter writes check results.
type EvidenceWriter interface {
	EvidenceReader
	// Record keeps one check result. The same ID replaces its status and details, because a
	// forge can run a check again on the same commit.
	Record(evidence workorders.Evidence) error
	// RecordVerdict keeps one review's verdict on one criterion. The store refuses a verdict that is not
	// whole, a second verdict for one criterion of one review, and a verdict that does not come from a
	// lane of the review work order that names the reviewed work: no lane judges its own work, in any
	// generation (FR-REVIEW-5).
	RecordVerdict(verdict workorders.Verdict) error
	// RecordRework keeps why work went back. The same work order and attempt replace their note.
	RecordRework(note workorders.ReworkNote) error
	// RecordCalibration keeps one member's decision on reviewed work, and reports whether it was new. The
	// store refuses a record that is not whole, and leaves alone a second record for one review, source,
	// and decision. Nothing it keeps ever reaches a repository's merge authority (FR-CAL-3).
	RecordCalibration(calibration workorders.Calibration) (bool, error)
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
	// Plan returns one plan, or ErrNotFound.
	Plan(id string) (planning.Plan, error)
	// Goals returns every goal.
	Goals() ([]planning.Goal, error)
	// Goal returns one goal, or ErrNotFound.
	Goal(id string) (planning.Goal, error)
	// Requirement returns a requirement's newest version by its key, or ErrNotFound.
	Requirement(key string) (planning.Requirement, error)
	// Requirements returns every version, by key and then by version.
	Requirements() ([]planning.Requirement, error)
	// Document returns one document by its reference, `<kind>:<id>`, or ErrNotFound.
	Document(id string) (planning.DocumentRecord, error)
	// Documents returns every document.
	Documents() ([]planning.DocumentRecord, error)
	// PlanDocuments returns the documents whose files name a plan.
	PlanDocuments(plan string) ([]planning.DocumentRecord, error)
	// DocumentVersion returns one version of a document, or ErrNotFound.
	DocumentVersion(id string, version int64) (planning.DocumentVersion, error)
	// LatestDocumentVersion returns a document's newest version, or ErrNotFound.
	LatestDocumentVersion(id string) (planning.DocumentVersion, error)
	// DocumentVersions returns every version of every document, by document and then by version.
	DocumentVersions() ([]planning.DocumentVersion, error)
	// LatestPlanVersion returns a plan's newest version, or ErrNotFound for a plan recorded before
	// plans had versions.
	LatestPlanVersion(plan string) (planning.PlanVersion, error)
	// PlanVersion returns one version of a plan, or ErrNotFound.
	PlanVersion(plan string, version int64) (planning.PlanVersion, error)
	// PlanVersions returns every version of every plan, by plan and then by version.
	PlanVersions() ([]planning.PlanVersion, error)
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
	// UpdatePlan replaces what a plan's file says.
	UpdatePlan(planning.Plan) error
	// SaveGoal creates or replaces a goal.
	SaveGoal(planning.Goal) error
	// InsertRequirement adds one requirement version.
	InsertRequirement(planning.Requirement) error
	// SaveDocument creates a document, or replaces the title and plan its newest version gives it.
	SaveDocument(planning.DocumentRecord) error
	// InsertDocumentVersion adds one document version.
	InsertDocumentVersion(planning.DocumentVersion) error
	// InsertPlanVersion adds one plan version.
	InsertPlanVersion(planning.PlanVersion) error
	// InsertContract adds one contract version, with the document versions it cites.
	InsertContract(planning.Contract) error
	// InsertLink adds one external key.
	InsertLink(ledger.ExternalLink) error
	// InsertDependency adds one edge.
	InsertDependency(planning.Dependency) error
	// DeleteDependency removes one edge, found by its work order, target, and kind.
	DeleteDependency(planning.Dependency) error
	// SaveBinding creates or replaces a binding.
	SaveBinding(planning.SourceBinding) error
}

// MaxRecords bounds one search or filter, so a broad one stays a short transaction.
const MaxRecords = 500

// SearchQuery asks for lexical candidates among some kinds of record. A question that is
// resolved and a decision that was overturned or replaced are not candidates.
type SearchQuery struct {
	Text  string
	Kinds []string
	Limit int
}

// SearchHit is one candidate. A higher score is a better match within one search, and a
// score means nothing across two.
type SearchHit struct {
	Kind     string
	RecordID string
	Score    float64
}

// RecordFilter narrows questions or decisions. An empty field does not filter, and Text
// orders the result by rank.
type RecordFilter struct {
	Category string
	Status   string
	Scope    string
	Text     string
	Limit    int
	// After reads on from one record's ID, so a reader can walk past the bound of one read.
	After string
}

// QuestionReader reads questions, decisions, and a workspace's added categories.
type QuestionReader interface {
	// Question returns one question, or ErrNotFound.
	Question(id string) (questions.Question, error)
	// Questions returns the questions a filter selects.
	Questions(filter RecordFilter) ([]questions.Question, error)
	// HeldBy returns the unresolved questions one holder holds, oldest first.
	HeldBy(holder string) ([]questions.Question, error)
	// ByHash returns the questions with one normalized text.
	ByHash(hash string) ([]questions.Question, error)
	// Attachments returns one question's lanes, or every attachment for an empty ID.
	Attachments(question string) ([]questions.Attachment, error)
	// OpenBlocking counts the open blocking questions about a work order, its contract, or its plan.
	OpenBlocking(workOrder, contract, plan string) (int64, error)
	// Decision returns one decision, or ErrNotFound.
	Decision(id string) (questions.Decision, error)
	// Decisions returns the decisions a filter selects.
	Decisions(filter RecordFilter) ([]questions.Decision, error)
	// ReplacedBy returns the decision that superseded one, or ErrNotFound.
	ReplacedBy(decision string) (questions.Decision, error)
	// AnsweredBy returns the questions one decision answered.
	AnsweredBy(decision string) ([]questions.Question, error)
	// Overlapping returns every decision in force whose scope overlaps one, by ID. It stops at no
	// page, because a recording decision states how it stands to each, and a pack selects from them.
	Overlapping(scope string) ([]questions.Decision, error)
	// Assumptions counts the open assumptions about one work order.
	Assumptions(workOrder string) (int64, error)
	// OpenAssumptions names them, by ID.
	OpenAssumptions(workOrder string) ([]string, error)
	// Notices returns what one decision's end told lanes, or every notice for an empty ID.
	Notices(decision string) ([]questions.Notice, error)
	// Categories returns the categories this workspace added.
	Categories() ([]string, error)
	// Batch returns one batch, or ErrNotFound.
	Batch(id string) (questions.Batch, error)
	// OpenBatch returns the batch that gathers now, or ErrNotFound.
	OpenBatch() (questions.Batch, error)
	// Batches returns every batch, oldest first.
	Batches() ([]questions.Batch, error)
	// DueBatches lists the batches whose window closed by a time. A tick reads this with no
	// lock, so CloseBatch decides each one.
	DueBatches(now int64) ([]string, error)
	// WaitingBatches returns the delivered batches that hold a question a person has not
	// answered, oldest first.
	WaitingBatches() ([]questions.Batch, error)
	// BatchQuestions returns the questions one batch holds.
	BatchQuestions(id string) ([]questions.Question, error)
}

// QuestionWriter writes questions, decisions, and added categories.
type QuestionWriter interface {
	QuestionReader
	// Insert adds a new question.
	Insert(questions.Question) error
	// Save replaces what can change on a question.
	Save(questions.Question) error
	// Attach joins a lane to a question and reports whether it was new.
	Attach(questions.Attachment) (bool, error)
	// Take is the compare-and-set on who works a question. It reports whether the row
	// matched: an open question nobody holds, one whose hold passed, or this holder's own.
	Take(id, holder string, ttlMs int64) (bool, error)
	// InsertDecision adds a decision.
	InsertDecision(questions.Decision) error
	// EndDecision is the compare-and-set that ends a decision in force, so a decision ends
	// once. It reports whether the row matched.
	EndDecision(id string, status questions.DecisionStatus) (bool, error)
	// Notice tells a lane once, and reports whether it was new.
	Notice(questions.Notice) (bool, error)
	// AddCategory adds a category and reports whether it was new.
	AddCategory(name string) (bool, error)
	// InsertBatch adds a batch that gathers.
	InsertBatch(questions.Batch) error
	// HastenBatch moves an open batch's close to an earlier time, and reports whether it
	// moved.
	HastenBatch(id string, closesAt int64) (bool, error)
	// CloseBatch is the compare-and-set that ends an open batch, so a batch reaches its end
	// once. It reports whether the row matched.
	CloseBatch(id string, status questions.BatchStatus, deliveredAt int64) (bool, error)
}

// PolicyReader reads policy versions.
type PolicyReader interface {
	// InForce returns the newest version of a kind, or ErrNotFound for a workspace that has
	// none yet.
	InForce(kind authority.PolicyKind) (authority.PolicyVersion, error)
	// Version returns one version, or ErrNotFound.
	Version(kind authority.PolicyKind, version int64) (authority.PolicyVersion, error)
	// Versions returns every version of a kind, newest first.
	Versions(kind authority.PolicyKind) ([]authority.PolicyVersion, error)
}

// PolicyWriter writes policy versions. A version never changes once it is written.
type PolicyWriter interface {
	PolicyReader
	// Insert adds a version and reports whether it was new.
	Insert(authority.PolicyVersion) (bool, error)
}

// RosterReader reads the versions of one side's layers of the agent roster (ADR-0025), by scope:
// roster.ScopeInstallation or roster.ScopeProject.
type RosterReader interface {
	// Latest returns the newest version of a scope, or ErrNotFound for a scope that has none yet.
	Latest(scope string) (roster.Version, error)
	// Version returns one version, or ErrNotFound.
	Version(scope string, version int64) (roster.Version, error)
	// Versions returns every version of a scope, newest first.
	Versions(scope string) ([]roster.Version, error)
}

// RosterWriter writes roster versions. A version never changes once it is written.
type RosterWriter interface {
	RosterReader
	// Insert adds a version and reports whether it was new.
	Insert(roster.Version) (bool, error)
}

// MeasureReader reads experience records.
type MeasureReader interface {
	// Experience returns the record of one subject, or ErrNotFound.
	Experience(subjectKind, subjectID string) (measures.Experience, error)
	// Experiences returns every record, by ID.
	Experiences() ([]measures.Experience, error)
}

// MeasureWriter writes experience records.
type MeasureWriter interface {
	MeasureReader
	// InsertExperience adds a record with no outcome.
	InsertExperience(measures.Experience) error
	// FillOutcome is the compare-and-set that fills a record's outcome, so it is filled
	// once. It reports whether the row matched.
	FillOutcome(subjectKind, subjectID string, outcome measures.Outcome, at int64) (bool, error)
}

// MemberReader reads who can sign in and who has.
type MemberReader interface {
	// Member returns one member and the hash a passphrase is checked against, or ErrNotFound.
	Member(id string) (authority.MemberRecord, error)
	// CountMembers counts the members of this workspace.
	CountMembers() (int64, error)
	// Session returns a session by the digest of its token, or ErrNotFound. It may have
	// expired, and the caller judges that against database time.
	Session(digest string) (authority.Session, error)
	// Sessions returns every session the ledger still holds.
	Sessions() ([]authority.Session, error)
	// Delegate returns one delegate credential by its ID, or ErrNotFound. DelegateByDigest returns one by
	// the digest of its token. Either may have expired or been revoked, which the caller judges.
	Delegate(id string) (authority.DelegateCredential, error)
	DelegateByDigest(digest string) (authority.DelegateCredential, error)
	// Delegates lists a member's delegate credentials, oldest first.
	Delegates(member string) ([]authority.DelegateCredential, error)
	// ConversationDelegates lists the assistant credentials of one conversation's sessions, oldest first: its
	// member's in its project, since another project may name a conversation of its own the same.
	ConversationDelegates(project ledger.WorkspaceID, member, conversation string) ([]authority.DelegateCredential, error)
}

// MemberWriter writes who can sign in and who has.
type MemberWriter interface {
	MemberReader
	// SaveMember adds a member, or replaces the hash of one that exists.
	SaveMember(authority.MemberRecord) error
	// InsertSession adds a session.
	InsertSession(authority.Session) error
	// DropSessions removes the sessions that ended by a time.
	DropSessions(before int64) error
	// DropSession removes one session, by the digest of its token.
	DropSession(digest string) error
	// DropMemberSessions removes every session of a member, and says how many it removed.
	DropMemberSessions(member string) (int64, error)
	// InsertDelegate adds a delegate credential.
	InsertDelegate(authority.DelegateCredential) error
	// TouchDelegate records when a credential last acted.
	TouchDelegate(id string, at int64) error
	// RevokeDelegate ends a credential at a time, once, for a reason, and reports whether it changed it. A
	// routine reason ends only an active credential; an explicit one also turns a routine end into its own,
	// keeping when the credential ended.
	RevokeDelegate(id string, at int64, reason string) (bool, error)
	// DropDelegate removes a credential's record.
	DropDelegate(id string) error
}

// WorkerReader reads the worker plane's records.
type WorkerReader interface {
	// Find returns one worker and its proof digest, or ErrNotFound.
	Find(id string) (ledger.WorkerRecord, error)
	// List returns every worker.
	List() ([]ledger.Worker, error)
	// JoinGrant returns one join grant, or ErrNotFound.
	JoinGrant(id string) (ledger.JoinGrant, error)
	// Orchestrator returns one worker's orchestrator, or ErrNotFound.
	Orchestrator(worker string) (ledger.Orchestrator, error)
	// Autonomy returns what the ledger holds of a worker beyond the policy, which is a row with only
	// its worker's ID for a worker nobody chose anything for.
	Autonomy(worker string) (authority.WorkerAutonomy, error)
	// Window returns one run window, or ErrNotFound.
	Window(id string) (authority.RunWindow, error)
	// OpenWindow returns the run window that stands for a worker at a time, or ErrNotFound.
	OpenWindow(worker string, now int64) (authority.RunWindow, error)
	// CapacityIntervals returns every worker's capacity interval whose end, or last report while it stands,
	// is at or after a time, by start (FR-OBS-14).
	CapacityIntervals(since int64) ([]ledger.CapacityInterval, error)
}

// WorkerWriter writes the worker plane's records.
type WorkerWriter interface {
	WorkerReader
	// SaveOrchestrator writes an orchestrator's projection.
	SaveOrchestrator(ledger.Orchestrator) error
	// SaveAutonomy writes what the ledger holds of a worker. The store refuses an acknowledgement
	// from anyone but a member.
	SaveAutonomy(authority.WorkerAutonomy) error
	// InsertWindow adds a run window. The store refuses one from anyone but a member.
	InsertWindow(authority.RunWindow) error
	// CloseWindow is the compare-and-set that ends a window that stands, and reports whether it did.
	CloseWindow(id, by string, at int64) (bool, error)
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
