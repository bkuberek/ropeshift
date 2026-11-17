package server_test

import (
	"maps"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// titledScopes records a decision under each kind of scope, a decision whose repository's name is its identity,
// and one with a scope written before the grammar, which the store keeps as it was. It says the title each
// decision's scope reads with, by the decision's ID.
func titledScopes(t *testing.T) (*apitest.Daemon, map[string]string) {
	t.Helper()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Recorded("wo-trim", "Trim the log")
	w.ChangeContract("wo-trim", func(next *planning.Contract) { next.Title = "Trim the log to a day" })
	d.HoldRepository(t, "checkout")
	var contract string
	if err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		order, err := tx.WorkOrders().Get("wo-trim")
		contract = order.ContractID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scopes := map[string]string{
		"d-plan": "plan:" + w.PlanOf("wo-trim"), "d-contract": "contract:" + contract, "d-work": "wo:wo-trim", "d-repository": "repo:checkout",
	}
	for id, scope := range scopes {
		if _, err := d.Client.RecordDecision(t.Context(), api.RecordDecision{
			ID: id, Status: questions.DecisionDecided, Statement: "Decided under " + scope, Scope: scope, Reversibility: questions.Reversible,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Store.Write(t.Context(), apitest.Scope, "test.decision", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(questions.Decision{
			ID: "d-legacy", Status: questions.DecisionDecided, Statement: "Decided before the grammar", Scope: "payments/retries",
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.decision", Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}, Subject: ledger.Subject{Kind: "decision", ID: "d-legacy"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return d, map[string]string{
		"d-plan": "Trim the log", "d-contract": "Trim the log to a day", "d-work": "Trim the log", "d-repository": "", "d-legacy": "",
	}
}

// TestAQuestionReadsWithTheScopeItsAnswerTakesAndItsTitle holds that every read of questions says the scope an
// answer to each takes, from the record it is about, with that record's title, in the same answer: the list,
// one question, the questions view, the batches, and Needs you.
func TestAQuestionReadsWithTheScopeItsAnswerTakesAndItsTitle(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	w.Recorded("wo-trim", "Trim the log")
	plan := w.PlanOf("wo-trim")
	type named struct{ scope, title string }
	want := map[string]named{
		"question-plan": {"plan:" + plan, "Trim the log"}, "question-work": {"wo:wo-trim", "Trim the log"},
		"question-gone": {"wo:wo-gone", ""}, "question-none": {"", ""},
	}
	for id, subject := range map[string][2]string{
		"question-plan": {planning.SubjectPlan, plan}, "question-work": {workorders.SubjectKind, "wo-trim"},
		"question-gone": {workorders.SubjectKind, "wo-gone"}, "question-none": {"", ""},
	} {
		escalateAbout(t, w, questions.Ask{ID: id, Text: "Is " + id + " settled?", SubjectKind: subject[0], SubjectID: subject[1]})
	}
	clock.Advance(time.Minute)
	w.Deliver()

	listed := map[string]named{}
	rows, err := d.Client.Questions(t.Context(), api.RecordFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		listed[row.ID] = named{row.Scope, row.ScopeTitle}
	}
	if !maps.Equal(listed, want) {
		t.Errorf("the list names the scopes %v, want %v", listed, want)
	}
	for id, scope := range want {
		read, err := d.Client.Question(t.Context(), id)
		if err != nil || (named{read.Scope, read.ScopeTitle}) != scope {
			t.Errorf("%s reads with the scope %q · %q, want %v: %v", id, read.Scope, read.ScopeTitle, scope, err)
		}
	}
	viewed := map[string]named{}
	for _, row := range readView[api.Questions](t, d, api.ViewQuestions, "").Questions {
		viewed[row.ID] = named{row.Scope, row.ScopeTitle}
	}
	if !maps.Equal(viewed, want) {
		t.Errorf("the questions view names the scopes %v, want %v", viewed, want)
	}
	batches, err := d.Client.Batches(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	var needs []api.QuestionBatch
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Batch != nil {
			needs = append(needs, *item.Batch)
		}
	}
	for place, read := range map[string][]api.QuestionBatch{"the batches": batches, "needs you": needs} {
		presented := map[string]named{}
		for _, batch := range read {
			for _, item := range batch.Items {
				presented[item.ID] = named{item.Scope, item.ScopeTitle}
			}
		}
		if !maps.Equal(presented, want) {
			t.Errorf("%s name the scopes %v, want %v", place, presented, want)
		}
	}
}

// escalateAbout asks a question from a lane and takes it up the ladder to a person, as an orchestrator would.
func escalateAbout(t *testing.T, w *apitest.World, ask questions.Ask) {
	t.Helper()
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-asking"}
	ask.Lane, ask.Generation, ask.Categories = lane.ID, 1, []string{questions.CategoryProduct}
	asked, err := w.Questions.Ask(t.Context(), apitest.Scope, ask, lane)
	if err != nil {
		t.Fatal(err)
	}
	holder := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}
	if _, err := w.Questions.Take(t.Context(), apitest.Scope, asked.QuestionID, apitest.LeaseTTL, holder); err != nil {
		t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		if _, err := w.Questions.Climb(t.Context(), apitest.Scope, asked.QuestionID, questions.Step{Rung: rung, Outcome: "no match"}, holder); err != nil {
			t.Fatal(err)
		}
	}
	escalation := questions.Escalation{Reason: questions.ReasonScopeChange, Options: []string{"Yes", "No"}, Recommendation: "Yes"}
	if _, err := w.Questions.Escalate(t.Context(), apitest.Scope, asked.QuestionID, escalation, holder); err != nil {
		t.Fatal(err)
	}
}

// TestADecisionReadsWithTheTitleOfTheRecordItsScopeNames holds that every read of decisions names the record
// each scope names, in the same answer: the list, one decision, and the decisions view.
func TestADecisionReadsWithTheTitleOfTheRecordItsScopeNames(t *testing.T) {
	t.Parallel()
	d, want := titledScopes(t)
	listed := map[string]string{}
	rows, err := d.Client.Decisions(t.Context(), api.RecordFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		listed[row.ID] = row.ScopeTitle
	}
	if !maps.Equal(listed, want) {
		t.Errorf("the list titles the scopes %v, want %v", listed, want)
	}
	for id, title := range want {
		read, err := d.Client.Decision(t.Context(), id)
		if err != nil || read.ScopeTitle != title {
			t.Errorf("%s reads with the scope title %q, want %q: %v", id, read.ScopeTitle, title, err)
		}
	}
	viewed := map[string]string{}
	for _, line := range readView[api.Decisions](t, d, api.ViewDecisions, "").Decisions {
		viewed[line.ID] = line.ScopeTitle
	}
	if !maps.Equal(viewed, want) {
		t.Errorf("the decisions view titles the scopes %v, want %v", viewed, want)
	}
}
