package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// limitProfile asks little of a session that cannot answer.
const limitProfile = "[lifecycle]\nmax_restarts = 0\nhandoff_timeout = \"1s\"\n"

// scriptedLanes gives each session the script its lane and generation call for. The lock is what
// keeps one lane's script from reaching another's start.
type scriptedLanes struct {
	*fake.AgentDriver
	mu     sync.Mutex
	script func(spec ports.SessionSpec) fake.DriverScript
	// fleet counts the sessions a fleet still has to start.
	fleet *fleetGate
}

// fleetGate holds one session's limit until the whole fleet has a session. A limit stops a lane that
// has not started, so a rejection that landed first would leave the rest claimed, and a test of a
// fleet at its limit would wait on work that was never going to park.
type fleetGate struct {
	left atomic.Int64
	full chan struct{}
}

func newFleetGate(count int) *fleetGate {
	gate := &fleetGate{full: make(chan struct{})}
	gate.left.Store(int64(count))
	return gate
}

func (g *fleetGate) started() {
	if g.left.Add(-1) == 0 {
		close(g.full)
	}
}

func (g *fleetGate) up(ports.SessionSpec) {
	select {
	case <-g.full:
	case <-time.After(20 * time.Second):
	}
}

func (d *scriptedLanes) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fleet != nil && spec.Generation == 1 {
		d.fleet.started()
	}
	d.Play(d.script(spec))
	return d.AgentDriver.Start(ctx, spec)
}

func (d *scriptedLanes) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Play(d.script(spec))
	return d.AgentDriver.Resume(ctx, spec, from)
}

// limitRig is one worker with a fleet of lanes on one sign-in.
type limitRig struct {
	*laneRig
	orders []string
	lanes  []string
	gate   *fleetGate
}

// limitOnceAllStart is the first session of a fleet: its limit lands with every lane running.
func (r *limitRig) limitOnceAllStart() fake.DriverScript {
	return script([]fake.DriverStep{{Bound: true}, {Do: r.gate.up}, *rejected(0), {Hold: true}})
}

func fleetOf(t *testing.T, count int, profile string, script func(spec ports.SessionSpec) fake.DriverScript) *limitRig {
	t.Helper()
	return fleetOn(t, newRig(t), count, profile, script)
}

// fleetOn builds on a rig a test has already set up.
func fleetOn(t *testing.T, base *rig, count int, profile string, script func(spec ports.SessionSpec) fake.DriverScript) *limitRig {
	t.Helper()
	line := newLaneRigFor(t, base)
	line.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", profile)
	r := &limitRig{laneRig: line.dispatched(), orders: []string{"wo"}, lanes: []string{"lane-1"}, gate: newFleetGate(count)}
	r.clock.Advance(time.Since(r.clock.Now()))
	r.config.Driver = &scriptedLanes{AgentDriver: r.driver, script: script, fleet: r.gate}
	for i := 2; i <= count; i++ {
		order, lane := fmt.Sprintf("wo-%d", i), fmt.Sprintf("lane-%d", i)
		r.secondLane(order, lane)
		r.orders, r.lanes = append(r.orders, order), append(r.lanes, lane)
	}
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	return r
}

func (r *limitRig) order(t *testing.T, id string) workorders.WorkOrder {
	t.Helper()
	var out workorders.WorkOrder
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.WorkOrders().Get(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// awaitOrders passes as a daemon's ticks do, until every work order reads one state.
func (r *limitRig) awaitOrders(t *testing.T, state workorders.State) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		r.beat(t)
		if !slices.ContainsFunc(r.orders, func(id string) bool { return r.order(t, id).State != state }) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	for _, id := range r.orders {
		t.Logf("%s reads %s", id, r.order(t, id).State)
	}
	t.Fatalf("not every work order reached %s", state)
}

func (r *limitRig) wake(t *testing.T, lane string, generation int64) parking.Wake {
	t.Helper()
	var out parking.Wake
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Wakes().Get(lanes.WakeID(lane, generation))
		return err
	})
	if err != nil {
		t.Fatalf("lane %s: %v", lane, err)
	}
	return out
}

func (r *limitRig) key() string { return r.subject.Auth().WorkerID + ":" + fake.FakeDriverName }

// passTime moves both clocks, the worker's and the ledger's, past a moment.
func (r *limitRig) passTime(t *testing.T, by time.Duration) {
	t.Helper()
	r.clock.Advance(by)
	r.shiftDatabase(t, by.Milliseconds())
	if _, err := control.NewWakes(r.store).WakeTick(t.Context(), scope, member); err != nil {
		t.Fatal(err)
	}
}

func rejected(resetMs int64) *fake.DriverStep {
	return &fake.DriverStep{Limit: &lanes.UsageReading{Status: lanes.UsageRejected, ResetsAtMs: resetMs, Window: "five_hour"}}
}

// atRest is a session waiting for its turn to end.
func atRest() fake.DriverScript { return script([]fake.DriverStep{{Bound: true}, {Hold: true}}) }

func finishes(r *laneRig) fake.DriverScript {
	return script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.02)})
}

// firstLaneHits gives lane-1 one signal after it binds, and every other lane a rest, and a resumed
// generation of any lane the end of its work.
func firstLaneHits(r **limitRig, signal fake.DriverStep) func(ports.SessionSpec) fake.DriverScript {
	return func(spec ports.SessionSpec) fake.DriverScript {
		switch {
		case spec.Generation > 1:
			return finishes((*r).laneRig)
		case spec.Lane == "lane-1":
			return script([]fake.DriverStep{{Bound: true}, signal, {Hold: true}})
		}
		return atRest()
	}
}

// startedFleet starts every lane and waits until each has bound, then lets lane-1's signal go.
func startedFleet(t *testing.T, count int, signal fake.DriverStep) *limitRig {
	t.Helper()
	var r *limitRig
	gate := make(chan struct{})
	release := fake.DriverStep{Do: func(ports.SessionSpec) { <-gate }}
	inner := firstLaneHits(&r, signal)
	r = fleetOf(t, count, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		out := inner(spec)
		if spec.Lane == "lane-1" && spec.Generation == 1 {
			out.Turns[0] = slices.Insert(out.Turns[0], 1, release)
		}
		return out
	})
	r.start()
	r.eventually("the fleet at work", func(ledger.LaneView) bool { return len(r.driver.Specs()) == count })
	close(gate)
	return r
}

// TestALimitParksEveryLaneOnItsSignIn is the outcome: one lane reaches a limit, every lane on that
// sign-in parks with a wake of its own naming one key, and none is asked to write anything.
func TestALimitParksEveryLaneOnItsSignIn(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := startedFleet(t, 3, *rejected(reset))
	r.awaitOrders(t, workorders.Parked)
	for _, lane := range r.lanes {
		wake := r.wake(t, lane, 1)
		if wake.Expr != lanes.UsageWake(r.key(), reset) || !wake.Open() {
			t.Errorf("lane %s waits on %+v", lane, wake)
		}
	}
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("a lane at a limit was asked for something: %+v", sent)
	}
	if got := r.driver.UsageReads(); got != 0 {
		t.Errorf("a known reset read /usage %d times", got)
	}
}

// TestOneSignalWakesEveryLane is D10: a reset fires one signal, every wake that names the key
// fires, and each lane goes on in a new generation of its own session.
func TestOneSignalWakesEveryLane(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := startedFleet(t, 3, *rejected(reset))
	r.awaitOrders(t, workorders.Parked)
	r.passTime(t, time.Hour+time.Second)
	r.awaitOrders(t, workorders.Acceptance)
	signals := 0
	for _, event := range r.events() {
		if event.Type == parking.EventSignal {
			signals++
		}
	}
	if signals != 1 {
		t.Errorf("%d signals", signals)
	}
	if got := r.driver.Resumed(); len(got) != 3 {
		t.Errorf("%d lanes resumed their sessions, want the three a limit stopped: %+v", len(got), got)
	}
}

// TestAGradualStartAfterAReset holds what a reset releases to the sign-in's own cap.
func TestAGradualStartAfterAReset(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := startedFleet(t, 3, *rejected(reset))
	r.awaitOrders(t, workorders.Parked)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).PutUser("[admission]\nmax_lanes_per_sign_in = 1\n")
	gate := make(chan struct{})
	var once sync.Once
	driver := r.config.Driver.(*scriptedLanes)
	driver.mu.Lock()
	driver.script = func(ports.SessionSpec) fake.DriverScript {
		held := fake.DriverStep{Do: func(ports.SessionSpec) { <-gate }}
		return script([]fake.DriverStep{{Bound: true}, held, r.reports(workorders.OutcomeCompleted), completed(0.02)})
	}
	driver.mu.Unlock()
	t.Cleanup(func() { once.Do(func() { close(gate) }) })
	r.passTime(t, time.Hour+time.Second)
	r.eventually("one lane back", func(ledger.LaneView) bool {
		r.beat(t)
		return len(r.driver.Resumed()) == 1
	})
	for range 5 {
		r.beat(t)
	}
	if got := r.driver.Resumed(); len(got) != 1 {
		t.Fatalf("%d lanes back under a cap of one", len(got))
	}
	once.Do(func() { close(gate) })
	r.awaitOrders(t, workorders.Acceptance)
}

func TestARetryOfARateLimitParksOnlyPastItsTries(t *testing.T) {
	t.Parallel()
	var r *limitRig
	seen := make(chan struct{})
	r = fleetOf(t, 1, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		return script([]fake.DriverStep{
			{Bound: true}, {Retry: lanes.FailureUsageLimit, Attempt: lanes.RetriesBeforeLimit - 1},
			{Do: func(ports.SessionSpec) { close(seen) }}, {Hold: true},
		})
	})
	r.start()
	<-seen
	for range 5 {
		r.beat(t)
	}
	if got := r.order(t, "wo").State; got != workorders.Running {
		t.Errorf("one 429 took a lane to %s", got)
	}
}

func TestARetryPastItsTriesParks(t *testing.T) {
	t.Parallel()
	r := startedFleet(t, 2, fake.DriverStep{Retry: lanes.FailureUsageLimit, Attempt: lanes.RetriesBeforeLimit})
	r.awaitOrders(t, workorders.Parked)
}

func TestARetryOfAnythingElseParksNothing(t *testing.T) {
	t.Parallel()
	var r *limitRig
	seen := make(chan struct{})
	r = fleetOf(t, 1, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		return script([]fake.DriverStep{
			{Bound: true}, {Retry: "overloaded", Attempt: 50}, {Do: func(ports.SessionSpec) { close(seen) }}, {Hold: true},
		})
	})
	r.start()
	<-seen
	for range 5 {
		r.beat(t)
	}
	if got := r.order(t, "wo").State; got != workorders.Running {
		t.Errorf("an overloaded API took a lane to %s", got)
	}
}

// TestATurnThatFailedAtALimitParks keeps a lane that hit its limit in a turn from reading as failed.
func TestATurnThatFailedAtALimitParks(t *testing.T) {
	t.Parallel()
	failedAtLimit := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: lanes.FailureUsageLimit, TotalUSD: 0.01}}
	r := startedFleet(t, 2, failedAtLimit)
	r.awaitOrders(t, workorders.Parked)
}

// TestAStopFailureParks is the signal a pane has, which reaches a headless lane as a hook too.
func TestAStopFailureParks(t *testing.T) {
	t.Parallel()
	r := fleetOf(t, 1, limitProfile, func(ports.SessionSpec) fake.DriverScript { return atRest() })
	r.start()
	r.eventually("the lane at work", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	if reply := r.hookEvent(t, lanes.HookEvent{Event: "StopFailure", Detail: lanes.FailureUsageLimit}); !reply.OK {
		t.Fatalf("%+v", reply)
	}
	r.awaitOrders(t, workorders.Parked)
}

func TestAStopFailureOfAnythingElseParksNothing(t *testing.T) {
	t.Parallel()
	r := fleetOf(t, 1, limitProfile, func(ports.SessionSpec) fake.DriverScript { return atRest() })
	r.start()
	r.eventually("the lane at work", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	if reply := r.hookEvent(t, lanes.HookEvent{Event: "StopFailure", Detail: "server_error"}); !reply.OK {
		t.Fatalf("%+v", reply)
	}
	for range 5 {
		r.beat(t)
	}
	if got := r.order(t, "wo").State; got != workorders.Running {
		t.Errorf("a server error took a lane to %s", got)
	}
}

// TestTheLastReadingNamesTheReset is the design's rule: what says a limit was reached carries no
// time, so a worker uses the last one any lane read.
func TestTheLastReadingNamesTheReset(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := fleetOf(t, 1, limitProfile, func(ports.SessionSpec) fake.DriverScript {
		return script([]fake.DriverStep{
			{Bound: true}, {Limit: &lanes.UsageReading{Status: lanes.UsageAllowed, ResetsAtMs: reset}},
			{Retry: lanes.FailureUsageLimit, Attempt: lanes.RetriesBeforeLimit}, {Hold: true},
		})
	})
	r.start()
	r.awaitOrders(t, workorders.Parked)
	if wake := r.wake(t, "lane-1", 1); wake.Expr != lanes.UsageWake(r.key(), reset) {
		t.Errorf("waits on %s", wake.Expr)
	}
}

// TestNoKnownResetReadsUsageAndProbes is the path with no time at all: `/usage` is read once and
// kept on this machine, one lane is the probe, and it backs off until it gets through.
func TestNoKnownResetReadsUsageAndProbes(t *testing.T) {
	t.Parallel()
	var r *limitRig
	tries := 0
	r = fleetOf(t, 3, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		switch {
		case spec.Generation == 1 && spec.Lane == "lane-1":
			return r.limitOnceAllStart()
		case spec.Generation == 1:
			return atRest()
		case spec.Lane != "lane-1":
			return finishes(r.laneRig)
		}
		tries++
		if tries == 1 {
			return script([]fake.DriverStep{{Bound: true}, *rejected(0), {Hold: true}})
		}
		return finishes(r.laneRig)
	})
	r.driver.SetUsageRead("Current session: 100% used. A plugin named private-thing is loaded.", nil)
	r.start()
	r.awaitOrders(t, workorders.Parked)
	probe := r.wake(t, "lane-1", 1)
	expr, err := parking.Parse(probe.Expr, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	first, timed := expr.FirstTime()
	if !timed {
		t.Fatalf("the probe waits on %s", probe.Expr)
	}
	for _, lane := range r.lanes[1:] {
		if wake := r.wake(t, lane, 1); wake.Expr != lanes.UsageWake(r.key(), 0) {
			t.Errorf("lane %s waits on %s", lane, wake.Expr)
		}
	}
	for range 3 {
		r.beat(t)
	}
	if got := r.driver.UsageReads(); got != 1 {
		t.Errorf("/usage read %d times", got)
	}
	for _, event := range r.events() {
		if strings.Contains(string(event.Data), "private-thing") {
			t.Errorf("a hint left the machine: %s", event.Data)
		}
	}
	r.passTime(t, lanes.DefaultUsageBackoff+time.Second)
	r.eventually("the probe parked again", func(ledger.LaneView) bool {
		r.beat(t)
		order := r.order(t, "wo")
		return order.State == workorders.Parked && len(r.generations(t)) == 2
	})
	second := r.wake(t, "lane-1", 2)
	expr, err = parking.Parse(second.Expr, "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	next, timed := expr.FirstTime()
	if !timed || next-first < (2*lanes.DefaultUsageBackoff).Milliseconds() {
		t.Errorf("the second try waits %d ms more than the first", next-first)
	}
	if got := r.order(t, "wo-2").State; got != workorders.Parked {
		t.Errorf("the other lanes went at %s with the limit still there", got)
	}
	r.passTime(t, 2*lanes.DefaultUsageBackoff+time.Second)
	r.awaitOrders(t, workorders.Acceptance)
}

// TestAProbeThatGetsThroughWakesTheOthers holds the probe at work, so only what it heard back can
// end the limit: a lane that goes on has no end for a worker to read.
func TestAProbeThatGetsThroughWakesTheOthers(t *testing.T) {
	t.Parallel()
	for name, heard := range map[string]fake.DriverStep{
		"a turn that finished":        completed(0.02),
		"a reading that says allowed": {Limit: &lanes.UsageReading{Status: lanes.UsageAllowed}},
	} {
		t.Run(name, func(t *testing.T) {
			var r *limitRig
			back, release := make(chan struct{}), make(chan struct{})
			r = fleetOf(t, 2, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
				switch {
				case spec.Generation == 1 && spec.Lane == "lane-1":
					return r.limitOnceAllStart()
				case spec.Generation == 1:
					return atRest()
				case spec.Lane == "lane-1":
					waits := fake.DriverStep{Do: func(ports.SessionSpec) {
						close(back)
						<-release
					}}
					return script([]fake.DriverStep{{Bound: true}, waits, heard, {Hold: true}})
				}
				return finishes(r.laneRig)
			})
			r.start()
			r.awaitOrders(t, workorders.Parked)
			r.passTime(t, lanes.DefaultUsageBackoff+time.Second)
			r.eventually("the probe back at work", func(ledger.LaneView) bool {
				r.beat(t)
				select {
				case <-back:
					return true
				default:
					return false
				}
			})
			// Every pass parks what it finds at a limit, and the lane sent to find out is not that.
			for range 5 {
				r.beat(t)
			}
			if got := r.order(t, "wo").State; got != workorders.Running {
				close(release)
				t.Fatalf("the probe reads %s before it heard anything", got)
			}
			close(release)
			r.eventually("the other lane's end", func(ledger.LaneView) bool {
				r.beat(t)
				return r.order(t, "wo-2").State == workorders.Acceptance
			})
			if got := r.order(t, "wo").State; got != workorders.Running {
				t.Errorf("the probe reads %s", got)
			}
		})
	}
}

// refusedSignal is a journal that cannot take the first word on a sign-in's end.
type refusedSignal struct {
	ports.Journal
	refused atomic.Bool
}

func (j *refusedSignal) SaveSignIn(ctx context.Context, state lanes.SignInState, events ...ledger.UploadEvent) error {
	if len(events) != 0 && j.refused.CompareAndSwap(false, true) {
		return errors.New("the disk is full")
	}
	return j.Journal.SaveSignIn(ctx, state, events...)
}

// TestAWriteThatFailedEndsNoLimit keeps memory behind the journal: a worker that thought a limit was
// over, with no word of that written, would leave every other lane waiting for good.
func TestAWriteThatFailedEndsNoLimit(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &refusedSignal{Journal: base.journal}
	base.workerJournal = journal
	var r *limitRig
	r = fleetOn(t, base, 2, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		switch {
		case spec.Generation == 1 && spec.Lane == "lane-1":
			return r.limitOnceAllStart()
		case spec.Generation == 1:
			return atRest()
		}
		return finishes(r.laneRig)
	})
	r.start()
	r.awaitOrders(t, workorders.Parked)
	r.passTime(t, lanes.DefaultUsageBackoff+time.Second)
	r.awaitOrders(t, workorders.Acceptance)
	if !journal.refused.Load() {
		t.Error("the journal was never asked")
	}
}

// TestAProbeThatEndsReleasesTheOthers: nothing else would ever wake them.
func TestAProbeThatEndsReleasesTheOthers(t *testing.T) {
	t.Parallel()
	var r *limitRig
	r = fleetOf(t, 2, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		switch {
		case spec.Generation == 1 && spec.Lane == "lane-1":
			return r.limitOnceAllStart()
		case spec.Generation == 1:
			return atRest()
		case spec.Lane == "lane-1":
			return script([]fake.DriverStep{{Bound: true}, {Crash: true}})
		}
		return finishes(r.laneRig)
	})
	r.start()
	r.awaitOrders(t, workorders.Parked)
	r.passTime(t, lanes.DefaultUsageBackoff+time.Second)
	r.eventually("the other lane's end", func(ledger.LaneView) bool {
		r.beat(t)
		return r.order(t, "wo-2").State == workorders.Acceptance
	})
}

func TestALimitHoldsANewLane(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := startedFleet(t, 1, *rejected(reset))
	r.awaitOrders(t, workorders.Parked)
	r.secondLane("wo-2", "lane-2")
	for range 3 {
		r.beat(t)
	}
	if got := len(r.driver.Specs()); got != 1 {
		t.Fatalf("%d sessions at a limit", got)
	}
	notes := r.heldNotes()
	if !slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, lanes.HeldUsageLimit) }) {
		t.Errorf("held notes %v", notes)
	}
}

func TestNearTheLimitHoldsANewLaneAndNotAWokenOne(t *testing.T) {
	t.Parallel()
	warning := fake.DriverStep{Limit: &lanes.UsageReading{Status: lanes.UsageWarning}}
	var r *limitRig
	seen := make(chan struct{})
	r = fleetOf(t, 1, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		if spec.Generation > 1 {
			return finishes(r.laneRig)
		}
		return script([]fake.DriverStep{{Bound: true}, warning, {Do: func(ports.SessionSpec) { close(seen) }}, {Hold: true}})
	})
	r.start()
	<-seen
	r.eventually("the reading kept", func(ledger.LaneView) bool {
		state, err := r.journal.SignIn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if state.Last.Status != lanes.UsageWarning {
			return false
		}
		r.secondLaneOnce(t)
		r.beat(t)
		notes := r.heldNotes()
		return slices.ContainsFunc(notes, func(note string) bool { return strings.Contains(note, lanes.HeldNearLimit) })
	})
	if got := len(r.driver.Specs()); got != 1 {
		t.Fatalf("%d sessions near a limit", got)
	}
	r.orders = []string{"wo"}
	request := lanes.ParkRequest{Trigger: lanes.ParkFleetDrain, Expr: lanes.HumanWake(), SkipNarrative: true}
	if err := r.subject.Park(t.Context(), "lane-1", request); err != nil {
		t.Fatal(err)
	}
	r.awaitOrders(t, workorders.Parked)
	r.subject.Wait()
	r.wake2(t)
	r.awaitOrders(t, workorders.Acceptance)
}

func (r *limitRig) secondLaneOnce(t *testing.T) {
	t.Helper()
	if len(r.orders) == 1 {
		r.secondLane("wo-2", "lane-2")
		r.orders = append(r.orders, "wo-2")
	}
}

func (r *limitRig) wake2(t *testing.T) {
	t.Helper()
	if _, err := control.NewWakes(r.store).ResumeLane(t.Context(), scope, "lane-1", member); err != nil {
		t.Fatal(err)
	}
}

// TestAnExpiredSignInParksUntilTheDoctorSaysSo holds a lane that has no budget and no sign-in, and
// the doctor is what ends that.
func TestAnExpiredSignInParksUntilTheDoctorSaysSo(t *testing.T) {
	t.Parallel()
	r := startedFleet(t, 2, fake.DriverStep{Retry: lanes.FailureSignIn, Attempt: 1})
	r.driver.SetSignInStatus(lanes.SignedOut)
	r.awaitOrders(t, workorders.Parked)
	for _, lane := range r.lanes {
		if wake := r.wake(t, lane, 1); wake.Expr != lanes.SignInWake(r.key()) {
			t.Errorf("lane %s waits on %s", lane, wake.Expr)
		}
	}
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("a lane with no sign-in was asked for something: %+v", sent)
	}
	before := r.driver.Doctors()
	r.passTime(t, time.Minute)
	r.beat(t)
	if r.driver.Doctors() == before {
		t.Error("nothing asked the doctor")
	}
	if got := r.order(t, "wo").State; got != workorders.Parked {
		t.Fatalf("a sign-in that is still out woke a lane to %s", got)
	}
	r.driver.SetSignInStatus(lanes.SignInUnknown)
	r.passTime(t, time.Minute)
	r.beat(t)
	if got := r.order(t, "wo").State; got != workorders.Parked {
		t.Fatalf("a doctor that could not tell woke a lane to %s", got)
	}
	r.driver.SetSignInStatus(lanes.SignedIn)
	r.passTime(t, time.Minute)
	r.awaitOrders(t, workorders.Acceptance)
}

// watchedParking stands a moment in each lane's parking write, and counts any word on the sign-in
// that lands inside one.
type watchedParking struct {
	ports.Journal
	reached  chan struct{}
	once     sync.Once
	inFlight atomic.Int64
	landed   atomic.Int64
}

func (j *watchedParking) BeginParking(ctx context.Context, lane string, parking lanes.ParkProgress, handoff lanes.HandoffProgress, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	j.inFlight.Add(1)
	defer j.inFlight.Add(-1)
	j.once.Do(func() { close(j.reached) })
	time.Sleep(50 * time.Millisecond)
	return j.Journal.BeginParking(ctx, lane, parking, handoff, event)
}

func (j *watchedParking) SaveSignIn(ctx context.Context, state lanes.SignInState, events ...ledger.UploadEvent) error {
	if j.inFlight.Load() != 0 {
		j.landed.Add(1)
	}
	return j.Journal.SaveSignIn(ctx, state, events...)
}

// TestNoWordOnASignInLandsInsideAParking is what keeps a wake ahead of the signal that ends a limit:
// a signal counts only for wakes stored before it, so a wake journaled after one would wait for
// good. Nothing else on a sign-in can land between what a lane read and what it journaled.
func TestNoWordOnASignInLandsInsideAParking(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &watchedParking{Journal: base.journal, reached: make(chan struct{})}
	base.workerJournal = journal
	reset := time.Now().Add(time.Hour).UnixMilli()
	gate := make(chan struct{})
	r := fleetOn(t, base, 2, limitProfile, func(spec ports.SessionSpec) fake.DriverScript {
		if spec.Lane == "lane-1" {
			return script([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) { <-gate }}, *rejected(reset), {Hold: true}})
		}
		later := fake.DriverStep{Do: func(ports.SessionSpec) {
			close(gate)
			<-journal.reached
		}}
		return script([]fake.DriverStep{{Bound: true}, later, {Limit: &lanes.UsageReading{Window: "seven_day"}}, {Hold: true}})
	})
	r.start()
	r.awaitOrders(t, workorders.Parked)
	if got := journal.landed.Load(); got != 0 {
		t.Errorf("%d words on the sign-in landed inside a parking", got)
	}
}

// TestARestartKeepsWhatASignInSaid holds a new lane on a limit an earlier process saw, because the
// journal is what a worker knows.
func TestARestartKeepsWhatASignInSaid(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := startedFleet(t, 1, *rejected(reset))
	r.awaitOrders(t, workorders.Parked)
	r.subject.Wait()
	r.stopWorker()
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: r.config})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.secondLane("wo-2", "lane-2")
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(r.driver.Specs()); got != 1 {
		t.Errorf("%d sessions after a restart at a limit", got)
	}
}

func TestAWorkerSaysNothingOfASignInToTheLedger(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(time.Hour).UnixMilli()
	r := startedFleet(t, 1, *rejected(reset))
	r.awaitOrders(t, workorders.Parked)
	r.passTime(t, time.Hour+time.Second)
	r.awaitOrders(t, workorders.Acceptance)
	for _, event := range r.events() {
		if event.Type != parking.EventSignal {
			continue
		}
		var signal parking.Signal
		if err := json.Unmarshal(event.Data, &signal); err != nil {
			t.Fatal(err)
		}
		if signal.Key != r.key() {
			t.Errorf("signal %+v", signal)
		}
	}
}
