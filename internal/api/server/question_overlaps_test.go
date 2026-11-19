package server_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestAQuestionStillOnTheLadderNamesTheDecisionsItsScopeOverlaps holds that a member who answers a question in place
// before it reached a person, whether it waits open or an orchestrator holds it, sees the decisions in force its scope
// overlaps, so the answer can replace one (FR-QD-5).
func TestAQuestionStillOnTheLadderNamesTheDecisionsItsScopeOverlaps(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	decision := questions.Decision{ID: "d-checkout", Status: questions.DecisionDecided, Statement: "The site uses the monogram.", Scope: "plan:checkout"}
	err := d.Store.Write(t.Context(), apitest.Scope, "test.decision", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(decision); err != nil {
			return err
		}
		event, err := questions.RecordEvent(questions.EventDecisionRecorded, questions.SubjectDecision, decision.ID, decision, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"}
	for _, id := range []string{"q-open", "q-held"} {
		if _, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
			ID: id, Text: "Which logo set is " + id + "?", Categories: []string{questions.CategoryProduct}, Lane: lane.ID, Generation: 1,
			SubjectKind: planning.SubjectPlan, SubjectID: "checkout",
		}, lane); err != nil {
			t.Fatal(err)
		}
	}
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	if _, err := w.Questions.Take(t.Context(), apitest.Scope, "q-held", apitest.LeaseTTL, holder); err != nil {
		t.Fatal(err)
	}
	want := []string{"d-checkout"}
	for id, status := range map[string]questions.Status{"q-open": questions.StatusOpen, "q-held": questions.StatusTriage} {
		read, err := d.Client.Question(t.Context(), id)
		if err != nil || read.Status != status || !slices.Equal(read.Overlaps, want) {
			t.Errorf("%s reads %s with the overlaps %q, want %s and %q: %v", id, read.Status, read.Overlaps, status, want, err)
		}
	}
	for _, row := range readView[api.Questions](t, d, api.ViewQuestions, "").Questions {
		if !slices.Equal(row.Overlaps, want) {
			t.Errorf("the questions view names the overlaps %q for %s, want %q", row.Overlaps, row.ID, want)
		}
	}
}
