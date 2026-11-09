package worker_test

import (
	"encoding/json"
	"errors"
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

// awaitProbes waits until the driver has been asked for n probes.
func (r *laneRig) awaitProbes(n int) {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Probes()) < n {
		if time.Now().After(deadline) {
			r.t.Fatalf("%d probes, want %d", len(r.driver.Probes()), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func completedLane(r *laneRig) fake.DriverScript {
	return script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)})
}

// TestANewVersionIsProbedOnceAndHoldsDispatchUntilItEnds is FR-DRV-2 on a real upgrade: the first
// lane to see a version starts one probe, no session starts until it ends, a pass that comes while
// it runs starts no second probe, and a probe that lasts reaches a person as a held lane.
func TestANewVersionIsProbedOnceAndHoldsDispatchUntilItEnds(t *testing.T) {
	r := laneRigWith(t, "")
	release := r.driver.HoldProbes()
	r.driver.Play(completedLane(r))
	for range 3 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	r.awaitProbes(1)
	time.Sleep(1500 * time.Millisecond)
	if n := len(r.driver.Specs()); n != 0 {
		t.Fatalf("%d sessions started while the probe ran", n)
	}
	if n := len(r.driver.Probes()); n != 1 {
		t.Errorf("%d probes of one version", n)
	}
	if notes := r.heldNotes(); len(notes) != 1 || !strings.Contains(notes[0], lanes.HeldQualifying) {
		t.Errorf("held notes %v", notes)
	}
	release()
	r.subject.Wait()
	if n := len(r.driver.Specs()); n != 1 {
		t.Fatalf("%d sessions after the probe ended", n)
	}
	if n := len(r.driver.Probes()); n != 1 {
		t.Errorf("%d probes", n)
	}
	got := r.qualifications()
	if len(got) != 1 || got[0].Version != "1.0.0" || got[0].Source != lanes.QualifiedByProbe {
		t.Errorf("recorded %+v", got)
	}
	if q := r.driver.Qualified(); q.Version != "1.0.0" {
		t.Errorf("the driver was handed %+v", q)
	}
	stored, err := r.journal.Qualification(t.Context())
	if err != nil || stored.Version != "1.0.0" {
		t.Errorf("the journal holds %+v, %v", stored, err)
	}
}

// TestAQualificationReplaysFromTheLog is the record of a probe: the ledger's log replays to what the
// worker holds, and a document that is no probe's result is refused and leaves nothing to replay.
func TestAQualificationReplaysFromTheLog(t *testing.T) {
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

// A worker that restarts on a version it already probed reads its result from the journal, hands it
// to the driver, and starts work with no second probe.
func TestARestartOnTheSameVersionDoesNotProbeAgain(t *testing.T) {
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
	r := laneRigWith(t, "")
	r.driver.SetVersion("", true)
	r.driver.HoldProbes()
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
	r := laneRigWith(t, "")
	r.driver.FailProbe(errors.New("the CLI would not start"))
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
