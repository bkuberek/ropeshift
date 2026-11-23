package worker_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A lane that asked a blocking question and has no answer yet cannot report blocked: the report would end the lane, and
// the answer would reach a generation that is over. The refusal tells it to end its turn and wait, and the lane stays
// at work with nothing recorded.
func TestABlockedReportIsRefusedWhileTheLanesOwnQuestionIsOpen(t *testing.T) {
	t.Parallel()
	g := newGate()
	r := laneRigWith(t, restingAsk)
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("Which retry budget?"), g.step(), completed(0.01)}))
	r.start()
	r.atGate(g)
	if asked := r.questionList(t); len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	reply := r.laneReport(t, workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: "waiting_on_answer"})
	if reply.OK {
		t.Fatal("a blocked report was taken while the lane's own blocking question was open")
	}
	for _, want := range []string{"End your turn", "wait for the answer"} {
		if !strings.Contains(reply.Message, want) {
			t.Errorf("the refusal %q does not say %q", reply.Message, want)
		}
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Report) != 0 {
		t.Errorf("the journal kept the refused report: %s", record.Report)
	}
	if done := r.laneReport(t, workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "gave_up"}); !done.OK {
		t.Errorf("a failed report was refused too: %+v", done)
	}
}

// Once the lane has read the answer to its question nothing is open, so a blocked report is taken.
func TestABlockedReportIsTakenOnceTheLanesQuestionIsAnswered(t *testing.T) {
	t.Parallel()
	g := newGate()
	r := laneRigWith(t, restingAsk)
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("Which retry budget?"), g.step(), completed(0.01)}))
	r.start()
	r.atGate(g)
	asked := r.questionList(t)
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	r.answerQuestion(t, asked[0].ID, "Three retries.")
	answer := r.answerOf(t)
	r.awaitMessage(t, answer.ID, messages.HandedOff)
	r.hookEvent(t, lanes.HookEvent{Event: "UserPromptSubmit", Subject: "s", Message: answer.ID})
	r.awaitMessage(t, answer.ID, messages.Admitted)
	if reply := r.laneReport(t, workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: "cannot_go_on"}); !reply.OK {
		t.Errorf("a blocked report was refused after the answer: %+v", reply)
	}
}
