package questions

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
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

// ReasonPlannerAsked says a planning lane asked, so the question went to the person with no ladder
// (FR-SESSION-3). The gate does not accept it, so no orchestrator can give it to a question.
const ReasonPlannerAsked = "planner_asked"

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
		if !q.Ran(rung) {
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

// ToPerson puts a drafted question straight to the person, which is the one way past the gate
// (FR-QD-3, FR-SESSION-3). It stands at the last rung with no ladder trace, and it needs no options and
// no recommendation. The gate would have named a category, so this does, because members find questions
// by category.
func ToPerson(q Question) (Question, error) {
	if len(q.Categories) == 0 {
		return Question{}, fmt.Errorf("%w: a question for the person needs a category, so a member can find it", ErrInvalid)
	}
	q.Status, q.Rung, q.EscalationReason = StatusNeedsHuman, RungEscalate, ReasonPlannerAsked
	return q, nil
}

// Direct reports a question that went to the person from a planning lane. No orchestrator takes,
// decides, assumes, or escalates it.
func (q Question) Direct() bool { return q.EscalationReason == ReasonPlannerAsked }

// OutcomeMerged is the ladder step of a question closed in favor of one that stays.
const OutcomeMerged = "merged"

// MergedInto names the question that took this one's place, because whoever waits on this
// one waits on that answer now.
func (q Question) MergedInto() (string, bool) {
	if q.Status != StatusDismissed {
		return "", false
	}
	for _, step := range slices.Backward(q.LadderTrace) {
		if step.Outcome == OutcomeMerged && len(step.Candidates) == 1 {
			return step.Candidates[0], true
		}
	}
	return "", false
}

// Ran reports a rung the ladder trace holds.
func (q Question) Ran(rung int64) bool {
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
	// ErrAssumptionLimit means a work order holds all the open assumptions the policy allows.
	ErrAssumptionLimit = errors.New("this work order holds as many assumptions as the policy allows")
	// ErrDecisionEnded means a decision was already overturned or replaced.
	ErrDecisionEnded = errors.New("this decision is no longer in force")
	// ErrDirect means the question is a planning lane's, which belongs to the person.
	ErrDirect = errors.New("a planning lane's question belongs to the person, so no orchestrator can take, decide, assume, or escalate it")
)

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
	// Direct says the question goes straight to the person. The control plane sets it from the
	// ledger's work order when the asking lane runs a plan, so it never travels from a lane.
	Direct bool `json:"-"`
}

// AskResult is what `ask` returns at once. Answer and Citations are set for `answered`.
type AskResult struct {
	Status     AskStatus `json:"status"`
	QuestionID string    `json:"question_id"`
	DecisionID string    `json:"decision_id,omitempty"`
	Answer     string    `json:"answer,omitempty"`
	Citations  []string  `json:"citations,omitempty"`
}

// Ruling is what an orchestrator records on rung 3 or 4, for a question it holds. Status says
// which: `decided` or `assumed`. Scope and Categories default to the question's. The client
// generates ID, so a retry finds what the first attempt wrote.
type Ruling struct {
	ID            string         `json:"id"`
	Status        DecisionStatus `json:"status"`
	Statement     string         `json:"statement"`
	Rationale     string         `json:"rationale,omitempty"`
	Citations     []string       `json:"citations,omitempty"`
	Scope         string         `json:"scope,omitempty"`
	Reversibility string         `json:"reversibility,omitempty"`
	Categories    []string       `json:"categories,omitempty"`
	Supersedes    string         `json:"supersedes,omitempty"`
	NoConflict    []string       `json:"no_conflict,omitempty"`
}

// WorkerRuling is a ruling a worker carries for its orchestrator. A machine runs one
// orchestrator (FR-ORCH-1), so the worker's identity names it.
type WorkerRuling struct {
	Auth     ledger.WorkerAuth `json:"-"`
	Epoch    int64             `json:"epoch"`
	Question string            `json:"question"`
	Ruling   Ruling            `json:"ruling"`
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
