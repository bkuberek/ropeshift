package worker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const clearedSession = "session-2"

// cleared is the pair of hooks a clear runs, with the old session's ID and then the new one's.
func (r *laneRig) cleared(t *testing.T, session string) lanes.Reply {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: "SessionEnd", Subject: "session-1", SessionID: "session-1", Detail: "clear", AtMs: 1})
	return r.hookEvent(t, lanes.HookEvent{Event: "SessionStart", Subject: session, SessionID: session, Detail: "clear"})
}

func (r *laneRig) readGenerations(t *testing.T) []ledger.Generation {
	t.Helper()
	return r.read().Generations
}

// aLaneAtWork leaves a lane in a turn, with a test holding it there.
func aLaneAtWork(t *testing.T) (*laneRig, *gate) {
	t.Helper()
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	return r, g
}

// aLaneAtRest is a lane that waits on an answer, with what its session does next.
func aLaneAtRest(t *testing.T, next ...[]fake.DriverStep) *laneRig {
	t.Helper()
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\n")
	turns := append([][]fake.DriverStep{{{Bound: true}, r.asking("What is the retry budget?"), completed(0.01)}}, next...)
	r.driver.Play(script(turns...))
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	return r
}

// clearAmidSave is a clear that lands while a save of the generation before it is on its way to the
// journal, because a clear runs on a hook's goroutine and a save on the one that reads the session.
type clearAmidSave struct {
	ports.Journal
	during func()
	once   sync.Once
	armed  atomic.Bool
	landed chan struct{}
}

func (j *clearAmidSave) SaveProgress(ctx context.Context, lane string, generation int64, progress lanes.Progress) (bool, error) {
	raced := false
	if j.armed.Load() {
		j.once.Do(func() {
			j.during()
			raced = true
		})
	}
	saved, err := j.Journal.SaveProgress(ctx, lane, generation, progress)
	if raced {
		close(j.landed)
	}
	return saved, err
}

// What a clear wrote stands: a save that started before it is of a generation that has ended, and it
// would tell a restart the new one owes no brief.
func TestASaveOfTheGenerationBeforeAClearChangesNothing(t *testing.T) {
	base := newRig(t)
	raced := &clearAmidSave{Journal: base.journal, landed: make(chan struct{})}
	base.workerJournal = raced
	r := newLaneRigFor(t, base).dispatched()
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	raced.during = func() { r.cleared(t, clearedSession) }
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) { raced.armed.Store(true) }}, completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.start()
	select {
	case <-raced.landed:
	case <-time.After(20 * time.Second):
		t.Fatal("no save came")
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 2 || !record.Progress.OwesBrief {
		t.Errorf("after a clear the journal holds generation %d, owing a brief: %v", record.Generation, record.Progress.OwesBrief)
	}
}

// A clear runs inside a process, so it starts no session, and the record says so: a new generation,
// with no handoff, on the session the second hook named. A reply tells a hook which one it is.
func TestAClearIsANewGenerationWithNoHandoff(t *testing.T) {
	r, _ := aLaneAtWork(t)
	reply := r.cleared(t, clearedSession)
	if !reply.OK || reply.Generation != 2 {
		t.Fatalf("reply %+v", reply)
	}
	generations := r.readGenerations(t)
	if len(generations) != 2 {
		t.Fatalf("generations %+v", generations)
	}
	first, second := generations[0], generations[1]
	if first.EndReason != string(ledger.StartClear) || first.SessionID != "session-1" {
		t.Errorf("the old generation %+v", first)
	}
	if second.StartReason != ledger.StartClear || second.SessionID != clearedSession || second.HandoffIn != "" {
		t.Errorf("the new generation %+v", second)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions started", len(specs))
	}
}

// The token a process started with goes on, and acts as the generation the lane is in now (D4).
func TestATokenOutlivesAClearAsTheCurrentGeneration(t *testing.T) {
	r, _ := aLaneAtWork(t)
	before := r.token(t)
	r.cleared(t, clearedSession)
	if reply := r.wait(t, 5); !reply.OK {
		t.Fatalf("the token stopped working: %+v", reply)
	}
	record, err := r.journal.LaneByToken(t.Context(), lanes.TokenDigest(before))
	if err != nil {
		t.Fatal(err)
	}
	if record.Generation != 2 {
		t.Errorf("the token acts as generation %d", record.Generation)
	}
}

// A hook repeats, so a second report of one clear is no second generation, and a second clear is.
func TestARepeatOfAClearChangesNothing(t *testing.T) {
	r, _ := aLaneAtWork(t)
	r.cleared(t, clearedSession)
	r.cleared(t, clearedSession)
	if got := r.readGenerations(t); len(got) != 2 {
		t.Fatalf("generations %+v", got)
	}
	r.cleared(t, "session-3")
	if got := r.readGenerations(t); len(got) != 3 || got[2].SessionID != "session-3" {
		t.Errorf("generations %+v", got)
	}
}

// A process that ended takes its token with it, whatever comes next (ADR-0016).
func TestATokenEndsWithItsProcess(t *testing.T) {
	r := laneRigWith(t, "[lifecycle]\nmax_restarts = 2\nrestart_window = \"1h\"\nrestart_backoff = \"1h\"\n")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Crash: true}}))
	r.start()
	t.Cleanup(func() {
		r.stopWorker()
		r.subject.Wait()
	})
	r.eventually("a lane in backoff", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Backoff) })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(t), Wait: &lanes.WaitDeclaration{Minutes: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if reply := r.subject.AnswerLane(t.Context(), line); reply.Code == lanes.CodeBadToken {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("the token of a process that ended still works")
}

// A hook's tags say the generation its process started in, and a spooled line has no token to
// correct them, so what it says is read against the process it came from.
func TestASpooledHookAfterAClearReachesTheRun(t *testing.T) {
	r, _ := aLaneAtWork(t)
	r.cleared(t, clearedSession)
	path := filepath.Join(r.dir, "spool")
	line := sealedLine(t, r.token(t), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PreCompact", Subject: clearedSession, AtMs: 3})
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("what the spool said", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Compacting) })
}

// A session's driver knows it by the process, which a clear does not change.
func TestAHookAfterAClearReachesItsSession(t *testing.T) {
	r, _ := aLaneAtWork(t)
	r.cleared(t, clearedSession)
	r.hook(t, lanes.HookEvent{Event: "Stop", Subject: clearedSession, SessionID: clearedSession, AtMs: 4})
	hooks := r.driver.Hooks()
	last := hooks[len(hooks)-1]
	if last.Event != "Stop" || last.Generation != 1 {
		t.Errorf("the driver took %+v", last)
	}
}

// A worker that returns finds a cleared session where its process started.
func TestAClearedLaneSurvivesARestart(t *testing.T) {
	r, g := aLaneAtWork(t)
	r.cleared(t, clearedSession)
	r.eventually("the new generation", func(view ledger.LaneView) bool { return view.Generation == 2 })
	g.release <- struct{}{}
	driver := r.takeOver(t, script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.02)}))
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	if adopted := driver.Adopted(); adopted[0].Spec.Generation != 1 {
		t.Errorf("adopted %+v", adopted[0].Spec)
	}
}

// A clear no worker heard of is in what its hooks left, and the worker that returns records it.
func TestAClearWithNoWorkerListeningIsRecordedOnItsReturn(t *testing.T) {
	r, g := aLaneAtWork(t)
	g.release <- struct{}{}
	r.stopWorker()
	err := r.journal.MergeHook(t.Context(), lanes.HookEvent{
		Lane: "lane-1", Generation: 1, Event: "SessionStart", Subject: clearedSession, SessionID: clearedSession, Detail: "clear", AtMs: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	r.takeOver(t, script([]fake.DriverStep{{Hold: true}}))
	r.eventually("the new generation", func(view ledger.LaneView) bool { return view.Generation == 2 })
}

// A cleared session knows nothing, so a lane at rest with no outcome is told its work once, on the
// next pass of its worker, and a restart does not tell it twice.
func TestAClearedLaneAtRestIsToldItsWorkOnce(t *testing.T) {
	var r *laneRig
	finish := fake.DriverStep{Do: func(spec ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(spec) }}
	r = aLaneAtRest(t, []fake.DriverStep{finish, completed(0.02)})
	r.cleared(t, clearedSession)
	for range 3 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	r.awaitOrderState(t, workorders.Acceptance)
	told := 0
	for _, input := range r.driver.Sent() {
		if strings.Contains(input.Text, "wo") {
			told++
		}
	}
	if told != 1 {
		t.Errorf("told its work %d times: %+v", told, r.driver.Sent())
	}
}

// A headless `/clear` ends with a result of its own, which is no turn the lane ran: it owes the
// session its work, not a nudge.
func TestTheEndOfAClearIsNoTurnOfTheLanes(t *testing.T) {
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.start()
	r.reach(t, g)
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	r.cleared(t, clearedSession)
	g.release <- struct{}{}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	time.Sleep(5 * superviseStep)
	for _, input := range r.driver.Sent() {
		if input.Text == lanes.Nudge {
			t.Errorf("a clear drew a nudge: %+v", r.driver.Sent())
		}
	}
	if row := r.row(); row.State != workorders.Running {
		t.Errorf("the work order reads %s", row.State)
	}
}
