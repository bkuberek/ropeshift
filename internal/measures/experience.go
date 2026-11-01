package measures

// SubjectDecision is the subject kind of a decision's experience record.
const SubjectDecision = "decision"

// Experience is what one action chose and how it turned out (FR-EXP-1). It never changes,
// except that its outcome is filled in once, which may be long after the action.
type Experience struct {
	ID                string   `json:"id"`
	SubjectKind       string   `json:"subject_kind"`
	SubjectID         string   `json:"subject_id"`
	PolicyVersion     int64    `json:"policy_version"`
	Settings          string   `json:"settings,omitempty"`
	OptionsConsidered []string `json:"options_considered,omitempty"`
	Chosen            string   `json:"chosen"`
	Outcome           *Outcome `json:"outcome,omitempty"`
	OutcomeObservedAt int64    `json:"outcome_observed_at,omitempty"`
}

// OutcomeOverturned is the outcome of a decision a member reversed.
const OutcomeOverturned = "overturned"

// Outcome is how an action turned out. One overturned decision can send several completed
// work orders back, so the work it created is a list.
type Outcome struct {
	Status           string   `json:"status"`
	ReworkWorkOrders []string `json:"rework_work_orders,omitempty"`
}
