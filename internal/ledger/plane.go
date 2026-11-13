package ledger

import (
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/roster"
)

// ProtocolVersion is the plane protocol this build speaks. Version 2 names the project on every item
// (ADR-0021). Version 3 adds the assistant's sessions: desired state's assistants, the worker routes that
// make a generation's credential and record a reply, and uploads from an assistant's run (ADR-0024).
const ProtocolVersion = 3

// ProtocolAssistants is the oldest protocol a worker runs the assistant on.
const ProtocolAssistants = 3

// ProtocolOldest is the oldest plane protocol any build serves. Version 1 named no project and served
// only the installation's default project; a worker that speaks it is refused and must be upgraded.
const ProtocolOldest = 2

// RevisionInstallation keys the installation's own revision among a desired state's revisions. No
// project takes this ID.
const RevisionInstallation = string(InstallationScope)

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
		return fmt.Sprintf("worker protocol %d is no longer served (this control plane accepts %d to %d): upgrade the worker", e.Worker, e.Oldest, e.Newest)
	}
	return fmt.Sprintf("worker protocol %d is newer than this control plane accepts (%d to %d): upgrade the control plane", e.Worker, e.Oldest, e.Newest)
}

// NegotiateProtocol accepts a worker one version behind the control plane and
// refuses an older one (NFR-COMPAT-2), and any version before ProtocolOldest. A
// worker ahead of the control plane is refused too, because a control plane
// cannot promise what it has not seen.
func NegotiateProtocol(server, worker int) (int, error) {
	oldest := max(server-1, ProtocolOldest)
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
	// Project is the project whose work order this is. Empty names the plane's only project.
	Project   string `json:"project,omitempty"`
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
	Project   string `json:"project,omitempty"`
	WorkOrder string `json:"work_order"`
	Fence     int64  `json:"fence"`
	TTLMs     int64  `json:"ttl_ms"`
}

// FencedLease is a lease the worker no longer holds.
type FencedLease struct {
	Project      string `json:"project,omitempty"`
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
	// Revisions is where each project's desired state and the installation's stand, on a plane that
	// serves several projects. A worker whose cached revisions differ reads desired state again.
	Revisions map[string]int64 `json:"revisions,omitempty"`
}

// DesiredRequest reads desired state. Since is the revision the worker has, and Revisions what it has
// of each project, on a plane that serves several.
type DesiredRequest struct {
	Auth      WorkerAuth
	Since     int64
	Revisions map[string]int64
}

// DesiredProject is one project a worker serves: its name, and the fleet mode it gives its lanes.
type DesiredProject struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	FleetMode FleetMode `json:"fleet_mode"`
}

// DesiredLease is one lease this worker should act on.
type DesiredLease struct {
	// Project is the project the work order belongs to. Empty names the plane's only project.
	Project   string `json:"project,omitempty"`
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
	// CriterionIDs are the IDs of the criteria above, in the same order, so a worker can hold a review
	// lane's report to them without taking the ID out of the text.
	CriterionIDs []string `json:"criterion_ids,omitempty"`
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
	// Continues names the in-flight branch the lane finishes, and ContinuesRef and ContinuesCommit the ref
	// it was read at and its tip, where the lane's own branch begins (ADR-0023). BaseCommit is then where
	// that branch left the base. The lane never moves the branch it continues.
	Continues       string `json:"continues,omitempty"`
	ContinuesRef    string `json:"continues_ref,omitempty"`
	ContinuesCommit string `json:"continues_commit,omitempty"`
	// Reads are the other repositories of the project the lane reads, read-only, at the commits its claim
	// recorded (ADR-0022).
	Reads []RepositoryRead `json:"reads,omitempty"`
	// RepositoryName is what the project calls Repository.
	RepositoryName string `json:"repository_name,omitempty"`
	// Inventory asks the worker to write the inventory of the lane's repository and of every repository it
	// reads into the lane's context folder before the lane starts, as it does for a lane that adopts a
	// project (ADR-0023).
	Inventory bool `json:"inventory,omitempty"`
	// Adopt says the lane adopts an existing project (ADR-0023): a planning lane whose brief onboards what the
	// project already holds and plans what remains.
	Adopt bool `json:"adopt,omitempty"`
	// Deadline is the work order's, in database time. Zero means it has none.
	Deadline int64 `json:"deadline,omitempty"`
	// Resets counts the human resets of this lane's breaker, for a worker to compare with
	// the count it has acted on.
	Resets int64 `json:"resets,omitempty"`
	// WindowResets counts an orchestrator's resets of this lane's breaker, which clear its window and leave
	// its day, the same way (ADR-0014).
	WindowResets int64 `json:"window_resets,omitempty"`
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
	// Attempt is the work a `review` lane judges.
	Attempt *DesiredAttempt `json:"attempt,omitempty"`
	// Rework is why the work came back for this attempt, when it did.
	Rework *DesiredRework `json:"rework,omitempty"`
	// Agent is the roster agent the lane runs as, and Roster the roster its claim pinned, which the
	// worker renders into the session on every start and resume (ADR-0025). A claim from before the roster
	// pinned none.
	Agent  string           `json:"agent,omitempty"`
	Roster *roster.Resolved `json:"roster,omitempty"`
}

// RepositoryRead is another repository of a lane's project that the lane reads, read-only, at a commit
// recorded on its claim when it starts (ADR-0022). The worker checks it out with no branch, and the brief
// names it. A repository that could not be read has no commit, and Detail says why.
type RepositoryRead struct {
	Repository string `json:"repository"`
	Name       string `json:"name,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// DesiredRework is why a work order's last attempt went back: the reason a member gave, or the review whose
// failed criteria sent it, with each finding. The lane that takes the work next reads it in its brief, so it
// does not rediscover what was wrong.
type DesiredRework struct {
	// Review is the review work order whose verdicts sent the work back. Empty for a member's own reason.
	Review string `json:"review,omitempty"`
	// Reason is the reason recorded with the move.
	Reason string `json:"reason"`
	// Findings are the failed criteria, each with the review's reasons and citations.
	Findings []DesiredFinding `json:"findings,omitempty"`
}

// DesiredFinding is one failed criterion with what the review said of it.
type DesiredFinding struct {
	Criterion string   `json:"criterion"`
	Text      string   `json:"text,omitempty"`
	Reasons   string   `json:"reasons"`
	Citations []string `json:"citations,omitempty"`
}

// DesiredAttempt is the work a `review` lane judges: one attempt of another work order, at the commit its
// lane ended on. The review's own BaseRef and BaseCommit are that branch and commit, so its worktree
// starts there, and the diff it reads begins at the commit that attempt's lane began from.
type DesiredAttempt struct {
	// WorkOrder and Title name the work order under review.
	WorkOrder string `json:"work_order"`
	Title     string `json:"title,omitempty"`
	// Branch is the branch that attempt left, and Commit its head: the commit under review.
	Branch string `json:"branch"`
	Commit string `json:"commit"`
	// BaseRef and BaseCommit are where that attempt's lane began its branch. Empty when none was recorded,
	// and then there is no diff to read.
	BaseRef    string `json:"base_ref,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
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
	Project    string `json:"project,omitempty"`
	WorkOrder  string `json:"work_order"`
	Lane       string `json:"lane"`
	Repository string `json:"repository,omitempty"`
	// Fence is the work order's now, so one claim ID answers for one offer, and a claim given
	// back leaves the next one free to be made.
	Fence int64 `json:"fence"`
}

// DesiredArchive is a lane whose worktree should go.
type DesiredArchive struct {
	Project    string `json:"project,omitempty"`
	Lane       string `json:"lane"`
	Repository string `json:"repository,omitempty"`
	// Forced says a member accepts losing what no remote holds.
	Forced bool `json:"forced,omitempty"`
}

// DesiredState is what the control plane wants of one worker. Unchanged says the
// worker already holds this revision.
type DesiredState struct {
	Rev       int64     `json:"rev"`
	FleetMode FleetMode `json:"fleet_mode"`
	Unchanged bool      `json:"unchanged,omitempty"`
	// Revisions and Projects are each project this worker serves, on a plane that serves several, and
	// every item below names its project. Revisions holds the installation's own revision too.
	Revisions map[string]int64 `json:"revisions,omitempty"`
	Projects  []DesiredProject `json:"projects,omitempty"`
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
	// Forge is what this worker is asked to do on a forge and has not reported done (ADR-0020).
	Forge []DesiredForge `json:"forge,omitempty"`
	// ForgeRepositories are the repositories whose review target needs the forge, for this worker to report
	// its forge sign-in's health in each it keeps a checkout of.
	ForgeRepositories []DesiredForgeRepository `json:"forge_repositories,omitempty"`
	// PullRequests are the pull requests this worker opened whose work is still open, for it to watch.
	PullRequests []DesiredPullRequest `json:"pull_requests,omitempty"`
	// Assistants are the conversations a member runs on this worker, each the assistant's session for one
	// member in one project (ADR-0024).
	Assistants []DesiredAssistant `json:"assistants,omitempty"`
	// Forget are the deleted conversations whose text this worker still holds, until it says it forgot it.
	Forget []DesiredForget `json:"forget,omitempty"`
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
	Epoch int64 `json:"epoch"`
	Seq   int64 `json:"seq"`
	// Project is the project of the lane or work order it is about. Empty names the plane's only project,
	// or, for what belongs to no lane, the worker itself.
	Project    string `json:"project,omitempty"`
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
	// UploadApprovalRaised carries a request a lane raised, UploadApprovalAnswered what a tool's end
	// showed of one, and UploadApprovalVoid that the call a hook holds lands somewhere else than the
	// request says.
	UploadApprovalRaised   = "approval_raised"
	UploadApprovalAnswered = "approval_answered"
	UploadApprovalVoid     = "approval_void"
	// UploadDriverQualified carries what a worker's probe observed of a CLI version, which belongs
	// to the worker and no lane.
	UploadDriverQualified = "driver_qualified"
	// UploadAssistantForgotten carries a worker's word that it removed what it held of a deleted
	// conversation, which belongs to no lane.
	UploadAssistantForgotten = "assistant_forgotten"
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

// RunKindAssistant is the kind of a run that is the assistant's session, which a worker carries in the run's
// lease record. The run has no work order, as an orchestrator's has none.
const RunKindAssistant = "assistant"

// DesiredAssistant is one conversation whose session this worker should run. Lane is the run lane this start
// named, which no other start reuses, and Generation the conversation's generation the run began at: the
// run's generation n is the conversation's Generation+n-1. Fresh says the session begins with an empty
// context, after a clear, and Compactions counts the compactions the member asked of this run.
type DesiredAssistant struct {
	Project      string           `json:"project,omitempty"`
	Conversation string           `json:"conversation"`
	Member       string           `json:"member"`
	MemberName   string           `json:"member_name,omitempty"`
	Lane         string           `json:"lane"`
	Generation   int64            `json:"generation"`
	IdleStopMs   int64            `json:"idle_stop_ms"`
	Fresh        bool             `json:"fresh,omitempty"`
	Compactions  int64            `json:"compactions,omitempty"`
	Messages     []DesiredMessage `json:"messages,omitempty"`
}

// DesiredForget is a deleted conversation this worker ran: the run lanes whose messages and records its
// journal keeps, and, by the conversation, the directory its sessions ran in. It names no text. The worker
// removes all of it once none of those runs goes on, and says so with UploadAssistantForgotten.
type DesiredForget struct {
	Project      string   `json:"project,omitempty"`
	Conversation string   `json:"conversation"`
	Lanes        []string `json:"lanes"`
}

// AssistantForgotten is a worker's word that it removed what it held of a deleted conversation.
type AssistantForgotten struct {
	Conversation string `json:"conversation"`
}

// AssistantCredentialRequest asks for the credential of one generation of an assistant's run, where the
// generation is the run's own. It carries a worker's proof like any other call.
type AssistantCredentialRequest struct {
	Auth         WorkerAuth `json:"-"`
	Epoch        int64      `json:"epoch"`
	Project      string     `json:"project"`
	Conversation string     `json:"conversation"`
	Lane         string     `json:"lane"`
	Generation   int64      `json:"generation"`
}

// AssistantGrant is a generation's credential, whose token this is the one time anyone sees, with what the
// session's first prompt reads: the project's facts, and the conversation's newest entries, oldest first.
type AssistantGrant struct {
	Credential string                  `json:"credential"`
	Token      string                  `json:"token"`
	Context    AssistantContext        `json:"context"`
	History    []AssistantHistoryEntry `json:"history,omitempty"`
}

// AssistantContext is what an assistant's pack states of its project beside its role.
type AssistantContext struct {
	Project      string                `json:"project"`
	ProjectName  string                `json:"project_name"`
	Member       string                `json:"member"`
	MemberName   string                `json:"member_name,omitempty"`
	Repositories []AssistantRepository `json:"repositories,omitempty"`
	Plans        []AssistantPlan       `json:"plans,omitempty"`
	Work         map[string]int64      `json:"work,omitempty"`
}

// AssistantRepository is one active repository of the project, and the branch its read-only tree follows.
type AssistantRepository struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Base string `json:"base"`
}

// AssistantPlan is one open plan of the project.
type AssistantPlan struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// AssistantHistoryEntry is one entry of a conversation as a fresh generation's first prompt states it.
type AssistantHistoryEntry struct {
	Kind string `json:"kind"`
	Body string `json:"body,omitempty"`
	At   int64  `json:"at"`
}

// AssistantEntryRequest is a reply an assistant's session wrote. ID names the vendor's message, so a retry
// records it once.
type AssistantEntryRequest struct {
	Auth         WorkerAuth `json:"-"`
	Epoch        int64      `json:"epoch"`
	Project      string     `json:"project"`
	Conversation string     `json:"conversation"`
	Lane         string     `json:"lane"`
	ID           string     `json:"id"`
	Body         string     `json:"body"`
}
