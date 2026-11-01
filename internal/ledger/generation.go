package ledger

import "fmt"

// StartReason says why a generation began (FR-LANE-10). A vendor's session ID cannot say,
// because one session can span several generations.
type StartReason string

// The start reasons.
const (
	StartInitial      StartReason = "initial"
	StartHandoff      StartReason = "handoff"
	StartResume       StartReason = "resume"
	StartCrashRestart StartReason = "crash_restart"
	StartUserRespawn  StartReason = "user_respawn"
	StartReset        StartReason = "reset"
	StartClear        StartReason = "clear"
)

// Generation is one continuous run of an agent within a lane. Ropeshift assigns its identity,
// and SessionID is only what the vendor called it, so nothing is unique on that.
type Generation struct {
	Lane        string      `json:"lane_id"`
	Number      int64       `json:"number"`
	StartReason StartReason `json:"start_reason"`
	EndReason   string      `json:"end_reason,omitempty"`
	SessionID   string      `json:"vendor_session_id,omitempty"`
	// HandoffIn and HandoffOut name the artifacts this generation read and left.
	HandoffIn  string `json:"handoff_in,omitempty"`
	HandoffOut string `json:"handoff_out,omitempty"`
	StartedAt  int64  `json:"started_at"`
	EndedAt    int64  `json:"ended_at,omitempty"`
}

// HandoffRecord is a handoff document in the ledger, so a later generation on another machine
// can read it. Body is the document's only form, and Digest pins it.
type HandoffRecord struct {
	ArtifactID string
	Lane       string
	Generation int64
	Digest     string
	Degraded   bool
	Body       []byte
	CreatedAt  int64
}

// HandoffUpload is a worker's handoff on its way to the ledger.
type HandoffUpload struct {
	Body     []byte `json:"body"`
	Digest   string `json:"digest"`
	Degraded bool   `json:"degraded,omitempty"`
}

// GenerationStart says why a worker began a generation. An upload with no payload is a crash
// restart, which is what every journal held before handoffs.
type GenerationStart struct {
	Reason StartReason `json:"reason"`
}

// LaneFactsRequest asks what the ledger holds about a lane a worker is handing off.
type LaneFactsRequest struct {
	Auth      WorkerAuth `json:"-"`
	Epoch     int64      `json:"epoch"`
	WorkOrder string     `json:"work_order"`
	Fence     int64      `json:"fence"`
	Lane      string     `json:"lane"`
}

// CheckState is one check's name and how it stands.
type CheckState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// LaneFacts is what a handoff states and only the ledger knows. Each list is empty, not nil,
// when the ledger read it and found nothing.
type LaneFacts struct {
	OpenQuestions   []string     `json:"open_questions"`
	OpenAssumptions []string     `json:"open_assumptions"`
	Checks          []CheckState `json:"checks"`
}

// HandoffURI names a handoff in the ledger, where its body lives.
func HandoffURI(lane string, generation int64) string {
	return fmt.Sprintf("handoff:%s/%d", lane, generation)
}
