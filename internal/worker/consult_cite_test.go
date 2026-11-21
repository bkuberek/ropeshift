package worker_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
)

// consultedQ1 runs a consultation of question q1 to its end, and returns what the orchestrator was told it started.
func (r *toolRig) consultedQ1(t *testing.T) ledger.ConsultStarted {
	t.Helper()
	r.nextSession([]fake.DriverStep{{Bound: true}, r.reportsConsultation(t, consultReport), completed(0.4)})
	started := r.consult(t, map[string]any{"question": "q1", "ask": consultAsk})
	r.awaitConsulted(t, "q1")
	return started
}

// FR-ORCH-9: the ledger cites a question's consultation on every decision that ends the question, whichever way the
// decision reaches it: a ruling the worker's own endpoint records, as the orchestrator's tools do, and an answer from a
// decision already on record, which cites it on the step of the ladder that closed the question.
func TestEveryAnswerToAConsultedQuestionCitesItsConsultation(t *testing.T) {
	t.Parallel()
	t.Run("a ruling the worker's endpoint records", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		started := r.consultedQ1(t)
		orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: r.subject.Auth().WorkerID}
		use := control.NewQuestions(r.store, questions.BatchPolicy{})
		if _, err := use.Take(t.Context(), scope, "q1", 60_000, orchestrator); err != nil {
			t.Fatal(err)
		}
		for rung := questions.RungLookup; rung < questions.RungDecide; rung++ {
			if _, err := use.Climb(t.Context(), scope, "q1", questions.Step{Rung: rung, Outcome: "no_match"}, orchestrator); err != nil {
				t.Fatal(err)
			}
		}
		decision, err := r.plane.Rule(t.Context(), questions.WorkerRuling{
			Auth: r.subject.Auth(), Epoch: r.subject.Session().Epoch, Question: "q1",
			Ruling: questions.Ruling{
				ID: "d-ruled", Status: questions.DecisionDecided, Statement: "Three retries.", Rationale: "The consultation recommends it.",
				Citations: []string{"req-retries"}, Scope: "repo:retries", Reversibility: questions.Reversible,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		want := ledger.ConsultationCitation(started.Consultation)
		if !slices.Contains(decision.Citations, want) || !slices.Contains(decision.Citations, "req-retries") {
			t.Errorf("the ruling cites %v, want %s too", decision.Citations, want)
		}
	})
	t.Run("an answer from a decision on record", func(t *testing.T) {
		t.Parallel()
		r := aToolRig(t)
		started := r.consultedQ1(t)
		_, err := control.NewQuestions(r.store, questions.BatchPolicy{}).RecordDecision(t.Context(), scope, questions.Decision{
			ID: "d1", Status: questions.DecisionDecided, Statement: "Three retries.", Rationale: "It follows.", Scope: "repo:retries",
			Reversibility: "reversible", DecidedBy: "member-1",
		}, member)
		if err != nil {
			t.Fatal(err)
		}
		r.done(t, lanes.ToolDecisionRecord, map[string]any{"question": "q1", "decision": "d1"})
		got := r.question(t, "q1")
		if got.Status != questions.StatusAnswered || got.DecisionID != "d1" || len(got.LadderTrace) == 0 {
			t.Fatalf("the question reads %+v", got)
		}
		last := got.LadderTrace[len(got.LadderTrace)-1]
		if want := ledger.ConsultationCitation(started.Consultation); last.Decision != "d1" || !slices.Contains(last.Candidates, want) {
			t.Errorf("the step that closed it holds %+v, want %s among its candidates", last, want)
		}
	})
}
