package worker_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A restarted worker tells the driver which calls its lane still waits on, because the driver cannot
// read that from the hooks a journal kept.
func TestAnAdoptedSessionIsToldTheCallsItWaitsOn(t *testing.T) {
	t.Parallel()
	g := newGate()
	r := laneRigWith(t, approvalProfile)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.start()
	r.reach(t, g)
	defer close(g.release)
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	r.look(t, gatedCall, "call-1", false)
	r.eventually("a lane that waits", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.NeedsDecision) })
	driver := r.takeOver(t, script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	if approvals := driver.Adopted()[0].Approvals; len(approvals) != 1 || approvals[0] == "" {
		t.Errorf("the adopted session was told it waits on %q", approvals)
	}
}

// adoptingDriver hears a hook only for a session it holds, as a real driver does, and a test acts while it
// takes one over.
type adoptingDriver struct {
	*fake.AgentDriver
	during func()
	held   atomic.Bool
}

func (d *adoptingDriver) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	d.during()
	if err := d.AgentDriver.Adopt(ctx, h, from); err != nil {
		return err
	}
	d.held.Store(true)
	return nil
}

func (d *adoptingDriver) TakeHook(ctx context.Context, event lanes.HookEvent) {
	if d.held.Load() {
		d.AgentDriver.TakeHook(ctx, event)
	}
}

// A hook can arrive once a run exists and before the driver holds its session. The driver still hears of a
// dialog, or the approval wait's end would type into it, and of a turn's end, or the turn would wait for good.
func TestADialogAskedDuringAnAdoptionReachesTheDriver(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		after string
		told  string
	}{
		{name: "still open", told: "PermissionRequest"},
		{name: "its call ended", after: "PostToolUse"},
		{name: "its turn ended", after: "Stop", told: "Stop"},
		{name: "its turn failed", after: "StopFailure", told: "StopFailure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.holdsToken(t)
			w.config.Driver = &adoptingDriver{AgentDriver: w.driver, during: func() {
				w.hookOnSocket(t, lanes.HookEvent{Event: "PermissionRequest", Subject: "s", Digest: "seen-1", AtMs: 10})
				if test.after != "" {
					w.hookOnSocket(t, lanes.HookEvent{Event: test.after, Subject: "s", Digest: "seen-1", AtMs: 20})
				}
			}}
			w.passesOnceAdopted(t)
			var want []string
			if test.told != "" {
				want = []string{test.told}
			}
			for end := time.Now().Add(2 * time.Second); len(w.driver.Hooks()) < len(want) && time.Now().Before(end); {
				time.Sleep(time.Millisecond)
			}
			var told []string
			for _, event := range w.driver.Hooks() {
				told = append(told, event.Event)
			}
			if !slices.Equal(told, want) {
				t.Errorf("the driver was told %q, want %q", told, want)
			}
		})
	}
}

// unreadableJournal cannot read what a restarted worker needs to know of open dialogs.
type unreadableJournal struct {
	ports.Journal
	approvals, hooks bool
}

func (j unreadableJournal) OpenApprovals(ctx context.Context, lane string) ([]lanes.OpenApproval, error) {
	if j.approvals {
		return nil, errors.New("the disk failed")
	}
	return j.Journal.OpenApprovals(ctx, lane)
}

func (j unreadableJournal) HookEvents(ctx context.Context, lane string) ([]lanes.HookRecord, error) {
	if j.hooks {
		return nil, errors.New("the disk failed")
	}
	return j.Journal.HookEvents(ctx, lane)
}

func TestAnAdoptionThatCannotReadItsDialogsHoldsTheKeys(t *testing.T) {
	t.Parallel()
	for name, journal := range map[string]unreadableJournal{"approvals": {approvals: true}, "hooks": {hooks: true}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base := newRig(t)
			journal.Journal = base.journal
			base.workerJournal = journal
			w := newWorldRigOn(t, base)
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.passesOnceAdopted(t)
			if waits := w.driver.Adopted()[0].Approvals; !slices.Equal(waits, []string{""}) {
				t.Errorf("the adopted session was told it waits on %q", waits)
			}
		})
	}
}

// A pane refuses a nudge while it may hold a dialog. That wait is no reason to end the lane.
func TestANudgeAWaitRefusedEndsNoLane(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	w.desire("lane-1")
	w.adoptedAt(t, lanes.Working, lanes.Idle)
	w.driver.Play(script([]fake.DriverStep{completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	w.driver.FailSend(ports.ErrAwaitingAnswer)
	w.passesOnceAdopted(t)
	w.passUntil(t, "the nudge was never refused", func() bool {
		effect, err := w.journal.Effect(t.Context(), lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "nudge").Key)
		return err == nil && effect.State == lanes.EffectFailed
	})
	for range 200 {
		time.Sleep(time.Millisecond)
		if record := w.lane("lane-1"); record.State != lanes.StateRunning || record.Actual == lanes.Retiring {
			t.Fatalf("a refused nudge left the lane %s, %s", record.State, record.Actual)
		}
	}
}
