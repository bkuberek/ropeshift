package worker_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// briefDeadline is how long a test gives the worker's passes to tell a cleared session its work.
const briefDeadline = 10 * time.Second

// aClearedLane is a lane a clear left owing its brief, as a worker that restarted finds it: the
// second generation, at rest, on the process the first one started, with a session the driver left.
func aClearedLane(t *testing.T, plays fake.DriverScript) *worldRig {
	t.Helper()
	ctx := t.Context()
	w := newWorldRig(t)
	err := w.journal.SaveLane(ctx, lanes.Record{
		ID: "lane-1", WorkOrder: "wo-lane-1", ClaimID: "c-lane-1", Fence: 1, Generation: 2, Process: 1,
		State: lanes.StateRunning, Worktree: "/worktrees/lane-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.journal.SaveActual(ctx, "lane-1", lanes.Idle, lanes.Working); err != nil {
		t.Fatal(err)
	}
	if _, err := w.journal.SaveProgress(ctx, "lane-1", 2, lanes.Progress{OwesBrief: true}); err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(plays)
	return w
}

// afterAClear is the rest of a session a clear left: it reads the clear's turn end once it is taken
// over, and then waits for the brief it is owed.
func afterAClear() fake.DriverScript {
	return fake.DriverScript{SessionID: clearedSession, Model: "resolved-model", Turns: [][]fake.DriverStep{
		{{Bound: true}, completed(0.01)},
		{{Hold: true}},
	}}
}

// briefs counts the inputs that state a lane's work.
func briefs(sent []ports.Input) int {
	told := 0
	for _, input := range sent {
		if strings.Contains(input.Text, "work order") {
			told++
		}
	}
	return told
}

// passUntil makes the worker's passes until the lane has been told its work, which a lane at rest
// waits for, because no turn of a session left idle draws one.
func (w *worldRig) passUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(briefDeadline)
	for time.Now().Before(deadline) {
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		if done() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s: the session was sent %+v", what, w.driver.Sent())
}

// heldAdoption takes a session over only when a test lets it, as an adoption still on its way to the
// session does when the pass that started it goes on.
type heldAdoption struct {
	ports.AgentDriver
	release chan struct{}
	once    sync.Once
}

func (d *heldAdoption) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	select {
	case <-d.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return d.AgentDriver.Adopt(ctx, h, from)
}

func (d *heldAdoption) letGo() { d.once.Do(func() { close(d.release) }) }

// The pass that starts an adoption goes on to the passes that brief a cleared lane before the
// session is in the driver's hands, and a brief sent to a session nobody holds yet reaches no one. It
// must not be spent: the lane still owes it when the session is there.
func TestAClearedSessionIsBriefedOnceItIsAdopted(t *testing.T) {
	t.Parallel()
	w := aClearedLane(t, afterAClear())
	held := &heldAdoption{AgentDriver: w.driver, release: make(chan struct{})}
	t.Cleanup(held.letGo)
	w.config.Driver = held
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	held.letGo()
	w.passUntil(t, "the cleared session was never told its work", func() bool { return briefs(w.driver.Sent()) == 1 })
	for range 3 {
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if told := briefs(w.driver.Sent()); told != 1 {
		t.Errorf("told its work %d times: %+v", told, w.driver.Sent())
	}
}

// A brief the driver says no session took did not happen. The lane owes it until a session has it,
// and then tells it once.
func TestABriefThatFoundNoSessionIsSentOnceThereIsOne(t *testing.T) {
	t.Parallel()
	w := aClearedLane(t, afterAClear())
	w.driver.FailSend(fmt.Errorf("lane-1/1: %w", ports.ErrNoSession))
	w.passUntil(t, "the brief was never sent again", func() bool { return briefs(w.driver.Sent()) == 1 })
	for range 3 {
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if told := briefs(w.driver.Sent()); told != 1 {
		t.Errorf("told its work %d times: %+v", told, w.driver.Sent())
	}
	deliveries := w.briefEffects(t)
	if len(deliveries) != 2 || !deliveries[0].Undelivered() || deliveries[1].State != lanes.EffectCompleted {
		t.Errorf("the deliveries on record: %+v", deliveries)
	}
}

// briefEffects lists the deliveries of the cleared lane's brief, in the order they began.
func (w *worldRig) briefEffects(t *testing.T) []lanes.Effect {
	t.Helper()
	all, err := w.journal.Effects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []lanes.Effect
	for _, effect := range all {
		if effect.Kind == lanes.EffectInput && effect.Generation == 2 && strings.HasPrefix(effect.Key, lanes.NewEffect(lanes.EffectInput, "lane-1", 2, "", worker.BriefLabel(1)).Key) {
			out = append(out, effect)
		}
	}
	return out
}

// recordedBrief lays down one delivery of the brief as an earlier worker left it.
func (w *worldRig) recordedBrief(t *testing.T, attempt int, state lanes.EffectState, detail string) {
	t.Helper()
	effect := lanes.NewEffect(lanes.EffectInput, "lane-1", 2, "", worker.BriefLabel(attempt))
	if _, err := w.journal.BeginEffect(t.Context(), effect); err != nil {
		t.Fatal(err)
	}
	if err := w.journal.FinishEffect(t.Context(), effect.Key, state, detail); err != nil {
		t.Fatal(err)
	}
}

// passesOnceAdopted makes passes of the worker until it holds the session, and a few after, so a
// brief it owes would have been sent by now.
func (w *worldRig) passesOnceAdopted(t *testing.T) {
	t.Helper()
	w.passUntil(t, "the session was never adopted", func() bool { return len(w.driver.Adopted()) == 1 })
	for range 5 {
		time.Sleep(time.Millisecond)
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

// A journal can say a lane owes its brief when it has been told: progress is saved when a turn ends,
// and the brief goes in the turn after. The record of the delivery is what a restart goes by, and the
// retry's is the one that stands for a first that found no session.
func TestARestartDoesNotRepeatABriefItsRetryDelivered(t *testing.T) {
	t.Parallel()
	w := aClearedLane(t, afterAClear())
	w.recordedBrief(t, 1, lanes.EffectFailed, lanes.DetailNotDelivered+": lane-1/1: no such session")
	w.recordedBrief(t, 2, lanes.EffectCompleted, "")
	w.passesOnceAdopted(t)
	if told := briefs(w.driver.Sent()); told != 0 {
		t.Errorf("told its work again %d times: %+v", told, w.driver.Sent())
	}
}

// A brief that failed some other way may have reached the session, so it is not sent again.
func TestABriefThatFailedAnotherWayIsNotSentAgain(t *testing.T) {
	t.Parallel()
	w := aClearedLane(t, afterAClear())
	w.recordedBrief(t, 1, lanes.EffectFailed, "lane-1/1: the pane went away")
	w.passesOnceAdopted(t)
	if told := briefs(w.driver.Sent()); told != 0 {
		t.Errorf("told its work again %d times: %+v", told, w.driver.Sent())
	}
}

// noSessionTakes is a driver that holds a session and never takes an input for it.
type noSessionTakes struct{ ports.AgentDriver }

func (d noSessionTakes) Send(_ context.Context, h ports.SessionHandle, _ ports.Input) (ports.SendReceipt, error) {
	return ports.SendReceipt{}, fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
}

// A driver that never takes the brief is asked a bounded number of times, and each ask leaves its
// record, so the journal does not grow with the passes of a worker that goes on for days.
func TestABriefNoSessionTakesIsAskedABoundedNumberOfTimes(t *testing.T) {
	t.Parallel()
	w := aClearedLane(t, afterAClear())
	w.config.Driver = noSessionTakes{AgentDriver: w.driver}
	w.passUntil(t, "the brief was never asked for", func() bool { return len(w.briefEffects(t)) == worker.MaxBriefAttempts })
	for range 5 {
		if _, err := w.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if asked := len(w.briefEffects(t)); asked != worker.MaxBriefAttempts {
		t.Errorf("asked %d times, want %d", asked, worker.MaxBriefAttempts)
	}
}
