package delivery

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Version is the version of the derivation. A timeline records the version that derived it, and a new
// version re-derives every timeline from the events (FR-EST-3).
const Version = 1

// Kind is what a work order spent one segment of its time on (FR-EST-1).
type Kind string

// The kinds of segment. Waiting for a person is always its own kind, and agent time never holds it.
const (
	// Queued is ready, or claimed before its lane started, waiting for a lane.
	Queued Kind = "queued"
	// Held is held by a start hold or an admission hold other than a cap or a usage limit, or back in
	// planning after its first `ready` while no member's act ended the wait.
	Held Kind = "held"
	// Working is running with a lane whose agent is working.
	Working Kind = "working"
	// Ladder is waiting on a question or an approval that an orchestrator, or the ladder's rules, closed.
	Ladder Kind = "ladder"
	// Person is waiting on anything a member's act ended.
	Person Kind = "person"
	// UsageLimit is parked on a usage limit, or held back by one at admission.
	UsageLimit Kind = "usage_limit"
	// Review is in acceptance while its review work order is open.
	Review Kind = "review"
	// Integration is waiting on required checks or on the merge itself, when no member's act ended the wait.
	Integration Kind = "integration"
	// External is parked on an external check, a time, or a dependency found during the work.
	External Kind = "external"
	// Stuck is lapsed, blocked, or failed, until an orchestrator or a policy moved it.
	Stuck Kind = "stuck"
)

// The causes of a queued segment: every worker serving the project stood at a lane cap, or the project's
// work in progress was full, or else a lane was free and nobody had claimed the work.
const (
	CauseLaneCap  = "lane_cap"
	CauseDispatch = "dispatch"
)

// The reasons a held segment names when its work order went back to planning after its first `ready`. Any
// other held segment names its start hold's or its admission hold's own reason.
const (
	HeldContractReview = "contract_review"
	HeldReplanning     = "replanning"
)

// Ending is what ended a segment: the event of the act, or the question, approval, or review whose close
// ended it, with the party that acted. A boundary no act made, where the lanes were capped or freed, has
// the control plane as its actor.
type Ending struct {
	Event  string       `json:"event,omitempty"`
	Record string       `json:"record,omitempty"`
	Actor  ledger.Actor `json:"actor"`
}

// Segment is one stretch of a work order's time with one kind, in one state of the work order. Round is 1
// until the first `rework` and one more after each. Lane names the lane while one held the work, and Review
// the review work order of a review segment. Ended is absent on the last segment of work that has not ended.
type Segment struct {
	Kind   Kind             `json:"kind"`
	State  workorders.State `json:"state"`
	Cause  string           `json:"cause,omitempty"`
	Round  int64            `json:"round"`
	Lane   string           `json:"lane,omitempty"`
	Review string           `json:"review,omitempty"`
	From   int64            `json:"from"`
	To     int64            `json:"to"`
	Ended  *Ending          `json:"ended,omitempty"`
}

// Timeline is where one work order's time went, from its first `ready` to its terminal state, or to now
// while it has not ended (FR-EST-1). Its segments cover Start to End with no gap and no overlap. A work
// order that never reached `ready` has no segment.
type Timeline struct {
	WorkOrder string           `json:"work_order"`
	Version   int              `json:"version"`
	Start     int64            `json:"start"`
	End       int64            `json:"end"`
	Terminal  workorders.State `json:"terminal,omitempty"`
	Segments  []Segment        `json:"segments"`
}
