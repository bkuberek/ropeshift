package worker_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// beganAndCrashed lays the journal out as a kill leaves it after a lane's start is journaled and
// before its session has any effect: a running lane under its claim, with the state the journal
// kept, and a fresh worker that has not yet reconciled. The first turn waits for the test.
func beganAndCrashed(t *testing.T, kept lanes.ActualState) *laneRig {
	t.Helper()
	r := laneRigWith(t, handoffProfile)
	release := make(chan struct{})
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	t.Cleanup(func() { close(release) })
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) { <-release }}, completed(0.01)}))
	record := lanes.Record{
		ID: "lane-1", WorkOrder: r.id, ClaimID: r.grant.ClaimID, Fence: r.grant.Fence, Generation: 1, State: lanes.StateRunning,
	}
	begun := ledger.UploadEvent{
		Epoch: r.subject.Session().Epoch, Type: ledger.UploadLaneStarted, WorkOrder: r.id, ClaimID: r.grant.ClaimID, Fence: r.grant.Fence,
		Lane: "lane-1", Generation: 1, OccurredAt: 1,
	}
	if _, err := r.journal.BeginLane(t.Context(), record, begun); err != nil {
		t.Fatal(err)
	}
	if kept != lanes.Pending {
		if err := r.journal.SaveActual(t.Context(), "lane-1", kept, ""); err != nil {
			t.Fatal(err)
		}
	}
	stored, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Actual != kept {
		t.Fatalf("the journal holds %q, and the test means %q", stored.Actual, kept)
	}
	return r
}

// A lane a crash stopped before its session continues on a restart, and must leave the state the
// journal kept. `claim_granted` is the only way out of `pending`, and only the path that begins a
// lane used to send it, so a lane continued from `pending` ran its session and heard every later
// signal from a state with no edge for it. It never reached `working`, took no message, and
// whatever depends on its state never happened. The states after `pending` already moved on their
// own, and stay covered.
func TestAContinuedLaneLeavesTheStateItsJournalKept(t *testing.T) {
	t.Parallel()
	for _, kept := range []lanes.ActualState{lanes.Pending, lanes.Provisioning, lanes.Starting} {
		t.Run(string(kept), func(t *testing.T) {
			t.Parallel()
			r := beganAndCrashed(t, kept)
			r.start()
			r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
			m := r.send(t, "m-1", messages.KindInstruction, nil)
			r.awaitMessage(t, m.ID, messages.HandedOff)
			if inputs := r.peerInputs(); len(inputs) != 1 || inputs[0].ID != messages.InputID(m.ID) {
				t.Errorf("a message to a continued lane was written as %+v", inputs)
			}
		})
	}
}
