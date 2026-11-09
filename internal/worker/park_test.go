package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// superviseStep is how often a lane's own loops look at their clock.
const superviseStep = 20 * time.Millisecond

// humanPark asks a lane to wait for a person.
func humanPark() lanes.ParkRequest {
	return lanes.ParkRequest{Trigger: lanes.ParkFleetDrain, Expr: lanes.HumanWake()}
}

// parking asks for a lane's parking and lets its first turn end, as an instruction that waits
// for a tool boundary would.
func (r *handoffRig) park(t *testing.T, request lanes.ParkRequest) {
	t.Helper()
	if err := r.subject.Park(t.Context(), "lane-1", request); err != nil {
		t.Fatal(err)
	}
	close(r.release)
}

// awaitOrder reads until a work order gets there, and moves the worker's clock along, so a
// wait on that clock ends. Nothing here depends on how far it moved.
func (r *laneRig) awaitOrder(t *testing.T, state workorders.State) workorders.WorkOrder {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var row workorders.WorkOrder
	for time.Now().Before(deadline) {
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row = r.row(); row.State == state {
			return row
		}
		r.clock.Advance(time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("work order %s, want %s", row.State, state)
	return row
}

func (r *laneRig) wakeOf(t *testing.T, generation int64) parking.Wake {
	t.Helper()
	var wake parking.Wake
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		wake, err = tx.Wakes().Get(lanes.WakeID("lane-1", generation))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return wake
}

func (r *laneRig) checkpointOf(t *testing.T, generation int64) ledger.Checkpoint {
	t.Helper()
	var out ledger.Checkpoint
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Checkpoint("lane-1", generation)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAParkedLaneSavedItsWorkAndHoldsNoProcess(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) {
		r.worktrees().Dirty(stopWorktree)
		r.worktrees().UseRemote("origin")
		r.choosesPush()
		r.callHandoff(narrativeText)
	}}, completed(0.02))
	r.park(t, humanPark())
	row := r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()

	if row.ClaimID != "" || row.LeaseWorker != "" || row.Fence != r.grant.Fence+1 {
		t.Errorf("the lease outlived the process: %+v", row)
	}
	sent := r.driver.Sent()
	if len(sent) != 1 || sent[0].Text != lanes.HandoffInstruction() {
		t.Errorf("sent %+v", sent)
	}
	if wake := r.wakeOf(t, 1); !wake.Open() || wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v", wake)
	}
	_, doc := r.storedHandoff(t, 1)
	if doc.Reason != string(lanes.ParkFleetDrain) || doc.Narrative == nil || !doc.Narrative.CarriesNextStep {
		t.Errorf("document %+v", doc)
	}
	checkpoint := r.checkpointOf(t, 1)
	if checkpoint.SnapshotRef != lanes.CheckpointRef("lane-1", 1) || !checkpoint.Pushed || checkpoint.HandoffArtifact == "" {
		t.Errorf("checkpoint %+v", checkpoint)
	}
	if got := r.worktrees().Pushed(); len(got) == 0 {
		t.Error("nothing was pushed")
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateParked || record.TokenDigest != "" {
		t.Errorf("the token outlived its process: %+v", record)
	}
	if held, err := r.journal.Allocations(t.Context()); err != nil || len(held) != 0 {
		t.Errorf("a parked lane holds %+v, %v", held, err)
	}
	if view := r.read(); view.ActualState != ledger.StateParked {
		t.Errorf("the lane reads %+v", view)
	}
	generations := r.generations(t)
	if len(generations) != 1 || generations[0].EndReason != workorders.EndParked {
		t.Errorf("generations %+v", generations)
	}
}

// TestAParkedLaneNobodyChoseToPushStaysWithThisMachine is the default with a remote: the work
// goes to a local ref, nothing is pushed, and the checkpoint says no remote holds it, which binds
// the lane's resume to the machine that has it.
func TestAParkedLaneNobodyChoseToPushStaysWithThisMachine(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) {
		r.worktrees().Dirty(stopWorktree)
		r.worktrees().UseRemote("origin")
		r.callHandoff(narrativeText)
	}}, completed(0.02))
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	checkpoint := r.checkpointOf(t, 1)
	if checkpoint.SnapshotRef != lanes.CheckpointRef("lane-1", 1) || checkpoint.Pushed {
		t.Errorf("checkpoint %+v", checkpoint)
	}
	if got := r.worktrees().Pushed(); len(got) != 0 {
		t.Errorf("pushed %v with nobody's choice", got)
	}
}

func TestAnAnswerDuringParkingFiresAtTheCommit(t *testing.T) {
	var r *handoffRig
	question := "q-during"
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) {
		asked := control.NewQuestions(r.store, questions.BatchPolicy{})
		if _, err := asked.RecordQuestion(context.Background(), scope, questions.Question{ID: question, Text: "What now?"}, member); err != nil {
			r.t.Error(err)
		}
		if _, err := asked.Answer(context.Background(), scope, question, questions.Answer{DecisionID: "d1", Statement: "Go.", Rationale: "It follows."}, member); err != nil {
			r.t.Error(err)
		}
		r.callHandoff(narrativeText)
	}}, completed(0.02))
	r.park(t, lanes.ParkRequest{Trigger: lanes.ParkBlockingQuestion, Expr: lanes.QuestionWake([]string{question})})
	row := r.awaitOrder(t, workorders.Ready)
	r.subject.Wait()
	if row.ResumeLaneID != "lane-1" {
		t.Errorf("woken %+v", row)
	}
	if wake := r.wakeOf(t, 1); wake.Open() || wake.FiredBy != parking.CauseParked {
		t.Errorf("wake %+v", wake)
	}
}

// TestParkingRunsWithNoControlPlane holds what a partition must not break: a worker runs the
// same sequence, and the ledger hears of it on return.
func TestParkingRunsWithNoControlPlane(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}, completed(0.02))
	r.link.FailUpload.Store(true)
	r.park(t, humanPark())
	r.subject.Wait()
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateParked {
		t.Fatalf("record %+v", record)
	}
	if row := r.row(); row.State != workorders.Running {
		t.Fatalf("the ledger heard nothing, and reads %+v", row)
	}
	r.link.FailUpload.Store(false)
	r.awaitOrder(t, workorders.Parked)
}

func TestNoNarrativeInTimeDegradesAndStillParks(t *testing.T) {
	r := atWorkOn(t, profileAt+restarts+"handoff_timeout = \"30ms\"\n", (*laneRig).toldToGoOn, fake.DriverStep{Hold: true})
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	record, doc := r.storedHandoff(t, 1)
	if !record.Degraded || doc.Narrative != nil {
		t.Errorf("document %+v", doc)
	}
}

func TestAStopIsAnInterruptFirst(t *testing.T) {
	var how []ports.StopMode
	r := atWorkOn(t, handoffProfile, (*laneRig).toldToGoOn, fake.DriverStep{Hold: true})
	r.config.Driver = stopWatcher{AgentDriver: r.driver, how: &how}
	r.park(t, lanes.ParkRequest{Trigger: lanes.ParkUnknownScreen, Expr: lanes.HumanWake(), SkipNarrative: true})
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if len(how) != 1 || how[0] != ports.StopInterrupt {
		t.Errorf("stops %v", how)
	}
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("a skipped narrative still asked: %+v", sent)
	}
}

// stopWatcher keeps how each stop was asked for.
type stopWatcher struct {
	*fake.AgentDriver
	how *[]ports.StopMode
}

func (d stopWatcher) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	*d.how = append(*d.how, how)
	return d.AgentDriver.Stop(ctx, h, how)
}

// liveDriver reads a session as alive after its stream ended, which only a defect could do.
type liveDriver struct {
	*fake.AgentDriver
	alive chan struct{}
}

func (d liveDriver) Observe(ctx context.Context, h ports.SessionHandle) (ports.Observation, error) {
	select {
	case <-d.alive:
		return d.AgentDriver.Observe(ctx, h)
	default:
		return ports.Observation{Liveness: ports.LivenessAlive}, nil
	}
}

// TestALiveProcessKeepsItsLease is the first ordering rule: if the ledger recorded `parked`
// first, a crash could leave a live process while another worker resumed the same lane.
func TestALiveProcessKeepsItsLease(t *testing.T) {
	alive := make(chan struct{})
	r := atWorkOn(t, handoffProfile, (*laneRig).toldToGoOn, fake.DriverStep{Hold: true})
	r.config.Driver = liveDriver{AgentDriver: r.driver, alive: alive}
	r.park(t, lanes.ParkRequest{Trigger: lanes.ParkUnknownScreen, Expr: lanes.HumanWake(), SkipNarrative: true})
	time.Sleep(100 * time.Millisecond)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Running {
		t.Fatalf("a process read alive and the lease went: %+v", row)
	}
	close(alive)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
}

func TestALaneThatReportsWhileParkingEnds(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(spec ports.SessionSpec) {
		r.reports(workorders.OutcomeCompleted).Do(spec)
	}}, completed(0.02))
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Open() || wake.FiredBy != parking.RetiredFinished {
		t.Errorf("a finished lane left %+v", wake)
	}
}

func TestAFencedLaneDoesNotPark(t *testing.T) {
	r := atWorkOn(t, handoffProfile, (*laneRig).toldToGoOn, fake.DriverStep{Hold: true})
	holder := workorders.Holder{Lease: r.grant.Lease, Lane: "lane-1", Generation: 1}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "taken"}}
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := control.NewLeases(r.store).EndLane(t.Context(), scope, holder, result, member); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Beat(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Park(t.Context(), "lane-1", humanPark()); !errors.Is(err, worker.ErrNoParking) {
		t.Errorf("a fenced lane parked: %v", err)
	}
	close(r.release)
}

func TestAParkedCommitTheLedgerFencedEndsTheLane(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}, completed(0.02))
	r.link.FailUpload.Store(true)
	r.park(t, humanPark())
	r.subject.Wait()
	holder := workorders.Holder{Lease: r.grant.Lease, Lane: "lane-1", Generation: 1}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "taken"}}
	if err := control.NewLeases(r.store).EndLane(t.Context(), scope, holder, result, member); err != nil {
		t.Fatal(err)
	}
	r.link.FailUpload.Store(false)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateEnded {
		t.Errorf("a lane no wake can reach reads %+v", record)
	}
}

func TestAnUnknownScreenParksForAPerson(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.config.Driver = startRefusal{AgentDriver: r.driver, err: fmt.Errorf("check: %w", ports.ErrUnknownScreen)}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if row := r.awaitOrder(t, workorders.Parked); row.ClaimID != "" {
		t.Errorf("row %+v", row)
	}
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v", wake)
	}
	record, doc := r.storedHandoff(t, 1)
	if !record.Degraded || doc.Reason != string(lanes.ParkUnknownScreen) {
		t.Errorf("a first start cannot write a narrative, and left %+v", doc)
	}
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("input went to a screen nobody knows: %+v", sent)
	}
	if _, err := control.NewWakes(r.store).ResumeLane(t.Context(), scope, "lane-1", member); err != nil {
		t.Fatal(err)
	}
	if woken := r.row(); woken.State != workorders.Ready || woken.ResumeLaneID != "lane-1" {
		t.Errorf("a member's word left %+v", woken)
	}
}

// asking is a session that asks a blocking question, then ends its turn with nothing to do.
func (r *laneRig) asking(text string) fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) {
		line, err := json.Marshal(lanes.Request{
			Version: lanes.SocketProtocol, Token: r.token(r.t), Ask: &lanes.AskRequest{Text: text, Blocking: true},
		})
		if err != nil {
			r.t.Error(err)
			return
		}
		if reply := r.subject.AnswerLane(context.Background(), line); !reply.OK {
			r.t.Error(reply.Message)
		}
	}}
}

func TestAnIdleLaneWithABlockingQuestionParksAfterItsWait(t *testing.T) {
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.asking("What is the retry budget?"), completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
	r.clock.Advance(4*time.Minute + 59*time.Second)
	time.Sleep(5 * superviseStep)
	if view := r.read(); view.ActualState != string(lanes.Idle) {
		t.Fatalf("a lane inside its wait reads %s", view.ActualState)
	}
	r.clock.Advance(2 * time.Second)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	var open []questions.Question
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		open, err = tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords})
		return err
	})
	if err != nil || len(open) != 1 {
		t.Fatalf("questions %+v, %v", open, err)
	}
	if want := lanes.QuestionWake([]string{open[0].ID}); r.wakeOf(t, 1).Expr != want {
		t.Errorf("wake %+v, want %s", r.wakeOf(t, 1), want)
	}
	for _, sent := range r.driver.Sent() {
		if strings.Contains(sent.Text, "no outcome") {
			t.Errorf("a lane waiting on a person was nudged: %q", sent.Text)
		}
	}
}

func (r *laneRig) drainFleet(t *testing.T) {
	t.Helper()
	if err := control.NewFleet(r.store).SetMode(t.Context(), scope, ledger.FleetDrain, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// TestAQuestionAnsweredOnAReadHoldsNothing is a lane that asked again and read its answer: it
// has nothing left to wait on, so it takes the ordinary nudge, and parks on nothing.
func TestAQuestionAnsweredOnAReadHoldsNothing(t *testing.T) {
	r := laneRigWith(t, profileAt+"[lifecycle]\nask_wait = \"1s\"\n")
	const text = "What is the retry budget?"
	answer := fake.DriverStep{Do: func(ports.SessionSpec) {
		var open []questions.Question
		err := r.store.Read(context.Background(), scope, func(tx ports.ReadTx) error {
			var err error
			open, err = tx.Questions().Questions(ports.RecordFilter{Limit: ports.MaxRecords})
			return err
		})
		if err != nil || len(open) != 1 {
			r.t.Errorf("questions %+v, %v", open, err)
			return
		}
		asked := control.NewQuestions(r.store, questions.BatchPolicy{})
		if _, err := asked.Answer(context.Background(), scope, open[0].ID, questions.Answer{DecisionID: "d1", Statement: "Three.", Rationale: "It follows."}, member); err != nil {
			r.t.Error(err)
		}
	}}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, r.asking(text), answer, r.asking(text), completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if sent := r.driver.Sent(); len(sent) != 1 || sent[0].Text != lanes.Nudge {
		t.Errorf("sent %+v", sent)
	}
}

func TestADrainingFleetParksALaneAtRest(t *testing.T) {
	r := laneRigWith(t, profileAt+"[lifecycle]\nhandoff_timeout = \"30ms\"\n")
	g := newGate()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.atGate(g)
	r.drainFleet(t)
	close(g.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v", wake)
	}
	if sent := r.driver.Sent(); len(sent) != 1 || sent[0].Text != lanes.HandoffInstruction() {
		t.Errorf("a draining lane is asked for its narrative and nothing else: %+v", sent)
	}
}

func TestADrainParksAWorkingLanePastItsDeadline(t *testing.T) {
	r := atWorkOn(t, profileAt+"[lifecycle]\ndrain_deadline = \"5m\"\nhandoff_timeout = \"30ms\"\n", (*laneRig).toldToGoOn, fake.DriverStep{Hold: true})
	r.drainFleet(t)
	parking := func() *lanes.ParkProgress {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		record, err := r.journal.Lane(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		return record.Parking
	}
	if parking() != nil {
		t.Fatal("a turn inside the deadline was stopped")
	}
	r.clock.Advance(5 * time.Minute)
	if parking() == nil {
		t.Fatal("a turn past the deadline went on")
	}
	close(r.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
}

func TestParkingFinishesAfterARestartFromItsWake(t *testing.T) {
	r := atWork(t, fake.DriverStep{Hold: true})
	if err := r.subject.Park(t.Context(), "lane-1", humanPark()); err != nil {
		t.Fatal(err)
	}
	for len(r.driver.Sent()) == 0 {
		time.Sleep(time.Millisecond)
	}
	r.stopWorker()
	close(r.release)
	r.driver.StopEverySession()
	r.subject.Wait()

	next := r.nextWorker(t)
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	next.Wait()
	if err := next.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Parked {
		t.Fatalf("a stored wake did not finish parking: %+v", row)
	}
}

// nextWorker stands for a new process on the same journal, with no session left to find.
func (r *laneRig) nextWorker(t *testing.T) *worker.Worker {
	t.Helper()
	next := worker.New(r.link, r.journal, r.clock, worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return r.plane.CreateJoinToken(ctx, member) },
		Lanes:     r.config,
		Report:    func(err error) { t.Log(err) },
	})
	r.config.Driver = fake.NewAgentDriver()
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return next
}
