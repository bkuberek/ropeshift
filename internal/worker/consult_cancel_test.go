package worker_test

import (
	"math"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
)

// answers is a member answering a question with a decision of their own.
func answers(t *testing.T, r *toolRig, question string) {
	t.Helper()
	answer := questions.Answer{DecisionID: "d-member", Statement: "Three retries.", Rationale: "It is what the contract tests."}
	if _, err := control.NewQuestions(r.store, questions.BatchPolicy{}).Answer(t.Context(), scope, question, answer, member); err != nil {
		t.Fatal(err)
	}
}

// cancelsOf reads the consultations the ledger asks this worker, in its desired state, to cancel.
func (r *toolRig) cancelsOf(t *testing.T) []string {
	t.Helper()
	state, err := r.plane.Desired(t.Context(), ledger.DesiredRequest{Auth: r.subject.Auth()})
	if err != nil {
		t.Fatal(err)
	}
	return state.CancelConsultations
}

// costKept waits for the journal to keep what a consultation's session has cost so far, which says the worker read it.
func (r *toolRig) costKept(t *testing.T, usd float64) {
	t.Helper()
	kept := r.journaledWhile(t, func(kept []ledger.ConsultationRun) bool { return len(kept) == 1 && kept[0].CostUSD >= usd-1e-9 })
	if len(kept) != 1 || kept[0].CostUSD < usd-1e-9 {
		t.Fatalf("the journal keeps %+v, and the session has cost %v", kept, usd)
	}
}

// FR-ORCH-9: a consultation whose question a member answers or dismisses while its session runs is cancelled through the
// worker's desired state, with no call of its own: the ledger names it there, the worker stops its session on its next
// pass, and its end says it was cancelled, with what the session spent until then, which the project's work is charged.
// Once that end arrives the ledger names it no more.
func TestAConsultationWhoseQuestionIsSettledWhileItRunsIsCancelled(t *testing.T) {
	t.Parallel()
	for name, act := range map[string]memberActs{"answered": answers, "dismissed": dismisses} {
		t.Run("a question "+name, func(t *testing.T) {
			t.Parallel()
			r := aToolRig(t)
			r.nextSession([]fake.DriverStep{{Bound: true}, {Running: 0.6}, {Hold: true}})
			started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
			r.costKept(t, 0.6)
			if cancels := r.cancelsOf(t); len(cancels) != 0 {
				t.Fatalf("the ledger cancels %v while the question is open", cancels)
			}
			act(t, r, "q1")
			if cancels := r.cancelsOf(t); !slices.Equal(cancels, []string{started.Consultation}) {
				t.Fatalf("the ledger cancels %v, want %s", cancels, started.Consultation)
			}
			r.tick(t)
			ended := r.awaitConsulted(t, "q1")
			if ended.Outcome != ledger.ConsultationCancelled || math.Abs(ended.CostUSD-0.6) > 1e-9 || ended.Recommendation != "" ||
				!strings.Contains(ended.Detail, "decided or dismissed") {
				t.Errorf("the consultation ended %+v", ended)
			}
			if spent := r.workSpentToday(t); spent < 0.6-1e-9 {
				t.Errorf("the project's work was charged %v for the cancelled consultation", spent)
			}
			if cancels := r.cancelsOf(t); len(cancels) != 0 {
				t.Errorf("the ledger still cancels %v once the consultation ended", cancels)
			}
		})
	}
	t.Run("a question taken to a person is not settled", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		r.nextSession([]fake.DriverStep{{Bound: true}, {Running: 0.2}, {Hold: true}})
		r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
		r.costKept(t, 0.2)
		decidesNow(t, r, "q1")
		if row := r.question(t, "q1"); row.Status != questions.StatusNeedsHuman {
			t.Fatalf("the question is %s", row.Status)
		}
		if cancels := r.cancelsOf(t); len(cancels) != 0 {
			t.Errorf("the ledger cancels %v of a question that waits for a person", cancels)
		}
	})
}
