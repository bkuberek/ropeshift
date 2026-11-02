package questions

import (
	"fmt"
	"slices"
	"strings"
)

// scopeSeparator divides a scope into the parts a narrower scope extends.
const scopeSeparator = "/"

// AnswerText is what a lane reads of an answer. The answer leads, so text that has to be cut keeps
// it and the record that holds the rest.
func AnswerText(question Question, decision Decision) string {
	return fmt.Sprintf("Decision %s answers question %s.\n%sQuestion: %s\n", decision.ID, question.ID, decisionLines(decision), question.Text)
}

// NoticeText is what a lane reads when a decision it may rest on stops standing.
func NoticeText(ended string, replacement Decision) string {
	return fmt.Sprintf("Decision %s no longer stands, and decision %s replaces it.\n%s", ended, replacement.ID, decisionLines(replacement))
}

func decisionLines(decision Decision) string {
	var out strings.Builder
	out.WriteString("Statement: " + decision.Statement + "\n")
	if decision.Rationale != "" {
		out.WriteString("Rationale: " + decision.Rationale + "\n")
	}
	if len(decision.Citations) != 0 {
		out.WriteString("Citations: " + strings.Join(decision.Citations, ", ") + "\n")
	}
	return out.String()
}

// ScopesOverlap reports two scopes that cover some of the same thing: the same scope, or one
// inside the other. An empty scope covers nothing.
func ScopesOverlap(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.HasPrefix(a, b+scopeSeparator) || strings.HasPrefix(b, a+scopeSeparator)
}

// OverlapError refuses a decision that has not said how it stands to the decisions it
// overlaps. The author reads them, then states that it supersedes one or that they do not
// conflict, and a real conflict escalates with `requirement_conflict`.
type OverlapError struct {
	Overlapping []string
}

func (e *OverlapError) Error() string {
	return "decisions with an overlapping scope need a statement: " + strings.Join(e.Overlapping, ", ")
}

// Unstated lists the overlapping decisions a draft says nothing about.
func (d Decision) Unstated(overlapping []string) []string {
	var unstated []string
	for _, id := range overlapping {
		if id != d.Supersedes && !slices.Contains(d.NoConflict, id) {
			unstated = append(unstated, id)
		}
	}
	return unstated
}

// CheckBound holds a decision made under the policy to what the requirements ask of one:
// a reversible choice, with a rationale and a scope, and citations when it was decided
// (FR-QD-6, FR-QD-8, FR-QD-9). A one-way choice belongs to a human.
func (d Decision) CheckBound() error {
	switch d.Reversibility {
	case Reversible:
	case OneWay:
		return fmt.Errorf("%w: %s is a one-way decision", ErrOneWay, d.ID)
	default:
		return fmt.Errorf("%w: decision %s does not say it is reversible", ErrInvalid, d.ID)
	}
	if d.Rationale == "" {
		return fmt.Errorf("%w: decision %s has no rationale", ErrInvalid, d.ID)
	}
	if d.Scope == "" {
		return fmt.Errorf("%w: decision %s has no scope", ErrInvalid, d.ID)
	}
	if d.Status == DecisionDecided && len(d.Citations) == 0 {
		return fmt.Errorf("%w: decision %s has no citation", ErrInvalid, d.ID)
	}
	return nil
}

// Overturn is what a member records in place of a decision. The client generates DecisionID,
// so a retry finds what the first attempt wrote.
type Overturn struct {
	DecisionID string   `json:"decision_id"`
	Statement  string   `json:"statement"`
	Rationale  string   `json:"rationale,omitempty"`
	Citations  []string `json:"citations,omitempty"`
}

// Check accepts an overturn a member can make.
func (o Overturn) Check() error {
	if o.DecisionID == "" {
		return fmt.Errorf("%w: an overturn names no new decision", ErrInvalid)
	}
	if Normalize(o.Statement) == "" {
		return fmt.Errorf("%w: an overturn states nothing", ErrInvalid)
	}
	return nil
}

// Overturned is what one overturn did: the two decisions, the rework it created, and the
// lanes it told.
type Overturned struct {
	Old     Decision `json:"old"`
	New     Decision `json:"new"`
	Rework  []string `json:"rework,omitempty"`
	Noticed []string `json:"noticed,omitempty"`
}

// Notice tells a lane that a decision it read no longer stands. The ledger holds it, so a
// lane that missed it can read it, and delivery is a later concern.
type Notice struct {
	DecisionID string `json:"decision_id"`
	Lane       string `json:"lane"`
	Generation int64  `json:"generation"`
	ReplacedBy string `json:"replaced_by"`
	NoticedAt  int64  `json:"noticed_at"`
}
