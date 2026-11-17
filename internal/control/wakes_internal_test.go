package control

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// standingDecisions is a ledger of decisions with nothing else in it. Any other read panics.
type standingDecisions struct {
	ports.QuestionReader
	rows []questions.Decision
}

func (s standingDecisions) Decisions(filter ports.RecordFilter) ([]questions.Decision, error) {
	var out []questions.Decision
	for _, row := range s.rows {
		if (filter.Scope == "" || row.Scope == filter.Scope) && (filter.Status == "" || string(row.Status) == filter.Status) {
			out = append(out, row)
		}
	}
	return out, nil
}

// TestADecisionWakeFindsTheDecisionItWasWrittenFor holds a wake to the scope it names: a key in the grammar is
// held by that scope alone, and a key written before the grammar, a bare ID, is held by the decision of that ID
// that migration 0130 gave its prefix, and by one that still carries the bare ID.
func TestADecisionWakeFindsTheDecisionItWasWrittenFor(t *testing.T) {
	inForce := func(scope string) questions.Decision {
		return questions.Decision{ID: "d-" + scope, Status: questions.DecisionDecided, Scope: scope}
	}
	ended := func(scope string) questions.Decision {
		return questions.Decision{ID: "e-" + scope, Status: questions.DecisionReplaced, Scope: scope}
	}
	tests := []struct {
		name string
		key  string
		rows []questions.Decision
		want bool
	}{
		{"a typed key and its scope", "repo:payments", []questions.Decision{inForce("repo:payments")}, true},
		{"a typed key and another kind of the same ID", "repo:payments", []questions.Decision{inForce("plan:payments")}, false},
		{"a typed key and a bare ID", "repo:payments", []questions.Decision{inForce("payments")}, false},
		{"a typed key and a decision that ended", "repo:payments", []questions.Decision{ended("repo:payments")}, false},
		{"a bare key and the decision migration 0130 rewrote", "payments", []questions.Decision{inForce("repo:payments")}, true},
		{"a bare key and a rewritten decision of another kind", "payments", []questions.Decision{inForce("contract:payments")}, true},
		{"a bare key and a decision that still carries the bare ID", "payments", []questions.Decision{inForce("payments")}, true},
		{"a bare key and a rewritten decision that ended", "payments", []questions.Decision{ended("repo:payments")}, false},
		{"a bare key and another ID", "payments", []questions.Decision{inForce("repo:payments-eu")}, false},
		{"a bare key and a bare ID with a path", "payments", []questions.Decision{inForce("payments/refunds")}, false},
		{"no decision", "payments", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decisionRecorded(standingDecisions{rows: tt.rows}, tt.key)
			if err != nil || got != tt.want {
				t.Errorf("decisionRecorded(%q) = %v, %v, want %v", tt.key, got, err, tt.want)
			}
		})
	}
}

// TestANewDecisionWakeNamesAScopeInTheGrammar holds a lane's wake on a decision to the grammar, so a key the
// migration does not rewrite is not stored now.
func TestANewDecisionWakeNamesAScopeInTheGrammar(t *testing.T) {
	tests := []struct {
		name string
		expr string
		ok   bool
	}{
		{"a scope of the grammar", `{"decision_recorded":"repo:payments"}`, true},
		{"a bare ID", `{"decision_recorded":"payments"}`, false},
		{"free text", `{"decision_recorded":"payments/refunds"}`, false},
		{"an unknown kind", `{"decision_recorded":"team:payments"}`, false},
		{"one wake of two", `{"any_of":[{"question_answered":"q1"},{"decision_recorded":"payments"}]}`, false},
		{"a wake that names no decision", `{"question_answered":"q1"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := parking.Parse(tt.expr, "lane-1")
			if err != nil {
				t.Fatal(err)
			}
			err = checkDecisionKeys(expr)
			if tt.ok && err != nil {
				t.Errorf("refused %s: %v", tt.expr, err)
			}
			if !tt.ok && (!errors.Is(err, questions.ErrBadScope) || !errors.Is(err, questions.ErrInvalid)) {
				t.Errorf("%s: %v, want a refusal that names the grammar", tt.expr, err)
			}
		})
	}
}
