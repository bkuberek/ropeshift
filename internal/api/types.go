package api

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The error codes of the API. A client decides on the code, never on the text.
const (
	CodeInvalid   = "invalid"
	CodeNotFound  = "not_found"
	CodeForbidden = "forbidden"
	// CodeNotAcceptable says a check failed, so the work goes back for rework.
	CodeNotAcceptable = "not_acceptable"
	CodeLost          = "lost"
	CodeIneligible    = "ineligible"
	CodeFenced        = "fenced"
	CodeLapsed        = "lapsed"
	CodeNotLapsed     = "not_lapsed"
	CodeIllegalMove   = "illegal_move"
	// CodeMoved refuses a recovery act whose subject another actor already moved, which records nothing.
	CodeMoved = "moved"
	// CodeUnpushedWork refuses a removal that could lose work no remote holds.
	CodeUnpushedWork = "unpushed_work"
	CodeUnavailable  = "unavailable"
	// CodeSessionRequired means a call needs an authenticated member, and came with none
	// that is live.
	CodeSessionRequired = "session_required"
	// CodeBadPassphrase means a sign-in's passphrase does not match.
	CodeBadPassphrase = "bad_passphrase"
	// CodeResolved means a question already has its answer.
	CodeResolved = "resolved"
	// CodeNotEnrolled means nobody can sign in, because no passphrase is set.
	CodeNotEnrolled = "not_enrolled"
	// CodeOneWay means a one-way choice belongs to a human.
	CodeOneWay = "one_way"
	// CodeNotHeld means the caller does not hold the question it worked.
	CodeNotHeld = "not_held"
	// CodeWrongRung means a ruling does not belong to the rung its question stands on.
	CodeWrongRung = "wrong_rung"
	// CodeAssumptionLimit means a work order holds all the assumptions the policy allows.
	CodeAssumptionLimit = "assumption_limit"
	// CodeOverlap means a decision has not said how it stands to the decisions it overlaps.
	CodeOverlap = "overlap"
	// CodeDecisionEnded means a decision was already overturned or replaced.
	CodeDecisionEnded = "decision_ended"
	// CodeRiskNotAcknowledged means a subscription worker stays below unattended until a member
	// acknowledges the stated risk for it.
	CodeRiskNotAcknowledged = "risk_not_acknowledged"
	// CodeWindowOpen means this worker has a run window that stands.
	CodeWindowOpen = "window_open"
	// CodeStaleStatement means an acknowledgement names text other than the current statement.
	CodeStaleStatement = "stale_statement"
	// CodeNoProject means a path names a project the installation does not hold.
	CodeNoProject = "no_project"
	// CodeRepositoryHeld means another project holds the repository, which the message names.
	CodeRepositoryHeld = "repository_held"
	// CodeRepositoryState means a repository's record refuses the change as it stands, which the message says.
	CodeRepositoryState = "repository_state"
	// CodeProjectExists means another project holds the ID or the name.
	CodeProjectExists = "project_exists"
	// CodeProjectState means a project's state refuses a lifecycle step, which the message names.
	CodeProjectState = "project_state"
	// CodeProjectArchived means a change to an archived project, which is read-only until restored.
	CodeProjectArchived = "project_archived"
	// CodeAlreadyPlanned refuses to adopt a project that already has an approved plan (ADR-0023).
	CodeAlreadyPlanned = "already_planned"
	// CodeRosterConflict refuses a roster change that conflicts with what the roster holds: an agent it
	// already holds, a reset of nothing, or an agent something still names (ADR-0025).
	CodeRosterConflict = "roster_conflict"
)

// ErrorBody is every error response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail says what went wrong. A fenced write carries the current fence
// (FR-WO-3), so a holder learns what replaced it.
type ErrorDetail struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	CurrentFence int64  `json:"current_fence,omitempty"`
	State        string `json:"state,omitempty"`
	// Oldest and Newest say which protocols a refusing control plane accepts.
	Worker int `json:"worker_protocol,omitempty"`
	Oldest int `json:"oldest_protocol,omitempty"`
	Newest int `json:"newest_protocol,omitempty"`
	// Decisions names the decisions an overlap refusal asks about.
	Decisions []string `json:"decisions,omitempty"`
}

// Health answers only after the server read the store.
type Health struct {
	Status   string `json:"status"`
	Revision int64  `json:"revision"`
	// Web is the address the daemon's web UI listens on, as `host:port`, with the port it bound even
	// when it asked the kernel to pick one. It is empty when the daemon serves no web UI.
	Web string `json:"web,omitempty"`
}

// CreateWorkOrder asks for a work order that lives only in the ledger, with the
// contract it pins. The client generates ID, so a retry returns the first answer.
type CreateWorkOrder struct {
	ID           string               `json:"id"`
	Kind         workorders.Kind      `json:"kind,omitempty"`
	Title        string               `json:"title"`
	Statement    string               `json:"statement,omitempty"`
	Criteria     []planning.Criterion `json:"criteria,omitempty"`
	Priority     int64                `json:"priority,omitempty"`
	Size         string               `json:"size,omitempty"`
	Requirements []string             `json:"requirements,omitempty"`
	Needs        map[string]string    `json:"needs,omitempty"`
	DependsOn    []string             `json:"depends_on,omitempty"`
	RepositoryID string               `json:"repository_id,omitempty"`
	// Adopt makes planning work adopt an existing project (ADR-0023). A project with an approved plan
	// refuses it.
	Adopt bool `json:"adopt,omitempty"`
	// Agent names the roster agent the work's lane runs as (ADR-0025). Empty leaves it to the default
	// mapping.
	Agent string `json:"agent,omitempty"`
}

// WorkOrderList is the answer to a list.
type WorkOrderList struct {
	WorkOrders []workorders.WorkOrder `json:"work_orders"`
}

// ReviewResult says where a passed review left a work order.
type ReviewResult struct {
	State    workorders.State          `json:"state"`
	Failures []workorders.ReadyFailure `json:"failures,omitempty"`
}

// ClaimRequest asks for a work order. The client generates ClaimID, so a retry
// after a timeout returns the original grant. A TTLMs of zero takes the policy's
// lease.
type ClaimRequest struct {
	ClaimID  string `json:"claim_id"`
	WorkerID string `json:"worker_id"`
	TTLMs    int64  `json:"ttl_ms,omitempty"`
	// WorkOrder names the work a worker claims on the plane protocol's route, which has no work order
	// in its path, and Project the project it belongs to.
	WorkOrder string `json:"work_order,omitempty"`
	Project   string `json:"project,omitempty"`
}

// ContractPin names the contract version a work order pinned.
type ContractPin struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// Grant is everything a lane needs to start.
type Grant struct {
	WorkOrder    string       `json:"work_order"`
	ClaimID      string       `json:"claim_id"`
	Fence        int64        `json:"fence"`
	TTLMs        int64        `json:"ttl_ms"`
	ResumeLaneID string       `json:"resume_lane_id,omitempty"`
	Branch       string       `json:"branch,omitempty"`
	Contract     *ContractPin `json:"contract,omitempty"`
	Replayed     bool         `json:"replayed,omitempty"`
}

// StartLane asks the worker holding a claim to run a lane. The client generates
// LaneID, so a retry names the same lane.
type StartLane struct {
	LaneID string `json:"lane_id"`
	// Overrides is this lane's own profile layer, as TOML.
	Overrides string `json:"overrides,omitempty"`
}

// LaneDispatched says which lease a lane will run under.
type LaneDispatched struct {
	LaneID    string `json:"lane_id"`
	WorkOrder string `json:"work_order"`
	ClaimID   string `json:"claim_id"`
	Fence     int64  `json:"fence"`
}

// Fleet is the one control for a whole fleet, and the caps a claim is held to.
type Fleet struct {
	Mode ledger.FleetMode `json:"fleet_mode"`
	ledger.Caps
}

// SetFleetMode changes the fleet's mode.
type SetFleetMode struct {
	Mode ledger.FleetMode `json:"fleet_mode"`
}

// ArchiveLane asks for a lane's worktree to be removed. Force accepts losing what no
// remote holds, and only a member can send it.
type ArchiveLane struct {
	LaneID string `json:"lane_id"`
	Force  bool   `json:"force,omitempty"`
}

// ReleaseRequest gives a claim back.
type ReleaseRequest struct {
	Fence int64 `json:"fence"`
}

// ReworkRequest sends completed work back for another attempt.
type ReworkRequest struct {
	Reason string `json:"reason"`
}

// Accepted says where acceptance left a work order.
type Accepted struct {
	State workorders.State `json:"state"`
}

// NoticeList says how each notice stands on each channel.
type NoticeList struct {
	Notices []ledger.NoticeLine `json:"notices"`
}

// CancelWork ends a work order for good, and says why.
type CancelWork struct {
	Reason string `json:"reason"`
}

// ReassignRequest ends a lapsed holder's ownership.
type ReassignRequest struct {
	Reason string `json:"reason"`
}

// WriteRequest is one write under a fence. The client generates WriteID, so a write
// retried after a lost answer stores one event.
type WriteRequest struct {
	WriteID    string `json:"write_id"`
	ClaimID    string `json:"claim_id"`
	Fence      int64  `json:"fence"`
	Lane       string `json:"lane,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Payload    []byte `json:"payload,omitempty"`
}

// WriteResponse names the event a write stored.
type WriteResponse struct {
	ID  string `json:"id"`
	Rev int64  `json:"rev"`
}

// SourceList is the answer to a list of sources.
type SourceList struct {
	Sources []planning.SourceBinding `json:"sources"`
}
