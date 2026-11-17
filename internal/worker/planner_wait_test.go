package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// planningRigWith is a planning lane rig with a repository layer, dispatched.
func planningRigWith(t *testing.T, profile string, setup func(*fake.AgentDriver)) *laneRig {
	t.Helper()
	r := newPlanningRig(t, setup)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", profile)
	return r.dispatched()
}

// waitsForItsPerson reads until the lane waits at rest for its person, and fails at once on a lane that
// stopped instead.
func (r *laneRig) waitsForItsPerson(t *testing.T) {
	t.Helper()
	stopped := func() bool {
		state := r.row().State
		return state == workorders.Blocked || state == workorders.Acceptance
	}
	r.eventually("a planning lane waiting for its person", func(view ledger.LaneView) bool {
		return stopped() || (view.ActualState == string(lanes.Idle) && slices.Contains(view.Conditions, string(lanes.WaitingForPerson)))
	})
	if stopped() {
		t.Fatalf("the lane stopped with %+v instead of waiting for its person", r.outcome())
	}
}

// awaitParked reads until the work order parks, moving the worker's clock a second at a time, so every wait
// on that clock ends however many reads it takes.
func (r *laneRig) awaitParked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if r.row().State == workorders.Parked {
			return
		}
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("work order %s, want parked", r.row().State)
}

// nudged says the worker sent the lane its nudge.
func (r *laneRig) nudged() bool {
	return slices.ContainsFunc(r.driver.Sent(), func(sent ports.Input) bool { return sent.Text == lanes.Nudge })
}

// A planning lane is a conversation with the person attached to its pane, so a turn that ends with no
// report and no question waits for the person's next line, however long they take: the worker sends no
// nudge and records no outcome, and the lane stays at rest with its session (ADR-0018). A line the person
// types starts the next turn, and the rest after it waits again.
func TestAPlanningLaneWaitsForThePersonAttachedToItsPane(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\n", func(driver *fake.AgentDriver) { driver.SetAttached(true) })
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.03)},
	))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	r.clock.Advance(time.Hour)
	time.Sleep(10 * superviseStep)
	r.waitsForItsPerson(t)
	if row := r.row(); row.State != workorders.Running {
		t.Fatalf("a planning lane waiting with its person attached left its work %s", row.State)
	}

	if err := r.driver.Type("lane-1", 1, "Keep the admin console for staff."); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	if err := r.driver.Type("lane-1", 1, "That is the plan. Report it."); err != nil {
		t.Fatal(err)
	}
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if r.nudged() {
		t.Errorf("a planning lane waiting for its person was nudged: %+v", r.driver.Sent())
	}
}

// A planning lane waiting for its person holds its session for `ask_wait`, as a lane that asked does, and
// a person attached when that runs out keeps it for another. Once nobody is attached as it runs out, the
// lane parks on a wake only a person ends, which gives its slot to other work and puts it in Needs you
// for a member to resume.
func TestAPlanningLaneParksWhenNobodyIsAttachedAsItsWaitRunsOut(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(driver *fake.AgentDriver) {
		driver.SetAttached(true)
	})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	r.clock.Advance(5*time.Minute + time.Second)
	time.Sleep(10 * superviseStep)
	r.waitsForItsPerson(t)

	r.driver.SetAttached(false)
	r.clock.Advance(4*time.Minute + 50*time.Second)
	time.Sleep(10 * superviseStep)
	r.waitsForItsPerson(t)
	r.clock.Advance(20 * time.Second)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v, want one only a person ends", wake)
	}
	if r.nudged() {
		t.Errorf("a planning lane waiting for its person was nudged: %+v", r.driver.Sent())
	}
}

// A worker that restarts while a planning lane waits for its person takes the wait up again from the
// session it adopts, so the lane still parks once nobody has been attached for `ask_wait`.
func TestAPlanningLaneWaitsOnForItsPersonAfterAWorkerRestart(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(*fake.AgentDriver) {})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	r.start()
	r.waitsForItsPerson(t)
	r.clock.Advance(4 * time.Minute)

	r.takeOver(t, fake.DriverScript{SessionID: "session-1", Model: "resolved-model", Silent: true})
	r.waitsForItsPerson(t)
	r.awaitParked(t)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v, want one only a person ends", wake)
	}
}

// Every lane that is no plan keeps the nudge and then the outcome `no_outcome`, whoever is attached to its
// pane.
func TestALaneThatIsNoPlanIsNudgedAndBlockedWithAPersonAttached(t *testing.T) {
	t.Parallel()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.SetAttached(true) })
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
	))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if change := r.outcome(); r.row().State != workorders.Blocked || change.Reason != workorders.ReasonNoOutcome {
		t.Errorf("outcome %+v", change)
	}
	if sent := r.driver.Sent(); len(sent) != 1 || sent[0].Text != lanes.Nudge {
		t.Errorf("sent %+v, want the one nudge", sent)
	}
}
