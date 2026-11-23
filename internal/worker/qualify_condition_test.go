package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A probe adds degraded mode to a lane whose process runs on the version it checked, and the lane keeps that while its
// process runs. A later probe of another version, after the CLI updated under the worker, replaces the result the worker
// holds, and the lane's process is unchanged, so its condition stays. Only a later result for the lane's own version, as a
// probe of it after the CLI went back, clears it.
func TestAProbeOfAnotherVersionLeavesARunningLanesCondition(t *testing.T) {
	t.Parallel()
	var release func()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) {
		driver.SetVersion("1.0.0", false)
		release = driver.HoldProbes()
	})
	t.Cleanup(release)
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.driver.SetVersion("1.0.0", true)
	release()
	r.eventually("the probe's result", func(view ledger.LaneView) bool {
		return view.Checking == nil && slices.Contains(view.Conditions, string(lanes.DegradedMode))
	})

	probed := func(version string) {
		t.Helper()
		r.clock.Advance(25 * time.Hour)
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		r.eventually("the probe of "+version, func(ledger.LaneView) bool {
			results := r.qualifications()
			return results[len(results)-1].Version == version && len(r.driver.Probes()) == len(results)
		})
	}
	r.driver.SetVersion("1.0.1", false)
	probed("1.0.1")
	if view := r.read(); !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a probe of another version cleared the condition of a lane whose process is unchanged: %v", view.Conditions)
	}

	r.driver.SetVersion("1.0.0", false)
	probed("1.0.0")
	if view := r.read(); slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a later result for the lane's own version, with every check passed, left its condition: %v", view.Conditions)
	}
}
