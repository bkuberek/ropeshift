package worker_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// parkedInProject starts a lane in a project, parks it on a person's word, and wakes it. The lane's next
// generation reports the work done.
func (r *installationRig) parkedInProject(project ledger.WorkspaceID, lane string) {
	r.t.Helper()
	r.profile.Put("/repository", handoffProfile)
	r.dispatch(project, lane)
	reached, release := make(chan struct{}), make(chan struct{})
	r.t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) {
		close(reached)
		<-release
	}}, completed(0.01)}))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(), completed(0.03)}))
	if err := r.subject.Sync(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	select {
	case <-reached:
	case <-time.After(20 * time.Second):
		r.t.Fatal("the session never reached its work")
	}
	r.parkWhenItCan(lane)
	close(release)
	r.awaitOrder(project, workorders.Parked)
	r.subject.Wait()
	if _, err := control.NewWakes(r.stores[project]).ResumeLane(r.t.Context(), project, lane, member); err != nil {
		r.t.Fatal(err)
	}
}

// parkWhenItCan asks for parking with no narrative until the lane's turn reads as one it can park from.
func (r *installationRig) parkWhenItCan(lane string) {
	r.t.Helper()
	request := humanPark()
	request.SkipNarrative = true
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := r.subject.Park(r.t.Context(), lane, request)
		if err == nil {
			return
		}
		if !errors.Is(err, worker.ErrNoParking) || time.Now().After(deadline) {
			r.t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

// reports is a session that says its work is done.
func (r *installationRig) reports() fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		if _, err := r.journal.SaveReport(r.t.Context(), spec.Lane, []byte(`{"outcome":"completed"}`)); err != nil {
			r.t.Error(err)
		}
	}}
}

// pass is one tick of a daemon, twice over, because a claim is what puts a resume in desired state.
func (r *installationRig) pass() {
	r.t.Helper()
	for range 2 {
		if err := r.subject.Sync(r.t.Context()); err != nil {
			r.t.Fatal(err)
		}
		if _, err := r.subject.Reconcile(r.t.Context()); err != nil {
			r.t.Fatal(err)
		}
	}
}

// awaitOrder uploads until a project's work order gets to a state, and moves the worker's clock along.
func (r *installationRig) awaitOrder(project ledger.WorkspaceID, state workorders.State) workorders.WorkOrder {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var row workorders.WorkOrder
	for time.Now().Before(deadline) {
		if err := r.subject.Upload(r.t.Context()); err != nil {
			r.t.Fatal(err)
		}
		if row = r.row(project); row.State == state {
			return row
		}
		r.clock.Advance(time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	r.t.Fatalf("project %s: work order %s, want %s", project, row.State, state)
	return row
}

// generations reads a lane's generations from its project's ledger.
func (r *installationRig) generations(project ledger.WorkspaceID, lane string) []ledger.Generation {
	r.t.Helper()
	var out []ledger.Generation
	err := r.stores[project].Read(r.t.Context(), project, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Generations(lane)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

// noRefusals fails a test whose worker holds an event the ledger refused.
func (r *installationRig) noRefusals() {
	r.t.Helper()
	rejected, err := r.journal.Rejected(r.t.Context())
	if err != nil {
		r.t.Fatal(err)
	}
	if len(rejected) != 0 {
		r.t.Errorf("the ledger refused %+v", rejected)
	}
}

// leftByAnEarlierBuild claims a woken lane, and journals what a build that named no project in a resume
// left of the generation it began there: the resume, which the ledger refused, and the generation's end,
// which the ledger fenced. It returns the claim the lane woke under.
func (r *installationRig) leftByAnEarlierBuild(lane string) ledger.DesiredLease {
	r.t.Helper()
	ctx := r.t.Context()
	if err := r.subject.Sync(ctx); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(ctx); err != nil {
		r.t.Fatal(err)
	}
	state, err := r.journal.Desired(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	var lease ledger.DesiredLease
	for _, held := range state.Leases {
		if held.Lane == lane {
			lease = held
		}
	}
	if lease.Resume == nil {
		r.t.Fatalf("no claim awaits the resume of %s: %+v", lane, state.Leases)
	}
	identity, err := r.journal.Identity(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	next := lease.Resume.Generation + 1
	at := func(eventType, project string, payload any) ledger.UploadEvent {
		raw, err := json.Marshal(payload)
		if err != nil {
			r.t.Fatal(err)
		}
		return ledger.UploadEvent{
			Epoch: identity.Epoch, Project: project, Type: eventType, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID,
			Fence: lease.Fence, Lane: lane, Generation: next, Payload: raw, OccurredAt: r.clock.Now().UnixMilli(),
		}
	}
	record := lanes.Record{
		ID: lane, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence, Generation: next,
		State: lanes.StateRunning, Actual: lanes.Provisioning,
	}
	if _, err := r.journal.BeginResume(ctx, record, at(ledger.UploadLaneResumed, "", ledger.GenerationStart{Reason: ledger.StartResume})); err != nil {
		r.t.Fatal(err)
	}
	if err := r.subject.Upload(ctx); err != nil {
		r.t.Fatal(err)
	}
	ended := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}
	if _, err := r.journal.EndLane(ctx, lane, at(ledger.UploadLaneEnded, lease.Project, ended)); err != nil {
		r.t.Fatal(err)
	}
	if err := r.subject.Upload(ctx); err != nil {
		r.t.Fatal(err)
	}
	rejected, err := r.journal.Rejected(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	if len(rejected) != 2 || rejected[0].Status != ledger.UploadInvalid || rejected[1].Status != ledger.UploadFenced {
		r.t.Fatalf("the earlier build's writes were refused as %+v", rejected)
	}
	return lease
}

// TestAWorkerSendsAgainAResumeTheLedgerRefusedForItsProject starts a worker on a journal an earlier build
// left: a lane of the second project woke, its resume named no project and was refused, and its end was
// fenced, while the work order stayed claimed with no lane. The worker sends the resume again with the
// claim's project, and the end after it, so the ledger records the generation and the claim goes.
func TestAWorkerSendsAgainAResumeTheLedgerRefusedForItsProject(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	lease := r.leftByAnEarlierBuild("lane-b")
	if row := r.row("pb"); row.State != workorders.Claimed || row.ClaimID != lease.ClaimID {
		t.Fatalf("the earlier build left %+v", row)
	}
	r.restart()
	r.pass()
	row := r.awaitOrder("pb", workorders.Acceptance)
	if row.ClaimID != "" {
		t.Errorf("the work order keeps a claim: %+v", row)
	}
	r.noRefusals()
	if generations := r.generations("pb", "lane-b"); len(generations) != 2 || generations[1].StartReason != ledger.StartResume {
		t.Errorf("generations %+v", generations)
	}
	record, err := r.journal.Lane(t.Context(), "lane-b")
	if err != nil || record.Project != "pb" {
		t.Errorf("the journal keeps the lane as %+v: %v", record, err)
	}
}

// TestALaneOfAnotherProjectResumes parks and wakes a lane in the installation's second project: its
// resume names that project, so the ledger records the new generation and the lane's end, and the
// work order moves on with no claim left behind (ADR-0021).
func TestALaneOfAnotherProjectResumes(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	r.pass()
	r.subject.Wait()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.noRefusals()
	if generations := r.generations("pb", "lane-b"); len(generations) != 2 || generations[1].StartReason != ledger.StartResume {
		t.Errorf("generations %+v", generations)
	}
	if row := r.row("pb"); row.State != workorders.Acceptance || row.ClaimID != "" {
		t.Errorf("the work order %+v", row)
	}
}
