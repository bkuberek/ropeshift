package worker_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
)

// moveAndLook moves the worker's clock by d and makes one pass, which reads the installed version when a
// version check is due, and waits for any probe the pass started.
func (r *laneRig) moveAndLook(d time.Duration) {
	r.t.Helper()
	r.clock.Advance(d)
	if _, err := r.subject.Reconcile(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	r.subject.Wait()
}

// degradedRig is a worker whose installed version fails a check, so every probe of it records a result with a
// check that did not pass. It has no lane, so a pass of it ends with its probe.
func degradedRig(t *testing.T) *laneRig {
	t.Helper()
	return newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.SetVersion("1.0.0", true) })
}

// TestAWorkerProbesADegradedResultAgainWhenItStarts is a restart on the version a probe left with a check
// that did not pass: the journal's result goes to the driver, and the worker probes the version once more, so a
// probe that was fixed takes effect.
func TestAWorkerProbesADegradedResultAgainWhenItStarts(t *testing.T) {
	t.Parallel()
	first := degradedRig(t)
	first.subject.Wait()
	if got := first.qualifications(); len(got) != 1 || !got[0].Degraded || got[0].AllPassed() {
		t.Fatalf("the first worker recorded %+v", got)
	}
	driver := fake.NewAgentDriver()
	second := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := second.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if q := driver.Qualified(); q.Version != "1.0.0" || !q.Degraded {
		t.Errorf("until its probe ends, a restarted worker hands its driver %+v, the last result", q)
	}
	second.Wait()
	if n := len(driver.Probes()); n != 1 {
		t.Fatalf("%d probes of a version the journal holds with a check that did not pass", n)
	}
	if q := driver.Qualified(); q.Version != "1.0.0" || q.Degraded || !q.AllPassed() {
		t.Errorf("after its probe the driver was handed %+v", q)
	}
}

// TestAWorkerProbesAFullPassOnlyOnce is a result with every check passed: it holds across a restart and
// across the backoff of a degraded version, and no later look probes it again.
func TestAWorkerProbesAFullPassOnlyOnce(t *testing.T) {
	t.Parallel()
	first := newLaneRig(t)
	first.subject.Wait()
	for range 3 {
		first.moveAndLook(25 * time.Hour)
	}
	if n := len(first.driver.Probes()); n != 1 {
		t.Fatalf("%d probes of a result with every check passed", n)
	}
	driver := fake.NewAgentDriver()
	second := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := second.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	if n := len(driver.Probes()); n != 0 {
		t.Errorf("%d probes on restart of a result with every check passed", n)
	}
}

// TestADegradedVersionIsProbedOnABackoffThatDoublesToItsCap is the wait between probes of one version whose result
// has a check that did not pass: an hour after the last probe, twice that after the next, and never more than a
// day. Each wait is read from the worker's clock, and the look that falls short of it starts no probe.
func TestADegradedVersionIsProbedOnABackoffThatDoublesToItsCap(t *testing.T) {
	t.Parallel()
	r := degradedRig(t)
	r.subject.Wait()
	probes := len(r.driver.Probes())
	if probes != 1 {
		t.Fatalf("%d probes at the start", probes)
	}
	for _, wait := range []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		r.moveAndLook(wait - 2*time.Minute)
		r.moveAndLook(90 * time.Second)
		if n := len(r.driver.Probes()); n != probes {
			t.Fatalf("a probe started %v after the last, before the %v wait ended", wait-30*time.Second, wait)
		}
		r.moveAndLook(time.Minute)
		probes++
		if n := len(r.driver.Probes()); n != probes {
			t.Fatalf("%d probes, want %d: none started once the %v wait had ended", n, probes, wait)
		}
	}
}

// TestAVersionChangeAndAFullPassEndTheBackoff is the reset of the backoff: a version that moves is probed at
// once, whatever the wait the last version had grown to, and a probe whose checks all pass leaves the version to
// hold.
func TestAVersionChangeAndAFullPassEndTheBackoff(t *testing.T) {
	t.Parallel()
	r := degradedRig(t)
	r.subject.Wait()
	for _, wait := range []time.Duration{time.Hour, 2 * time.Hour} {
		r.moveAndLook(wait + time.Minute)
	}
	probes := len(r.driver.Probes())
	if probes != 3 {
		t.Fatalf("%d probes before the version moved", probes)
	}
	r.driver.SetVersion("1.0.1", true)
	r.moveAndLook(time.Minute)
	if n := len(r.driver.Probes()); n != probes+1 {
		t.Fatalf("%d probes, want one for the version that moved", n)
	}
	// The new version starts its own backoff at an hour, and not at the 4 hours the old one had reached.
	r.moveAndLook(time.Hour + time.Minute)
	if n := len(r.driver.Probes()); n != probes+2 {
		t.Fatalf("%d probes, want the new version probed an hour after its first", n)
	}
	r.driver.SetVersion("1.0.1", false)
	r.moveAndLook(2*time.Hour + time.Minute)
	if n := len(r.driver.Probes()); n != probes+3 {
		t.Fatalf("%d probes, want the version probed at the end of its second wait", n)
	}
	if q := r.driver.Qualified(); !q.AllPassed() || q.Version != "1.0.1" {
		t.Fatalf("the driver was handed %+v", q)
	}
	for range 3 {
		r.moveAndLook(25 * time.Hour)
	}
	if n := len(r.driver.Probes()); n != probes+3 {
		t.Errorf("%d probes, want none after a full pass", n)
	}
}

// TestAProbeThatErrorsIsNotProbedInALoop is a version whose probe cannot run: a probe spends real sessions, so
// neither the minute-by-minute look at the version nor each lane's start probes it again before its wait ends.
func TestAProbeThatErrorsIsNotProbedInALoop(t *testing.T) {
	t.Parallel()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.FailProbe(errors.New("the CLI would not start")) }).dispatched()
	r.driver.Play(completedLane(r))
	r.subject.Wait()
	for range 58 {
		r.moveAndLook(time.Minute)
	}
	if n := len(r.driver.Probes()); n != 1 {
		t.Fatalf("%d probes of a version whose probe cannot run, in under an hour", n)
	}
	if n := len(r.driver.Specs()); n != 1 {
		t.Fatalf("%d sessions, want the lane's one start, which asked for no second probe", n)
	}
	r.moveAndLook(3 * time.Minute)
	if n := len(r.driver.Probes()); n != 2 {
		t.Errorf("%d probes, want the second once the hour ended", n)
	}
	for range 58 {
		r.moveAndLook(time.Minute)
	}
	if n := len(r.driver.Probes()); n != 2 {
		t.Errorf("%d probes, want the third no sooner than two hours after the second", n)
	}
}

// TestAProbeThatNamesNoResultIsNotProbedInALoop is a version whose probe keeps ending because the version moved
// under it: no result is recorded, and the same version is not probed on every look.
func TestAProbeThatNamesNoResultIsNotProbedInALoop(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	r.driver.FailProbe(ports.ErrVersionMoved)
	r.moveAndLook(time.Minute)
	for range 58 {
		r.moveAndLook(time.Minute)
	}
	if n := len(r.driver.Probes()); n != 2 {
		t.Fatalf("%d probes, want one for each version, and none on the looks after", n)
	}
	r.moveAndLook(3 * time.Minute)
	if n := len(r.driver.Probes()); n != 3 {
		t.Errorf("%d probes, want the next once the hour ended", n)
	}
}

// unkept is a journal that cannot keep a probe's result.
type unkept struct {
	ports.Journal
	saves atomic.Int64
}

func (j *unkept) SaveQualification(context.Context, lanes.Qualification, ...ledger.UploadEvent) error {
	j.saves.Add(1)
	return errors.New("the journal is full")
}

// TestAProbeWhoseResultCannotBeKeptIsNotProbedInALoop is a journal that refuses each result: the worker holds no
// result for the version, and it still does not probe it on every look.
func TestAProbeWhoseResultCannotBeKeptIsNotProbedInALoop(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &unkept{Journal: base.journal}
	base.workerJournal = journal
	r := newLaneRigFor(t, base)
	r.subject.Wait()
	for range 58 {
		r.moveAndLook(time.Minute)
	}
	if n := len(r.driver.Probes()); n != 1 || journal.saves.Load() != 1 {
		t.Fatalf("%d probes and %d writes of a result the journal refuses, in under an hour", n, journal.saves.Load())
	}
}

// TestTheLastResultStaysInForceWhileAVersionIsProbedAgain is a probe that runs for a version whose result is
// held: the driver keeps that result, a lane's view of its version reads it and names no check, and a lane that
// starts does not wait.
func TestTheLastResultStaysInForceWhileAVersionIsProbedAgain(t *testing.T) {
	t.Parallel()
	r := degradedRig(t).dispatched()
	r.subject.Wait()
	release := r.driver.HoldProbes()
	t.Cleanup(release)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.clock.Advance(time.Hour + time.Minute)
	view := r.atGate(g)
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Probes()) != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("%d probes, want the second to be running", len(r.driver.Probes()))
		}
		time.Sleep(time.Millisecond)
	}
	if q := r.driver.Qualified(); q.Version != "1.0.0" || !q.Degraded {
		t.Errorf("while the second probe runs, the driver holds %+v, the last result", q)
	}
	if view.Checking != nil {
		t.Errorf("a lane on a version with a held result names the check %+v", view.Checking)
	}
	if n := len(r.driver.Specs()); n != 1 {
		t.Errorf("%d sessions: the lane waited for the probe", n)
	}
	if got := r.qualifications(); len(got) != 1 {
		t.Errorf("a probe that has not ended recorded %+v", got)
	}
	release()
	r.eventually("the second probe's end", func(ledger.LaneView) bool { return len(r.qualifications()) == 2 })
}
