package questions

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// BatchStatus is where a batch stands. A batch that gathers is `open`, and it ends as
// `delivered`, or as `empty` when nothing in it still needs a person.
type BatchStatus string

// The statuses of a batch.
const (
	BatchOpen      BatchStatus = "open"
	BatchDelivered BatchStatus = "delivered"
	BatchEmpty     BatchStatus = "empty"
)

// SubjectBatch names a batch in the log.
const SubjectBatch = "question_batch"

// The events of a batch. A delivered batch's event is the one interruption it counts as,
// however many questions it holds (FR-QD-11).
const (
	EventBatchChanged   = "question_batch.changed"
	EventBatchDelivered = "question_batch.delivered"
)

// Batch is a set of questions that reaches members together.
type Batch struct {
	ID          string      `json:"id"`
	Status      BatchStatus `json:"status"`
	OpenedAt    int64       `json:"opened_at"`
	ClosesAt    int64       `json:"closes_at"`
	DeliveredAt int64       `json:"delivered_at,omitempty"`
}

// Delivery is what a delivered batch's event holds.
type Delivery struct {
	Batch     Batch    `json:"batch"`
	Questions []string `json:"questions"`
}

// Default batch limits, in milliseconds, for a workspace that states neither.
const (
	DefaultBatchWindowMs  int64 = 15 * 60 * 1000
	DefaultBatchMaxWaitMs int64 = 5 * 60 * 1000
)

// BatchPolicy says how long questions gather. WindowMs fixes when a batch closes, counted
// from its first question. MaxWaitMs bounds a blocking question's wait, so it closes the
// batch early only when it is shorter than what is left of the window.
type BatchPolicy struct {
	WindowMs  int64
	MaxWaitMs int64
}

// Filled gives each limit a value.
func (p BatchPolicy) Filled() BatchPolicy {
	if p.WindowMs <= 0 {
		p.WindowMs = DefaultBatchWindowMs
	}
	if p.MaxWaitMs <= 0 {
		p.MaxWaitMs = DefaultBatchMaxWaitMs
	}
	return p
}

// Item is what a member needs to answer one question once: its options, the recommendation,
// what the ladder considered, the scope an answer to it takes (ScopeOf), the reversibility the answer will have
// (Policy.AnswerReversibility), and the decisions in force its scope overlaps, which an
// answer may replace. Nothing is assigned to anyone.
type Item struct {
	ID                      string   `json:"id"`
	Text                    string   `json:"text"`
	Categories              []string `json:"categories,omitempty"`
	Blocking                bool     `json:"blocking,omitempty"`
	Options                 []string `json:"options,omitempty"`
	Recommendation          string   `json:"recommendation,omitempty"`
	RecommendationRationale string   `json:"recommendation_rationale,omitempty"`
	EscalationReason        string   `json:"escalation_reason,omitempty"`
	Reversibility           string   `json:"reversibility"`
	Citations               []string `json:"citations,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	Overlaps                []string `json:"overlaps,omitempty"`
	// NoOrchestrator says the core took the question to a person because no live orchestrator finished its ladder.
	// LadderTrace is every rung it passed: a rung an orchestrator ran before it stopped keeps what it judged, and a rung
	// the core ran lists what it found by text, judged by nobody, so the trace says which citation is which.
	NoOrchestrator bool   `json:"no_orchestrator,omitempty"`
	LadderTrace    []Step `json:"ladder_trace,omitempty"`
	// AskedBy is who asked the question, so a member can discuss it with them.
	AskedBy string `json:"asked_by,omitempty"`
	// Title, Tradeoffs, and Unblocks are the question's shape, with a tradeoff only for an option it offers now.
	Title     string            `json:"title,omitempty"`
	Tradeoffs map[string]string `json:"tradeoffs,omitempty"`
	Unblocks  string            `json:"unblocks,omitempty"`
}

// Presented is a delivered batch as members see it.
type Presented struct {
	Batch Batch  `json:"batch"`
	Items []Item `json:"items"`
}

// Present lists what still waits for a member in a batch, within one category. An empty
// category leaves out nothing. The policy in force says which answers are one-way.
func Present(batch Batch, rows []Question, category string, policy Policy) Presented {
	presented := Presented{Batch: batch, Items: []Item{}}
	for _, row := range rows {
		if row.Status != StatusNeedsHuman || (category != "" && !slices.Contains(row.Categories, category)) {
			continue
		}
		presented.Items = append(presented.Items, Item{
			ID: row.ID, Text: row.Text, Categories: row.Categories, Blocking: row.Blocking, Options: row.Options,
			Recommendation: row.Recommendation, RecommendationRationale: row.RecommendationRationale,
			EscalationReason: row.EscalationReason, Reversibility: policy.AnswerReversibility(row), Citations: citationsOf(row),
			Scope: ScopeOf(row.SubjectKind, row.SubjectID), Overlaps: row.Overlaps,
			NoOrchestrator: row.NoOrchestrator(), LadderTrace: row.LadderTrace, AskedBy: row.AskedBy,
			Title: row.Title, Tradeoffs: TradeoffsOf(row), Unblocks: row.Unblocks,
		})
	}
	return presented
}

// citationsOf reads what a question's ladder trace considered, in the order it did, once
// each.
func citationsOf(row Question) []string {
	var cited []string
	for _, step := range row.LadderTrace {
		for _, candidate := range step.Candidates {
			if !slices.Contains(cited, candidate) {
				cited = append(cited, candidate)
			}
		}
	}
	return cited
}

// Answer is a member's answer to one question. DecisionID comes from the client, which
// makes a retry idempotent. Supersedes names the decision in force the answer replaces, and
// NoConflict the decisions its scope overlaps that the answer does not conflict with: the decision
// the answer becomes records both, as a decision recorded by itself does (FR-QD-9).
type Answer struct {
	DecisionID string
	Statement  string
	Rationale  string
	Citations  []string
	Supersedes string
	NoConflict []string
	// Assumed records the answer as an assumption, which a member records in an orchestrator's place on rung 4, and
	// only for a question whose answer is reversible.
	Assumed bool
}

var errNoAnswer = errors.New("an answer states nothing")

// BatchAnswer is a member's answer to one question of a batch, in a request that answers the batch once. Accepted
// takes the question's recommendation as the answer, with the recommendation's rationale and the item's citations,
// so it states no answer, rationale, or citations of its own. Anything else is the member's own answer, which says
// why (FR-QD-9). Supersedes and NoConflict apply to either.
type BatchAnswer struct {
	Question string
	Answer   Answer
	Accepted bool
	// ExpectReversibility is the reversibility the member read the item with, when the request says: the ledger
	// refuses the answer when the policy in force now gives another, so an answer taken on the word of an item
	// never records a one-way decision nobody chose. Empty takes what the policy gives.
	ExpectReversibility string
}

// ErrReversibilityChanged means an answer expected a reversibility the policy in force no longer gives.
var ErrReversibilityChanged = errors.New("the answer's reversibility changed since it was read")

// CheckExpectation refuses an expectation the policy in force now contradicts, and names which way it changed.
func (b BatchAnswer) CheckExpectation(policy Policy, q Question) error {
	got := policy.AnswerReversibility(q)
	if b.ExpectReversibility == "" || b.ExpectReversibility == got {
		return nil
	}
	if got == OneWay {
		return fmt.Errorf("%w: it is one-way now", ErrReversibilityChanged)
	}
	return fmt.Errorf("%w: it is reversible now", ErrReversibilityChanged)
}

// Resolve reads the answer to record for the question this one names, or refuses it.
func (b BatchAnswer) Resolve(q Question) (Answer, error) {
	switch b.ExpectReversibility {
	case "", Reversible, OneWay:
	default:
		return Answer{}, fmt.Errorf("%w: expect_reversibility is %s or %s", ErrInvalid, Reversible, OneWay)
	}
	answer := b.Answer
	if b.Accepted {
		if answer.Statement != "" || answer.Rationale != "" || len(answer.Citations) != 0 {
			return Answer{}, fmt.Errorf("%w: accepting a recommendation records its own rationale and citations, so it states no answer, rationale, or citations", ErrInvalid)
		}
		if q.Recommendation == "" {
			return Answer{}, fmt.Errorf("%w: the question has no recommendation to accept", ErrInvalid)
		}
		answer.Statement, answer.Rationale, answer.Citations = q.Recommendation, q.RecommendationRationale, citationsOf(q)
		return answer, nil
	}
	if err := answer.Check(); err != nil {
		return Answer{}, err
	}
	if strings.TrimSpace(answer.Rationale) == "" {
		return Answer{}, fmt.Errorf("%w: a member's own answer states its rationale", ErrInvalid)
	}
	return answer, nil
}

// Check accepts an answer that says something.
func (a Answer) Check() error {
	if a.Statement == "" {
		return errors.Join(ErrInvalid, errNoAnswer)
	}
	return nil
}
