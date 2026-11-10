package worker_test

import (
	"encoding/json"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ask reaches the worker the way a session's `ask` tool does.
func (r *laneRig) ask(t *testing.T, request lanes.AskRequest) lanes.Reply {
	t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(t), Ask: &request})
	if err != nil {
		t.Fatal(err)
	}
	return r.subject.AnswerLane(t.Context(), line)
}

func (r *laneRig) questionList(t *testing.T) []questions.Question {
	t.Helper()
	var rows []questions.Question
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Questions().Questions(ports.RecordFilter{})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAnAskBecomesALedgerQuestion(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	reply := r.ask(t, lanes.AskRequest{Text: "What is the retry budget?", Categories: []string{questions.CategoryProduct}, Blocking: true})
	if !reply.OK || reply.Ask == nil || reply.Ask.Status != questions.AskPending {
		t.Fatalf("reply %+v", reply)
	}
	rows := r.questionList(t)
	if len(rows) != 1 {
		t.Fatalf("questions %+v", rows)
	}
	row := rows[0]
	if row.AskedBy != "lane-1" || row.SubjectKind != workorders.SubjectKind || row.SubjectID != r.id || !row.Blocking {
		t.Errorf("question %+v", row)
	}
	repeat := r.ask(t, lanes.AskRequest{Text: "what is the retry budget"})
	if !repeat.OK || repeat.Ask.Status != questions.AskAttached || repeat.Ask.QuestionID != row.ID {
		t.Errorf("a repeat: %+v", repeat)
	}
	if bad := r.ask(t, lanes.AskRequest{Text: " ?! "}); bad.OK || bad.Code != lanes.CodeInvalid {
		t.Errorf("an empty question: %+v", bad)
	}
}

// TestNoAskStaysOpen is the unit's check: a control plane that took the call and went quiet
// costs the lane one bounded wait, because `ask` never holds a call open.
func TestNoAskStaysOpen(t *testing.T) {
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.link.HangAsk.Store(true)
	started := time.Now()
	reply := r.ask(t, lanes.AskRequest{Text: "What is the retry budget?"})
	if reply.OK || reply.Code != lanes.CodeUnavailable {
		t.Errorf("reply %+v", reply)
	}
	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Errorf("the call stayed open for %s", elapsed)
	}
}

// forceFence leaves the worker believing an old fence, as a worker that was away does.
func (r *laneRig) forceFence(t *testing.T) {
	t.Helper()
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	record.Fence--
	if err := r.journal.SaveLane(t.Context(), record); err != nil {
		t.Fatal(err)
	}
}

func TestAFencedLaneIsToldSo(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.forceFence(t)
	reply := r.ask(t, lanes.AskRequest{Text: "What is the retry budget?"})
	if reply.OK || reply.Code != lanes.CodeFenced {
		t.Errorf("reply %+v", reply)
	}
	if rows := r.questionList(t); len(rows) != 0 {
		t.Errorf("questions %+v", rows)
	}
}

// TestAQuestionToolCallBecomesALedgerQuestion is the conversion on a headless lane. The hook
// is answered at once, and the question follows.
func TestAQuestionToolCallBecomesALedgerQuestion(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	event := lanes.HookEvent{
		Event: "PreToolUse", Tool: lanes.ToolQuestion, Subject: "toolu_1",
		Asked: []lanes.AskedQuestion{{Text: "Which retry budget?", Options: []string{"Three", "Five"}}},
	}
	r.hook(t, event)
	r.hook(t, event)
	deadline := time.Now().Add(10 * time.Second)
	var rows []questions.Question
	for time.Now().Before(deadline) {
		if rows = r.questionList(t); len(rows) != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(rows) != 1 || rows[0].Text != "Which retry budget?" || !rows[0].Blocking || len(rows[0].Options) != 2 {
		t.Fatalf("questions %+v", rows)
	}
}
