package server_test

import (
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
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
	d := apitest.StartWith(t, apitest.Options{Windows: messages.Windows{messages.KindAnswer: answerWindow}})
	lane := attached(t, d, "q1")
	if _, err := d.Client.AnswerQuestion(t.Context(), "q1-lane", api.AnswerQuestion{ID: "d1", Answer: "Twice.", Rationale: "It follows."}); err != nil {
		t.Fatal(err)
	}
	checkAnswerWindow(t, d, lane)
}

// TestARulingTakesTheDaemonsWindow is the same on the worker plane, which holds use cases of its own.
func TestARulingTakesTheDaemonsWindow(t *testing.T) {
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
