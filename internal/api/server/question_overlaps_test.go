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

// TestEveryUnresolvedQuestionNamesTheDecisionsItsScopeOverlapsNow holds that each read of a question that still
// waits, open, held, or waiting for a person, names the decisions in force whose scope overlaps its own as they stand
// at the read, and leaves out a decision about other work (FR-QD-5). The question put to the person before any
// decision was recorded keeps no overlaps of its own, and still names the one recorded since.
func TestEveryUnresolvedQuestionNamesTheDecisionsItsScopeOverlapsNow(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"}
	ask := func(id string, direct bool) {
		t.Helper()
		if _, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
			ID: id, Text: "Which logo set is " + id + "?", Categories: []string{questions.CategoryProduct}, Lane: lane.ID, Generation: 1,
			SubjectKind: planning.SubjectPlan, SubjectID: "checkout", Direct: direct,
		}, lane); err != nil {
			t.Fatal(err)
		}
	}
	ask("q-person", true)
	w.Deliver()
	for id, scope := range map[string]string{"d-checkout": "plan:checkout", "d-billing": "plan:billing"} {
		decision := questions.Decision{ID: id, Status: questions.DecisionDecided, Statement: "Settled " + id + ".", Scope: scope}
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
	}
	ask("q-open", false)
	ask("q-held", false)
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	if _, err := w.Questions.Take(t.Context(), apitest.Scope, "q-held", apitest.LeaseTTL, holder); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]questions.Status{"q-person": questions.StatusNeedsHuman, "q-open": questions.StatusOpen, "q-held": questions.StatusTriage}
	want := []string{"d-checkout"}
	check := func(where, id string, status questions.Status, overlaps []string) {
		t.Helper()
		if status != statuses[id] || !slices.Equal(overlaps, want) {
			t.Errorf("%s reads %s as %s with the overlaps %q, want %s and %q", where, id, status, overlaps, statuses[id], want)
		}
	}
	for id := range statuses {
		read, err := d.Client.Question(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		check("the question", id, read.Status, read.Overlaps)
	}
	listed, err := d.Client.Questions(t.Context(), api.RecordFilter{})
	if err != nil || len(listed) != len(statuses) {
		t.Fatalf("the list holds %d questions: %v", len(listed), err)
	}
	for _, row := range listed {
		check("the list", row.ID, row.Status, row.Overlaps)
	}
	for _, row := range readView[api.Questions](t, d, api.ViewQuestions, "").Questions {
		check("the questions view", row.ID, row.Status, row.Overlaps)
	}
	batches, err := d.Client.Batches(t.Context(), "")
	if err != nil || len(batches) != 1 || len(batches[0].Items) != 1 {
		t.Fatalf("the batches hold %+v: %v", batches, err)
	}
	if item := batches[0].Items[0]; item.ID != "q-person" || !slices.Equal(item.Overlaps, want) {
		t.Errorf("the batch item %s names the overlaps %q, want %q", item.ID, item.Overlaps, want)
	}
}
