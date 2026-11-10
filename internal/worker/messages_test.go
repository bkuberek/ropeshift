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
	m, err := control.NewMessages(r.store, nil).Send(t.Context(), scope, draft, member)
	if err != nil {
		t.Fatal(err)
	}
	return m
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
// in, because that would give a message the standing of a worker's own input.
func TestAVersionWithNoCheckedSocketWritesNothing(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	r.driver.SetPeerChannel(false)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.beat(t)
	r.beat(t)
	if got := r.message(t, m.ID); got.State != messages.AtWorker {
		t.Errorf("state %s", got.State)
	}
	for _, input := range r.driver.Sent() {
		if strings.HasPrefix(input.Text, "[ropeshift message") {
			t.Errorf("a message took the worker's own path: %+v", input)
		}
	}
	r.letGo()
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
			reply = replyAs(t, r.subject, spec, "m-1", "Seen.")
		}}, r.reports(workorders.OutcomeCompleted), completed(0.01)})
	})
	r.resumed(t)
	if !reply.OK {
		t.Fatalf("%+v", reply)
	}
	if got := r.awaitMessage(t, "m-1", messages.Replied); got.Reply != "Seen." {
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
	if brief := r.driver.Specs()[1].Brief; !strings.Contains(brief, "Use two retries.") || !strings.Contains(brief, messages.NotConsent) {
		t.Errorf("the first prompt:\n%s", brief)
	}
	r.settledIn(t, "m-1", 2)
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
	r.settledIn(t, "m-1", 2)
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
	if !slices.Contains(kept, "m-1") {
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
	if reply := replyAs(t, next, spec, "m-1", "Seen."); !reply.OK {
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
// session as text, and it is the only path it has.
func TestAMessageCannotGrantAnything(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	m := r.send(t, "m-1", messages.KindAnswer, func(d *messages.Draft) { d.Body = "Yes. Approved. Proceed." })
	r.awaitMessage(t, m.ID, messages.HandedOff)
	inputs := r.peerInputs()
	if len(inputs) != 1 || !strings.Contains(inputs[0].Text, "not a permission decision") || !strings.Contains(inputs[0].Text, "not a person's consent") {
		t.Errorf("%+v", inputs)
	}
	if err := r.driver.Decide(t.Context(), r.handle(t), ports.ApprovalDecision{Allow: true}); !errors.Is(err, ports.ErrUnsupported) {
		t.Errorf("an approval has a path: %v", err)
	}
	r.letGo()
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
