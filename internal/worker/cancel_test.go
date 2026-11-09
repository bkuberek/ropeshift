package worker_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

func (r *laneRig) cancelWork(t *testing.T) {
	t.Helper()
	r.eventuallyUp()
	if err := control.NewLeases(r.store).Cancel(t.Context(), scope, r.grant.WorkOrder, "the plan changed", member); err != nil {
		t.Fatal(err)
	}
}

func (r *laneRig) waitEnded(t *testing.T) {
	t.Helper()
	waited := make(chan struct{})
	go func() {
		r.subject.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(20 * time.Second):
		t.Fatal("a lane of canceled work is still running")
	}
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (r *laneRig) expectCanceled(t *testing.T) {
	t.Helper()
	view := r.view()
	if view.ActualState != ledger.StateEnded {
		t.Errorf("the ledger reads the lane as %s", view.ActualState)
	}
	last := view.Generations[len(view.Generations)-1]
	if last.EndReason != workorders.ReasonCanceled {
		t.Errorf("its last generation is %+v", last)
	}
	row := r.awaitOrder(t, workorders.Canceled)
	if row.ClaimID != "" {
		t.Errorf("row %+v", row)
	}
}

func TestACanceledLaneStopsThroughTheGuardAndTheLedgerHearsItEnd(t *testing.T) {
	tests := []struct {
		name string
		told func(*testing.T, *laneRig)
	}{
		{"a hint moves desired state first", func(t *testing.T, r *laneRig) {
			if err := r.subject.Sync(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := r.subject.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
		}},
		{"a beat says so first", func(t *testing.T, r *laneRig) {
			reply, err := r.subject.Beat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(reply.Fenced) != 1 {
				t.Fatalf("beat %+v", reply)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := laneRigWith(t, profileAt)
			r.worktrees().UseRemote("origin")
			wrote := make(chan struct{})
			r.driver.Play(script([]fake.DriverStep{
				{Bound: true}, r.dirtying(stopWorktree), r.committing(stopWorktree),
				{Do: func(ports.SessionSpec) { close(wrote) }}, {Hold: true},
			}))
			r.start()
			<-wrote
			fence := r.grant.Fence
			r.cancelWork(t)
			tt.told(t, r)
			r.waitEnded(t)
			r.expectCanceled(t)
			ref := lanes.SalvageRef("lane-1", fence)
			if refs := r.worktrees().Snapshots(); refs[ref] != stopWorktree {
				t.Errorf("snapshots %v", refs)
			}
			artifacts := r.view().Artifacts
			if len(artifacts) != 1 || artifacts[0].URI != ref {
				t.Errorf("the ledger holds no salvage: %+v", artifacts)
			}
		})
	}
}

// Setting a lane up can take minutes, and a cancel can land in them. A session that started after that
// would run on work nobody wants.
func TestALaneCanceledWhileItIsSetUpStartsNoSession(t *testing.T) {
	r := laneRigWith(t, profileAt+"[hooks]\non_lane_created = [\"./scripts/setup.sh\"]\n")
	reached, release := make(chan struct{}), make(chan struct{})
	r.config.Provisioner.Hooks.(*fake.HookRunner).Programs["./scripts/setup.sh"] = func(ports.HookCommand) ports.HookResult {
		close(reached)
		<-release
		return ports.HookResult{}
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.start()
	<-reached
	r.cancelWork(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	close(release)
	r.waitEnded(t)
	r.expectCanceled(t)
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Errorf("a session started on canceled work: %+v", specs)
	}
}

// slowAdoption takes a session over only once the worker asked it to stop, or after a moment, as an
// adoption still on its way to the session does when a stop overtakes it.
type slowAdoption struct {
	ports.AgentDriver
	asked chan struct{}
	once  sync.Once
}

func (d *slowAdoption) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	d.once.Do(func() { close(d.asked) })
	return d.AgentDriver.Stop(ctx, h, how)
}

func (d *slowAdoption) Adopt(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	select {
	case <-d.asked:
	case <-time.After(time.Second):
	}
	return d.AgentDriver.Adopt(ctx, h, from)
}

// A restart takes over a session whose claim desired state no longer names. The pass that adopts it
// also stops it, and a stop that reaches the driver before the adoption finds no session there: that
// failure must not be the lane's only stop, or the session runs on and the pass waits on it forever.
func TestALaneLostAsItsSessionIsAdoptedStops(t *testing.T) {
	w := newWorldRig(t)
	w.config.Driver = &slowAdoption{AgentDriver: w.driver, asked: make(chan struct{})}
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
	converged := make(chan error, 1)
	go func() { converged <- w.subject.Converge(t.Context()) }()
	select {
	case err := <-converged:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the pass still waits on a lane whose claim went")
	}
	if got := w.lane("lane-1"); got.State == lanes.StateRunning {
		t.Errorf("the lane reads %s", got.State)
	}
}

func TestACanceledLaneWaitingOnItsBreakerEnds(t *testing.T) {
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
	r.cancelWork(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitEnded(t)
	r.expectCanceled(t)
}

// A cancel can land between a lane's parked commit in the journal and that commit reaching the ledger.
// The ledger refuses the commit, the worker ends the lane instead, and the ledger hears that end.
func TestACanceledLaneThatParkedOnItsWayOutEnds(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}, completed(0.02))
	r.link.FailUpload.Store(true)
	r.park(t, humanPark())
	r.subject.Wait()
	if err := control.NewLeases(r.store).Cancel(t.Context(), scope, r.grant.WorkOrder, "the plan changed", member); err != nil {
		t.Fatal(err)
	}
	r.link.FailUpload.Store(false)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateEnded {
		t.Errorf("the journal reads %+v", record)
	}
	view, err := r.plane.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.ActualState != ledger.StateEnded {
		t.Errorf("the ledger reads the lane as %s", view.ActualState)
	}
}
