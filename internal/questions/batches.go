package questions

import (
	"errors"
	"slices"
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
// and what the ladder considered. Nothing is assigned to anyone.
type Item struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Categories       []string `json:"categories,omitempty"`
	Blocking         bool     `json:"blocking,omitempty"`
	Options          []string `json:"options,omitempty"`
	Recommendation   string   `json:"recommendation,omitempty"`
	EscalationReason string   `json:"escalation_reason,omitempty"`
	Citations        []string `json:"citations,omitempty"`
}

// Presented is a delivered batch as members see it.
type Presented struct {
	Batch Batch  `json:"batch"`
	Items []Item `json:"items"`
}

// Present lists what still waits for a member in a batch, within one category. An empty
// category leaves out nothing.
func Present(batch Batch, rows []Question, category string) Presented {
	presented := Presented{Batch: batch, Items: []Item{}}
	for _, row := range rows {
		if row.Status != StatusNeedsHuman || (category != "" && !slices.Contains(row.Categories, category)) {
			continue
		}
		presented.Items = append(presented.Items, Item{
			ID: row.ID, Text: row.Text, Categories: row.Categories, Blocking: row.Blocking, Options: row.Options,
			Recommendation: row.Recommendation, EscalationReason: row.EscalationReason, Citations: citationsOf(row),
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
// makes a retry idempotent.
type Answer struct {
	DecisionID string
	Statement  string
	Rationale  string
	Citations  []string
}

var errNoAnswer = errors.New("an answer states nothing")

// Check accepts an answer that says something.
func (a Answer) Check() error {
	if a.Statement == "" {
		return errors.Join(ErrInvalid, errNoAnswer)
	}
	return nil
}
