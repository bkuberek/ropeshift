package worker_test

import (
	"encoding/json"
	"errors"
	"slices"
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

// writer builds the events a lane's next generation journals under its claim.
type writer func(eventType, project string, payload any) ledger.UploadEvent

// resumeRefused claims a woken lane, and journals what a build that named no project in a resume left of
// the generation it began there: the resume, which the ledger refused. It returns the claim the lane woke
// under, and a writer of that generation's other events.
func (r *installationRig) resumeRefused(lane string) (ledger.DesiredLease, writer) {
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
	return lease, at
}

// leftByAnEarlierBuild is a woken lane that build left with its resume refused and the generation's end,
// which the ledger fenced, or which that build never uploaded when uploaded is false.
func (r *installationRig) leftByAnEarlierBuild(lane string, uploaded bool) ledger.DesiredLease {
	r.t.Helper()
	ctx := r.t.Context()
	lease, at := r.resumeRefused(lane)
	ended := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}
	if _, err := r.journal.EndLane(ctx, lane, at(ledger.UploadLaneEnded, lease.Project, ended)); err != nil {
		r.t.Fatal(err)
	}
	want := []string{ledger.UploadInvalid}
	if uploaded {
		if err := r.subject.Upload(ctx); err != nil {
			r.t.Fatal(err)
		}
		want = append(want, ledger.UploadFenced)
	}
	rejected, err := r.journal.Rejected(ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	statuses := make([]string, 0, len(rejected))
	for _, result := range rejected {
		statuses = append(statuses, result.Status)
	}
	if !slices.Equal(statuses, want) {
		r.t.Fatalf("the earlier build's writes were refused as %+v", rejected)
	}
	return lease
}

// leftWithAParkItRefused is a woken lane that build left with its resume refused, and with the lane's own
// writes after it: it began to park and committed the park, which the ledger fenced, and the worker
// ended the lane because its park commit was refused, which the ledger fenced too. It returns the claim the
// lane woke under.
func (r *installationRig) leftWithAParkItRefused(lane string) ledger.DesiredLease {
	r.t.Helper()
	return r.leftWithAPark(lane, parkLeft{})
}

// parkLeft says what an earlier build wrote in the lane's park. A commit that names a wake the lane never
// began, or an end whose payload is no lane result, is a write the ledger refuses on its own account, so
// it refuses it again when it is sent again. The zero value is a park and an end the ledger would take.
type parkLeft struct {
	commitWake string
	endPayload any
}

// leftWithAPark is leftWithAParkItRefused with the writes of the park as given.
func (r *installationRig) leftWithAPark(lane string, left parkLeft) ledger.DesiredLease {
	r.t.Helper()
	ctx := r.t.Context()
	lease, at := r.resumeRefused(lane)
	next := lease.Resume.Generation + 1
	parking := lanes.ParkProgress{
		Wake: lanes.WakeID(lane, next), Expr: lanes.HumanWake(), Trigger: lanes.ParkFleetDrain, Generation: next,
	}
	start := at(ledger.UploadLaneParking, lease.Project, ledger.ParkingStart{Wake: parking.Wake, Expr: parking.Expr})
	if _, err := r.journal.BeginParking(ctx, lane, parking, lanes.HandoffProgress{}, start); err != nil {
		r.t.Fatal(err)
	}
	wake := left.commitWake
	if wake == "" {
		wake = parking.Wake
	}
	commit := at(ledger.UploadLaneParked, lease.Project, ledger.ParkedCommit{Wake: wake, Checkpoint: ledger.Checkpoint{Lane: lane, Generation: next}})
	if _, err := r.journal.CommitParked(ctx, lane, parking, commit); err != nil {
		r.t.Fatal(err)
	}
	if err := r.subject.Upload(ctx); err != nil {
		r.t.Fatal(err)
	}
	var ended any = workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}
	endStatus := ledger.UploadFenced
	if left.endPayload != nil {
		ended, endStatus = left.endPayload, ledger.UploadInvalid
	}
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
	statuses := make([]string, 0, len(rejected))
	for _, result := range rejected {
		statuses = append(statuses, result.Status)
	}
	want := []string{ledger.UploadInvalid, ledger.UploadFenced, ledger.UploadFenced, endStatus}
	if !slices.Equal(statuses, want) {
		r.t.Fatalf("the earlier build's writes were refused as %+v", rejected)
	}
	return lease
}

// TestAWorkerLeavesNoRefusalAfterSendingAgainAParkTheLedgerRefused starts a worker on that journal. The
// resume goes again with the park the lane made, so the ledger records the generation parked, and the end
// the worker wrote only because the park was refused is no write the ledger should hear: nothing stays
// refused, and the claim goes the way the park ended it.
func TestAWorkerLeavesNoRefusalAfterSendingAgainAParkTheLedgerRefused(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	lease := r.leftWithAParkItRefused("lane-b")
	r.restart()
	r.pass()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.noRefusals()
	if row := r.row("pb"); row.ClaimID == lease.ClaimID {
		t.Errorf("the work order keeps the claim: %+v", row)
	}
}

// refusedTypes lists the types of the events the ledger refused, in order.
func (r *installationRig) refusedTypes() []string {
	r.t.Helper()
	refused, err := r.journal.Refused(r.t.Context())
	if err != nil {
		r.t.Fatal(err)
	}
	out := make([]string, 0, len(refused))
	for _, event := range refused {
		out = append(out, event.Type)
	}
	return out
}

// TestAWorkerKeepsAParkRefusedAgainAndEndsTheClaim is the same journal, but the ledger refuses the park
// copy too, since the wake it names was never begun. No lane is parked there, so the refusal of the park
// stays, and the end the worker wrote because the park was refused is the one write of the lane the ledger
// takes: it ends the claim, and no end stays refused.
func TestAWorkerKeepsAParkRefusedAgainAndEndsTheClaim(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	lease := r.leftWithAPark("lane-b", parkLeft{commitWake: "no-such-wake"})
	r.restart()
	r.pass()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := r.refusedTypes(); !slices.Equal(got, []string{ledger.UploadLaneParked}) {
		t.Errorf("the worker holds refusals of %v, want only the park", got)
	}
	if row := r.row("pb"); row.State != workorders.Acceptance || row.ClaimID == lease.ClaimID {
		t.Errorf("the work order %+v", row)
	}
}

// TestAWorkerKeepsARefusedEndBesideAParkRefusedAgain is that journal with an end the ledger refuses on its
// own account as well. An end is forgotten only for a park the ledger took, and this park is refused
// again, so the end stays refused with it: what still matters is never dropped.
func TestAWorkerKeepsARefusedEndBesideAParkRefusedAgain(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	r.leftWithAPark("lane-b", parkLeft{commitWake: "no-such-wake", endPayload: map[string]string{"report": "malformed"}})
	r.restart()
	r.pass()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{ledger.UploadLaneParked, ledger.UploadLaneEnded}
	if got := r.refusedTypes(); !slices.Equal(got, want) {
		t.Errorf("the worker holds refusals of %v, want %v", got, want)
	}
}

// TestAWorkerSendsAgainAResumeTheLedgerRefusedForItsProject starts a worker on a journal an earlier build
// left: a lane of the second project woke, its resume named no project and was refused, and its end was
// fenced, while the work order stayed claimed with no lane. The worker sends the resume again with the
// claim's project, and the end after it, so the ledger records the generation and the claim goes.
func TestAWorkerSendsAgainAResumeTheLedgerRefusedForItsProject(t *testing.T) {
	t.Parallel()
	sendsAgain(t, true)
}

// TestAWorkerSendsAgainAResumeAndAnEndItNeverUploaded is the same journal with the lane's end never
// uploaded before the earlier build stopped. The end goes first and is fenced, and it goes again after
// the resume with the rest, so the claim still goes.
func TestAWorkerSendsAgainAResumeAndAnEndItNeverUploaded(t *testing.T) {
	t.Parallel()
	sendsAgain(t, false)
}

// TestAWorkerSendsNothingAgainUnderAClaimTheLedgerMovedOn starts a worker on the same journal after the
// claim the lane woke under was given back: the lane is claimed again under a new claim, the refused writes
// belong to one the ledger awaits nothing under, so none goes again, and the lane resumes under the new
// claim as any woken lane does.
func TestAWorkerSendsNothingAgainUnderAClaimTheLedgerMovedOn(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	lease := r.leftByAnEarlierBuild("lane-b", true)
	held := workorders.Lease{WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence}
	if err := control.NewLeases(r.stores["pb"]).Release(t.Context(), "pb", held, member); err != nil {
		t.Fatal(err)
	}
	before, err := r.journal.Rejected(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.restart()
	r.pass()
	r.subject.Wait()
	r.awaitOrder("pb", workorders.Acceptance)
	after, err := r.journal.Rejected(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(after, before) {
		t.Errorf("the refusals went from %+v to %+v", before, after)
	}
	generations := r.generations("pb", "lane-b")
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff {
		t.Errorf("generations %+v", generations)
	}
}

func sendsAgain(t *testing.T, uploaded bool) {
	t.Helper()
	r := newInstallationRig(t)
	r.parkedInProject("pb", "lane-b")
	lease := r.leftByAnEarlierBuild("lane-b", uploaded)
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
