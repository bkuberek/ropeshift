package ledger

// The freshness every state carries when it is read (FR-LANE-6, FR-UI-5).
const (
	// FreshnessObserved means a worker confirmed the value within the freshness window.
	FreshnessObserved = "observed"
	// FreshnessLastReported means it is the last value received.
	FreshnessLastReported = "last_reported"
)

// SubjectLane is what a lane's own events are about.
const SubjectLane = "lane"

// EventLaneStatus records a lane's status changing.
const EventLaneStatus = "lane.status"

// EventLaneReset records a member resetting a lane's breaker.
const EventLaneReset = "lane.reset"

// The events of a lane's stop and removal.
const (
	// EventLaneGuarded records how a stop left a worktree. It is the one thing a fenced
	// holder may still say, because it adds no ownership and keeps a person's work in view.
	EventLaneGuarded = "lane.guarded"
	// EventLaneArchiveRequested records a request to take a worktree away.
	EventLaneArchiveRequested = "lane.archive_requested"
	// EventLaneArchived records that a worktree went.
	EventLaneArchived = "lane.archived"
)

// ArtifactSalvage is the kind of a ref a stop left. A later unit adds the other kinds.
const ArtifactSalvage = "salvage"

// Artifact is something a lane left. URI names it, and Digest pins its content.
type Artifact struct {
	ID        string `json:"id"`
	Lane      string `json:"lane_id"`
	Kind      string `json:"kind"`
	URI       string `json:"uri"`
	Digest    string `json:"digest,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// LaneGuard is what a worker says about how a stop left a worktree.
type LaneGuard struct {
	UnpushedWork bool   `json:"unpushed_work"`
	Ref          string `json:"ref,omitempty"`
	Commit       string `json:"commit,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// StateEnded is a lane's last state, which the ledger records for itself.
const StateEnded = "ended"

// LaneStatus is what a worker says about a lane. Every field derives from what happened,
// and no client of the API can write one (FR-LANE-5).
type LaneStatus struct {
	ActualState string   `json:"actual_state"`
	Conditions  []string `json:"conditions,omitempty"`
	Liveness    string   `json:"liveness"`
	// ModelReach and LedgerReach are the lane's own view outward, apart from whether its
	// machine is healthy (FR-LANE-7).
	ModelReach  string `json:"model_reach"`
	LedgerReach string `json:"ledger_reach"`
}

// LaneRow is a lane's status as the ledger holds it.
type LaneRow struct {
	ID         string
	WorkOrder  string
	WorkerID   string
	Generation int64
	Status     LaneStatus
	// ReportedAt is database time, when the status arrived.
	ReportedAt      int64
	ReportedByEpoch int64
	// StartedFence is the fence this lane started under.
	StartedFence int64
	// UnpushedWork says the last stop left work no remote holds.
	UnpushedWork bool
	// Archived says its worktree went.
	Archived bool
}

// LaneView is a lane as a client reads it: its status, and how much to believe it.
type LaneView struct {
	ID         string `json:"id"`
	WorkOrder  string `json:"work_order"`
	WorkerID   string `json:"worker_id,omitempty"`
	Generation int64  `json:"generation"`
	LaneStatus
	AsOf            int64  `json:"as_of"`
	Freshness       string `json:"freshness"`
	ReportedByEpoch int64  `json:"reported_by_epoch"`
	// Archived says its worktree went.
	Archived  bool       `json:"archived,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}
