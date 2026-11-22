package worker_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// awaitSentOn waits for a driver to have been sent n inputs, moving the worker's clock a second at a time, so a bound
// on that clock ends however many reads it takes.
func (r *laneRig) awaitSentOn(t *testing.T, driver *fake.AgentDriver, n int) []ports.Input {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for len(driver.Sent()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("sent %+v, want %d inputs", driver.Sent(), n)
		}
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	return driver.Sent()
}

// quietFor moves the worker's clock on by d, a minute at a time, and fails if the lane was sent anything meanwhile.
func (r *laneRig) quietFor(t *testing.T, driver *fake.AgentDriver, d time.Duration) {
	t.Helper()
	for range int(d / time.Minute) {
		r.clock.Advance(time.Minute)
		settle()
	}
	if sent := driver.Sent(); len(sent) != 0 {
		t.Fatalf("sent %+v inside the live wait", sent)
	}
}

// A turn that a prompt began can report no end: a hook blocked the prompt, a person interrupted it in the pane, or it
// hangs. The lane is not left in it for good: once it shows no sign of work for as long as a lane may wait live
// (max_live_wait), the worker takes it as at rest and decides what follows, here the nudge.
func TestATurnThatNeverEndsIsBoundedByTheLiveWait(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	r.atGate(stopped)
	at := time.Now().UnixMilli()
	r.stopAt(t, at, 1)
	stopped.free()
	r.restsOnBackground()
	r.promptAt(t, at+1)
	r.quietFor(t, r.driver, lanes.MaxLiveWait(nil)-time.Minute)
	if sent := r.awaitSentOn(t, r.driver, 1); sent[0].Text != lanes.Nudge {
		t.Fatalf("sent %+v, want the nudge", sent)
	}
	r.awaitOrderState(t, workorders.Acceptance)
}

// An end older than the newest prompt leaves the lane in that prompt's turn, and that turn may report no end of its
// own: a Stop that a hook blocked and a prompt continued, or a Stop heard after the prompt that came after it. That
// wait is bounded as any other.
func TestAnEndOlderThanItsPromptIsBoundedByTheLiveWait(t *testing.T) {
	t.Parallel()
	for name, promptFirst := range map[string]bool{"a prompt after the Stop": false, "a Stop heard after the prompt after it": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := laneRigWith(t, "")
			stopped := newGate()
			r.gates(stopped)
			r.driver.Play(script(
				[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
				[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
			))
			r.atGate(stopped)
			at := time.Now().UnixMilli()
			if promptFirst {
				r.promptAt(t, at+1)
				r.stopAt(t, at, 1)
			} else {
				r.stopAt(t, at, 1)
				r.promptAt(t, at+1)
			}
			stopped.free()
			r.turnDecided(t, 0.01)
			r.quietFor(t, r.driver, lanes.MaxLiveWait(nil)-time.Minute)
			if sent := r.awaitSentOn(t, r.driver, 1); sent[0].Text != lanes.Nudge {
				t.Fatalf("sent %+v, want the nudge", sent)
			}
			r.awaitOrderState(t, workorders.Acceptance)
		})
	}
}

// A Stop that reaches the worker after the end of its own turn was decided says nothing of the turn after it, so it
// cannot keep that turn's end, which has no Stop of its own, from ending a lane whose nudge is spent for good.
func TestALateStopDoesNotStrandTheEndAfterIt(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	nudged := newGate()
	r.gates(nudged)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{nudged.step(), completed(0.02)},
	))
	r.start()
	r.reach(t, nudged)
	at := time.Now().UnixMilli()
	r.stopAt(t, at, 1)
	r.promptAt(t, at+1)
	nudged.free()
	r.turnDecided(t, 0.02)
	for end := time.Now().Add(20 * time.Second); r.row().State == workorders.Running; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the lane was left running with its nudge spent and its last turn ended")
		}
		r.clock.Advance(5 * time.Second)
	}
	r.awaitOrderState(t, workorders.Blocked)
	if change := r.outcome(); change.Reason != workorders.ReasonNoOutcome {
		t.Errorf("outcome %+v, want %s", change, workorders.ReasonNoOutcome)
	}
}

// A worker that takes a lane over reads its hooks again, so what they say of the lane's turns outlives the worker
// that heard them: a rest on background work is bounded again, and a turn a prompt began while no worker listened
// reads as working and is bounded too.
func TestATakenOverLaneKeepsItsTurnStateAndItsBounds(t *testing.T) {
	t.Parallel()
	// restsOnItsWork leaves a lane at rest on the background work its first turn left running.
	restsOnItsWork := func(t *testing.T) (*laneRig, int64) {
		t.Helper()
		r := laneRigWith(t, "")
		stopped := newGate()
		r.gates(stopped)
		r.driver.Play(script([]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)}))
		r.start()
		r.reach(t, stopped)
		at := time.Now().UnixMilli()
		r.stopAt(t, at, 1)
		stopped.free()
		r.restsOnBackground()
		return r, at
	}
	t.Run("a rest on background work", func(t *testing.T) {
		t.Parallel()
		r, _ := restsOnItsWork(t)
		driver := r.takeOver(t, script())
		r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
		r.quietFor(t, driver, lanes.MaxLiveWait(nil)-time.Minute)
		if sent := r.awaitSentOn(t, driver, 1); sent[0].Text != lanes.BackgroundNudge {
			t.Fatalf("sent %+v, want the nudge that names the background work", sent)
		}
	})
	t.Run("a turn begun while no worker listened", func(t *testing.T) {
		t.Parallel()
		r, at := restsOnItsWork(t)
		journalHook(t, r, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: at + 1})
		driver := r.takeOver(t, script())
		r.eventually("a lane at work on the prompt", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
		r.quietFor(t, driver, lanes.MaxLiveWait(nil)-time.Minute)
		if sent := r.awaitSentOn(t, driver, 1); sent[0].Text != lanes.Nudge {
			t.Fatalf("sent %+v, want the nudge", sent)
		}
	})
	t.Run("an end older than a prompt", func(t *testing.T) {
		t.Parallel()
		r := laneRigWith(t, "")
		stopped := newGate()
		r.gates(stopped)
		r.driver.Play(script([]fake.DriverStep{{Bound: true}, stopped.step()}))
		r.start()
		r.reach(t, stopped)
		at := time.Now().UnixMilli()
		r.stopAt(t, at, 1)
		r.promptAt(t, at+1)
		driver := r.takeOver(t, script([]fake.DriverStep{completed(0.01)}))
		r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
		r.turnDecided(t, 0.01)
		settle()
		if sent := driver.Sent(); len(sent) != 0 {
			t.Fatalf("sent %+v for an end older than the prompt after it", sent)
		}
		stopped.free()
	})
}

// journalHook keeps a hook in the journal as a spool drained while no worker ran a lane would.
func journalHook(t *testing.T, r *laneRig, event lanes.HookEvent) {
	t.Helper()
	event.Lane, event.Generation = "lane-1", 1
	if err := r.journal.MergeHook(t.Context(), event); err != nil {
		t.Fatal(err)
	}
}
