package worker_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// stopHook is a session's Stop hook, with the background work it says still runs.
func (r *laneRig) stopHook(t *testing.T, background int) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", Background: background, AtMs: time.Now().UnixMilli()})
}

// promptHook is a session's UserPromptSubmit hook, which a background subagent's report fires too
// (https://code.claude.com/docs/en/hooks#userpromptsubmit).
func (r *laneRig) promptHook(t *testing.T) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: time.Now().UnixMilli()})
}

// restsOnBackground waits for the lane to rest on the background work its last turn left running, under the
// condition that names that wait.
func (r *laneRig) restsOnBackground() {
	r.t.Helper()
	r.eventually("a lane at rest on its background work", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Idle) && slices.Contains(view.Conditions, string(lanes.WaitingBackground))
	})
}

// sentText is what the worker sent the lane, in order.
func (r *laneRig) sentText() []string {
	var out []string
	for _, input := range r.driver.Sent() {
		out = append(out, input.Text)
	}
	return out
}

// awaitInputs waits for the worker to have sent the lane n inputs, moving the worker's clock a second at a time, so a
// rest on that clock ends however many reads it takes.
func (r *laneRig) awaitInputs(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for len(r.driver.Sent()) < n {
		if time.Now().After(deadline) {
			t.Fatalf("sent %q, want %d inputs", r.sentText(), n)
		}
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
}

// A lane whose turn ends while its own background agents still run has not finished: the worker neither
// nudges it nor ends it, and the turn their reports start is the one that ends it. A Stop hook says which
// session is paused on background work (https://code.claude.com/docs/en/hooks#stop-input), and a background
// subagent's report starts that session's next turn (https://code.claude.com/docs/en/hooks#userpromptsubmit).
func TestALaneWhoseTurnEndsOnBackgroundWorkWaitsForIt(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, reported := newGate(), newGate()
	r.gates(stopped, reported)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, stopped.step(), completed(0.01), reported.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(stopped)
	r.stopHook(t, 4)
	stopped.free()
	r.reach(t, reported)
	r.restsOnBackground()
	r.promptHook(t)
	reported.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v to a lane whose agents were still at work", sent)
	}
}

// A background subagent's report starts a turn with UserPromptSubmit, which ends the count its last Stop left: the
// turn it starts ends as any other, and one that ends with no outcome is nudged.
func TestALaneIsNudgedOnceItsBackgroundWorkEnds(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, returned := newGate(), newGate()
	r.gates(stopped, returned)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01), returned.step(), completed(0.02)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	stopped.free()
	r.reach(t, returned)
	r.restsOnBackground()
	r.promptHook(t)
	returned.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.sentText(); len(sent) != 1 || sent[0] != lanes.Nudge {
		t.Errorf("sent %q, want the one nudge", sent)
	}
}

// A lane rests on its background work for as long as it may wait live (max_live_wait) and no longer: then it is
// nudged with words that name the work it waits on.
func TestARestOnBackgroundWorkIsBoundedByTheLiveWait(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeFailed), completed(0.02)},
	))
	r.atGate(stopped)
	r.stopHook(t, 1)
	stopped.free()
	r.restsOnBackground()
	r.clock.Advance(lanes.MaxLiveWait(nil) - time.Minute)
	time.Sleep(10 * superviseStep)
	if sent := r.sentText(); len(sent) != 0 {
		t.Fatalf("sent %q inside the live wait", sent)
	}
	r.awaitInputs(t, 1)
	if sent := r.sentText(); sent[0] != lanes.BackgroundNudge {
		t.Errorf("sent %q, want the nudge that names the background work", sent)
	}
	r.awaitOrderState(t, workorders.Failed)
}

// A lane whose nudge is spent and that rests on its background work past the live wait again ends blocked, with a
// reason that names the wait and a detail that says since when and what ended it, so it reaches Needs you.
func TestALaneThatOutwaitsItsNudgeOnBackgroundWorkIsBlockedWithItsReason(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, again := newGate(), newGate()
	r.gates(stopped, again)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{again.step(), completed(0.02)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	stopped.free()
	r.restsOnBackground()
	r.awaitInputs(t, 1)
	r.reach(t, again)
	r.promptHook(t)
	r.stopHook(t, 3)
	again.free()
	r.restsOnBackground()
	deadline := time.Now().Add(20 * time.Second)
	for r.row().State == workorders.Running && time.Now().Before(deadline) {
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	r.awaitOrderState(t, workorders.Blocked)
	change := r.outcome()
	if change.Reason != workorders.ReasonBackgroundUnreported {
		t.Errorf("outcome %+v, want %s", change, workorders.ReasonBackgroundUnreported)
	}
	for _, want := range []string{"3 background tasks", "max_live_wait"} {
		if !strings.Contains(change.Detail, want) {
			t.Errorf("detail %q leaves out %q", change.Detail, want)
		}
	}
	if sent := r.sentText(); len(sent) != 1 {
		t.Errorf("sent %q, want the one nudge", sent)
	}
}

// The background count holds back only the nudge: a planning lane whose turn ends with background work in flight
// still waits for its person, as every other check after a turn still runs.
func TestAPlanningLaneWithBackgroundWorkStillWaitsForItsPerson(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\n", func(driver *fake.AgentDriver) { driver.SetAttached(true) })
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	stopped.free()
	r.waitsForItsPerson(t)
	if err := r.driver.Type("lane-1", 1, "That is the plan. Report it."); err != nil {
		t.Fatal(err)
	}
	r.awaitOrder(t, workorders.Acceptance)
	if r.nudged() {
		t.Errorf("a planning lane waiting for its person was nudged: %+v", r.driver.Sent())
	}
}

// A lane that reported has said its work is over, so it stops at the end of its turn whatever still runs.
func TestALaneThatReportedStopsWithBackgroundWorkInFlight(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), stopped.step(), completed(0.01)}))
	r.atGate(stopped)
	r.stopHook(t, 3)
	stopped.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v to a lane that reported", sent)
	}
}

// idleHeld holds the worker where a turn's end has made the lane idle and has not yet decided what follows.
type idleHeld struct {
	ports.Journal
	gate *gate
	once sync.Once
}

func (j *idleHeld) SaveActual(ctx context.Context, lane string, state, from lanes.ActualState) error {
	if state == lanes.Idle {
		j.once.Do(func() {
			close(j.gate.reached)
			<-j.gate.release
		})
	}
	return j.Journal.SaveActual(ctx, lane, state, from)
}

// turnDecided waits until the worker has decided what follows the turn that brought the total to total.
func (r *laneRig) turnDecided(t *testing.T, total float64) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		if record.Progress.Cost.SessionTotalUSD >= total {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the worker never decided the turn that brought the total to %v", total)
}

// Background work can start the next turn after the lane reads idle and before the worker decides what follows
// the turn that stopped. That turn's end draws no nudge.
func TestAPromptBetweenATurnsEndAndItsDecisionDrawsNoNudge(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	held := &idleHeld{Journal: base.journal, gate: newGate()}
	base.workerJournal = held
	r := newLaneRigFor(t, base).dispatched()
	stopped, reported := newGate(), newGate()
	r.gates(stopped, held.gate, reported)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, stopped.step(), completed(0.01), reported.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(stopped)
	r.stopHook(t, 4)
	stopped.free()
	r.reach(t, held.gate)
	r.promptHook(t)
	held.gate.free()
	r.turnDecided(t, 0.01)
	reported.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.sentText(); len(sent) != 0 {
		t.Errorf("sent %q to a lane whose background work started its next turn", sent)
	}
}

// A hook can come ahead of the stream's turn end, late from the spool, or twice, and its time says what it ends.
func TestHooksHeardBeforeATurnsEndKeepItsBackgroundPause(t *testing.T) {
	t.Parallel()
	stop := func(at int64) lanes.HookEvent {
		return lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", Background: 4, AtMs: at}
	}
	prompt := func(at int64) lanes.HookEvent {
		return lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: at}
	}
	for _, tt := range []struct {
		name  string
		hooks []lanes.HookEvent
		rests bool
	}{
		{"the work started the next turn", []lanes.HookEvent{stop(2000), prompt(3000)}, false},
		{"the stop arrived after that prompt", []lanes.HookEvent{prompt(3000), stop(2000)}, false},
		{"the stop arrived again after that prompt", []lanes.HookEvent{stop(2000), prompt(3000), stop(2000)}, false},
		{"the prompt of the turn that stopped arrived late", []lanes.HookEvent{stop(2000), prompt(1000)}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := laneRigWith(t, "")
			stopped, reported := newGate(), newGate()
			r.gates(stopped, reported)
			r.driver.Play(script([]fake.DriverStep{
				{Bound: true}, stopped.step(), completed(0.01), reported.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
			}))
			r.atGate(stopped)
			for _, event := range tt.hooks {
				r.hook(t, event)
			}
			stopped.free()
			r.turnDecided(t, 0.01)
			if got := slices.Contains(r.read().Conditions, string(lanes.WaitingBackground)); got != tt.rests {
				t.Errorf("waiting_background %v, want %v", got, tt.rests)
			}
			reported.free()
			r.awaitOrderState(t, workorders.Acceptance)
			if sent := r.sentText(); len(sent) != 0 {
				t.Errorf("sent %q to a lane whose turn left background work", sent)
			}
		})
	}
}

// The held nudge is for one turn's end: the turn the background work started is nudged as any other.
func TestATurnBackgroundWorkStartedIsNudgedWhenItsStartBeatTheEndBeforeIt(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, returned := newGate(), newGate()
	r.gates(stopped, returned)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01), returned.step(), completed(0.02)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)},
	))
	r.atGate(stopped)
	r.stopHook(t, 2)
	r.promptHook(t)
	stopped.free()
	r.turnDecided(t, 0.01)
	if sent := r.sentText(); len(sent) != 0 {
		t.Errorf("sent %q before the started turn ended", sent)
	}
	returned.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.sentText(); len(sent) != 1 || sent[0] != lanes.Nudge {
		t.Errorf("sent %q, want the one nudge", sent)
	}
}

// A lane whose nudge is spent is not ended under the turn its background work started.
func TestAPromptBeforeATurnsEndDoesNotEndALaneWithItsNudgeSpent(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	first, again, resumed := newGate(), newGate(), newGate()
	r.gates(first, again, resumed)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, first.step(), completed(0.01)},
		[]fake.DriverStep{again.step(), completed(0.02), resumed.step(), r.reports(workorders.OutcomeCompleted), completed(0.03)},
	))
	r.atGate(first)
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", AtMs: 1000})
	first.free()
	r.reach(t, again)
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", Background: 1, AtMs: 2000})
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: 3000})
	again.free()
	r.turnDecided(t, 0.02)
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Actual == lanes.Retiring {
		t.Error("the worker began to end a lane whose background work had started its next turn")
	}
	resumed.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// awaitRefusedNudge waits for the nudge a wait refused to be on record.
func (r *laneRig) awaitRefusedNudge(t *testing.T) {
	t.Helper()
	key := lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "nudge").Key
	r.eventually("a refused nudge", func(ledger.LaneView) bool {
		effect, err := r.journal.Effect(t.Context(), key)
		return err == nil && effect.State == lanes.EffectFailed
	})
}

// endsBlockedByTheBound moves the worker's clock on until the lane ends, and fails one that is held past the
// live wait.
func (r *laneRig) endsBlockedByTheBound(t *testing.T, reason string) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); r.row().State == workorders.Running; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the lane was held running past max_live_wait after a refused nudge")
		}
		r.clock.Advance(5 * time.Second)
	}
	r.awaitOrderState(t, workorders.Blocked)
	if change := r.outcome(); change.Reason != reason {
		t.Errorf("outcome %+v, want %s", change, reason)
	}
}

// The rest on background work has had the whole live wait already, so a nudge refused at its end ends the lane.
func TestARefusedBackgroundNudgeEndsTheLaneBlocked(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.atGate(stopped)
	r.stopHook(t, 1)
	stopped.free()
	r.restsOnBackground()
	r.driver.FailSend(ports.ErrAwaitingAnswer)
	r.clock.Advance(lanes.MaxLiveWait(nil))
	r.awaitRefusedNudge(t)
	// The clock stands still from here, so nothing but the refusal ends the lane.
	for end := time.Now().Add(5 * time.Second); r.row().State == workorders.Running; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("a refused background nudge left the lane running past max_live_wait")
		}
	}
	r.awaitOrderState(t, workorders.Blocked)
	if change := r.outcome(); change.Reason != workorders.ReasonBackgroundUnreported {
		t.Errorf("outcome %+v, want %s", change, workorders.ReasonBackgroundUnreported)
	}
}

// A refusal at a turn's end can be a dialog of the turn after it, so the lane waits, for as long as it may wait
// live and no longer.
func TestANudgeRefusedAtATurnsEndIsBoundedByTheLiveWait(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.driver.FailSend(ports.ErrAwaitingAnswer)
	r.start()
	r.awaitRefusedNudge(t)
	r.clock.Advance(lanes.MaxLiveWait(nil) - time.Minute)
	time.Sleep(10 * superviseStep)
	if state := r.row().State; state != workorders.Running {
		t.Fatalf("a refused nudge left the lane %s inside the live wait", state)
	}
	r.endsBlockedByTheBound(t, workorders.ReasonNoOutcome)
}

// The bound is the wait's and not the next turn's: an answer starts work, which can outlast it.
func TestAnAnsweredWaitOutlastsARefusedNudgesBound(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{lanes.EventUserPromptSubmit, "PostToolUse"} {
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			r := laneRigWith(t, "")
			working := newGate()
			r.gates(working)
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01), working.step(), r.reports(workorders.OutcomeCompleted), completed(0.02)}))
			r.driver.FailSend(ports.ErrAwaitingAnswer)
			r.start()
			r.reach(t, working)
			r.awaitRefusedNudge(t)
			r.hook(t, lanes.HookEvent{Event: answer, Subject: "session-1", SessionID: "session-1", AtMs: time.Now().UnixMilli()})
			r.clock.Advance(2 * lanes.MaxLiveWait(nil))
			time.Sleep(10 * superviseStep)
			if state := r.row().State; state != workorders.Running {
				t.Fatalf("a turn that an answer began was left %s at the refused nudge's bound", state)
			}
			working.free()
			r.awaitOrderState(t, workorders.Acceptance)
		})
	}
}

// The journal keeps the refusal, so a worker that takes the lane over bounds it too, unless a turn began since.
func TestARefusedNudgesBoundOutlivesAWorker(t *testing.T) {
	t.Parallel()
	for name, answered := range map[string]bool{"still waiting": false, "answered since": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := laneRigWith(t, "")
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}, []fake.DriverStep{{Hold: true}}))
			r.driver.FailSend(ports.ErrAwaitingAnswer)
			r.start()
			r.awaitRefusedNudge(t)
			r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
			if answered {
				r.promptHook(t)
			}
			driver := r.takeOver(t, script([]fake.DriverStep{{Hold: true}}))
			r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
			if !answered {
				r.endsBlockedByTheBound(t, workorders.ReasonNoOutcome)
				return
			}
			time.Sleep(10 * superviseStep)
			r.clock.Advance(2 * lanes.MaxLiveWait(nil))
			time.Sleep(10 * superviseStep)
			if state := r.row().State; state != workorders.Running {
				t.Errorf("a restart bounded a turn under way and left it %s", state)
			}
		})
	}
}
