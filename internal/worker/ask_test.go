package worker_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
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

// A lane's options, recommendation, and rationale reach the ledger with its question, and a recommendation that is
// none of its options is refused to the lane.
func TestAnAskCarriesItsRecommendation(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	options := []string{"Retry twice", "Retry until the deadline"}
	if bad := r.ask(t, lanes.AskRequest{Text: "How often does a call retry?", Options: options, Recommendation: "Never retry"}); bad.OK || bad.Code != lanes.CodeInvalid {
		t.Errorf("a recommendation that is no option: %+v", bad)
	}
	reply := r.ask(t, lanes.AskRequest{
		Text: "How often does a call retry?", Options: options, Recommendation: "Retry twice", RecommendationRationale: "It fits the deadline.",
	})
	if !reply.OK || reply.Ask == nil || reply.Ask.Status != questions.AskPending {
		t.Fatalf("reply %+v", reply)
	}
	rows := r.questionList(t)
	if len(rows) != 1 || !slices.Equal(rows[0].Options, options) || rows[0].Recommendation != "Retry twice" || rows[0].RecommendationRationale != "It fits the deadline." {
		t.Errorf("questions %+v", rows)
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

// heldAsks is a control plane that answers an ask only once the test lets it, past any deadline.
type heldAsks struct {
	*fake.WorkerLink
	reached chan struct{}
	once    sync.Once
	release chan struct{}
}

func (l *heldAsks) Ask(ctx context.Context, ask questions.LaneAsk) (questions.AskResult, error) {
	l.once.Do(func() { close(l.reached) })
	<-l.release
	return l.WorkerLink.Ask(ctx, ask)
}

// free lets every held ask go on. It is safe to call twice.
func (l *heldAsks) free() {
	select {
	case <-l.release:
	default:
		close(l.release)
	}
}

// TestWaitOutlastsAQuestionsConversion stops a worker while it carries a question tool's question to the
// control plane. The conversion goes on after its lane has ended, so the worker's wait covers it.
func TestWaitOutlastsAQuestionsConversion(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	link := &heldAsks{WorkerLink: base.link, reached: make(chan struct{}), release: make(chan struct{})}
	base.workerLink = link
	r := newLaneRigFor(t, base).dispatched()
	g := newGate()
	r.finishLater(g)
	t.Cleanup(link.free)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{
		Event: "PreToolUse", Tool: lanes.ToolQuestion, Subject: "toolu_1",
		Asked: []lanes.AskedQuestion{{Text: "Which retry budget?", Options: []string{"Three", "Five"}}},
	})
	select {
	case <-link.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the question never reached the control plane")
	}
	g.free()
	r.driver.StopEverySession()
	for deadline := time.Now().Add(20 * time.Second); r.subject.Running("lane-1"); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the lane never ended")
		}
	}
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		r.subject.Wait()
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned while a question's conversion was on its way")
	case <-time.After(100 * time.Millisecond):
	}
	link.free()
	select {
	case <-waited:
	case <-time.After(20 * time.Second):
		t.Fatal("Wait never returned once the conversion ended")
	}
}
