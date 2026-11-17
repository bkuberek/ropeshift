package worker_test

import (
	"errors"
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

// parkingOf is the lane's parking as the worker's journal holds it, nil when it never began.
func (r *laneRig) parkingOf(t *testing.T) *lanes.ParkProgress {
	t.Helper()
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	return record.Parking
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
	r.awaitParked(t)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v, want one only a person ends", wake)
	}
	if r.nudged() {
		t.Errorf("a planning lane waiting for its person was nudged: %+v", r.driver.Sent())
	}
}

// holdsFor moves the worker's clock a second at a time for a span, and fails as soon as the lane begins to
// park.
func (r *laneRig) holdsFor(t *testing.T, span time.Duration) {
	t.Helper()
	for moved := time.Duration(0); moved < span; moved += time.Second {
		r.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
		if parking := r.parkingOf(t); parking != nil {
			t.Fatalf("the lane began to park %v after its wait ran out: %+v", moved+time.Second, parking)
		}
	}
}

// The web pane reaches a lane's pane with no terminal attached to it, so what a member types there is what
// says they are at the pane. A planning lane whose person typed into the web pane while it waited keeps its
// session for another `ask_wait`, as one with a terminal attached does.
func TestTypingInTheWebPaneKeepsAPlanningLaneWaiting(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(*fake.AgentDriver) {})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	r.config.PaneInputs.Typed("lane-1", 1, r.clock.Now())
	r.clock.Advance(5*time.Minute + time.Second)
	r.holdsFor(t, time.Minute)
	r.waitsForItsPerson(t)
	if row := r.row(); row.State != workorders.Running {
		t.Fatalf("a planning lane its person typed to left its work %s", row.State)
	}
}

// Input in the web pane keeps a planning lane only for the wait it came in. Once a whole `ask_wait` passes
// with nothing typed and nobody attached, the lane parks on a wake only a person ends.
func TestAPlanningLaneParksOnceAWaitPassesWithNothingTypedInTheWebPane(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(*fake.AgentDriver) {})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	r.config.PaneInputs.Typed("lane-1", 1, r.clock.Now())
	r.clock.Advance(5*time.Minute + time.Second)
	r.holdsFor(t, 30*time.Second)

	r.awaitParked(t)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v, want one only a person ends", wake)
	}
	if _, doc := r.storedHandoff(t, 1); doc.Reason != string(lanes.ParkWaitingForPerson) {
		t.Errorf("the handoff says the lane parked for %q", doc.Reason)
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

// One read of the pane that fails as the wait runs out reads as nobody attached, as a pane a person left
// does. The lane reads the pane again a moment later before it parks, so a person still attached keeps
// the session.
func TestOneFailedReadOfThePaneDoesNotParkAPlanningLane(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(driver *fake.AgentDriver) {
		driver.SetAttached(true)
	})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)

	r.driver.FailObserve(errors.New("tmux: the read of the pane's clients timed out"))
	r.clock.Advance(5*time.Minute + time.Second)
	deadline := time.Now().Add(20 * time.Second)
	for !r.driver.ObserveFailed() {
		if time.Now().After(deadline) {
			t.Fatal("the wait never read the pane")
		}
		time.Sleep(time.Millisecond)
	}
	r.clock.Advance(time.Minute)
	time.Sleep(10 * superviseStep)
	if parking := r.parkingOf(t); parking != nil {
		t.Fatalf("one failed read parked a lane with its person attached: %+v", parking)
	}
	r.waitsForItsPerson(t)

	r.driver.SetAttached(false)
	r.awaitParked(t)
	r.subject.Wait()
}

// A lane parked for its person says so in the handoff it leaves, and a member's Resume wakes it into its
// next generation, which goes on with the plan.
func TestAPlanningLaneParkedForItsPersonSaysWhyAndWakesOnResume(t *testing.T) {
	t.Parallel()
	r := planningRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n", func(*fake.AgentDriver) {})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.01)}))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.02)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitsForItsPerson(t)
	r.awaitParked(t)
	r.subject.Wait()
	if _, doc := r.storedHandoff(t, 1); doc.Reason != string(lanes.ParkWaitingForPerson) {
		t.Errorf("the handoff says the lane parked for %q", doc.Reason)
	}

	r.wake(t)
	if woken := r.row(); woken.State != workorders.Ready || woken.ResumeLaneID != "lane-1" {
		t.Fatalf("a member's Resume left %+v", woken)
	}
	r.resumed(t)
	if specs := r.driver.Specs(); len(specs) != 2 || specs[1].Generation != 2 || !specs[1].Planning {
		t.Errorf("sessions %+v, want a second planning generation", specs)
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
