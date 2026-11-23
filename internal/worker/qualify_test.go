package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// qualifications lists what the ledger recorded of this worker's probes, in order.
func (r *laneRig) qualifications() []lanes.Qualification {
	r.t.Helper()
	if err := r.subject.Upload(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	var out []lanes.Qualification
	for _, event := range r.events() {
		if event.Type != ledger.EventDriverQualified {
			continue
		}
		var q lanes.Qualification
		if err := json.Unmarshal(event.Data, &q); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, q)
	}
	return out
}

func completedLane(r *laneRig) fake.DriverScript {
	return script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)})
}

// TestAWorkerProbesTheInstalledVersionWhenItStarts is FR-DRV-2 with no lane: a worker that starts on a
// version it has no probe's result for probes it at once, and keeps the result, before any lane asks.
func TestAWorkerProbesTheInstalledVersionWhenItStarts(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.subject.Wait()
	if n := len(r.driver.Probes()); n != 1 {
		t.Fatalf("%d probes of the version the worker started on", n)
	}
	if n := len(r.driver.Specs()); n != 0 {
		t.Errorf("%d sessions on a worker no lane reached", n)
	}
	got := r.qualifications()
	if len(got) != 1 || got[0].Version != "1.0.0" || got[0].Source != lanes.QualifiedByProbe {
		t.Errorf("recorded %+v", got)
	}
	if q := r.driver.Qualified(); q.Version != "1.0.0" {
		t.Errorf("the driver was handed %+v", q)
	}
}

// TestAWorkerProbesAVersionThatChangesUnderIt is the worker's own look at the installed version: a CLI
// that updated itself is probed on the next look, with no lane, and the worker looks once in each
// interval and not on every pass.
func TestAWorkerProbesAVersionThatChangesUnderIt(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if n := len(r.driver.Probes()); n != 1 {
		t.Fatalf("%d probes before the worker looked again", n)
	}
	r.clock.Advance(worker.VersionCheckEvery)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	got := r.qualifications()
	if len(got) != 2 || got[1].Version != "1.0.1" {
		t.Fatalf("recorded %+v", got)
	}
	if n := len(r.driver.Specs()); n != 0 {
		t.Errorf("%d sessions on a worker no lane reached", n)
	}
}

// TestAProbeThatEndsWhereTheVersionMovedLeavesTheDriverWhatTheWorkerHolds is a probe whose version changed
// while it ran, so it names no result: the driver is handed what the worker holds, which is the last probe's
// result, and the worker says why the probe ended.
func TestAProbeThatEndsWhereTheVersionMovedLeavesTheDriverWhatTheWorkerHolds(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	r.driver.FailProbe(ports.ErrVersionMoved)
	r.clock.Advance(worker.VersionCheckEvery)
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if n := len(r.driver.Probes()); n != 2 {
		t.Fatalf("%d probes, want one for each version", n)
	}
	if q := r.driver.Qualified(); q.Version != "1.0.0" || q.Degraded {
		t.Errorf("after a probe that named no result, the driver was handed %+v", q)
	}
	if got := r.qualifications(); len(got) != 1 || got[0].Version != "1.0.0" {
		t.Errorf("a probe that named no result recorded %+v", got)
	}
	if problems := r.problems(); len(problems) != 1 || !errors.Is(problems[0], ports.ErrVersionMoved) {
		t.Errorf("the worker said %v", problems)
	}
}

// TestALaneStartsAtOnceOnDocumentedSurfacesWhileItsVersionIsProbed is a lane that starts while the probe of a
// version no probe's result names runs: it does not wait, it runs on documented surfaces, and not on what the
// last probe passed for another version, and every pass that comes meanwhile shares the one probe.
func TestALaneStartsAtOnceOnDocumentedSurfacesWhileItsVersionIsProbed(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	release := r.driver.HoldProbes()
	t.Cleanup(release)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	view := r.atGate(g)
	for range 2 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(r.driver.Probes()); n != 2 {
		t.Errorf("%d probes, want one for each version", n)
	}
	if !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a lane that started while its version is probed reads %v", view.Conditions)
	}
	if q := r.driver.Qualified(); q.Version != "1.0.0" {
		t.Errorf("while the probe runs, the driver was handed %+v, which is no probe's result for 1.0.1", q)
	}
	caps, err := r.driver.Capabilities(t.Context())
	if err != nil || !caps.Degraded || caps.Injection != ports.InjectionNone {
		t.Errorf("capabilities %+v, %v", caps, err)
	}
	if got := r.qualifications(); len(got) != 1 {
		t.Errorf("a probe that has not ended recorded %+v", got)
	}
}

// TestALaneStartsAtOnceWithNothingQualified is a lane that starts while a worker's first probe runs: it
// does not wait, and the driver's own answer for the version stands, so only what nothing has checked
// runs degraded.
func TestALaneStartsAtOnceWithNothingQualified(t *testing.T) {
	t.Parallel()
	var release func()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) {
		driver.SetVersion("1.0.0", true)
		release = driver.HoldProbes()
	})
	t.Cleanup(release)
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	view := r.atGate(g)
	if n := len(r.driver.Probes()); n != 1 {
		t.Errorf("%d probes", n)
	}
	if !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a lane on a version nothing has checked reads %v", view.Conditions)
	}
	if q := r.driver.Qualified(); q.Version != "" {
		t.Errorf("with nothing qualified, the driver was handed %+v", q)
	}
}

// TestALaneSaysWhichVersionItsWorkerChecks is a lane whose process runs on a version its worker is probing:
// it says so in words, it runs on documented surfaces meanwhile, and the probe's end moves the lane's status
// at once. A process keeps the surfaces it started with, so a probe that passes clears the check and leaves
// the condition, and a respawn is what starts on the checked surfaces.
func TestALaneSaysWhichVersionItsWorkerChecks(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	release := r.driver.HoldProbes()
	t.Cleanup(release)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	view := r.eventually("the check named", func(view ledger.LaneView) bool { return view.Checking != nil })
	if c := view.Checking; c.Driver != fake.FakeDriverName || c.Version != "1.0.1" || c.Words != ledger.CheckingCLI(fake.FakeDriverName, "1.0.1", "").Words {
		t.Errorf("the lane names %+v", c)
	}
	if !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a lane on documented surfaces reads %v", view.Conditions)
	}
	release()
	view = r.eventually("the check ended", func(view ledger.LaneView) bool { return view.Checking == nil })
	if got := r.qualifications(); len(got) != 2 || got[1].Version != "1.0.1" || got[1].Degraded {
		t.Fatalf("the probe recorded %+v", got)
	}
	if !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a process that started on documented surfaces reads %v once its probe passed", view.Conditions)
	}
}

// TestALaneOnTheShippedMatrixNamesThoseSurfacesWhileItsWorkerChecks is a lane that starts while a worker with
// no probe history probes its version, on a version the driver answers for from the shipped matrix: the process
// runs on the surfaces the matrix qualified, so the check says that and not documented surfaces, and the lane is
// not degraded.
func TestALaneOnTheShippedMatrixNamesThoseSurfacesWhileItsWorkerChecks(t *testing.T) {
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
	view := r.eventually("the check named", func(view ledger.LaneView) bool { return view.Checking != nil })
	const want = "Checking Claude Code 1.0.0: this lane runs on the surfaces this release qualified for 1.0.0; a respawn after the check picks up its result"
	if c := view.Checking; c.Driver != fake.FakeDriverName || c.Version != "1.0.0" || strings.Contains(c.Words, "documented") || c.Words != want {
		t.Errorf("the lane names %+v, want words %q", c, want)
	}
	if slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("a lane on the shipped matrix's surfaces reads %v", view.Conditions)
	}
}

// TestAProbeThatFailsAddsDegradedModeToARunningLane is a lane that started on what the driver answered for its
// version, whose probe then found a surface failed: the lane reads degraded from the probe's end.
func TestAProbeThatFailsAddsDegradedModeToARunningLane(t *testing.T) {
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
	if view := r.atGate(g); slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Fatalf("a lane on a version the driver answers for reads %v", view.Conditions)
	}
	r.driver.SetVersion("1.0.0", true)
	release()
	r.eventually("the probe's result", func(view ledger.LaneView) bool {
		return view.Checking == nil && slices.Contains(view.Conditions, string(lanes.DegradedMode))
	})
}

// TestAMatrixLaneWhoseProbeFailsSaysItRunsOnTheMatrixSurfaces is a lane that started on the surfaces the shipped
// matrix qualified, whose probe then found a surface failed: it reads degraded, and its note says it runs on the
// surfaces the release qualified, because that is what its process started with.
func TestAMatrixLaneWhoseProbeFailsSaysItRunsOnTheMatrixSurfaces(t *testing.T) {
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
	if view := r.atGate(g); slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Fatalf("a lane on the shipped matrix's surfaces reads %v", view.Conditions)
	}
	r.driver.SetVersion("1.0.0", true)
	release()
	view := r.eventually("the probe's result", func(view ledger.LaneView) bool {
		return view.Checking == nil && slices.Contains(view.Conditions, string(lanes.DegradedMode))
	})
	const want = "Runs on the surfaces this release qualified for its version until it restarts on a qualified version."
	if got := view.ConditionNotes[string(lanes.DegradedMode)]; got != want {
		t.Errorf("the degraded lane says %q, want %q", got, want)
	}
}

// A lane on documented surfaces keeps the sentence it always had, once its probe passes.
func TestADocumentedLaneKeepsItsDegradedNoteAfterItsProbePasses(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	release := r.driver.HoldProbes()
	t.Cleanup(release)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.eventually("the check named", func(view ledger.LaneView) bool { return view.Checking != nil })
	release()
	view := r.eventually("the check ended", func(view ledger.LaneView) bool { return view.Checking == nil })
	const want = "Runs on documented surfaces until it restarts on a qualified version."
	if got := view.ConditionNotes[string(lanes.DegradedMode)]; got != want {
		t.Errorf("the degraded lane says %q, want %q", got, want)
	}
}

// A lane that ends while its version's probe runs names no check: nothing of it waits on the probe.
func TestALaneThatEndsDuringAProbeNamesNoCheck(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.subject.Wait()
	r.driver.SetVersion("1.0.1", false)
	release := r.driver.HoldProbes()
	t.Cleanup(release)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.atGate(g)
	r.eventually("the check named", func(view ledger.LaneView) bool { return view.Checking != nil })
	g.free()
	view := r.eventually("the lane's end", func(view ledger.LaneView) bool { return view.ActualState == ledger.StateEnded })
	if view.Checking != nil {
		t.Errorf("an ended lane names %+v", view.Checking)
	}
}

// TestAQualificationReplaysFromTheLog is the record of a probe: the ledger's log replays to what the
// worker holds, and a document that is no probe's result is refused and leaves nothing to replay.
func TestAQualificationReplaysFromTheLog(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.driver.Play(completedLane(r))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	for _, bad := range []string{`{"driver":"fake","version":"","source":"probe"}`, `{"driver":"fake","version":"1","source":"matrix"}`, `[]`} {
		if _, err := r.subject.Record(t.Context(), ledger.UploadEvent{Type: ledger.UploadDriverQualified, Payload: json.RawMessage(bad)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.qualifications()) != 1 {
		t.Fatalf("recorded %+v", r.qualifications())
	}
	replayed, err := lanes.ReplayQualifications(r.events())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := r.journal.Qualification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	key := lanes.QualificationKey(r.subject.Auth().WorkerID, fake.FakeDriverName)
	if got := replayed[key]; got.Version != stored.Version || got.Degraded != stored.Degraded || len(replayed) != 1 {
		t.Errorf("the log replays to %+v, and the worker holds %+v", replayed, stored)
	}
}

// TestALaneWithNoHeadlessModeRunsInTmux is ADR-0002's fallback: a version whose `-p` lost a
// subscription's login offers no headless lane, so a lane its profile sends headless starts in tmux.
func TestALaneWithNoHeadlessModeRunsInTmux(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "run_mode = \"headless\"\n")
	r.driver.SetHeadless(false)
	r.driver.Play(completedLane(r))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	specs := r.driver.Specs()
	if len(specs) != 1 || specs[0].RunMode != ports.RunModeInteractive {
		t.Fatalf("sessions %d, the first in %q", len(specs), firstMode(specs))
	}
}

func firstMode(specs []ports.SessionSpec) string {
	if len(specs) == 0 {
		return ""
	}
	return specs[0].RunMode
}

// A CLI that updates itself under a running worker is probed again before its next session, and
// a lane that was qualified does not carry that over: the probe's result decides.
func TestAVersionThatChangesUnderARunningWorkerIsProbedAgain(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, restarts)
	g := newGate()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), {Crash: true}}))
	r.driver.PlayAfter(completedLane(r))
	r.atGate(g)
	if n := len(r.driver.Probes()); n != 1 {
		t.Fatalf("%d probes before the update", n)
	}
	r.driver.SetVersion("1.0.1", true)
	close(g.release)
	r.awaitCharge(t)
	r.clock.Advance(time.Minute)
	r.subject.Wait()
	probes := r.driver.Probes()
	if len(probes) != 2 {
		t.Fatalf("%d probes, want one per version", len(probes))
	}
	specs := r.driver.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d sessions", len(specs))
	}
	got := r.qualifications()
	if len(got) != 2 || got[1].Version != "1.0.1" || !got[1].Degraded {
		t.Errorf("recorded %+v", got)
	}
}

// TestAWorkerToldNotToProbeStartsNoProbe is the worker of a daemon that runs for the length of one command and
// is stopped within seconds: a probe runs real agent CLI sessions that spend turns, and one that is always
// cancelled before it ends keeps nothing. The worker still registers, which is all that daemon is for.
func TestAWorkerToldNotToProbeStartsNoProbe(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	driver := fake.NewAgentDriver()
	subject := worker.New(base.link, base.journal, base.clock, worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
		Lanes:     &worker.LaneRuntime{Driver: driver, NoProbe: true},
	})
	if err := subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	subject.Wait()
	if n := len(driver.Probes()); n != 0 {
		t.Errorf("%d probes by a worker told not to probe", n)
	}
	if subject.Auth().WorkerID == "" {
		t.Error("a worker told not to probe did not register")
	}
}

// A worker that restarts on a version it already probed reads its result from the journal, hands it
// to the driver, and starts work with no second probe.
func TestARestartOnTheSameVersionDoesNotProbeAgain(t *testing.T) {
	t.Parallel()
	first := laneRigWith(t, "")
	first.driver.Play(completedLane(first))
	if _, err := first.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	first.subject.Wait()
	driver := fake.NewAgentDriver()
	second := worker.New(first.link, first.journal, first.clock, worker.Config{Name: "test", Lanes: &worker.LaneRuntime{Driver: driver}})
	if err := second.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if q := driver.Qualified(); q.Version != "1.0.0" || q.Source != lanes.QualifiedByProbe {
		t.Errorf("a restarted worker handed its driver %+v", q)
	}
	if n := len(driver.Probes()); n != 0 {
		t.Errorf("%d probes on a version the journal holds", n)
	}
}

// TestAVersionNobodyCouldReadIsNotProbed is a driver that reports no version. No probe's result holds
// for an empty version, so a probe of one would end only for the next to start, and the lane would wait
// on them for good. Nothing is probed, and the lane goes on to its start, where the driver reads the
// version again.
func TestAVersionNobodyCouldReadIsNotProbed(t *testing.T) {
	t.Parallel()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) {
		driver.SetVersion("", true)
		driver.HoldProbes()
	}).dispatched()
	r.driver.Play(completedLane(r))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Specs()) == 0 {
		if n := len(r.driver.Probes()); n != 0 || time.Now().After(deadline) {
			t.Fatalf("%d probes of a version nobody could read, and no session", n)
		}
		time.Sleep(time.Millisecond)
	}
	r.subject.Wait()
	if n := len(r.driver.Probes()); n != 0 {
		t.Errorf("%d probes of a version nobody could read", n)
	}
}

// A probe that cannot run holds nothing forever: the version is degraded, its record says every
// check is unobserved, and the lane starts on documented surfaces.
func TestAProbeThatCannotRunLeavesTheVersionDegraded(t *testing.T) {
	t.Parallel()
	r := newLaneRigOn(t, func(driver *fake.AgentDriver) {
		driver.FailProbe(errors.New("the CLI would not start"))
	}).dispatched()
	r.driver.Play(completedLane(r))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if n := len(r.driver.Specs()); n != 1 {
		t.Fatalf("%d sessions", n)
	}
	got := r.qualifications()
	if len(got) != 1 || !got[0].Degraded || got[0].Version != "1.0.0" {
		t.Fatalf("recorded %+v", got)
	}
	for surface, result := range got[0].Checks {
		if result != lanes.CheckUnobserved {
			t.Errorf("%s reads %s from a probe that never ran", surface, result)
		}
	}
	caps, err := r.driver.Capabilities(t.Context())
	if err != nil || !caps.Degraded {
		t.Errorf("capabilities %+v, %v", caps, err)
	}
}
