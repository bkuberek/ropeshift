package questions

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/ledger"
)

// The rungs of the ladder.
const (
	RungLookup   int64 = 1
	RungOpen     int64 = 2
	RungDecide   int64 = 3
	RungAssume   int64 = 4
	RungEscalate int64 = 5
)

// The reasons a question can reach a human (FR-QD-8).
const (
	ReasonOneWay              = "one_way"
	ReasonRequirementConflict = "requirement_conflict"
	ReasonScopeChange         = "scope_change"
)

// GateCheck names one thing the escalation gate requires.
type GateCheck string

// The gate's checks.
const (
	GateTrace          GateCheck = "ladder_trace"
	GateReason         GateCheck = "escalation_reason"
	GateCategories     GateCheck = "categories"
	GateOptions        GateCheck = "options"
	GateRecommendation GateCheck = "recommendation"
)

// GateFailure is one check that did not pass.
type GateFailure struct {
	Check  GateCheck `json:"check"`
	Detail string    `json:"detail"`
}

// minOptions is the fewest options that leave a member a choice.
const minOptions = 2

// Gate is the one way a question reaches a human (FR-QD-3). It checks structure and not
// truth, and an orchestrator can misclassify a one-way choice, so the policy forces whole
// categories and the overturn rate measures the rest (ADR-0007).
func Gate(q Question) []GateFailure {
	var failed []GateFailure
	for _, rung := range []int64{RungLookup, RungOpen} {
		if !q.ran(rung) {
			failed = append(failed, GateFailure{GateTrace, fmt.Sprintf("rung %d has no record in the ladder trace", rung)})
			break
		}
	}
	switch q.EscalationReason {
	case ReasonOneWay, ReasonRequirementConflict, ReasonScopeChange:
	default:
		failed = append(failed, GateFailure{GateReason, fmt.Sprintf("escalation reason %q is not one_way, requirement_conflict, or scope_change", q.EscalationReason)})
	}
	if len(q.Categories) == 0 {
		failed = append(failed, GateFailure{GateCategories, "no category, so no member could find it"})
	}
	if len(q.Options) < minOptions {
		failed = append(failed, GateFailure{GateOptions, "a member needs at least two options"})
	}
	if q.Recommendation == "" {
		failed = append(failed, GateFailure{GateRecommendation, "no recommendation"})
	}
	return failed
}

func (q Question) ran(rung int64) bool {
	for _, step := range q.LadderTrace {
		if step.Rung == rung {
			return true
		}
	}
	return false
}

// GateError refuses an escalation and names everything it lacks.
type GateError struct {
	Failures []GateFailure
}

func (e *GateError) Error() string {
	details := make([]string, 0, len(e.Failures))
	for _, failure := range e.Failures {
		details = append(details, failure.Detail)
	}
	return "the escalation gate refused: " + strings.Join(details, "; ")
}

// Errors of working a question.
var (
	// ErrTaken means another orchestrator holds the question.
	ErrTaken = errors.New("another holder is working this question")
	// ErrNotHeld means the caller's hold is gone, or was never taken.
	ErrNotHeld = errors.New("this caller does not hold the question")
	// ErrResolved means the question was answered or dismissed.
	ErrResolved = errors.New("this question is resolved")
	// ErrOneWay means the question's category can only be decided by a human.
	ErrOneWay = errors.New("a one-way category cannot be decided or assumed")
	// ErrWrongRung means a step does not belong to the rung the question stands on.
	ErrWrongRung = errors.New("this step is not the question's next rung")
)

// Policy is the part of a decision policy the ladder reads.
type Policy struct {
	OneWayCategories []string `json:"one_way_categories,omitempty"`
}

// DefaultPolicy is what a workspace reads until decision policies are versioned records.
func DefaultPolicy() Policy {
	return Policy{OneWayCategories: []string{CategorySecurity, CategoryCompliance, CategoryData}}
}

// MayDecide reports whether an orchestrator may decide or assume a question. One category
// the policy forces to one-way is enough to refuse.
func (p Policy) MayDecide(q Question) bool {
	for _, category := range q.Categories {
		if slices.Contains(p.OneWayCategories, category) {
			return false
		}
	}
	return true
}

// NextRung is the rung after the one a question stands on. A one-way category passes rungs
// 1 and 2, because an earlier human decision may answer it, and skips 3 and 4.
func (p Policy) NextRung(q Question) int64 {
	if q.Rung == RungOpen && !p.MayDecide(q) {
		return RungEscalate
	}
	return min(q.Rung+1, RungEscalate)
}

// AskStatus is how an ask ended.
type AskStatus string

// The answers of `ask`, which never holds a call open.
const (
	AskAnswered AskStatus = "answered"
	AskAttached AskStatus = "attached"
	AskPending  AskStatus = "pending"
)

// Ask is one question a lane asks. The client generates ID, so a retry finds its first
// attempt.
type Ask struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	Categories  []string `json:"categories,omitempty"`
	Blocking    bool     `json:"blocking,omitempty"`
	SubjectKind string   `json:"subject_kind,omitempty"`
	SubjectID   string   `json:"subject_id,omitempty"`
	Options     []string `json:"options,omitempty"`
	Lane        string   `json:"lane,omitempty"`
	Generation  int64    `json:"generation,omitempty"`
}

// AskResult is what `ask` returns at once. Answer and Citations are set for `answered`.
type AskResult struct {
	Status     AskStatus `json:"status"`
	QuestionID string    `json:"question_id"`
	DecisionID string    `json:"decision_id,omitempty"`
	Answer     string    `json:"answer,omitempty"`
	Citations  []string  `json:"citations,omitempty"`
}

// Escalation is what an orchestrator adds to take a question to the gate. A lane may not
// know a question's categories or its options, so these replace what it gave, and an empty
// list keeps it.
type Escalation struct {
	Reason         string   `json:"reason"`
	Categories     []string `json:"categories,omitempty"`
	Options        []string `json:"options,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
}

// LaneAsk is an ask a worker carries for one of its lanes. The control plane checks that
// this worker holds that lane at that fence.
type LaneAsk struct {
	Auth      ledger.WorkerAuth `json:"-"`
	Epoch     int64             `json:"epoch"`
	WorkOrder string            `json:"work_order"`
	Fence     int64             `json:"fence"`
	Ask       Ask               `json:"ask"`
}
