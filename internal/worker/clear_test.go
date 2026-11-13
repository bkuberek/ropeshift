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

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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
	t.Parallel()
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
	t.Parallel()
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

// heldBinding hands a worker a session's stream, but holds the session's binding until a test lets
// it go, as a stream the worker reads behind the session's hooks does. A driver that names a
// session reports it as the one it holds.
type heldBinding struct {
	ports.AgentDriver
	names   string
	release chan struct{}
	once    sync.Once
}

func (d *heldBinding) Observe(ctx context.Context, h ports.SessionHandle) (ports.Observation, error) {
	observation, err := d.AgentDriver.Observe(ctx, h)
	if err == nil && d.names != "" {
		observation.SessionID = d.names
	}
	return observation, err
}

func (d *heldBinding) Events(ctx context.Context, h ports.SessionHandle) (<-chan ports.DriverEvent, error) {
	events, err := d.AgentDriver.Events(ctx, h)
	if err != nil {
		return nil, err
	}
	out := make(chan ports.DriverEvent)
	go func() {
		defer close(out)
		for event := range events {
			if event.Kind == ports.EventSessionBound {
				<-d.release
			}
			out <- event
		}
	}()
	return out, nil
}

func (d *heldBinding) letGo() { d.once.Do(func() { close(d.release) }) }

// aLaneAheadOfItsBinding leaves a lane in a turn whose session's binding the worker has not read,
// on a driver that names the session it holds when names is set.
func aLaneAheadOfItsBinding(t *testing.T, names string) *laneRig {
	t.Helper()
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	held := &heldBinding{AgentDriver: r.driver, names: names, release: make(chan struct{})}
	t.Cleanup(held.letGo)
	r.config.Driver = held
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	return r
}

// A clear's hooks can reach the worker before it reads the session's binding from the stream. The
// generation the clear ends still names its session, or a resume or an audit loses that session.
func TestAClearAheadOfTheBindingKeepsTheOldSession(t *testing.T) {
	t.Parallel()
	r := aLaneAheadOfItsBinding(t, "")
	if reply := r.cleared(t, clearedSession); !reply.OK || reply.Generation != 2 {
		t.Fatalf("reply %+v", reply)
	}
	generations := r.readGenerations(t)
	if len(generations) != 2 {
		t.Fatalf("generations %+v", generations)
	}
	if first := generations[0]; first.EndReason != string(ledger.StartClear) || first.SessionID != "session-1" {
		t.Errorf("the old generation %+v", first)
	}
	if second := generations[1]; second.SessionID != clearedSession {
		t.Errorf("the new generation %+v", second)
	}
}

// A driver can hold the session a clear began before the clear's hook arrives, and then knows
// nothing of the one before. That session is not the old generation's, and the clear stands.
func TestAClearAheadOfTheBindingTakesNoNewSessionForTheOld(t *testing.T) {
	t.Parallel()
	r := aLaneAheadOfItsBinding(t, clearedSession)
	if reply := r.cleared(t, clearedSession); !reply.OK || reply.Generation != 2 {
		t.Fatalf("reply %+v", reply)
	}
	generations := r.readGenerations(t)
	if len(generations) != 2 || generations[0].SessionID == clearedSession || generations[1].SessionID != clearedSession {
		t.Errorf("generations %+v", generations)
	}
}

// The token a process started with goes on, and acts as the generation the lane is in now (D4).
func TestATokenOutlivesAClearAsTheCurrentGeneration(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
// correct them, so what it says is read against the process it came from. This one is a compaction
// of the generation the clear began, stamped after the clear.
func TestASpooledHookAfterAClearReachesTheRun(t *testing.T) {
	t.Parallel()
	r, _ := aLaneAtWork(t)
	r.cleared(t, clearedSession)
	after := time.Now().UnixMilli()
	r.spool(t, lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PreCompact", Subject: clearedSession, AtMs: after})
	r.eventually("what the spool said", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Compacting) })
}

// spool leaves one sealed line in the spool and drains it, as a worker's pass does.
func (r *laneRig) spool(t *testing.T, event lanes.HookEvent) {
	t.Helper()
	line := sealedLine(t, r.token(t), event)
	if err := os.WriteFile(filepath.Join(r.dir, "spool"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// The ways a hook the worker already heard can reach it again. A hook waits 25 ms for an ack and
// spools its event when none comes, so the worker may hold the event twice, and it takes the spool on
// every pass.
var repeatedHooks = map[string]func(t *testing.T, r *laneRig, event lanes.HookEvent){
	"the spool":  func(t *testing.T, r *laneRig, event lanes.HookEvent) { r.spool(t, event) },
	"the socket": func(t *testing.T, r *laneRig, event lanes.HookEvent) { r.hook(t, event) },
}

// The times of a compaction that ran before a clear. A clear's hook is stamped by the clock of the
// test, which is later than both.
const (
	compactionBeganMs = 10
	compactionEndedMs = 20
)

// aClearedLaneAtRest is a lane whose first generation was cleared in a turn, and whose turn has ended
// since, so it waits for its brief. A heard compaction ran in the turn before the clear.
func aClearedLaneAtRest(t *testing.T, heardCompaction bool) *laneRig {
	t.Helper()
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.atGate(g)
	if heardCompaction {
		r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: compactionBeganMs})
		r.hook(t, lanes.HookEvent{Event: "PostCompact", Subject: "s", Detail: "auto", AtMs: compactionEndedMs})
	}
	r.cleared(t, clearedSession)
	g.release <- struct{}{}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	return r
}

// briefs counts the inputs that told the session its work.
func (r *laneRig) briefs() int {
	told := 0
	for _, input := range r.driver.Sent() {
		if input.Text != lanes.Nudge && strings.Contains(input.Text, r.id) {
			told++
		}
	}
	return told
}

// A hook stamped before a clear says nothing about the generation the clear began, however late it
// arrives. A compaction start that did would leave the lane in a compaction that nothing ends, and a
// lane that is not at rest is not told its work.
func TestAHookFromBeforeAClearLeavesTheNewGenerationAtRest(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"a compaction that ended before the clear":           true,
		"a compaction the worker heard nothing of before it": false,
	}
	for name, heard := range cases {
		for path, deliver := range repeatedHooks {
			t.Run(name+" through "+path, func(t *testing.T) {
				t.Parallel()
				r := aClearedLaneAtRest(t, heard)
				deliver(t, r, lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PreCompact", Subject: "s", Detail: "auto", AtMs: compactionBeganMs})
				if view := r.read(); view.ActualState != string(lanes.Idle) {
					t.Fatalf("after the late hook the lane reads %s", view.ActualState)
				}
				r.eventually("the brief", func(ledger.LaneView) bool {
					if _, err := r.subject.Reconcile(t.Context()); err != nil {
						t.Fatal(err)
					}
					return r.briefs() == 1
				})
			})
		}
	}
}

// A session's driver knows it by the process, which a clear does not change.
func TestAHookAfterAClearReachesItsSession(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// heldSend holds the first input it is handed once armed, until a test lets it go, and counts every
// input it is handed from then, which a session that already ended would not.
type heldSend struct {
	ports.AgentDriver
	armed   atomic.Bool
	sends   atomic.Int32
	once    sync.Once
	inside  chan struct{}
	release chan struct{}
}

func (d *heldSend) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	held := false
	if d.armed.Load() {
		d.sends.Add(1)
		d.once.Do(func() { held = true })
	}
	if held {
		close(d.inside)
		<-d.release
	}
	return d.AgentDriver.Send(ctx, h, in)
}

// Two passes of one worker can overlap, as one a caller runs does one of the worker's own loop. The
// journal reads an effect one of them is performing as started, as it reads one a retry cleared, so
// a pass that finds the brief on its way leaves it, or a cleared session is told its work twice.
func TestTwoPassesAtOnceTellAClearedSessionItsWorkOnce(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\n")
	held := &heldSend{AgentDriver: r.driver, inside: make(chan struct{}), release: make(chan struct{})}
	r.config.Driver = held
	finish := fake.DriverStep{Do: func(spec ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(spec) }}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("What is the retry budget?"), completed(0.01)}, []fake.DriverStep{finish, completed(0.02)}))
	r.start()
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.cleared(t, clearedSession)
	held.armed.Store(true)
	first := make(chan error, 1)
	go func() {
		_, err := r.subject.Reconcile(t.Context())
		first <- err
	}()
	select {
	case <-held.inside:
	case <-time.After(20 * time.Second):
		t.Fatal("no pass handed the brief")
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(held.release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if told := held.sends.Load(); told != 1 {
		t.Fatalf("told its work %d times", told)
	}
	r.awaitOrderState(t, workorders.Acceptance)
}

// passAtRest runs a pass of the worker the moment a lane's end of turn reaches the journal, which is
// after the lane reads idle and before its run decides what the turn left.
type passAtRest struct {
	ports.Journal
	pass    func()
	armed   atomic.Bool
	once    sync.Once
	ran     atomic.Bool
	decided chan struct{}
	after   sync.Once
}

func (j *passAtRest) SaveActual(ctx context.Context, lane string, state, from lanes.ActualState) error {
	if j.armed.Load() && state == lanes.Idle {
		j.once.Do(func() {
			j.pass()
			j.ran.Store(true)
		})
	}
	return j.Journal.SaveActual(ctx, lane, state, from)
}

// SaveProgress follows the run's decision on a turn.
func (j *passAtRest) SaveProgress(ctx context.Context, lane string, generation int64, progress lanes.Progress) (bool, error) {
	saved, err := j.Journal.SaveProgress(ctx, lane, generation, progress)
	if j.ran.Load() {
		j.after.Do(func() { close(j.decided) })
	}
	return saved, err
}

// The pass that briefs a cleared session can come between the clear's end of turn and what its run
// makes of that turn. The turn is still the clear's, so it draws no nudge on top of the brief.
func TestABriefAsAClearsTurnEndsDrawsNoNudge(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &passAtRest{Journal: base.journal, decided: make(chan struct{})}
	base.workerJournal = journal
	r := newLaneRigFor(t, base).dispatched()
	g := newGate()
	r.finishLater(g)
	journal.pass = func() {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Error(err)
		}
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}, []fake.DriverStep{{Hold: true}}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	r.cleared(t, clearedSession)
	journal.armed.Store(true)
	g.release <- struct{}{}
	select {
	case <-journal.decided:
	case <-time.After(20 * time.Second):
		t.Fatal("the run never decided on the clear's turn")
	}
	briefs, nudges := 0, 0
	for _, input := range r.driver.Sent() {
		switch {
		case input.Text == lanes.Nudge:
			nudges++
		case strings.Contains(input.Text, "wo"):
			briefs++
		}
	}
	if briefs != 1 || nudges != 0 {
		t.Errorf("%d briefs and %d nudges: %+v", briefs, nudges, r.driver.Sent())
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
