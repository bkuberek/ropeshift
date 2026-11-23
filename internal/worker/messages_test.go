package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

const minuteMs = int64(time.Minute / time.Millisecond)

// send records a message, as a member's call would.
func (r *laneRig) send(t *testing.T, id string, kind messages.Kind, change func(*messages.Draft)) messages.Message {
	t.Helper()
	draft := messages.Draft{ID: id, To: messages.Target{Lane: "lane-1"}, Kind: kind, Body: "Use two retries."}
	if change != nil {
		change(&draft)
	}
	if kind == messages.KindAnswer {
		return r.answered(t, id, draft.Body)
	}
	m, err := control.NewMessages(r.store, nil).Send(t.Context(), scope, draft, member)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// answered has the lane ask a question and a member answer it, which is the one way a lane is sent an answer, and
// returns the message the answer made. Its ID is derived from the question, which answerID gives for the ID named here.
func (r *laneRig) answered(t *testing.T, id, statement string) messages.Message {
	t.Helper()
	ops := control.NewQuestions(r.store, questions.BatchPolicy{})
	question := "q-" + id
	if _, err := ops.Ask(t.Context(), scope, questions.Ask{ID: question, Text: "Which retry budget?", Lane: "lane-1"}, member); err != nil {
		t.Fatal(err)
	}
	answer := questions.Answer{DecisionID: "d-" + question, Statement: statement, Rationale: "It is what the contract tests."}
	if _, err := ops.Answer(t.Context(), scope, question, answer, member); err != nil {
		t.Fatal(err)
	}
	return r.message(t, answerID(id))
}

// answerID is the ID of the message the answer to the question that answered(id) asked is sent as.
func answerID(id string) string {
	return messages.DerivedID(string(messages.KindAnswer), "q-"+id, "lane-1")
}

// beat is one heartbeat's worth of work, by hand: it reads desired state, reconciles, and uploads.
func (r *laneRig) beat(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	if err := r.subject.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Upload(ctx); err != nil {
		t.Fatal(err)
	}
}

func (r *laneRig) message(t *testing.T, id string) messages.Message {
	t.Helper()
	m, err := control.NewMessages(r.store, nil).ReadMessage(t.Context(), scope, id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (r *laneRig) awaitMessage(t *testing.T, id string, state messages.State) messages.Message {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var m messages.Message
	for time.Now().Before(deadline) {
		r.beat(t)
		if m = r.message(t, id); m.State == state {
			return m
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("message %s reads %s, want %s", id, m.State, state)
	return m
}

// peerInputs lists what reached a session on a peer's path.
func (r *laneRig) peerInputs() []ports.Input {
	var out []ports.Input
	for _, input := range r.driver.Sent() {
		if input.Peer {
			out = append(out, input)
		}
	}
	return out
}

func (r *laneRig) hookEvent(t *testing.T, event lanes.HookEvent) lanes.Reply {
	t.Helper()
	event.AtMs = time.Now().UnixMilli()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(t), Hook: &event})
	if err != nil {
		t.Fatal(err)
	}
	return r.subject.AnswerLane(t.Context(), line)
}

func (r *laneRig) reply(t *testing.T, id, body string) lanes.Reply {
	t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(t), Reply: &lanes.ReplyRequest{ReplyTo: id, Body: body}})
	if err != nil {
		t.Fatal(err)
	}
	return r.subject.AnswerLane(t.Context(), line)
}

// held is a lane at work that a test can reach with messages, whose first turn ends when it says
// so. A test that stops early lets it go too, or its cleanup would wait on that turn for good.
func held(t *testing.T, answer ...fake.DriverStep) *handoffRig {
	t.Helper()
	r := atWork(t, answer...)
	t.Cleanup(r.letGo)
	return r
}

func (r *handoffRig) letGo() {
	select {
	case <-r.release:
	default:
		close(r.release)
	}
}

func heldBack(t *testing.T) *handoffRig {
	t.Helper()
	return held(t, completed(0.02))
}

// TestAMessageIsWrittenOnceAndClaimsOnlyAHandOff is the worker's half of FR-MSG-2. The session
// holds a turn, so no admission can arrive, and a write proves only that it was handed over.
func TestAMessageIsWrittenOnceAndClaimsOnlyAHandOff(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.beat(t)
	r.beat(t)
	r.beat(t)
	inputs := r.peerInputs()
	if len(inputs) != 1 {
		t.Fatalf("%d writes of one message: %+v", len(inputs), inputs)
	}
	if inputs[0].ID != messages.InputID(m.ID) {
		t.Errorf("input %s", inputs[0].ID)
	}
	if !strings.HasPrefix(inputs[0].Text, "[ropeshift message m-1 kind=instruction age=") || !strings.Contains(inputs[0].Text, "tier=fresh from=member]") {
		t.Errorf("first line %q", inputs[0].Text)
	}
	if !strings.Contains(inputs[0].Text, m.Body) {
		t.Errorf("no body in %q", inputs[0].Text)
	}
	if got := r.message(t, m.ID); got.State != messages.HandedOff {
		t.Errorf("state %s", got.State)
	}
	r.letGo()
}

// TestALiveAnswerToTheLanesQuestionIsStatedAsInForce is the delivery path of a running turn: the answer to a question
// the lane asked reaches its session as in force, with no line that says it is no consent, and any other message the
// session is sent keeps that line.
func TestALiveAnswerToTheLanesQuestionIsStatedAsInForce(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	answer := r.send(t, "m-1", messages.KindAnswer, nil)
	note := r.send(t, "m-2", messages.KindNotice, nil)
	r.beat(t)
	r.awaitMessage(t, answer.ID, messages.HandedOff)
	r.awaitMessage(t, note.ID, messages.HandedOff)
	sent := map[string]string{}
	for _, input := range r.peerInputs() {
		sent[input.ID] = input.Text
	}
	if text := sent[messages.InputID(answer.ID)]; !strings.HasSuffix(text, messages.AnswerInForce+"\n") || strings.Contains(text, messages.NotConsent) {
		t.Errorf("the answer reached the session as:\n%s", text)
	}
	if text := sent[messages.InputID(note.ID)]; !strings.HasSuffix(text, messages.NotConsent+"\n") || strings.Contains(text, messages.AnswerInForce) {
		t.Errorf("the notice reached the session as:\n%s", text)
	}
	r.letGo()
}

// TestAWorkerAcknowledgesWhatItHoldsBeforeASessionCanTakeIt is where transport acknowledgement
// stops: at the worker, and not at the session.
func TestAWorkerAcknowledgesWhatItHoldsBeforeASessionCanTakeIt(t *testing.T) {
	t.Parallel()
	r := inBriefing(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.beat(t)
	if got := r.message(t, m.ID); got.State != messages.AtWorker {
		t.Errorf("state %s", got.State)
	}
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write to a session that has not begun: %+v", r.peerInputs())
	}
}

// inBriefing is a lane whose session took no prompt yet, so nothing can be written to it.
func inBriefing(t *testing.T) *laneRig {
	t.Helper()
	r := laneRigWith(t, handoffProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.Play(fake.DriverScript{SessionID: "session-1", Model: "resolved-model", Silent: true})
	r.start()
	r.eventuallyUp()
	r.eventually("a session", func(view ledger.LaneView) bool { return view.Generation == 1 })
	return r
}

// TestAStreamReplayAdmitsAMessage is the headless signal: the input's own ID comes back.
func TestAStreamReplayAdmitsAMessage(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.letGo()
	r.awaitMessage(t, m.ID, messages.Admitted)
}

// TestAHookAdmitsAMessage is the signal in both run modes, and the only one in a pane.
func TestAHookAdmitsAMessage(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	if reply := r.hookEvent(t, lanes.HookEvent{Event: "UserPromptSubmit", Subject: "s", Message: m.ID}); !reply.OK {
		t.Fatalf("%+v", reply)
	}
	r.awaitMessage(t, m.ID, messages.Admitted)
	r.letGo()
}

// TestAHookOnAnotherLanesMessageChangesNothing stops at the message's address, because a prompt
// can open with any line at all.
func TestAHookOnAnotherLanesMessageChangesNothing(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	foreign := messages.Held{
		Message: messages.Message{ID: "m-foreign", To: messages.Target{Lane: "lane-2", Generation: 1}, Kind: messages.KindNotice, State: messages.AtWorker},
		InputID: messages.InputID("m-foreign"),
	}
	if _, err := r.journal.TakeMessage(t.Context(), foreign, ledger.UploadEvent{Type: "note", OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	// The other lane stands on this worker, so only the message's address stops the move.
	if err := r.journal.SaveLane(t.Context(), lanes.Record{ID: "lane-2", WorkOrder: "other", ClaimID: "c", Fence: 1, Generation: 1, State: lanes.StateRunning}); err != nil {
		t.Fatal(err)
	}
	r.hookEvent(t, lanes.HookEvent{Event: "UserPromptSubmit", Subject: "s", Message: foreign.ID})
	got, err := r.journal.Message(t.Context(), foreign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != messages.AtWorker {
		t.Errorf("one lane admitted another's message: %s", got.State)
	}
	r.letGo()
}

// TestAHookCannotReviveARefusedMessage holds an admission to the table: a session that refused a
// message did not take it, whatever a prompt's first line claims.
func TestAHookCannotReviveARefusedMessage(t *testing.T) {
	t.Parallel()
	m := messages.Message{ID: "m-1"}
	r := held(t, fake.DriverStep{Refused: messages.InputID(m.ID)}, completed(0.02))
	r.driver.SetIgnoresPeers(true)
	r.send(t, m.ID, messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.letGo()
	r.awaitMessage(t, m.ID, messages.Refused)
	r.hookEvent(t, lanes.HookEvent{Event: "UserPromptSubmit", Subject: "s", Message: m.ID})
	got, err := r.journal.Message(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != messages.Refused {
		t.Errorf("a hook moved a refused message to %s", got.State)
	}
}

// TestASpooledHookAdmitsAMessage is a worker that was away when the hook ran. The spool is the
// only record of the signal, and no run need exist yet.
func TestASpooledHookAdmitsAMessage(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.spoolLine(t, sealedLine(t, r.token(t), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "UserPromptSubmit", Subject: "s", Message: m.ID, AtMs: 1}))
	if err := r.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.awaitMessage(t, m.ID, messages.Admitted)
	r.letGo()
}

// TestAWorkersOwnInputAdmitsNoMessage holds an admission to what the message's own ID names: a
// brief or a nudge comes back through the same signal.
func TestAWorkersOwnInputAdmitsNoMessage(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.hookEvent(t, lanes.HookEvent{Event: "UserPromptSubmit", Subject: "s"})
	r.beat(t)
	if got := r.message(t, m.ID); got.State != messages.HandedOff {
		t.Errorf("a prompt with no message moved one to %s", got.State)
	}
	r.letGo()
}

// TestAMessageAtAParkingLaneWaitsForTheRollover writes nothing to a session that is on its way out.
func TestAMessageAtAParkingLaneWaitsForTheRollover(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = held(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}, completed(0.02))
	r.park(t, humanPark())
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	answer := r.send(t, "m-2", messages.KindAnswer, nil)
	r.beat(t)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write to a parking lane: %+v", r.peerInputs())
	}
	if got := r.awaitMessage(t, m.ID, messages.Expired); got.Reason != messages.ReasonGenerationEnded {
		t.Errorf("%+v", got)
	}
	if got := r.awaitMessage(t, answer.ID, messages.Expired); got.Reason != messages.ReasonLedgerFact {
		t.Errorf("%+v", got)
	}
}

// TestAHandedOffMessageTellsItsSenderWhenTheProcessDies is the case with no receipt: a message
// that has not entered a turn dies with its process, and only the end of its generation says so.
func TestAHandedOffMessageTellsItsSenderWhenTheProcessDies(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = held(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}, completed(0.02))
	r.driver.SetIgnoresPeers(true)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	got := r.awaitMessage(t, m.ID, messages.Expired)
	if got.Reason != messages.ReasonGenerationEnded {
		t.Errorf("%+v", got)
	}
}

// TestAMessageToAnEndedGenerationIsNeverWritten holds the worker to its own read: the ledger has
// not heard the lane moved on, and the worker will not write to a session that is gone.
func TestAMessageToAnEndedGenerationIsNeverWritten(t *testing.T) {
	t.Parallel()
	r := held(t, fake.DriverStep{Do: func(spec ports.SessionSpec) {}}, completed(0.02))
	old := messages.Held{
		Message: messages.Message{ID: "m-old", To: messages.Target{Lane: "lane-1", Generation: 0}, Kind: messages.KindInstruction, Body: "Old.", State: messages.AtWorker},
		InputID: messages.InputID("m-old"), TakenMs: r.clock.Now().UnixMilli(), StaleAfterMs: 2 * minuteMs, ExpireAfterMs: 10 * minuteMs,
	}
	if _, err := r.journal.TakeMessage(t.Context(), old, ledger.UploadEvent{Type: "note", OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	r.beat(t)
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write to an ended generation: %+v", r.peerInputs())
	}
	waiting, err := r.journal.WaitingMessages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 0 {
		t.Errorf("a worker still holds %+v", waiting)
	}
	r.letGo()
}

// TestAMessageAheadOfThisRunWaits is the other side of that read: a generation this run has not
// reached yet has a session to come, so the message is neither written here nor let go.
func TestAMessageAheadOfThisRunWaits(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	ahead := messages.Held{
		Message: messages.Message{ID: "m-ahead", To: messages.Target{Lane: "lane-1", Generation: 2}, Kind: messages.KindInstruction, Body: "Later.", State: messages.AtWorker},
		InputID: messages.InputID("m-ahead"), TakenMs: r.clock.Now().UnixMilli(), StaleAfterMs: 2 * minuteMs, ExpireAfterMs: 10 * minuteMs,
	}
	if _, err := r.journal.TakeMessage(t.Context(), ahead, ledger.UploadEvent{Type: "note", OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	r.beat(t)
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write to a generation that has not begun: %+v", r.peerInputs())
	}
	waiting, err := r.journal.WaitingMessages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 {
		t.Errorf("waiting %+v", waiting)
	}
}

// TestAStaleMessageSaysSo reads its age from the ledger, plus what this worker's clock saw.
func TestAStaleMessageSaysSo(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.shiftDatabase(t, 3*minuteMs)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	inputs := r.peerInputs()
	if len(inputs) != 1 || !strings.Contains(inputs[0].Text, "tier=stale") || !strings.Contains(inputs[0].Text, "age=3m") {
		t.Errorf("%+v", inputs)
	}
	r.letGo()
}

// TestAMessagePastItsWindowIsNeverWritten is expiry at the worker. The ledger's tick says so to the
// sender, and the worker only stops holding it.
func TestAMessagePastItsWindowIsNeverWritten(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.shiftDatabase(t, 11*minuteMs)
	r.beat(t)
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write past the window: %+v", r.peerInputs())
	}
	if _, err := control.NewMessages(r.store, nil).ExpireMessages(t.Context(), scope, member); err != nil {
		t.Fatal(err)
	}
	if got := r.message(t, m.ID); got.State != messages.Expired || got.Reason != messages.ReasonTimedOut {
		t.Errorf("%+v", got)
	}
	r.letGo()
}

// TestAMessageAgesOnTheWorkersClockAfterItArrives never asks the ledger again: a message that
// outlives its window behind a worker that cannot reach the ledger is not written.
func TestAMessageAgesOnTheWorkersClockAfterItArrives(t *testing.T) {
	t.Parallel()
	r := inBriefing(t)
	r.send(t, "m-1", messages.KindInstruction, nil)
	r.beat(t)
	waiting, err := r.journal.WaitingMessages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 {
		t.Fatalf("waiting %+v", waiting)
	}
	r.clock.Advance(11 * time.Minute)
	r.beat(t)
	if waiting, err = r.journal.WaitingMessages(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 0 {
		t.Errorf("a worker still holds what outlived its window: %+v", waiting)
	}
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write past the window: %+v", r.peerInputs())
	}
}

// TestAVersionWithNoCheckedSocketWritesNothing is a driver with no path for a peer. No stdin stands
// in, because that would give a message the standing of a worker's own input. The message is not left
// waiting at the worker: it reads undeliverable, or, when it bears a decision, that the next generation
// is told of it, and the worker stops holding it.
func TestAVersionWithNoCheckedSocketWritesNothing(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	r.driver.SetPeerChannel(false)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	notice := r.send(t, "m-2", messages.KindDecisionNotice, nil)
	r.beat(t)
	r.beat(t)
	if got := r.message(t, m.ID); got.State != messages.Expired || got.Reason != messages.ReasonUndeliverable {
		t.Errorf("a message no channel took reads %s (%s)", got.State, got.Reason)
	}
	if got := r.message(t, notice.ID); got.State != messages.Expired || got.Reason != messages.ReasonLedgerFact {
		t.Errorf("a decision no channel took reads %s (%s)", got.State, got.Reason)
	}
	waiting, err := r.journal.WaitingMessages(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 0 {
		t.Errorf("a worker still holds %+v", waiting)
	}
	for _, input := range r.driver.Sent() {
		if strings.HasPrefix(input.Text, "[ropeshift message") {
			t.Errorf("a message took the worker's own path: %+v", input)
		}
	}
	r.letGo()
}

// TestABlockingAnswerWithNoChannelGoesInThroughAResume is the answer a lane waits on, on a version with no path for a
// peer: the lane parks at once, on a wake its answer has already fired, and the answer reaches the generation that
// resumes it in its first prompt. No input of the worker's carries it into the session it waited in.
func TestABlockingAnswerWithNoChannelGoesInThroughAResume(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"3m\"\nmax_restarts = 0\n")
	r.driver.SetPeerChannel(false)
	r.driver.Play(r.asksThenNarrates("Which retry budget?", notCarried))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.awaitAskWait()
	asked := r.questionList(t)
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	answer := questions.Answer{DecisionID: "d-member", Statement: "Three retries.", Rationale: "It is what the contract tests."}
	if _, err := control.NewQuestions(r.store, questions.BatchPolicy{}).Answer(t.Context(), scope, asked[0].ID, answer, member); err != nil {
		t.Fatal(err)
	}
	r.beat(t)
	r.awaitWoken(t)
	r.subject.Wait()
	for _, input := range r.driver.Sent() {
		if strings.Contains(input.Text, "Three retries.") {
			t.Errorf("the answer went in as an input of the worker's: %+v", input)
		}
	}
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.QuestionWake([]string{asked[0].ID}) || wake.Open() {
		t.Errorf("the lane parked on %+v, want a fired wake on its answered question", wake)
	}
	if parked := r.parkingOf(t); parked == nil || parked.Trigger != lanes.ParkBlockingQuestion {
		t.Errorf("the lane parked with %+v", parked)
	}
	var told string
	for _, m := range r.laneMessages(t) {
		if m.Kind == messages.KindAnswer {
			told = m.ID
			if m.State != messages.Expired || m.Reason != messages.ReasonLedgerFact {
				t.Errorf("the answer reads %s (%s)", m.State, m.Reason)
			}
		}
	}
	if told == "" {
		t.Fatal("no answer was sent to the lane")
	}
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write on a peer's path: %+v", r.peerInputs())
	}
	r.resumed(t)
	if brief := r.driver.Specs()[1].Brief; !strings.Contains(brief, "Three retries.") {
		t.Errorf("the first prompt of the resumed generation:\n%s", brief)
	}
}

// asksAllThenNarrates is a session that asks each of its questions as a blocking one and comes to rest, then answers
// the instruction that asks for its narrative.
func (r *laneRig) asksAllThenNarrates(narrative string, asked ...string) fake.DriverScript {
	steps := []fake.DriverStep{{Bound: true}}
	for _, text := range asked {
		steps = append(steps, fake.DriverStep{Do: func(spec ports.SessionSpec) {
			r.callAs(spec, lanes.Request{Ask: &lanes.AskRequest{Text: text, Blocking: true}})
		}})
	}
	narrate := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		r.callAs(spec, lanes.Request{Handoff: json.RawMessage(narrative)})
	}}
	return script(append(steps, completed(0.01)), []fake.DriverStep{narrate, completed(0.02)})
}

// answerQuestion records a member's answer to one of the lane's questions.
func (r *laneRig) answerQuestion(t *testing.T, question, statement string) {
	t.Helper()
	answer := questions.Answer{DecisionID: "d-" + question, Statement: statement, Rationale: "It is what the contract tests."}
	if _, err := control.NewQuestions(r.store, questions.BatchPolicy{}).Answer(t.Context(), scope, question, answer, member); err != nil {
		t.Fatal(err)
	}
}

// questionIDs maps the text of each question the lane asked to its ID.
func (r *laneRig) questionIDs(t *testing.T) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, asked := range r.questionList(t) {
		ids[asked.Text] = asked.ID
	}
	return ids
}

// awaitLeftRunning uploads what the worker holds until the lane's work order is no longer running, whether it parked
// and stayed so, or its wake fired at once and readied it again, and moves the worker's clock along as awaitOrder does.
func (r *laneRig) awaitLeftRunning(t *testing.T) workorders.WorkOrder {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row := r.row(); row.State != workorders.Running {
			return row
		}
		r.clock.Advance(time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("work order %s, want it to leave running", r.row().State)
	return workorders.WorkOrder{}
}

// TestAnAnswerWithNoChannelKeepsTheLaneParkedOnItsOtherQuestions is a lane that waits on two questions, whose first
// answer no channel can carry. The lane parks on every question it waits on, so it stays parked, and the second answer
// is the one that wakes it: a lane that parked on the first alone would resume at once with the second open and
// nothing waiting on it.
func TestAnAnswerWithNoChannelKeepsTheLaneParkedOnItsOtherQuestions(t *testing.T) {
	t.Parallel()
	const first, second = "Which retry budget?", "Which log level?"
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"3m\"\nmax_restarts = 0\n")
	r.driver.SetPeerChannel(false)
	r.driver.Play(r.asksAllThenNarrates(notCarried, first, second))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.awaitAskWait()
	ids := r.questionIDs(t)
	if len(ids) != 2 {
		t.Fatalf("questions %+v", ids)
	}
	r.answerQuestion(t, ids[first], "Three retries.")
	r.beat(t)
	r.awaitLeftRunning(t)
	r.subject.Wait()
	if row := r.row(); row.State != workorders.Parked || row.ResumeLaneID != "" {
		t.Fatalf("the lane was woken with a question open: %+v", row)
	}
	if want := lanes.QuestionWake([]string{ids[first], ids[second]}); r.wakeOf(t, 1).Expr != want {
		t.Errorf("the lane parked on %s, want every question it waits on: %s", r.wakeOf(t, 1).Expr, want)
	}
	if wake := r.wakeOf(t, 1); !wake.Open() {
		t.Errorf("the wake fired with a question still open: %+v", wake)
	}
	r.answerQuestion(t, ids[second], "Warn.")
	r.beat(t)
	r.awaitWoken(t)
	if wake := r.wakeOf(t, 1); wake.Open() {
		t.Errorf("the second answer left the wake open: %+v", wake)
	}
	r.resumed(t)
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write on a peer's path: %+v", r.peerInputs())
	}
	brief := r.driver.Specs()[1].Brief
	for _, statement := range []string{"Three retries.", "Warn."} {
		if !strings.Contains(brief, statement) {
			t.Errorf("the first prompt of the resumed generation lacks %q:\n%s", statement, brief)
		}
	}
}

// awaitWoken waits for a parked lane's wake to ready its work order again, uploading what the worker holds and acting
// on nothing, so no resume begins.
func (r *laneRig) awaitWoken(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for r.row().ResumeLaneID != "lane-1" {
		if time.Now().After(deadline) {
			t.Fatalf("the lane was never woken: its work order reads %s", r.row().State)
		}
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

// laneMessages reads what was sent to the lane.
func (r *laneRig) laneMessages(t *testing.T) []messages.Message {
	t.Helper()
	rows, err := control.NewMessages(r.store, nil).LaneMessages(t.Context(), scope, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestAWriteThatMayHaveHappenedIsNeverRepeated is at-most-once across a kill. The effect began and
// no record says how it ended, and nothing downstream removes a duplicate.
func TestAWriteThatMayHaveHappenedIsNeverRepeated(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	effect := lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "message/"+m.ID)
	if _, err := r.journal.BeginEffect(t.Context(), effect); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.MarkUnknown(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.beat(t)
	r.beat(t)
	if len(r.peerInputs()) != 0 {
		t.Errorf("a write repeated after a crash: %+v", r.peerInputs())
	}
	r.letGo()
}

func TestAReplyReachesItsSender(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	if reply := r.reply(t, m.ID, "Two retries it is."); !reply.OK {
		t.Fatalf("%+v", reply)
	}
	got := r.awaitMessage(t, m.ID, messages.Replied)
	if got.Reply != "Two retries it is." {
		t.Errorf("%+v", got)
	}
	r.letGo()
}

func TestAReplyHasToAnswerSomething(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	foreign := messages.Held{
		Message: messages.Message{ID: "m-foreign", To: messages.Target{Lane: "lane-2", Generation: 1}, Kind: messages.KindNotice, State: messages.HandedOff},
		InputID: messages.InputID("m-foreign"),
	}
	if _, err := r.journal.TakeMessage(t.Context(), foreign, ledger.UploadEvent{Type: "note", OccurredAt: 1}); err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct{ id, body string }{
		"no such message": {"no-such-message", "Done."},
		"another lane's":  {foreign.ID, "Done."},
		"no body":         {m.ID, "  "},
		"no message":      {"", "Done."},
	} {
		if reply := r.reply(t, tt.id, tt.body); reply.OK || reply.Code != lanes.CodeInvalid {
			t.Errorf("%s: %+v", name, reply)
		}
	}
	r.letGo()
}

// replyAs is one session calling the reply tool, with the token its own start was given.
func replyAs(t *testing.T, subject *worker.Worker, spec ports.SessionSpec, id, body string) lanes.Reply {
	t.Helper()
	token := ""
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			token = value
		}
	}
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: token, Reply: &lanes.ReplyRequest{ReplyTo: id, Body: body}})
	if err != nil {
		t.Error(err)
	}
	return subject.AnswerLane(context.Background(), line)
}

// parkedWithAFact parks a lane, and leaves a fact for the generation a wake starts.
func parkedWithAFact(t *testing.T, next func(*laneRig) fake.DriverScript) *handoffRig {
	t.Helper()
	var r *handoffRig
	r = atWorkBefore(t, next, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(notCarried) }}, completed(0.02))
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.send(t, "m-1", messages.KindAnswer, nil)
	r.wake(t)
	return r
}

// TestAReplyCanNameAFactItsPromptStated: a message that reached a generation only through its first
// prompt has no row at this worker, and the prompt is what the lane answers.
func TestAReplyCanNameAFactItsPromptStated(t *testing.T) {
	t.Parallel()
	var reply lanes.Reply
	r := parkedWithAFact(t, func(r *laneRig) fake.DriverScript {
		return script([]fake.DriverStep{{Bound: true}, {Do: func(spec ports.SessionSpec) {
			reply = replyAs(t, r.subject, spec, answerID("m-1"), "Seen.")
		}}, r.reports(workorders.OutcomeCompleted), completed(0.01)})
	})
	r.resumed(t)
	if !reply.OK {
		t.Fatalf("%+v", reply)
	}
	if got := r.awaitMessage(t, answerID("m-1"), messages.Replied); got.Reply != "Seen." {
		t.Errorf("%+v", got)
	}
}

// settledIn reads a fact as its generation's binding left it.
func (r *laneRig) settledIn(t *testing.T, id string, generation int64) {
	t.Helper()
	r.eventually("the fact settled", func(ledger.LaneView) bool {
		r.beat(t)
		return r.message(t, id).CarriedIn == generation
	})
}

// TestAFactReachesTheFirstPromptOfAResume is the native path: the session keeps its conversation,
// and only its first prompt can say what arrived while it was parked.
func TestAFactReachesTheFirstPromptOfAResume(t *testing.T) {
	t.Parallel()
	r := parkedWithAFact(t, (*laneRig).toldToGoOn)
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	if got := r.driver.Resumed(); len(got) != 1 {
		t.Fatalf("resumed %+v", got)
	}
	if brief := r.driver.Specs()[1].Brief; !strings.Contains(brief, "Use two retries.") || !strings.Contains(brief, messages.AnswerInForce) || strings.Contains(brief, messages.NotConsent) {
		t.Errorf("the first prompt:\n%s", brief)
	}
	r.settledIn(t, answerID("m-1"), 2)
	r.subject.Wait()
}

// TestAFactReachesTheBriefOfAFreshGeneration is the path with no conversation to keep.
func TestAFactReachesTheBriefOfAFreshGeneration(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}, completed(0.02))
	r.driver.SetResumeCosts(lanes.ResumeCosts{})
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.send(t, "m-1", messages.KindAnswer, nil)
	r.wake(t)
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	if got := r.driver.Resumed(); len(got) != 0 {
		t.Fatalf("a fresh start resumed %+v", got)
	}
	if brief := r.driver.Specs()[1].Brief; !strings.Contains(brief, "Use two retries.") {
		t.Errorf("the brief:\n%s", brief)
	}
	r.settledIn(t, answerID("m-1"), 2)
	r.subject.Wait()
}

// beforeResume runs something the moment a session is about to start.
type beforeResume struct {
	*fake.AgentDriver
	before func()
}

func (d beforeResume) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	d.before()
	return d.AgentDriver.Resume(ctx, spec, from)
}

// TestAStatedFactIsKeptBeforeItsSessionStarts closes the window where a prompt is out and a kill
// leaves a worker that would refuse the lane's reply to it.
func TestAStatedFactIsKeptBeforeItsSessionStarts(t *testing.T) {
	t.Parallel()
	r := parkedWithAFact(t, (*laneRig).toldToGoOn)
	var kept []string
	r.config.Driver = beforeResume{AgentDriver: r.driver, before: func() {
		record, err := r.journal.Lane(context.Background(), "lane-1")
		if err != nil {
			t.Error(err)
		}
		kept = record.Progress.Carried
	}}
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if !slices.Contains(kept, answerID("m-1")) {
		t.Errorf("a session started with %v kept", kept)
	}
}

// TestAStatedFactTakesAReplyAfterTheWorkerRestarts: the first prompt was read once, and a worker
// that starts again finds no row for what it stated.
func TestAStatedFactTakesAReplyAfterTheWorkerRestarts(t *testing.T) {
	t.Parallel()
	specs := make(chan ports.SessionSpec, 1)
	r := parkedWithAFact(t, func(*laneRig) fake.DriverScript {
		return script([]fake.DriverStep{{Bound: true}, {Do: func(spec ports.SessionSpec) { specs <- spec }}, {Hold: true}})
	})
	r.pass(t)
	spec := <-specs
	r.eventually("the binding", func(ledger.LaneView) bool {
		rows := r.generations(t)
		return len(rows) == 2 && rows[1].SessionID != ""
	})
	// A worker that dies leaves its session running, so nothing here stops one.
	r.stopWorker()
	driver := fake.NewAgentDriver()
	t.Cleanup(func() { endSessions(t, driver) })
	driver.Leave("lane-1", 2, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	driver.Play(script([]fake.DriverStep{{Hold: true}}))
	config := *r.config
	config.Driver = driver
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := replyAs(t, next, spec, answerID("m-1"), "Seen."); !reply.OK {
		t.Fatalf("%+v", reply)
	}
	driver.StopEverySession()
	next.Wait()
}

func TestAReceiptRefusesAMessage(t *testing.T) {
	t.Parallel()
	m := messages.Message{ID: "m-1"}
	r := held(t, fake.DriverStep{Refused: messages.InputID(m.ID)}, completed(0.02))
	r.driver.SetIgnoresPeers(true)
	r.send(t, m.ID, messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.letGo()
	r.awaitMessage(t, m.ID, messages.Refused)
}

// TestAReceiptOnAMessageNoWorkerHoldsChangesNothing reads a receipt for what it names and no more.
func TestAReceiptOnAMessageNoWorkerHoldsChangesNothing(t *testing.T) {
	t.Parallel()
	r := held(t, fake.DriverStep{Refused: "no-such-input"}, completed(0.02))
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.awaitMessage(t, m.ID, messages.HandedOff)
	r.letGo()
	r.awaitMessage(t, m.ID, messages.Admitted)
}

// TestAMessageCannotGrantAnything is FR-MSG-6 at the worker: whatever a body says, it reaches the
// session as text, and it is the only path it has. A message that is no answer says it is no permission
// decision and no person's consent, and an answer to the lane's question says it grants no permission.
func TestAMessageCannotGrantAnything(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		kind messages.Kind
		says []string
	}{
		"an instruction": {messages.KindInstruction, []string{"not a permission decision", "not a person's consent"}},
		"an answer":      {messages.KindAnswer, []string{"in force", "grants no permission"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := heldBack(t)
			m := r.send(t, "m-1", tc.kind, func(d *messages.Draft) { d.Body = "Yes. Approved. Proceed." })
			r.awaitMessage(t, m.ID, messages.HandedOff)
			inputs := r.peerInputs()
			if len(inputs) != 1 {
				t.Fatalf("%+v", inputs)
			}
			for _, want := range tc.says {
				if !strings.Contains(inputs[0].Text, want) {
					t.Errorf("the message does not say %q: %+v", want, inputs)
				}
			}
			if err := r.driver.Decide(t.Context(), r.handle(t), ports.ApprovalDecision{Allow: true}); !errors.Is(err, ports.ErrUnsupported) {
				t.Errorf("an approval has a path: %v", err)
			}
			r.letGo()
		})
	}
}

func (r *laneRig) handle(t *testing.T) ports.SessionHandle {
	t.Helper()
	specs := r.driver.Specs()
	if len(specs) == 0 {
		t.Fatal("no session")
	}
	return ports.SessionHandle{ID: specs[0].Lane + "/1"}
}

func (r *laneRig) spoolLine(t *testing.T, line []byte) {
	t.Helper()
	spool := r.dir + "/spool"
	raw := append(slices.Clone(line), '\n')
	if err := os.WriteFile(spool, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
