package ledger

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The states a member asks of an orchestrator.
const (
	OrchestratorRunning = "running"
	OrchestratorStopped = "stopped"
)

// The orchestrator's events.
const (
	// EventOrchestratorDesired records what a member asked, and only a member can ask it: the first
	// passphrase needs no session, which is safe only while no agent runs before a member says so.
	EventOrchestratorDesired = "orchestrator.desired"
	// EventOrchestratorReported records what its worker said of it.
	EventOrchestratorReported = "orchestrator.reported"
)

// SubjectOrchestrator names the subject of those events.
const SubjectOrchestrator = "orchestrator"

// orchestratorPrefix starts every orchestrator's ID, and every lane an orchestrator runs under.
const orchestratorPrefix = "orchestrator-"

// Orchestrator is the one agent session of a worker (FR-ORCH-1). It has no work order and no
// outcome, so nothing here fences it: a worker speaks only for its own.
type Orchestrator struct {
	ID       string `json:"id"`
	WorkerID string `json:"worker_id"`
	Desired  string `json:"desired_state"`
	// Run counts a member's starts. A journal keeps a lane that ended, so each start names a lane of
	// its own.
	Run int64 `json:"run"`
	// Resets counts a member's resets of a breaker that tripped.
	Resets     int64  `json:"resets"`
	Actual     string `json:"actual_state,omitempty"`
	Generation int64  `json:"current_generation,omitempty"`
}

// OrchestratorOf names a worker's orchestrator. One per worker is the rule, and the name holds it.
func OrchestratorOf(worker string) string { return orchestratorPrefix + worker }

// IsOrchestratorLane says a lane is an orchestrator's, which an upload with no work order must be. No
// other lane may take such a name.
func IsOrchestratorLane(lane string) bool { return strings.HasPrefix(lane, orchestratorPrefix) }

// Lane names the record this start runs under. A lane's name reaches a session's socket path, which
// has a limit, and a journal holds one worker's lanes, so it carries no worker's ID.
func (o Orchestrator) Lane() string { return orchestratorPrefix + strconv.FormatInt(o.Run, 10) }

// Belongs says a lane is a start this orchestrator made, whichever one.
func (o Orchestrator) Belongs(lane string) bool {
	text, found := strings.CutPrefix(lane, orchestratorPrefix)
	run, err := strconv.ParseInt(text, 10, 64)
	return found && err == nil && run >= 1 && run <= o.Run
}

// DesiredOrchestrator is what a worker should run. Absent, it runs none.
type DesiredOrchestrator struct {
	Lane   string `json:"lane"`
	Resets int64  `json:"resets,omitempty"`
}

// OrchestratorReport is what a worker said, in the shape Replay reads.
type OrchestratorReport struct {
	Lane       string `json:"lane"`
	Generation int64  `json:"generation"`
	Actual     string `json:"actual_state,omitempty"`
	Kind       string `json:"kind"`
}

// Apply moves the projection. A report of a start this one replaced says nothing of it.
func (o Orchestrator) Apply(report OrchestratorReport) Orchestrator {
	if report.Lane != o.Lane() {
		return o
	}
	if report.Actual != "" {
		o.Actual = report.Actual
	}
	o.Generation = max(o.Generation, report.Generation)
	return o
}

// ReplayOrchestrators rebuilds the projection from its events, so a store can be held to what it
// was told.
func ReplayOrchestrators(events []Event) (map[string]Orchestrator, error) {
	out := map[string]Orchestrator{}
	for _, event := range events {
		switch event.Type {
		case EventOrchestratorDesired:
			var row Orchestrator
			if err := json.Unmarshal(event.Data, &row); err != nil {
				return nil, err
			}
			out[row.ID] = row
		case EventOrchestratorReported:
			var report OrchestratorReport
			if err := json.Unmarshal(event.Data, &report); err != nil {
				return nil, err
			}
			if row, ok := out[event.Subject.ID]; ok {
				out[event.Subject.ID] = row.Apply(report)
			}
		}
	}
	return out, nil
}

// The things an orchestrator can be woken for.
const (
	WakeWork     = "work"
	WakeQuestion = "question"
	WakeFinished = "finished_lane"
	WakeStalled  = "stalled_lane"
	WakeOverdue  = "overdue_lane"
	WakeApproval = "approval"
)

// WakeItem is one thing to judge. Stamp says how it stands, so an item that changed counts again and
// one that did not counts once. Digest is what a decision on a request binds to.
type WakeItem struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Stamp  int64  `json:"stamp"`
	Text   string `json:"text"`
	Digest string `json:"digest,omitempty"`
}

// Key names an item as it stands.
func (i WakeItem) Key() string { return i.Kind + "/" + i.ID + "/" + strconv.FormatInt(i.Stamp, 10) }

// WakeDigest is what a worker's orchestrator could judge now.
type WakeDigest struct {
	Items []WakeItem `json:"items"`
}

// WakeRequest reads a digest. It carries a worker's proof like any other call.
type WakeRequest struct {
	Auth  WorkerAuth `json:"-"`
	Epoch int64      `json:"epoch"`
}

// The acts a worker carries for its orchestrator. Each is one route, and the ledger holds each to
// what a person could do, for this worker's orchestrator and nobody else's.
const (
	VerbCandidates = "candidates"
	VerbClaim      = "claim"
	VerbDecline    = "decline"
	VerbStart      = "start"
	VerbStop       = "stop"
	VerbNext       = "next"
	VerbTake       = "take"
	VerbLetGo      = "let_go"
	VerbClimb      = "climb"
	VerbEscalate   = "escalate"
	VerbResolve    = "resolve"
	VerbMerge      = "merge"
	VerbRule       = "rule"
	VerbAccept     = "accept"
)

// OrchestratorCall is one such act. Lane names the run it speaks for, so a call from a start that was
// replaced acts for nobody. Body carries what a verb reads that this package has no type for.
type OrchestratorCall struct {
	Auth      WorkerAuth      `json:"-"`
	Epoch     int64           `json:"epoch"`
	Lane      string          `json:"lane"`
	WorkOrder string          `json:"work_order,omitempty"`
	Question  string          `json:"question,omitempty"`
	Target    string          `json:"target,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
}

// OrchestratorReply is what such an act found.
type OrchestratorReply struct {
	Result json.RawMessage `json:"result,omitempty"`
}

// EventOrchestratorDeclined records an item an orchestrator passed on, with its reason.
const EventOrchestratorDeclined = "orchestrator.declined"
