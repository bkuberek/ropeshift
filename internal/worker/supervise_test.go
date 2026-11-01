package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// restarts is a profile whose breaker a test can reach in a few steps.
const restarts = `[lifecycle]
stall_after = "20m"
max_restarts = 2
restart_window = "10m"
max_generations_per_day = 8
restart_backoff = "30s"
brief_grace = "2m"
`

func crashed() []fake.DriverStep { return []fake.DriverStep{{Bound: true}, {Crash: true}} }

// start runs a lane under a context a test can end, which is how a worker goes away.
func (r *laneRig) start() {
	r.t.Helper()
	ctx, cancel := context.WithCancel(r.t.Context())
	r.t.Cleanup(cancel)
	r.stopWorker = cancel
	if _, err := r.subject.Reconcile(ctx); err != nil {
		r.t.Fatal(err)
	}
}

// wait reaches the worker the way a session's `lane_wait` does.
func (r *laneRig) wait(t *testing.T, minutes int64) lanes.Reply {
	t.Helper()
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: r.token(t), Wait: &lanes.WaitDeclaration{Reason: "a CI run", Minutes: minutes},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r.subject.AnswerLane(t.Context(), line)
}

func (r *laneRig) token(t *testing.T) string {
	t.Helper()
	for _, entry := range r.driver.Specs()[0].Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			return value
		}
	}
	t.Fatal("no token")
	return ""
}

func (r *laneRig) awaitGeneration(t *testing.T, generation int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if len(r.driver.Specs()) >= generation {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("generation %d never started", generation)
}

// restartWorker stands for a new process on the same journal, with a driver that finds the
// session an earlier process left, and plays what comes after it.
func (r *laneRig) restartWorker(t *testing.T, left fake.DriverScript, after fake.DriverScript) *worker.Worker {
	t.Helper()
	r.stopWorker()
	r.subject.Wait()
	driver := fake.NewAgentDriver()
	driver.Leave("lane-1", 1, lanes.Session{Found: true, Liveness: lanes.LivenessDead, Readable: true})
	driver.Play(left)
	driver.PlayAfter(after)
	config := *r.config
	config.Driver = driver
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	return next
}

// laneState waits until a lane's journal record reads one state.
func (r *laneRig) laneState(want lanes.ActualState) {
	r.t.Helper()
	r.eventually("state "+string(want), func(view ledger.LaneView) bool { return view.ActualState == string(want) })
}

func TestStalledComesOnlyFromAQuietWorkingLane(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.clock.Advance(19 * time.Minute)
	if view := r.read(); slices.Contains(view.Conditions, string(lanes.Stalled)) {
		t.Errorf("before stall_after: %v", view.Conditions)
	}
	r.clock.Advance(2 * time.Minute)
	if view := r.read(); !slices.Contains(view.Conditions, string(lanes.Stalled)) {
		t.Errorf("past stall_after: %v", view.Conditions)
	}
}

func TestAToolInFlightSuppressesStalled(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "tool-1", Tool: "Bash"})
	r.clock.Advance(time.Hour)
	view := r.read()
	if slices.Contains(view.Conditions, string(lanes.Stalled)) || !slices.Contains(view.Conditions, string(lanes.WaitingExternal)) {
		t.Errorf("conditions %v", view.Conditions)
	}
}

func TestLaneWaitDeclaresAWaitThatEnds(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	if reply := r.wait(t, 30); !reply.OK {
		t.Fatalf("reply %+v", reply)
	}
	r.clock.Advance(25 * time.Minute)
	view := r.read()
	if slices.Contains(view.Conditions, string(lanes.Stalled)) || !slices.Contains(view.Conditions, string(lanes.WaitingExternal)) {
		t.Errorf("during the wait: %v", view.Conditions)
	}
	r.clock.Advance(10 * time.Minute)
	view = r.read()
	if !slices.Contains(view.Conditions, string(lanes.Stalled)) || slices.Contains(view.Conditions, string(lanes.WaitingExternal)) {
		t.Errorf("after the wait: %v", view.Conditions)
	}
}

func TestAWaitDoesNotSuppressOverdue(t *testing.T) {
	base := newRig(t)
	base.deadline = base.clock.Now().Add(time.Hour).UnixMilli()
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", restarts)
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	if reply := r.wait(t, 600); !reply.OK {
		t.Fatalf("reply %+v", reply)
	}
	if view := r.read(); slices.Contains(view.Conditions, string(lanes.Overdue)) {
		t.Errorf("before the deadline: %v", view.Conditions)
	}
	r.clock.Advance(2 * time.Hour)
	view := r.read()
	if !slices.Contains(view.Conditions, string(lanes.Overdue)) || !slices.Contains(view.Conditions, string(lanes.WaitingExternal)) {
		t.Errorf("past the deadline: %v", view.Conditions)
	}
}

func TestACrashRestartsAfterItsBackoffAndResumes(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(29 * time.Second)
	time.Sleep(60 * time.Millisecond)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Fatalf("a restart before its delay: %d sessions", len(specs))
	}
	r.clock.Advance(2 * time.Second)
	r.subject.Wait()
	specs := r.driver.Specs()
	if len(specs) != 2 || specs[1].Generation != 2 {
		t.Fatalf("specs %+v", specs)
	}
	if resumed := r.driver.Resumed(); len(resumed) != 1 || resumed[0].SessionID == "" {
		t.Errorf("resumed %+v", resumed)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

// heldCharge stops a worker at its first read of a lane's restarts, which falls after the
// lane reads `backoff` and before the worker judges the restart.
type heldCharge struct {
	ports.Journal
	once    sync.Once
	reached chan struct{}
	release chan struct{}
}

func (j *heldCharge) Restarts(ctx context.Context, lane string) ([]lanes.Restart, error) {
	j.once.Do(func() {
		close(j.reached)
		<-j.release
	})
	return j.Journal.Restarts(ctx, lane)
}

// A delay counts from the crash. A reader who sees `backoff` and moves the clock has moved
// it past that crash, however late the worker gets to the charge.
func TestARestartDelayCountsFromTheCrash(t *testing.T) {
	base := newRig(t)
	held := &heldCharge{Journal: base.journal, reached: make(chan struct{}), release: make(chan struct{})}
	base.workerJournal = held
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", restarts)
	r.dispatched()
	r.driver.Play(script(crashed()))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.start()
	select {
	case <-held.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the worker never came to its charge")
	}
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	close(held.release)
	r.awaitGeneration(t, 2)
	r.subject.Wait()
}

func TestACrashBeforeASessionBoundStartsFresh(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script([]fake.DriverStep{{Crash: true}}))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(time.Minute)
	r.subject.Wait()
	if resumed := r.driver.Resumed(); len(resumed) != 0 {
		t.Errorf("a resume with no session: %+v", resumed)
	}
	if specs := r.driver.Specs(); len(specs) != 2 {
		t.Fatalf("specs %+v", specs)
	}
}

func TestTheSlidingWindowTrips(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	r.awaitGeneration(t, 2)
	r.laneState(lanes.Backoff)
	r.clock.Advance(61 * time.Second)
	r.awaitGeneration(t, 3)
	r.laneState(lanes.Tripped)
	r.clock.Advance(5 * time.Minute)
	time.Sleep(60 * time.Millisecond)
	if specs := r.driver.Specs(); len(specs) != 3 {
		t.Errorf("a tripped lane started: %d sessions", len(specs))
	}
}

func TestAHumanResetLeavesTripped(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	r.awaitGeneration(t, 2)
	r.laneState(lanes.Backoff)
	r.clock.Advance(61 * time.Second)
	r.awaitGeneration(t, 3)
	r.laneState(lanes.Tripped)
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if err := r.plane2(t).ResetLane(t.Context(), scope, r.id, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 4 {
		t.Fatalf("sessions after the reset: %d", len(specs))
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

func TestOnlyAMemberResetsABreaker(t *testing.T) {
	r := newLaneRig(t)
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: "w"}
	err := r.plane2(t).ResetLane(t.Context(), scope, r.id, worker)
	if !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("reset by a worker: %v", err)
	}
}

func TestUnknownLivenessIsNoCrash(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.driver.ReadUnknown(true)
	r.clock.Advance(time.Hour)
	view := r.read()
	if view.Liveness != string(lanes.LivenessUnknown) || view.ActualState != string(lanes.Working) {
		t.Errorf("view %+v", view)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("unknown restarted a lane: %d sessions", len(specs))
	}
	if charges, err := r.journal.Restarts(t.Context(), "lane-1"); err != nil || len(charges) != 0 {
		t.Errorf("charges %+v, %v", charges, err)
	}
	r.driver.ReadUnknown(false)
}

func TestAnUnconfirmedBriefRetriesOnceWithANewID(t *testing.T) {
	r := laneRigWith(t, restarts)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(fake.DriverScript{SessionID: "s", Model: "m", Silent: true, Turns: [][]fake.DriverStep{{g.step()}}})
	r.start()
	r.laneState(lanes.Briefing)
	r.clock.Advance(3 * time.Minute)
	r.eventually("the retry", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
	view := r.read()
	if !slices.Contains(view.Conditions, string(lanes.BriefUnconfirmed)) {
		t.Errorf("conditions %v", view.Conditions)
	}
	sent := r.driver.Sent()
	if sent[0].ID == "" || sent[0].Text == "" {
		t.Errorf("retry %+v", sent[0])
	}
	r.clock.Advance(time.Hour)
	r.read()
	r.read()
	if sent := r.driver.Sent(); len(sent) != 1 {
		t.Errorf("%d retries", len(sent))
	}
	charges, err := r.journal.Restarts(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 1 {
		t.Errorf("a retry counts toward the breaker: %+v", charges)
	}
}

func TestACrashAfterAReportKeepsItsEndAndDoesNotRestart(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), {Crash: true}}))
	r.start()
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("a lane that reported restarted: %d sessions", len(specs))
	}
}

// A worker that dies inside a backoff leaves one charge, and the next one waits out the
// rest of the delay it finds.
func TestACrashRestartChargesOnceAcrossAWorkerRestart(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.awaitCharge(t)
	if charges, err := r.journal.Restarts(t.Context(), "lane-1"); err != nil || len(charges) != 1 {
		t.Fatalf("charges %+v, %v", charges, err)
	}
	after := script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)})
	next := r.restartWorker(t, script([]fake.DriverStep{{Crash: true}}), after)
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(40 * time.Second)
	next.Wait()
	charges, err := r.journal.Restarts(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(charges) != 1 {
		t.Errorf("one crash, %d charges: %+v", len(charges), charges)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

// plane2 is the use case a member's command reaches.
func (r *laneRig) plane2(t *testing.T) *control.Leases {
	t.Helper()
	return control.NewLeases(r.store)
}
