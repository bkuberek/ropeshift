package worker_test

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// newPlanningRig is a lane rig whose work order is a plan.
func newPlanningRig(t *testing.T, setup func(*fake.AgentDriver)) *laneRig {
	t.Helper()
	base := newRig(t)
	base.kind = workorders.KindPlan
	return newLaneRigWith(t, base, setup)
}

// noPane is a driver that offers no interactive run mode, as one does on a machine with no tmux.
type noPane struct {
	*fake.AgentDriver
}

func (d noPane) Capabilities(ctx context.Context) (ports.Capabilities, error) {
	capabilities, err := d.AgentDriver.Capabilities(ctx)
	capabilities.RunModes = slices.DeleteFunc(slices.Clone(capabilities.RunModes), func(mode string) bool { return mode == ports.RunModeInteractive })
	return capabilities, err
}

// A planning lane is a planner for the thresholds it is held to and for the record of what it ran on.
// The worker reads that from the kind of the work order its lease names, and the session says nothing.
func TestAPlanningLaneRunsAsAPlanner(t *testing.T) {
	r := newPlanningRig(t, func(*fake.AgentDriver) {})
	r.config.Telemetry.Endpoint = "http://127.0.0.1:7318"
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	if role := specs[0].Telemetry.Tags.Role; role != lanes.RolePlanner {
		t.Errorf("telemetry role %q, want %q", role, lanes.RolePlanner)
	}
	want := measures.ContextSettings{Role: lanes.RolePlanner, WarnTokens: 120000, CompactTokens: 150000, HandoffTokens: 180000}
	if got := r.experience("lane-1").Settings.Context; got != want {
		t.Errorf("context settings %+v, want %+v", got, want)
	}
}

// A planning lane runs where a person can attach, whatever the sign-in and the profile say, because a
// person is present by construction (ADR-0018).
func TestAPlanningLaneAlwaysRunsInAPane(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		profile string
	}{
		{"an API key and no word from the profile", ports.SignInAPIKey, ""},
		{"an API key and a profile that says headless", ports.SignInAPIKey, "run_mode = \"headless\"\n"},
		{"a subscription and a profile that says headless", ports.SignInSubscription, "run_mode = \"headless\"\n"},
		{"a subscription and no word from the profile", ports.SignInSubscription, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newPlanningRig(t, func(driver *fake.AgentDriver) { driver.SetSignIn(tt.kind) })
			if tt.profile != "" {
				r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", tt.profile)
			}
			r.dispatched()
			r.runWith()
			if specs := r.driver.Specs(); len(specs) != 1 || specs[0].RunMode != ports.RunModeInteractive {
				t.Errorf("%d sessions, the first in %q", len(specs), firstMode(specs))
			}
		})
	}
}

// Any other lane keeps what its profile and sign-in say, so the planner's rule does not leak.
func TestALaneThatIsNoPlanKeepsItsRunMode(t *testing.T) {
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.SetSignIn(ports.SignInAPIKey) })
	r.dispatched()
	r.runWith()
	if specs := r.driver.Specs(); len(specs) != 1 || specs[0].RunMode != ports.RunModeHeadless {
		t.Errorf("%d sessions, the first in %q", len(specs), firstMode(specs))
	}
}

// With no way to run in a pane, a planning lane does not start, and it never falls back to headless.
// The work order stops in `blocked` with a reason a person can read, and nothing is retried.
func TestAPlanningLaneWithNoPaneDoesNotStart(t *testing.T) {
	r := newPlanningRig(t, func(*fake.AgentDriver) {})
	r.config.Driver = noPane{r.driver}
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", "run_mode = \"headless\"\n")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Fatalf("a session started: %+v", specs)
	}
	change := r.outcome()
	if change.Outcome != workorders.OutcomeBlocked || change.Reason != workorders.ReasonNoInteractiveMode {
		t.Errorf("outcome %+v", change)
	}
	if row := r.row(); row.State != workorders.Blocked {
		t.Errorf("row %+v", row)
	}
}

// A lane that is no plan is not held to the pane, so a machine with no tmux still runs its headless work.
func TestAWriterWithNoPaneStillRunsHeadless(t *testing.T) {
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.SetSignIn(ports.SignInAPIKey) })
	r.config.Driver = noPane{r.driver}
	r.dispatched()
	r.runWith()
	if specs := r.driver.Specs(); len(specs) != 1 || specs[0].RunMode != ports.RunModeHeadless {
		t.Errorf("%d sessions, the first in %q", len(specs), firstMode(specs))
	}
}
