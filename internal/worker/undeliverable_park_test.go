package worker_test

import (
	"context"
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// parkLost is a journal that loses the start of a park, which is the write that follows the move of an undeliverable
// answer to expired. A worker that reads its failed delivery through it leaves the lane where a crash between the two
// would: the answer settled, and the lane not parked.
type parkLost struct {
	ports.Journal
}

func (j parkLost) BeginParking(context.Context, string, lanes.ParkProgress, lanes.HandoffProgress, ledger.UploadEvent) (ledger.UploadEvent, error) {
	return ledger.UploadEvent{}, errWentAway
}

// answerOf finds the answer the lane was sent.
func (r *laneRig) answerOf(t *testing.T) messages.Message {
	t.Helper()
	for _, m := range r.laneMessages(t) {
		if m.Kind == messages.KindAnswer {
			return m
		}
	}
	t.Fatal("no answer was sent to the lane")
	return messages.Message{}
}

// A worker that settled an answer as undeliverable and died before it parked the lane parks it when it starts again: the
// lane still waits on the question, and the answer's message is expired as undeliverable, so nothing else would park it
// before its `ask_wait` ran out.
func TestARestartParksALaneWhoseUndeliverableAnswerWasSettledButNotParked(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	base.workerJournal = parkLost{Journal: base.journal}
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", restingAsk)
	r.dispatched()
	r.driver.SetPeerChannel(false)
	r.driver.Play(r.asksThenNarrates("Which retry budget?", notCarried))
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.awaitAskWait()
	asked := r.questionList(t)
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	r.answerQuestion(t, asked[0].ID, "Three retries.")
	answer := r.answerOf(t)
	for range 3 {
		r.beat(t)
	}
	if got := r.message(t, answer.ID); got.State != messages.Expired {
		t.Fatalf("the answer reads %s (%s): the worker was to settle it and die before it parked the lane", got.State, got.Reason)
	}
	if parked := r.parkingOf(t); parked != nil {
		t.Fatalf("the lane parked with %+v: the worker was to die before it parked it", parked)
	}

	r.restartedOnItsSession(t)
	for range 3 {
		r.beat(t)
	}
	r.awaitWoken(t)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.QuestionWake([]string{asked[0].ID}) || wake.Open() {
		t.Errorf("the lane parked on %+v, want a fired wake on its answered question", wake)
	}
	if parked := r.parkingOf(t); parked == nil || parked.Trigger != lanes.ParkBlockingQuestion {
		t.Errorf("the lane parked with %+v", parked)
	}
}

// An answer no channel takes parks a lane that rests, at once, and never one in a turn: a turn parked from where it
// stands is a turn cut off. The lane parks when its turn ends, as it would for any answer that arrived in the turn.
func TestAnUndeliverableAnswerParksALaneAtItsTurnsEndAndNotInTheTurn(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, restingAsk)
	r.driver.SetPeerChannel(false)
	g := newGate()
	r.finishLater(g)
	ask := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		r.callAs(spec, lanes.Request{Ask: &lanes.AskRequest{Text: "Which retry budget?", Blocking: true}})
	}}
	narrate := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		r.callAs(spec, lanes.Request{Handoff: json.RawMessage(notCarried)})
	}}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, ask, g.step(), completed(0.01)}, []fake.DriverStep{narrate, completed(0.02)}))
	r.atGate(g)
	asked := r.questionList(t)
	if len(asked) != 1 {
		t.Fatalf("questions %+v", asked)
	}
	r.answerQuestion(t, asked[0].ID, "Three retries.")
	answer := r.answerOf(t)
	for range 3 {
		r.beat(t)
	}
	if got := r.message(t, answer.ID); got.State != messages.Expired {
		t.Fatalf("the answer reads %s (%s), want it settled as undeliverable", got.State, got.Reason)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("the lane reads %s with its turn still open, want working", view.ActualState)
	}
	if parked := r.parkingOf(t); parked != nil {
		t.Errorf("the lane parked in its turn with %+v", parked)
	}

	g.free()
	r.awaitWoken(t)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.QuestionWake([]string{asked[0].ID}) || wake.Open() {
		t.Errorf("the lane parked on %+v, want a fired wake on its answered question", wake)
	}
	if parked := r.parkingOf(t); parked == nil || parked.Trigger != lanes.ParkBlockingQuestion {
		t.Errorf("the lane parked with %+v", parked)
	}
}
