package worker_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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
	if got := first.qualifications(); len(got) != 1 || !got[0].Degraded || got[0].PassedAll(first.driver.ProbeChecks("")) {
		t.Fatalf("the first worker recorded %+v", got)
	}
	driver := fake.NewAgentDriver()
	first.clock.Advance(restartedAfter)
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
	if q := driver.Qualified(); q.Version != "1.0.0" || q.Degraded || !q.PassedAll(driver.ProbeChecks("")) {
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
	if q := r.driver.Qualified(); !q.PassedAll(r.driver.ProbeChecks("")) || q.Version != "1.0.1" {
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

// restarted is a worker on the journal of an earlier one, with a driver of its own whose probe fails a check, which
// starts at the clock's time.
func restarted(t *testing.T, earlier *laneRig) (*worker.Worker, *fake.AgentDriver) {
	t.Helper()
	driver := fake.NewAgentDriver()
	driver.SetVersion("1.0.0", true)
	subject := worker.New(earlier.link, earlier.journal, earlier.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	subject.Wait()
	return subject, driver
}

// TestARestartInsideTheDelayHoldsBackItsProbe is a worker that starts on a result a probe left with a check that did
// not pass, where the last probe began minutes before: it holds that probe back until ten minutes after the last
// began, and then probes the version.
func TestARestartInsideTheDelayHoldsBackItsProbe(t *testing.T) {
	t.Parallel()
	first := degradedRig(t)
	first.subject.Wait()
	first.clock.Advance(5 * time.Minute)
	second, driver := restarted(t, first)
	if n := len(driver.Probes()); n != 0 {
		t.Fatalf("%d probes at a start five minutes after the last probe began", n)
	}
	if q := driver.Qualified(); q.Version != "1.0.0" || !q.Degraded {
		t.Errorf("until its probe the driver holds %+v, the last result", q)
	}
	first.clock.Advance(4 * time.Minute)
	if _, err := second.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	if n := len(driver.Probes()); n != 0 {
		t.Fatalf("%d probes nine minutes after the last probe began", n)
	}
	first.clock.Advance(90 * time.Second)
	if _, err := second.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	if n := len(driver.Probes()); n != 1 {
		t.Errorf("%d probes once ten minutes had passed since the last began", n)
	}
}

// TestARestartStormProbesOnce is a worker that keeps restarting on a result with a check that did not pass: each
// probe spends real sessions, so only the first start probes, and the restarts a minute apart inside the delay do not.
func TestARestartStormProbesOnce(t *testing.T) {
	t.Parallel()
	first := degradedRig(t)
	first.subject.Wait()
	total := len(first.driver.Probes())
	for range 8 {
		first.clock.Advance(time.Minute)
		_, driver := restarted(t, first)
		total += len(driver.Probes())
	}
	if total != 1 {
		t.Errorf("%d probes across a start and eight restarts a minute apart", total)
	}
}

// TestAProbeIsJournaledBeforeItsSessionsStart is the record a restart reads: it is in the journal by the time the
// probe's first session could start.
func TestAProbeIsJournaledBeforeItsSessionsStart(t *testing.T) {
	t.Parallel()
	var release func()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) {
		driver.SetVersion("1.0.0", true)
		release = driver.HoldProbes()
	})
	t.Cleanup(release)
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Probes()) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("the probe never started")
		}
		time.Sleep(time.Millisecond)
	}
	got, err := r.journal.ProbeAttempt(t.Context())
	if err != nil || got.Version != "1.0.0" || !got.BeganAt.Equal(r.clock.Now()) {
		t.Errorf("while the probe runs the journal reads the attempt %+v, %v, want 1.0.0 at %v", got, err, r.clock.Now())
	}
}

// unrecorded is a journal that cannot keep that a probe began.
type unrecorded struct {
	ports.Journal
	saves atomic.Int64
}

func (j *unrecorded) SaveProbeAttempt(context.Context, lanes.ProbeAttempt) error {
	j.saves.Add(1)
	return errors.New("the journal is full")
}

// TestAProbeThatCannotBeJournaledStartsNoSession is a journal that refuses the record of an attempt: a probe that a
// restart could not hold back is not started, and the worker tries again only when the wait ends.
func TestAProbeThatCannotBeJournaledStartsNoSession(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &unrecorded{Journal: base.journal}
	base.workerJournal = journal
	r := newLaneRigFor(t, base)
	r.subject.Wait()
	for range 58 {
		r.moveAndLook(time.Minute)
	}
	if n := len(r.driver.Probes()); n != 0 || journal.saves.Load() != 1 {
		t.Fatalf("%d probes and %d records of an attempt the journal refuses, in under an hour", n, journal.saves.Load())
	}
	r.moveAndLook(3 * time.Minute)
	if n := len(r.driver.Probes()); n != 0 || journal.saves.Load() != 2 {
		t.Errorf("%d probes and %d records once the hour ended", n, journal.saves.Load())
	}
}

// restartedAfter is how long a restart waits after the last worker began a probe: past the delay a start holds
// back its probe for.
const restartedAfter = 11 * time.Minute

// TestAResultThatLacksACheckAProbeRunsNowIsProbedAgain is a full pass stored by a probe that ran fewer checks, as
// one from an older build, or on another sign-in, or with no terminal, does: the missing check was never observed, so
// the worker that starts on it probes the version again, and the result it keeps holds.
func TestAResultThatLacksACheckAProbeRunsNowIsProbedAgain(t *testing.T) {
	t.Parallel()
	first := newLaneRig(t)
	first.subject.Wait()
	if got := first.qualifications(); len(got) != 1 || !got[0].PassedAll(first.driver.ProbeChecks("")) {
		t.Fatalf("the first worker recorded %+v", got)
	}
	const later lanes.Surface = "a_check_a_later_probe_runs"
	driver := fake.NewAgentDriver()
	driver.SetProbeChecks(fake.SurfacePeerChannel, later)
	first.clock.Advance(restartedAfter)
	second := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := second.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	if n := len(driver.Probes()); n != 1 {
		t.Fatalf("%d probes of a version whose result lacks a check a probe runs now", n)
	}
	if q := driver.Qualified(); !q.Passed(later) || !q.PassedAll(driver.ProbeChecks("")) {
		t.Errorf("after its probe the driver was handed %+v", q)
	}
	third := fake.NewAgentDriver()
	third.SetProbeChecks(fake.SurfacePeerChannel, later)
	again := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: third}})
	first.clock.Advance(restartedAfter)
	if err := again.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	again.Wait()
	if n := len(third.Probes()); n != 0 {
		t.Errorf("%d probes of a result that has every check a probe runs now", n)
	}
}

// TestAProbeThatCannotRunKeepsTheResultInForce is a probe of a version whose result is held that ends in an error, as
// a timeout or a CLI that would not start does: that result stays in force, in the driver and in the journal, no new
// result is recorded, and only the wait before the next probe starts.
func TestAProbeThatCannotRunKeepsTheResultInForce(t *testing.T) {
	t.Parallel()
	r := degradedRig(t)
	r.subject.Wait()
	held := r.driver.Qualified()
	if held.Version != "1.0.0" || held.Checks[fake.SurfacePeerChannel] != lanes.CheckFailed {
		t.Fatalf("the first probe left the driver %+v", held)
	}
	r.driver.FailProbe(errors.New("the CLI would not start"))
	r.moveAndLook(time.Hour + time.Minute)
	if n := len(r.driver.Probes()); n != 2 {
		t.Fatalf("%d probes, want the second once the hour ended", n)
	}
	if q := r.driver.Qualified(); !reflect.DeepEqual(q, held) {
		t.Errorf("a probe that could not run left the driver %+v, want %+v", q, held)
	}
	stored, err := r.journal.Qualification(t.Context())
	if err != nil || !reflect.DeepEqual(stored, held) {
		t.Errorf("the journal holds %+v, %v, want %+v", stored, err, held)
	}
	if got := r.qualifications(); len(got) != 1 {
		t.Errorf("a probe that could not run recorded %+v", got)
	}
	if problems := r.problems(); len(problems) != 1 || !strings.Contains(problems[0].Error(), "could not run") {
		t.Errorf("the worker said %v", problems)
	}
	for range 118 {
		r.moveAndLook(time.Minute)
	}
	if n := len(r.driver.Probes()); n != 2 {
		t.Errorf("%d probes inside the two hours the second wait lasts", n)
	}
}

// login stands for a check a probe runs on every sign-in but an API key.
const login lanes.Surface = "login"

// TestAnUnreadSignInKeepsTheResultInForce is a worker on an API key that starts after a read of its sign-in failed: the
// kind is unread, which a probe would take for a subscription and ask the login of, so the worker goes on with the
// kind the held result was probed on, probes nothing, and leaves that result as it was.
func TestAnUnreadSignInKeepsTheResultInForce(t *testing.T) {
	t.Parallel()
	first := newLaneRigOn(t, func(driver *fake.AgentDriver) { driver.SetSubscriptionChecks(login) })
	first.subject.Wait()
	held := first.driver.Qualified()
	if held.SignIn != ports.SignInAPIKey || !held.PassedAll(first.driver.ProbeChecks(ports.SignInAPIKey)) {
		t.Fatalf("the first probe left the driver %+v", held)
	}
	driver := fake.NewAgentDriver()
	driver.SetSubscriptionChecks(login)
	driver.SetSignIn("")
	first.clock.Advance(restartedAfter)
	second := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := second.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	for range 3 {
		first.clock.Advance(25 * time.Hour)
		if _, err := second.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		second.Wait()
	}
	if n := len(driver.Probes()); n != 0 {
		t.Fatalf("%d probes of a result with every check passed, because the sign-in was unread", n)
	}
	if q := driver.Qualified(); !reflect.DeepEqual(q, held) {
		t.Errorf("the driver holds %+v, want %+v", q, held)
	}
	if got := first.qualifications(); len(got) != 1 {
		t.Errorf("recorded %+v", got)
	}
}

// TestAProbeRunsOnTheKindTheHeldResultWasProbedOnWhenTheSignInIsUnread is a probe that falls due while the sign-in
// reads unread: it runs for the sign-in the result in force was probed on, and not as a subscription.
func TestAProbeRunsOnTheKindTheHeldResultWasProbedOnWhenTheSignInIsUnread(t *testing.T) {
	t.Parallel()
	first := newLaneRigOn(t, func(driver *fake.AgentDriver) {
		driver.SetVersion("1.0.0", true)
		driver.SetSubscriptionChecks(login)
	})
	first.subject.Wait()
	driver := fake.NewAgentDriver()
	driver.SetSubscriptionChecks(login)
	driver.SetSignIn("")
	first.clock.Advance(restartedAfter)
	second := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := second.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	probes := driver.Probes()
	if len(probes) != 1 || probes[0].SignIn != ports.SignInAPIKey {
		t.Fatalf("probes %+v, want one on the sign-in %q", probes, ports.SignInAPIKey)
	}
	if q := driver.Qualified(); q.SignIn != ports.SignInAPIKey || !q.PassedAll(driver.ProbeChecks(ports.SignInAPIKey)) {
		t.Errorf("the probe left the driver %+v", q)
	}
}

// TestAProbeKeepsItsEvidenceInTheWorkersStateDirectory names where a probe leaves what it saw of a check: the
// worker's own state directory, beside the probe's working directory and not within it, which the probe clears.
func TestAProbeKeepsItsEvidenceInTheWorkersStateDirectory(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.subject.Wait()
	probes := r.driver.Probes()
	if len(probes) != 1 {
		t.Fatalf("%d probes", len(probes))
	}
	want := filepath.Join(r.config.StateDir, "probe-evidence")
	if got := probes[0].EvidenceDir; got != want {
		t.Errorf("the probe's evidence directory is %q, want %q", got, want)
	}
	if probes[0].EvidenceDir == probes[0].Dir || filepath.Dir(probes[0].EvidenceDir) != filepath.Dir(probes[0].Dir) {
		t.Errorf("the evidence directory %q is not beside the probe's directory %q", probes[0].EvidenceDir, probes[0].Dir)
	}
}
