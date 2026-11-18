package server_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

var answerWindow = messages.Window{StaleAfter: time.Minute, ExpireAfter: 3 * time.Minute}

// attached has a lane that ran, on a question that waits, and returns that lane.
func attached(t *testing.T, d *apitest.Daemon, question string) string {
	t.Helper()
	d.Completed(t, "wo-"+question, workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}})
	lane := "lane-wo-" + question
	ask := questions.Ask{ID: question + "-lane", Text: "How often should a payment retry?", Categories: []string{questions.CategoryProduct}, Lane: lane, Generation: 1}
	if _, err := control.NewQuestions(d.Store, questions.BatchPolicy{}).Ask(t.Context(), apitest.Scope, ask, ledger.Actor{Kind: ledger.ActorLane, ID: lane}); err != nil {
		t.Fatal(err)
	}
	return lane
}

func checkAnswerWindow(t *testing.T, d *apitest.Daemon, lane string) {
	t.Helper()
	rows, err := d.Client.LaneMessages(t.Context(), lane)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("lane %s holds %+v", lane, rows)
	}
	if lived := time.Duration(rows[0].ExpiresAt-rows[0].CreatedAt) * time.Millisecond; lived != answerWindow.ExpireAfter {
		t.Errorf("an answer lives %s under a window of %s", lived, answerWindow.ExpireAfter)
	}
}

// TestAMembersAnswerTakesTheDaemonsWindow reads what the daemon was configured with on the path a
// member's command takes.
func TestAMembersAnswerTakesTheDaemonsWindow(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Windows: messages.Windows{messages.KindAnswer: answerWindow}})
	lane := attached(t, d, "q1")
	if _, err := d.Client.AnswerQuestion(t.Context(), "q1-lane", api.AnswerQuestion{ID: "d1", Answer: "Twice.", Rationale: "It follows."}); err != nil {
		t.Fatal(err)
	}
	checkAnswerWindow(t, d, lane)
}

// TestAnAnswerThatNamesADecisionReplacesIt holds the answer route to the transaction ADR-0030 describes: the
// decision the answer names ends `replaced`, naming the answer, and the answer records what it replaced and
// what it judged compatible.
func TestAnAnswerThatNamesADecisionReplacesIt(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	ctx := t.Context()
	record := func(id, statement string) {
		t.Helper()
		if _, err := d.Client.RecordDecision(ctx, api.RecordDecision{
			ID: id, Status: questions.DecisionDecided, Statement: statement, Rationale: "It follows.", Citations: []string{"PAY-12"},
			Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
		}); err != nil {
			t.Fatal(err)
		}
	}
	record("d1", "Retries log at info.")
	record("d2", "Retries emit a metric.")
	if _, err := d.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q1", Text: "At what level do retries log?"}); err != nil {
		t.Fatal(err)
	}
	answer := api.AnswerQuestion{ID: "d3", Answer: "Retries log at warn.", Supersedes: "d1", NoConflict: []string{"d2"}}
	got, err := d.Client.AnswerQuestion(ctx, "q1", answer)
	if err != nil {
		t.Fatal(err)
	}
	if got.Supersedes != "d1" || !slices.Equal(got.NoConflict, []string{"d2"}) {
		t.Fatalf("the answer became %+v", got)
	}
	read, err := d.Client.Decision(ctx, "d1")
	if err != nil {
		t.Fatal(err)
	}
	if read.Status != questions.DecisionReplaced {
		t.Errorf("the decision the answer named reads %s", read.Status)
	}
	if _, err := d.Client.RecordQuestion(ctx, api.RecordQuestion{ID: "q2", Text: "Again?"}); err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.AnswerQuestion(ctx, "q2", api.AnswerQuestion{ID: "d4", Answer: "Retries log at error.", Supersedes: "d1"})
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeDecisionEnded {
		t.Errorf("an answer that names a replaced decision was refused as %v, want 409 %s", err, api.CodeDecisionEnded)
	}
}

// TestARulingTakesTheDaemonsWindow is the same on the worker plane, which holds use cases of its own.
func TestARulingTakesTheDaemonsWindow(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Windows: messages.Windows{messages.KindAnswer: answerWindow}})
	r := rulingRigOn(t, d)
	lane := attached(t, d, "q1")
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: testWorker}
	if _, err := r.use.Take(t.Context(), apitest.Scope, "q1-lane", 60_000, orchestrator); err != nil {
		t.Fatal(err)
	}
	for step := questions.RungLookup; step < questions.RungDecide; step++ {
		if _, err := r.use.Climb(t.Context(), apitest.Scope, "q1-lane", questions.Step{Rung: step, Outcome: "no match"}, orchestrator); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.rule(t, "q1-lane", draftRuling("d1", questions.DecisionDecided)); err != nil {
		t.Fatal(err)
	}
	checkAnswerWindow(t, d, lane)
}
