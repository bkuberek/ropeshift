package worker_test

import (
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

func (w *worldRig) merged(t *testing.T, event string, atMs int64) {
	t.Helper()
	err := w.journal.MergeHook(t.Context(), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: event, Subject: "s", Detail: "auto", AtMs: atMs})
	if err != nil {
		t.Fatal(err)
	}
}

// adoptedAt leaves a session for a new pass to adopt, with a lane's state as a journal kept it.
func (w *worldRig) adoptedAt(t *testing.T, state, from lanes.ActualState) {
	t.Helper()
	w.record("lane-1", lanes.StateRunning)
	if err := w.journal.SaveActual(t.Context(), "lane-1", state, from); err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
}

func (w *worldRig) awaitActual(t *testing.T, want lanes.ActualState) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if w.lane("lane-1").Actual == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("the lane reads %s, want %s", w.lane("lane-1").Actual, want)
}

// A compaction in a turn ended while no worker listened. The state it left is in the journal, or a
// restart would read a running turn as a lane at rest, and hand it input.
func TestACompactionAdoptedInATurnReturnsToTheTurn(t *testing.T) {
	w := newWorldRig(t)
	w.adoptedAt(t, lanes.Compacting, lanes.Working)
	w.merged(t, "PreCompact", 10)
	w.merged(t, "PostCompact", 20)
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	w.awaitActual(t, lanes.Working)
}

// A merged hook keeps its last time, so the end of an earlier compaction must not close this one.
func TestAnEarlierCompactionsEndClosesNothing(t *testing.T) {
	w := newWorldRig(t)
	w.adoptedAt(t, lanes.Compacting, lanes.Idle)
	w.merged(t, "PreCompact", 1)
	w.merged(t, "PostCompact", 5)
	w.merged(t, "PreCompact", 10)
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * superviseStep)
	if got := w.lane("lane-1").Actual; got != lanes.Compacting {
		t.Errorf("a compaction under way reads %s", got)
	}
}

// The wait on an answer sits out a compaction at rest, however long it lasts, because nothing else
// would start it again: an automatic compaction ends no turn.
func TestAWaitOnAnAnswerSitsOutACompaction(t *testing.T) {
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("What is the retry budget?"), completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: 1})
	r.eventually("a compaction", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Compacting) })
	time.Sleep(5 * superviseStep)
	r.clock.Advance(6 * time.Minute)
	time.Sleep(5 * superviseStep)
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Fatalf("a compaction under way reads %s", view.ActualState)
	}
	r.hook(t, lanes.HookEvent{Event: "PostCompact", Subject: "s", Detail: "auto", AtMs: 2})
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
}

// A compaction that fails reports nothing, and a tool call shows the session went on, so the lane
// leaves through `working` and the policy reads it again.
func TestAFailedCompactionLeavesThroughWorking(t *testing.T) {
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: 1})
	r.eventually("a compaction", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Compacting) })
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", AtMs: 2})
	r.eventually("a turn again", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
}

// A compaction whose end no hook reported ends with its turn, or a lane reads `compacting` for
// the rest of its generation and no threshold reaches it.
func TestATurnsEndClosesACompactionWithNoEnd(t *testing.T) {
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\n")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("What is the retry budget?"), g.step(), completed(0.01)}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: 1})
	r.eventually("a compaction", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Compacting) })
	g.release <- struct{}{}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
}

// aCompactionsEnd is a compaction's boundary and the end of the turn it ran as, the way a pane's
// driver reads both from its last hook.
func aCompactionsEnd() []fake.DriverStep {
	return []fake.DriverStep{{Compaction: true}, {Turn: &fake.DriverTurn{Status: ports.TurnCompleted, Compaction: true, TotalUSD: 0.02}}}
}

// A person's compaction ran ahead of input the lane is waiting on. It ended no turn of the lane's:
// a read of it as one would end the lane with no outcome, with its nudge still on its way.
func TestAPersonsCompactionEndsNoTurnOfTheLanes(t *testing.T) {
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		append(aCompactionsEnd(), g.step()),
	))
	r.start()
	r.reach(t, g)
	r.eventually("the turn it was told to take", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	time.Sleep(5 * superviseStep)
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("the lane reads %s", view.ActualState)
	}
	if row := r.row(); row.State != workorders.Running {
		t.Errorf("the work order reads %s", row.State)
	}
}

// A compaction this lane asked for ends the same way in a pane, and only a request tells the two
// apart: that one is its turn, and the session is told to go on.
func TestACompactionItAskedForEndsItsTurn(t *testing.T) {
	var r *laneRig
	finish := fake.DriverStep{Do: func(spec ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(spec) }}
	r = contextRig(t,
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		aCompactionsEnd(),
		[]fake.DriverStep{tokens(40), finish, completed(0.03)},
	)
	r.awaitOrderState(t, workorders.Acceptance)
	if got := r.driver.Compacts(); got != 1 {
		t.Errorf("%d compactions", got)
	}
	r.awaitSent(t, lanes.AfterCompaction)
}
