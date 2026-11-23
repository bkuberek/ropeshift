package worker_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
)

// A session on a version whose socket passed its check can lack a channel for a peer's message for a reason that is its
// own: it has no socket yet, or its state could not be read. The message reads channel_unavailable, which names no version,
// and never undeliverable, which says the version takes no message.
func TestAMessageATemporarilyUnavailableChannelDidNotTakeNamesThatAsItsReason(t *testing.T) {
	t.Parallel()
	r := heldBack(t)
	r.driver.SetPeerChannelUnavailable(true)
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	notice := r.send(t, "m-2", messages.KindDecisionNotice, nil)
	r.beat(t)
	r.beat(t)
	if got := r.message(t, m.ID); got.State != messages.Expired || got.Reason != messages.ReasonChannelUnavailable {
		t.Errorf("a message a channel not available now did not take reads %s (%s)", got.State, got.Reason)
	}
	if got := r.message(t, notice.ID); got.State != messages.Expired || got.Reason != messages.ReasonLedgerFact {
		t.Errorf("a decision a channel not available now did not take reads %s (%s)", got.State, got.Reason)
	}
	r.letGo()
}

// A worker that failed a write for that reason and died before it reported it settles the message with the same reason
// when it starts again, from the failed write the journal kept.
func TestARestartKeepsTheReasonAChannelWasUnavailable(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	base.workerJournal = reportLost{Journal: base.journal}
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", restingAsk)
	r.dispatched()
	r.driver.SetPeerChannelUnavailable(true)
	r.driver.Play(r.asksThenNarrates("Which retry budget?", notCarried))
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.awaitAskWait()
	m := r.send(t, "m-1", messages.KindInstruction, nil)
	r.beat(t)
	if got := r.message(t, m.ID); got.State != messages.AtWorker {
		t.Fatalf("the message reads %s: the worker was to die before it reported it", got.State)
	}
	r.restartedOnItsSession(t)
	r.driver.SetPeerChannelUnavailable(true)
	for range 3 {
		r.beat(t)
	}
	if got := r.message(t, m.ID); got.State != messages.Expired || got.Reason != messages.ReasonChannelUnavailable {
		t.Errorf("the message reads %s (%s) after the restart, want expired (%s)", got.State, got.Reason, messages.ReasonChannelUnavailable)
	}
}
