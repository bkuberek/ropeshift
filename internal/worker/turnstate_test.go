package worker_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// stopAt is a session's Stop hook at a time, with the background work it says still runs.
func (r *laneRig) stopAt(t *testing.T, atMs int64, background int) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "session-1", SessionID: "session-1", Background: background, AtMs: atMs})
}

// failureAt is a session's StopFailure hook at a time.
func (r *laneRig) failureAt(t *testing.T, atMs int64, category string) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: "StopFailure", Subject: "session-1", SessionID: "session-1", Detail: category, AtMs: atMs})
}

// promptAt is a session's UserPromptSubmit hook at a time: a line a person typed, or the report of background work.
func (r *laneRig) promptAt(t *testing.T, atMs int64) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: atMs})
}

// callAt is a tool call's start or end, by its hook, at a time.
func (r *laneRig) callAt(t *testing.T, event, subject string, atMs int64) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: event, Subject: subject, Tool: "Bash", AtMs: atMs})
}

// settle lets the worker's goroutines read a clock a test moved.
func settle() { time.Sleep(10 * superviseStep) }

// A line a person types into a planning lane's pane starts a turn as the worker's own input does, and the lane
// works until that turn ends: its wait for its person does not run out under the turn, however long it takes.
func TestAPlanningLaneWorkingOnATypedLineIsNotParked(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(*fake.AgentDriver) {})
	working := newGate()
	r.gates(working)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{working.step(), r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	if err := r.driver.Type("lane-1", 1, "Put the sailings under each berth."); err != nil {
		t.Fatal(err)
	}
	r.reach(t, working)
	at := r.clock.Now().UnixMilli()
	r.promptAt(t, at)
	r.callAt(t, "PreToolUse", "tool-1", at+1)
	for range 11 {
		r.clock.Advance(time.Minute)
		settle()
	}
	if parking := r.parkingOf(t); parking != nil {
		t.Fatalf("a planning lane at work on a line its person typed parked: %+v", parking)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Fatalf("a planning lane at work on a line its person typed reads %s", view.ActualState)
	}
	r.callAt(t, "PostToolUse", "tool-1", at+2)
	working.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// The report of background work starts a turn of its own, and a lane in that turn works: a draining fleet parks it
// only past the drain deadline, as any working lane.
func TestADrainLeavesATurnBackgroundWorkStartedToRun(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, profileAt+"[lifecycle]\ndrain_deadline = \"5m\"\nhandoff_timeout = \"30ms\"\n")
	stopped, working := newGate(), newGate()
	r.gates(stopped, working)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, stopped.step(), completed(0.01), working.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(stopped)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 1)
	stopped.free()
	r.reach(t, working)
	r.restsOnBackground()
	r.promptAt(t, at+1)
	r.drainFleet(t)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if parking := r.parkingOf(t); parking != nil {
		t.Fatalf("a draining fleet parked a lane inside a turn its background work started: %+v", parking)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Fatalf("a lane in a turn its background work started reads %s", view.ActualState)
	}
	working.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// A threshold crossed in a turn that background work started is crossed by a working lane: the instruction to hand
// off goes on the mid-turn path, and nothing is typed into the session as if it were at rest.
func TestAThresholdInATurnBackgroundWorkStartedHandsOffMidTurn(t *testing.T) {
	t.Parallel()
	r := contextLane(t)
	stopped, resumed, g := newGate(), newGate(), newGate()
	r.gates(stopped, resumed, g)
	answer := fake.DriverStep{Do: func(_ ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), stopped.step(), completed(0.01), resumed.step(), tokens(185), g.step(), completed(0.02)},
		[]fake.DriverStep{answer, completed(0.03)},
	)
	r.reach(t, stopped)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 1)
	stopped.free()
	r.reach(t, resumed)
	r.restsOnBackground()
	r.promptAt(t, at+1)
	resumed.free()
	var instruction ports.Input
	r.eventually("the lane acted on its threshold", func(ledger.LaneView) bool {
		for _, input := range r.driver.Sent() {
			if strings.Contains(input.Text, lanes.ToolLaneHandoff) {
				instruction = input
				return true
			}
		}
		return r.driver.Compacts() != 0
	})
	if got := r.driver.Compacts(); got != 0 {
		t.Fatalf("a lane in a turn its background work started was told to compact as if at rest (%d)", got)
	}
	if !instruction.MidTurn {
		t.Fatalf("the instruction to a lane in a turn its background work started took the path of one at rest: %+v", instruction)
	}
	r.admit(instruction)
	g.free()
	r.awaitSecondGeneration(t)
}

// Two turns can end before the worker reads the first end, while a third turn runs. Both ends are older than the
// prompt that started the third, so neither is the lane's rest: neither nudges nor ends it.
func TestTwoEndsOlderThanTheNewestPromptLeaveTheLaneWorking(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	first, third := newGate(), newGate()
	r.gates(first, third)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, first.step(), completed(0.01), completed(0.02), third.step(), r.reports(workorders.OutcomeCompleted), completed(0.03),
	}))
	r.atGate(first)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 1)
	r.promptAt(t, at+1)
	r.stopAt(t, at+2, 1)
	r.promptAt(t, at+3)
	first.free()
	r.reach(t, third)
	r.turnDecided(t, 0.02)
	if sent := r.sentText(); len(sent) != 0 {
		t.Fatalf("sent %q to a lane whose third turn runs", sent)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Fatalf("a lane whose third turn runs reads %s", view.ActualState)
	}
	third.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// A StopFailure of the turn that background work started, heard before the end of the turn before it is read, says
// nothing of that end: the end is older than the prompt, so it draws no nudge, and the failure decides.
func TestAFailureOfTheNextTurnLeavesTheEndBeforeItUnnudged(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	first := newGate()
	r.gates(first)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, first.step(), completed(0.01), {Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: "server_error", TotalUSD: 0.02}},
	}))
	r.atGate(first)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 1)
	r.promptAt(t, at+1)
	r.failureAt(t, at+2, "server_error")
	first.free()
	r.awaitOrderState(t, workorders.Failed)
	if sent := r.sentText(); len(sent) != 0 {
		t.Errorf("sent %q for an end older than the turn that failed after it", sent)
	}
	if change := r.outcome(); change.Reason != "server_error" {
		t.Errorf("outcome %+v, want the failure", change)
	}
}

// effectHeld holds the worker as it begins one effect, after it decided the effect and before it acts.
type effectHeld struct {
	ports.Journal
	key  string
	gate *gate
	once sync.Once
}

func (j *effectHeld) BeginEffect(ctx context.Context, effect lanes.Effect) (bool, error) {
	if effect.Key == j.key {
		j.once.Do(func() {
			close(j.gate.reached)
			<-j.gate.release
		})
	}
	return j.Journal.BeginEffect(ctx, effect)
}

// The rest on background work can reach its bound as the work's report starts the next turn. Whether the lane works
// is read again just before the nudge is typed, so a turn that began after the bound was read is not nudged.
func TestARestsBoundReadsTheTurnAgainBeforeItNudges(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	held := &effectHeld{Journal: base.journal, key: lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "nudge").Key, gate: newGate()}
	base.workerJournal = held
	r := newLaneRigFor(t, base).dispatched()
	stopped, working := newGate(), newGate()
	r.gates(stopped, held.gate, working)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, stopped.step(), completed(0.01), working.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(stopped)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 1)
	stopped.free()
	r.reach(t, working)
	r.restsOnBackground()
	r.clock.Advance(lanes.MaxLiveWait(nil))
	r.reach(t, held.gate)
	r.promptAt(t, at+1)
	held.gate.free()
	settle()
	if sent := r.sentText(); len(sent) != 0 {
		t.Fatalf("sent %q to a lane whose background work started its next turn", sent)
	}
	if state := r.row().State; state != workorders.Running {
		t.Fatalf("a lane whose background work started its next turn was left %s", state)
	}
	working.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// An end older than the newest prompt says nothing of a compaction that began in the turn after it: the lane stays in
// that compaction until the compaction ends.
func TestAnEndOlderThanItsPromptLeavesANewerCompaction(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	first, next := newGate(), newGate()
	r.gates(first, next)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, first.step(), completed(0.01), next.step(), r.reports(workorders.OutcomeCompleted), completed(0.02),
	}))
	r.atGate(first)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 1)
	r.promptAt(t, at+1)
	r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "session-1", Detail: "auto", AtMs: at + 2})
	first.free()
	r.reach(t, next)
	r.turnDecided(t, 0.01)
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Fatalf("an end older than the prompt took the lane out of the compaction after it: it reads %s", view.ActualState)
	}
	r.hook(t, lanes.HookEvent{Event: "PostCompact", Subject: "session-1", Detail: "auto", AtMs: at + 3})
	next.free()
	r.awaitOrderState(t, workorders.Acceptance)
}

// A hook's time has the millisecond as its grain, so the prompt that background work's report fires can share one
// with the Stop before it. The prompt heard after that Stop is the newer: it starts the next turn, which the Stop's end
// does not end.
func TestAPromptInTheMillisecondOfAStopStartsTheNextTurn(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped, returned := newGate(), newGate()
	r.gates(stopped, returned)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01), returned.step(), completed(0.02)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)},
	))
	r.atGate(stopped)
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 2)
	r.promptAt(t, at)
	stopped.free()
	r.turnDecided(t, 0.01)
	if sent := r.sentText(); len(sent) != 0 {
		t.Errorf("sent %q to a lane whose background work started its next turn", sent)
	}
	returned.free()
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.sentText(); len(sent) != 1 || sent[0] != lanes.Nudge {
		t.Errorf("sent %q, want the one nudge", sent)
	}
}

// A turn cannot end in the millisecond it began, so a Stop in the newest prompt's millisecond is the end of the turn
// before that prompt, whichever the worker heard first: it ends no turn the lane is in, draws no nudge, and leaves the
// lane at work. A lane that reads working by mistake is still bounded by the live wait.
func TestAStopInThePromptsMillisecondHeardAfterItEndsNoTurn(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	r.atGate(stopped)
	at := r.clock.Now().UnixMilli()
	r.promptAt(t, at)
	r.stopAt(t, at, 0)
	stopped.free()
	r.turnDecided(t, 0.01)
	if sent := r.sentText(); len(sent) != 0 {
		t.Fatalf("sent %q for a Stop that fired in the millisecond its prompt began the turn", sent)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Fatalf("a lane whose newest prompt began in the millisecond of a Stop reads %s, want working", view.ActualState)
	}
	r.quietFor(t, r.driver, lanes.MaxLiveWait(nil)-time.Minute)
	if sent := r.awaitSentOn(t, r.driver, 1); sent[0].Text != lanes.Nudge {
		t.Fatalf("sent %+v, want the nudge once the live wait ended", sent)
	}
	r.awaitOrderState(t, workorders.Acceptance)
}

// workingHeld holds the first save of a working lane once armed, so a test acts while the worker decides a turn's end
// it has already placed.
type workingHeld struct {
	ports.Journal
	armed atomic.Bool
	gate  *gate
	once  sync.Once
}

func (j *workingHeld) SaveActual(ctx context.Context, lane string, state, from lanes.ActualState) error {
	if state == lanes.Working && j.armed.Load() {
		j.once.Do(func() {
			close(j.gate.reached)
			<-j.gate.release
		})
	}
	return j.Journal.SaveActual(ctx, lane, state, from)
}

// A prompt can land after the worker placed a turn's end and before the lane left that turn, where its start moves
// nothing: here the worker is closing the request the turn left open. The turn the prompt began is still the lane's,
// so the end before it draws no nudge.
func TestAPromptWhileATurnsEndIsDecidedDrawsNoNudge(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	held := &workingHeld{Journal: base.journal, gate: newGate()}
	base.workerJournal = held
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", approvalProfile)
	r.dispatched()
	stopped, returned := newGate(), newGate()
	r.gates(stopped, held.gate, returned)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01), returned.step(), r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	r.atGate(stopped)
	r.subject.HoldApprovalLoop("lane-1")
	r.look(t, gatedCall, "call-1", false)
	r.eventually("a lane that waits", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.NeedsDecision) })
	at := r.clock.Now().UnixMilli()
	r.stopAt(t, at, 0)
	held.armed.Store(true)
	stopped.free()
	r.reach(t, held.gate)
	r.promptAt(t, at+1)
	held.gate.free()
	r.turnDecided(t, 0.01)
	if sent := r.sentText(); len(sent) != 0 {
		t.Errorf("sent %q to a lane whose next turn began while its end was decided", sent)
	}
	returned.free()
	r.awaitOrderState(t, workorders.Acceptance)
}
