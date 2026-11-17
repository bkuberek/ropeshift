package workorders

// Response is what a decision that binds work does to one work order, by where the work stands (ADR-0030).
type Response string

// The responses. Only `change` and `rework` work orders have one: a `plan`, `contract_review`, or `review` work
// order reads or judges the work and is told through its lane, and a decision never moves or reworks it.
const (
	// Untouched leaves the work order as it is: work that read or judged, and work that failed or was canceled,
	// which a retry starts again from a renewed pack that holds the decision.
	Untouched Response = ""
	// Unstarted returns to, or stays in, contract review, whose pass reads the decision.
	Unstarted Response = "unstarted"
	// Live tells its lane, which goes on or parks as the lane policy says.
	Live Response = "live"
	// Finished gets tracked rework, when the decision calls for it.
	Finished Response = "finished"
)

// ResponseOf says what a decision that binds a work order does to it.
func ResponseOf(w WorkOrder) Response {
	if w.Kind != KindChange && w.Kind != KindRework {
		return Untouched
	}
	switch w.State {
	case Intake, Planning, ContractReview, Ready:
		return Unstarted
	case Claimed, Running, Parked, Lapsed, Blocked:
		return Live
	case Acceptance, Integrating, Done:
		return Finished
	}
	return Untouched
}
