package ledger

import (
	"errors"
	"fmt"
)

// ProtocolVersion is the plane protocol this build speaks.
const ProtocolVersion = 1

// The plane's refusals. Each is a different instruction to a worker.
var (
	// ErrBadJoinToken means the token is unknown, wrong, or spent.
	ErrBadJoinToken = errors.New("join token refused")
	// ErrBadCredential means the worker must register again.
	ErrBadCredential = errors.New("worker credential refused")
	// ErrStaleEpoch means a newer session of this worker is open, so this process
	// must open its own or stop.
	ErrStaleEpoch = errors.New("a newer worker epoch is open")
	// ErrNonce means an answer was not to this heartbeat, so it proves nothing about
	// the server.
	ErrNonce = errors.New("heartbeat answered with the wrong nonce")
)

// ProtocolError refuses a worker the control plane cannot speak to. It names both
// sides, so an operator knows which one to upgrade.
type ProtocolError struct {
	Worker int
	Oldest int
	Newest int
}

func (e *ProtocolError) Error() string {
	if e.Worker < e.Oldest {
		return fmt.Sprintf("worker protocol %d is older than this control plane accepts (%d to %d): upgrade the worker", e.Worker, e.Oldest, e.Newest)
	}
	return fmt.Sprintf("worker protocol %d is newer than this control plane accepts (%d to %d): upgrade the control plane", e.Worker, e.Oldest, e.Newest)
}

// NegotiateProtocol accepts a worker one version behind the control plane and
// refuses an older one (NFR-COMPAT-2). A worker ahead of the control plane is
// refused too, because a control plane cannot promise what it has not seen.
func NegotiateProtocol(server, worker int) (int, error) {
	oldest := max(server-1, 1)
	if worker < oldest || worker > server {
		return 0, &ProtocolError{Worker: worker, Oldest: oldest, Newest: server}
	}
	return worker, nil
}

// WorkerAuth identifies a worker on every call after registration. It never appears
// in a JSON body: the HTTP link carries the credential in a header.
type WorkerAuth struct {
	WorkerID   string `json:"-"`
	Credential string `json:"-"`
}

// WorkerIdentity is what a worker keeps across restarts.
type WorkerIdentity struct {
	WorkerID   string
	Credential string
	Epoch      int64
}

// Registration exchanges a join token for a credential. The worker generates its
// ID, so a retry after a lost answer is the same registration.
type Registration struct {
	JoinToken string `json:"join_token"`
	WorkerID  string `json:"worker_id"`
	Name      string `json:"name"`
}

// WorkerCredential is the answer to a registration.
type WorkerCredential struct {
	WorkerID   string `json:"worker_id"`
	Credential string `json:"credential"`
}

// SessionRequest opens a new epoch.
type SessionRequest struct {
	Auth     WorkerAuth `json:"-"`
	Protocol int        `json:"protocol"`
	// Manifest is what this worker advertises, for this epoch.
	Manifest Manifest `json:"manifest"`
}

// Session is what the control plane decided for one epoch.
type Session struct {
	Epoch       int64 `json:"epoch"`
	Protocol    int   `json:"protocol"`
	HeartbeatMs int64 `json:"heartbeat_ms"`
	LeaseTTLMs  int64 `json:"lease_ttl_ms"`
	DesiredRev  int64 `json:"desired_rev"`
}

// HeldLease is a lease a worker believes it holds.
type HeldLease struct {
	WorkOrder string `json:"work_order"`
	Claim     string `json:"claim"`
	Fence     int64  `json:"fence"`
}

// Heartbeat is the application handshake. A process with an open port can still
// pass no traffic, so the server must read the store before it answers, and the
// worker must receive its own nonce back.
type Heartbeat struct {
	Auth          WorkerAuth  `json:"-"`
	Epoch         int64       `json:"epoch"`
	Protocol      int         `json:"protocol"`
	Nonce         string      `json:"nonce"`
	DesiredRev    int64       `json:"desired_rev"`
	EventsThrough int64       `json:"events_through"`
	Leases        []HeldLease `json:"leases"`
}

// RenewedLease is a lease that holds, with what a worker counts from its send.
type RenewedLease struct {
	WorkOrder string `json:"work_order"`
	Fence     int64  `json:"fence"`
	TTLMs     int64  `json:"ttl_ms"`
}

// FencedLease is a lease the worker no longer holds.
type FencedLease struct {
	WorkOrder    string `json:"work_order"`
	Fence        int64  `json:"fence"`
	CurrentFence int64  `json:"current_fence"`
}

// HeartbeatReply answers one heartbeat.
type HeartbeatReply struct {
	Nonce        string         `json:"nonce"`
	ServerTimeMs int64          `json:"server_time_ms"`
	DesiredRev   int64          `json:"desired_rev"`
	FleetMode    FleetMode      `json:"fleet_mode"`
	Leases       []RenewedLease `json:"leases"`
	Fenced       []FencedLease  `json:"fenced"`
}

// DesiredRequest reads desired state. Since is the revision the worker has.
type DesiredRequest struct {
	Auth  WorkerAuth
	Since int64
}

// DesiredLease is one lease this worker should act on.
type DesiredLease struct {
	WorkOrder string `json:"work_order"`
	ClaimID   string `json:"claim_id"`
	Fence     int64  `json:"fence"`
	State     string `json:"state"`
	// Lane is the lane to run under this claim. Empty means hold the lease and start
	// nothing. The fields below come with it, and they are what the lane starts from.
	Lane       string   `json:"lane,omitempty"`
	Repository string   `json:"repository,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Title      string   `json:"title,omitempty"`
	Statement  string   `json:"statement,omitempty"`
	Criteria   []string `json:"criteria,omitempty"`
	// Contract and ContractVersion are the pin the lane works to, which holds across its
	// generations whatever a later version of the contract says.
	Contract        string `json:"contract,omitempty"`
	ContractVersion int64  `json:"contract_version,omitempty"`
	// BaseRef and BaseCommit are where this lane's branch begins, resolved when the claim was made. A
	// worktree starts at the commit, so the branch under a lane never moves with the base (FR-BASE-1).
	// Empty on a claim made before the control plane resolved starts, and then the worker begins the
	// branch where its checkout is.
	BaseRef    string `json:"base_ref,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
	// Deadline is the work order's, in database time. Zero means it has none.
	Deadline int64 `json:"deadline,omitempty"`
	// Resets counts the human resets of this lane's breaker, for a worker to compare with
	// the count it has acted on.
	Resets int64 `json:"resets,omitempty"`
	// Respawns counts the requests for this lane's next generation, the same way.
	Respawns int64 `json:"respawns,omitempty"`
	// Overrides is this lane's own profile layer, as TOML.
	Overrides string `json:"overrides,omitempty"`
	// Resume says this claim brings a parked lane back, and what from.
	Resume *DesiredResume `json:"resume,omitempty"`
	// Messages are what no worker has acknowledged for this lane. The control plane offers them
	// until one does, and the worker deduplicates by ID (FR-MSG-3).
	Messages []DesiredMessage `json:"messages,omitempty"`
	// Pack is the newest version of the context pack this lane pinned.
	Pack *DesiredPack `json:"pack,omitempty"`
	// ParkWanted says a policy asked this lane to park, and why.
	ParkWanted string `json:"park_wanted,omitempty"`
	// Kind says what this work produces, which decides what its lane is told.
	Kind string `json:"kind,omitempty"`
	// Review is what a `contract_review` lane reads.
	Review *DesiredReview `json:"review,omitempty"`
}

// DesiredReview is a plan as a review lane reads it, at the contract versions its review covers.
type DesiredReview struct {
	Plan      string             `json:"plan"`
	Contracts []ReviewedContract `json:"contracts"`
}

// ReviewedContract is one contract version a review reads.
type ReviewedContract struct {
	Title     string   `json:"title"`
	Statement string   `json:"statement,omitempty"`
	Criteria  []string `json:"criteria,omitempty"`
}

// DesiredPack is what a generation reads of its pack. Text crosses as it was built and is never
// rendered again, so it stays byte-identical across generations.
type DesiredPack struct {
	ID             string `json:"id"`
	Version        int64  `json:"version"`
	Digest         string `json:"digest"`
	TokensEstimate int64  `json:"tokens_estimate"`
	Text           string `json:"text"`
}

// DesiredMessage is one message on its way to a worker. Durations cross the wire, because the
// worker's clock decides nothing about the ledger's time.
type DesiredMessage struct {
	ID          string `json:"id"`
	Lane        string `json:"lane"`
	Generation  int64  `json:"generation"`
	FromKind    string `json:"from_kind"`
	ReplyTo     string `json:"reply_to,omitempty"`
	Kind        string `json:"kind"`
	Body        string `json:"body"`
	Traceparent string `json:"traceparent,omitempty"`
	// AgeMs is how old it was when this was read, and the two windows count from its creation.
	AgeMs         int64 `json:"age_ms"`
	StaleAfterMs  int64 `json:"stale_after_ms"`
	ExpireAfterMs int64 `json:"expire_after_ms"`
}

// MessageFact is what a generation's first prompt states of a message no earlier session took in,
// or took in and did not answer.
type MessageFact struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	ReplyTo string `json:"reply_to,omitempty"`
	Body    string `json:"body"`
	// Unanswered says an earlier generation took it into a turn and no reply came.
	Unanswered bool `json:"unanswered,omitempty"`
}

// DesiredResume is what the ledger holds of the generation that parked, and Approvals says how
// each request it made came out, in words a session reads. It is all a worker
// that never ran the lane has. Durations cross the wire, because the worker's clock decides
// nothing about the ledger's time.
type DesiredResume struct {
	// Generation is the one that parked.
	Generation int64 `json:"generation"`
	// SessionID is what the vendor called its session.
	SessionID string `json:"session_id,omitempty"`
	// Worker holds that session.
	Worker string `json:"worker,omitempty"`
	// Handoff is the document it left, and Degraded says it has no narrative.
	Handoff  []byte `json:"handoff,omitempty"`
	Degraded bool   `json:"degraded,omitempty"`
	// Pending is what it was doing when it stopped.
	Pending string `json:"pending,omitempty"`
	// Refs are what a worker with no copy of the work must fetch.
	Branch      string `json:"branch,omitempty"`
	SnapshotRef string `json:"snapshot_ref,omitempty"`
	// Responded says a model response is on record, and SinceResponseMs how long ago that was
	// when this was read.
	Responded       bool  `json:"responded,omitempty"`
	SinceResponseMs int64 `json:"since_response_ms,omitempty"`
	// Approvals says how each request of the generation that parked came out.
	Approvals []string `json:"approvals,omitempty"`
	// Calls states each call a decision covers and no session has made yet.
	Calls []string `json:"calls,omitempty"`
	// Facts are what the next generation's first prompt owes, as they stood at this read.
	Facts []MessageFact `json:"facts,omitempty"`
}

// DesiredWoken is a woken lane this worker may claim.
type DesiredWoken struct {
	WorkOrder  string `json:"work_order"`
	Lane       string `json:"lane"`
	Repository string `json:"repository,omitempty"`
	// Fence is the work order's now, so one claim ID answers for one offer, and a claim given
	// back leaves the next one free to be made.
	Fence int64 `json:"fence"`
}

// DesiredArchive is a lane whose worktree should go.
type DesiredArchive struct {
	Lane       string `json:"lane"`
	Repository string `json:"repository,omitempty"`
	// Forced says a member accepts losing what no remote holds.
	Forced bool `json:"forced,omitempty"`
}

// DesiredState is what the control plane wants of one worker. Unchanged says the
// worker already holds this revision.
type DesiredState struct {
	Rev       int64            `json:"rev"`
	FleetMode FleetMode        `json:"fleet_mode"`
	Unchanged bool             `json:"unchanged,omitempty"`
	Leases    []DesiredLease   `json:"leases"`
	Archives  []DesiredArchive `json:"archives,omitempty"`
	// Woken lists the woken lanes offered to this worker, which claims them, because a wake
	// must bring a lane back with nobody there to dispatch it.
	Woken []DesiredWoken `json:"woken,omitempty"`
	// Orchestrator is the session this worker should run, which only a member can ask for.
	Orchestrator *DesiredOrchestrator `json:"orchestrator,omitempty"`
	// MaxRespawnsPerDay is what this worker's autonomy holds a lane's generations to, where zero
	// leaves that to its profile (FR-ORCH-6).
	MaxRespawnsPerDay int64 `json:"max_respawns_per_day,omitempty"`
	// Effects are what a person decided of this worker's unknown effects and it has not acted on.
	Effects []DesiredEffect `json:"effects,omitempty"`
}

// WatchRequest opens the stream of revision hints.
type WatchRequest struct {
	Auth  WorkerAuth
	After int64
}

// UploadEvent is one journaled event. (worker, Epoch, Seq) identifies it for good,
// so a retried upload stores it once. Epoch is the epoch it was journaled under,
// which may be older than the session that uploads it.
type UploadEvent struct {
	Epoch      int64  `json:"epoch"`
	Seq        int64  `json:"seq"`
	Type       string `json:"type"`
	WorkOrder  string `json:"work_order,omitempty"`
	ClaimID    string `json:"claim_id,omitempty"`
	Fence      int64  `json:"fence,omitempty"`
	Lane       string `json:"lane,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Payload    []byte `json:"payload,omitempty"`
	OccurredAt int64  `json:"occurred_at"`
}

// The upload types the ledger gives a meaning of its own. Any other type is a fenced
// note on the work order.
const (
	UploadLaneStarted     = "lane_started"
	UploadLaneEnded       = "lane_ended"
	UploadGenerationBound = "generation_bound"
	// UploadGenerationStarted moves a lane to its next generation.
	UploadGenerationStarted = "generation_started"
	// UploadHandoffStored keeps a generation's handoff document in the ledger.
	UploadHandoffStored = "handoff_stored"
	UploadLaneStatus    = "lane_status"
	// UploadAdmissionHeld says a worker held a lane back. It is a fenced note.
	UploadAdmissionHeld = "admission_held"
	// UploadLaneGuarded says how a stop left a worktree, and passes no fence.
	UploadLaneGuarded = "lane_guarded"
	// UploadLaneArchived says a worktree went.
	UploadLaneArchived = "lane_archived"
	// UploadLaneParking stores a lane's wake as it begins to park.
	UploadLaneParking = "lane_parking"
	// UploadLaneParked commits `parked`, once its process is gone.
	UploadLaneParked = "lane_parked"
	// UploadLaneResumed starts a woken lane's next generation under the claim that took it.
	UploadLaneResumed = "lane_resumed"
	// UploadGenerationResponded says a generation's model answered, which the ledger stamps
	// with its own time, for the resume table's cache clock.
	UploadGenerationResponded = "generation_responded"
	// UploadGenerationBaseline keeps what a generation held at its first call.
	UploadGenerationBaseline = "generation_baseline"
	// UploadMessage is a worker's word on one message. It passes no fence, because what happened
	// to a message stays true after its lane moved on.
	UploadMessage = "message_report"
	// UploadWakeSignal is a worker's word that its own sign-in is back, which belongs to no lane.
	UploadWakeSignal = "wake_signal"
	// UploadApprovalRaised carries a request a lane raised, and UploadApprovalAnswered what a
	// tool's end showed of one.
	UploadApprovalRaised   = "approval_raised"
	UploadApprovalAnswered = "approval_answered"
	// UploadDriverQualified carries what a worker's probe observed of a CLI version, which belongs
	// to the worker and no lane.
	UploadDriverQualified = "driver_qualified"
)

// EventDriverQualified records a probe's result for one worker's driver: a version, its surfaces,
// and their results, and nothing of a session or an account.
const EventDriverQualified = "worker.driver_qualified"

// UploadedEventID is the event ID of one uploaded event. The ledger's unique index
// on event IDs then makes upload idempotent.
func UploadedEventID(worker string, epoch, seq int64) string {
	return fmt.Sprintf("%s:%d:%d", worker, epoch, seq)
}

// UploadBatch is a run of events in sequence order.
type UploadBatch struct {
	Auth   WorkerAuth    `json:"-"`
	Epoch  int64         `json:"epoch"`
	Events []UploadEvent `json:"events"`
}

// The statuses of a rejected event.
const (
	UploadFenced  = "fenced"
	UploadInvalid = "invalid"
)

// UploadResult is the result of one rejected event.
type UploadResult struct {
	Seq          int64  `json:"seq"`
	Status       string `json:"status"`
	CurrentFence int64  `json:"current_fence,omitempty"`
	State        string `json:"state,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// UploadReply carries the high-water mark, plus a result for each event the ledger
// rejected. A rejected event is answered, so it counts toward the mark.
type UploadReply struct {
	HighWater int64          `json:"high_water"`
	Rejected  []UploadResult `json:"rejected,omitempty"`
}

// Worker is one registered worker, as a client sees it.
type Worker struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Epoch           int64    `json:"epoch"`
	Protocol        int      `json:"protocol"`
	Manifest        Manifest `json:"manifest"`
	LastHandshakeAt int64    `json:"last_handshake_at,omitempty"`
}

// WorkerRecord is a worker as the ledger holds it. ProofDigest is the digest of its
// credential, so the ledger stores no secret value (NFR-SEC-4).
type WorkerRecord struct {
	Worker
	ProofDigest string
}

// JoinGrant is a join token as the ledger holds it: by digest, and spent once.
type JoinGrant struct {
	ID        string
	Digest    string
	CreatedBy string
	UsedBy    string
}
