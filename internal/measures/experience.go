package measures

// The subject kinds of experience records: one per lane and one per decision (FR-EXP-1).
const (
	SubjectDecision = "decision"
	SubjectLane     = "lane"
)

// SizeUnknown is the size a record holds when a contract declared none (D27).
const SizeUnknown = "unknown"

// SizeOf reads a work order's declared size, `small`, `medium`, or `large`, as a record holds it.
func SizeOf(declared string) string {
	if declared == "" {
		return SizeUnknown
	}
	return declared
}

// Settings is what an action ran on (FR-EXP-2): the agent, the model asked for and the one that
// answered, the effort, the context thresholds of its role, and the size of its work. A decision a
// person made ran on no session, so only its work's size is set.
type Settings struct {
	Driver         string          `json:"driver,omitempty"`
	ModelRequested string          `json:"model_requested,omitempty"`
	ModelResolved  string          `json:"model_resolved,omitempty"`
	Effort         string          `json:"effort,omitempty"`
	RunMode        string          `json:"run_mode,omitempty"`
	ProfileDigest  string          `json:"profile_digest,omitempty"`
	Context        ContextSettings `json:"context"`
	WorkOrderSize  string          `json:"work_order_size"`
}

// ContextSettings are the context thresholds of one role.
type ContextSettings struct {
	Role          string `json:"role,omitempty"`
	WarnTokens    int64  `json:"warn_tokens,omitempty"`
	CompactTokens int64  `json:"compact_tokens,omitempty"`
	HandoffTokens int64  `json:"handoff_tokens,omitempty"`
}

// Experience is what one action chose and how it turned out (FR-EXP-1). It never changes,
// except that its outcome is filled in once, which may be long after the action.
type Experience struct {
	ID                string   `json:"id"`
	SubjectKind       string   `json:"subject_kind"`
	SubjectID         string   `json:"subject_id"`
	PolicyVersion     int64    `json:"policy_version"`
	Settings          Settings `json:"settings"`
	OptionsConsidered []string `json:"options_considered,omitempty"`
	Chosen            string   `json:"chosen"`
	Outcome           *Outcome `json:"outcome,omitempty"`
	OutcomeObservedAt int64    `json:"outcome_observed_at,omitempty"`
}

// OutcomeOverturned is the outcome of a decision a member reversed.
const OutcomeOverturned = "overturned"

// Outcome is how an action turned out. One overturned decision can send several completed
// work orders back, so the work it created is a list. A lane's outcome is how it ended, with what
// its agent reported spending, which is a lower bound when a turn reported nothing.
type Outcome struct {
	Status           string   `json:"status"`
	Reason           string   `json:"reason,omitempty"`
	ReworkWorkOrders []string `json:"rework_work_orders,omitempty"`
	CostUSD          float64  `json:"cost_usd,omitempty"`
	CostLowerBound   bool     `json:"cost_lower_bound,omitempty"`
}
