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
}
