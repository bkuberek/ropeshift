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
	// LastResponseAt is database time, when the worker's report of the session's last model
	// response was recorded. It can run late and never early, and a late value can only choose
	// a costlier path.
	LastResponseAt int64 `json:"last_response_at,omitempty"`
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

// ParkingStart is a worker's wake on its way to the ledger. The worker generates the ID, so
// storing it twice stores it once.
type ParkingStart struct {
	Wake string `json:"wake"`
	Expr string `json:"expr"`
}

// Checkpoint is what a parked generation left. Pushed says the work is on a remote, which
// decides where the lane can resume.
type Checkpoint struct {
	Lane       string `json:"lane_id"`
	Generation int64  `json:"generation"`
	Branch     string `json:"branch,omitempty"`
	Head       string `json:"head_sha,omitempty"`
	// SnapshotRef holds what no commit does.
	SnapshotRef string `json:"snapshot_ref,omitempty"`
	Pushed      bool   `json:"pushed,omitempty"`
	// HandoffArtifact names the document this generation left.
	HandoffArtifact string `json:"handoff_artifact_id,omitempty"`
	// PendingInstruction is what the generation was doing when it stopped. A stopped turn is
	// not continued, so the resume prompt states it again.
	PendingInstruction string `json:"pending_instruction,omitempty"`
	CreatedAt          int64  `json:"created_at,omitempty"`
}

// ParkedCommit is a worker's report that a lane's process is gone.
type ParkedCommit struct {
	Wake       string     `json:"wake"`
	Checkpoint Checkpoint `json:"checkpoint"`
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
