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
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

const narrativeText = `{"done":["Added the retry budget"],"in_progress":["The second attempt"],` +
	`"next_steps":["Fix the failing test"],"risks":["The stub models no timeouts"],"carries_next_step":true}`

// handoffProfile sets what a handoff reads, with a repository whose worktrees live where a
// test can reach them.
const handoffProfile = profileAt + restarts + "handoff_timeout = \"3m\"\n"

// handoffRig is a lane at work that a test can ask for a handoff.
type handoffRig struct {
	*laneRig
	release chan struct{}
	reached chan struct{}
}

// toldToGoOn is the session that follows a handoff, which finishes the work order.
func (r *laneRig) toldToGoOn() fake.DriverScript {
	return script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.03)})
}

// atWork starts a lane whose first turn waits for a test, and whose next turn answers the
// handoff's instruction with what follows. The generation after it finishes the work.
func atWork(t *testing.T, answer ...fake.DriverStep) *handoffRig {
	t.Helper()
	return atWorkBefore(t, (*laneRig).toldToGoOn, answer...)
}

// atWorkBefore names what the next generation does.
func atWorkBefore(t *testing.T, next func(*laneRig) fake.DriverScript, answer ...fake.DriverStep) *handoffRig {
	t.Helper()
	return atWorkOn(t, handoffProfile, next, answer...)
}

// atWorkOn names the profile too.
func atWorkOn(t *testing.T, profile string, next func(*laneRig) fake.DriverScript, answer ...fake.DriverStep) *handoffRig {
	t.Helper()
	r := &handoffRig{laneRig: laneRigWith(t, profile), release: make(chan struct{}), reached: make(chan struct{})}
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	first := []fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) {
		close(r.reached)
		<-r.release
	}}, completed(0.01)}
	r.driver.Play(script(first, answer))
	r.driver.PlayAfter(next(r.laneRig))
	r.start()
	select {
	case <-r.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its work")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	return r
}

// handOff asks for the handoff and lets the first turn end, as an instruction that waits
// for a tool boundary would.
func (r *handoffRig) handOff(t *testing.T) {
	t.Helper()
	if err := r.subject.HandOff(t.Context(), "lane-1", lanes.HandoffContextThreshold); err != nil {
		t.Fatal(err)
	}
	close(r.release)
}

// callHandoff is a session calling the tool that carries its part of a handoff.
func (r *laneRig) callHandoff(raw string) lanes.Reply {
	r.t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(r.t), Handoff: json.RawMessage(raw)})
	if err != nil {
		r.t.Error(err)
	}
	return r.subject.AnswerLane(context.Background(), line)
}

func (r *laneRig) storedHandoff(t *testing.T, generation int64) (ledger.HandoffRecord, lanes.Handoff) {
	t.Helper()
	var record ledger.HandoffRecord
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Lanes().Handoff("lane-1", generation)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := lanes.ReadHandoff(record.Body)
	if err != nil {
		t.Fatal(err)
	}
	return record, doc
}

func (r *laneRig) generations(t *testing.T) []ledger.Generation {
	t.Helper()
	var out []ledger.Generation
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Lanes().Generations("lane-1")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (r *laneRig) awaitSecondGeneration(t *testing.T) {
	t.Helper()
	r.awaitGeneration(t, 2)
	r.subject.Wait()
}

func TestANarrativeBecomesADocumentAndANewGeneration(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) {
		r.worktrees().Dirty(stopWorktree)
		r.callHandoff(narrativeText)
	}}, completed(0.02))
	r.handOff(t)
	r.awaitSecondGeneration(t)

	sent := r.driver.Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, lanes.HandoffInstruction()) {
		t.Fatalf("sent %+v", sent)
	}
	specs := r.driver.Specs()
	if len(specs) != 2 || specs[1].Generation != 2 {
		t.Fatalf("specs %+v", specs)
	}
	if resumed := r.driver.Resumed(); len(resumed) != 0 {
		t.Errorf("a handoff starts fresh: %+v", resumed)
	}
	record, doc := r.storedHandoff(t, 1)
	if doc.Schema != lanes.HandoffSchema || doc.Lane != "lane-1" || doc.Generation != 1 || doc.Reason != lanes.HandoffContextThreshold {
		t.Errorf("document %+v", doc)
	}
	if doc.Narrative == nil || !doc.Narrative.CarriesNextStep || doc.Narrative.NextSteps[0] != "Fix the failing test" {
		t.Errorf("narrative %+v", doc.Narrative)
	}
	facts := doc.Deterministic
	if facts.WorkOrder != r.id || facts.Branch == "" || facts.ModelResolved != "resolved-model" {
		t.Errorf("facts %+v", facts)
	}
	if facts.SnapshotRef != lanes.CheckpointRef("lane-1", 1) || !slices.Contains(facts.DirtyPaths, "changed.txt") {
		t.Errorf("a dirty worktree: %+v", facts)
	}
	if facts.OpenQuestions == nil || facts.OpenAssumptions == nil || facts.Checks == nil {
		t.Errorf("what the ledger holds was not read: %+v", facts)
	}
	if record.Degraded || !strings.HasPrefix(record.Digest, "sha256:") {
		t.Errorf("record %+v", record)
	}
	if brief := specs[1].Brief; !strings.Contains(brief, "Fix the failing test") || !strings.Contains(brief, "handed off") {
		t.Errorf("brief:\n%s", brief)
	}
	view := r.view()
	var artifact ledger.Artifact
	for _, each := range view.Artifacts {
		if each.Kind == ledger.ArtifactHandoff {
			artifact = each
		}
	}
	if artifact.Digest != record.Digest || artifact.ID != record.ArtifactID {
		t.Errorf("artifacts %+v, record %+v", view.Artifacts, record)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

// A generation records why it began and what it read, and its identity is ropeshift's own.
func TestAGenerationRecordsItsStartAndItsHandoff(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.handOff(t)
	r.awaitSecondGeneration(t)
	generations := r.generations(t)
	if len(generations) != 2 {
		t.Fatalf("generations %+v", generations)
	}
	first, second := generations[0], generations[1]
	if first.StartReason != ledger.StartInitial || second.StartReason != ledger.StartHandoff {
		t.Errorf("reasons %s and %s", first.StartReason, second.StartReason)
	}
	record, _ := r.storedHandoff(t, 1)
	if first.HandoffOut != record.ArtifactID || second.HandoffIn != record.ArtifactID {
		t.Errorf("handoff links %+v", generations)
	}
	if first.EndReason != string(ledger.StartHandoff) || first.EndedAt == 0 {
		t.Errorf("first generation %+v", first)
	}
	if first.SessionID == "" || first.SessionID != second.SessionID {
		t.Errorf("one session name can span generations, and nothing is unique on it: %+v", generations)
	}
}

// r2 lets a step reach a rig that is still being built.
func r2(r **handoffRig, raw string) fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) { (*r).callHandoff(raw) }}
}

func TestATimedOutHandoffDegradesAndMovesOn(t *testing.T) {
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.worktrees().Dirty(stopWorktree) }}, completed(0.02))
	r.handOff(t)
	r.eventually("the wait", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
	r.clock.Advance(179 * time.Second)
	time.Sleep(60 * time.Millisecond)
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Fatalf("a handoff before its time ran out: %d sessions", len(specs))
	}
	r.clock.Advance(2 * time.Second)
	r.awaitSecondGeneration(t)
	checkDegraded(t, r.laneRig)
}

// checkDegraded is what every failure of the contract ends in: the deterministic part
// alone, a snapshot, and a next generation that is told.
func checkDegraded(t *testing.T, r *laneRig) {
	t.Helper()
	record, doc := r.storedHandoff(t, 1)
	if !record.Degraded || !doc.Degraded() {
		t.Errorf("record %+v, document %+v", record, doc)
	}
	if doc.Deterministic.SnapshotRef != lanes.CheckpointRef("lane-1", 1) {
		t.Errorf("no snapshot: %+v", doc.Deterministic)
	}
	if _, saved := r.worktrees().Snapshots()[lanes.CheckpointRef("lane-1", 1)]; !saved {
		t.Errorf("snapshots %+v", r.worktrees().Snapshots())
	}
	specs := r.driver.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d sessions", len(specs))
	}
	if brief := specs[1].Brief; !strings.Contains(brief, "left no narrative") || !strings.Contains(brief, lanes.CheckpointRef("lane-1", 1)) {
		t.Errorf("brief:\n%s", brief)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("a degraded handoff stopped the lane: %+v", change)
	}
}

func TestAMalformedNarrativeDegrades(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	var reply lanes.Reply
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) {
		r.worktrees().Dirty(stopWorktree)
		reply = r.callHandoff(`{"done":"not a list"}`)
	}}, completed(0.02))
	r.handOff(t)
	r.awaitSecondGeneration(t)
	checkDegraded(t, r.laneRig)
	if reply.OK || reply.Code != lanes.CodeInvalid {
		t.Errorf("reply %+v", reply)
	}
}

func TestASessionThatDiesDuringAHandoffDegrades(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.worktrees().Dirty(stopWorktree) }}, fake.DriverStep{Crash: true})
	r.handOff(t)
	r.awaitSecondGeneration(t)
	checkDegraded(t, r.laneRig)
	if charges, err := r.journal.Restarts(t.Context(), "lane-1"); err != nil || len(charges) != 1 || !charges[0].Planned {
		t.Errorf("a handoff that lost its session is no crash loop: %+v, %v", charges, err)
	}
}

func TestAPlaneThatIsAwayLeavesItsFactsOut(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	r.link.FailLaneFacts.Store(true)
	r.handOff(t)
	r.awaitSecondGeneration(t)
	_, doc := r.storedHandoff(t, 1)
	if doc.Deterministic.OpenQuestions != nil {
		t.Errorf("what nobody read reads as nothing open: %+v", doc.Deterministic)
	}
}

func TestAPreviousGenerationCannotWriteAfterAHandoff(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	token := r.token(t)
	r.handOff(t)
	r.awaitSecondGeneration(t)
	holder := workorders.Holder{Lease: r.grant.Lease, Lane: "lane-1", Generation: 1}
	_, err := control.NewLeases(r.store).Write(t.Context(), scope, holder, "late-write", nil, ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"})
	var fenced *workorders.FencedError
	if !errors.As(err, &fenced) {
		t.Errorf("a late write: %v", err)
	}
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: token, Report: &workorders.Report{Outcome: workorders.OutcomeCompleted}})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(t.Context(), line); reply.OK || reply.Code != lanes.CodeBadToken {
		t.Errorf("the last generation's token: %+v", reply)
	}
}

func TestAHandoffKeepsTheContractVersion(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	pinned := r.row().ContractVersion
	r.handOff(t)
	r.awaitSecondGeneration(t)
	_, doc := r.storedHandoff(t, 1)
	if doc.Deterministic.Contract == nil || doc.Deterministic.Contract.Version != pinned || pinned == 0 {
		t.Errorf("contract %+v, pinned %d", doc.Deterministic.Contract, pinned)
	}
	if got := r.row().ContractVersion; got != pinned {
		t.Errorf("the pin moved from %d to %d", pinned, got)
	}
}

func TestAnOpenQuestionGoesIntoTheHandoff(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	asked := r.ask(t, lanes.AskRequest{Text: "Which budget?", Blocking: true})
	if !asked.OK {
		t.Fatalf("reply %+v", asked)
	}
	r.handOff(t)
	r.awaitSecondGeneration(t)
	_, doc := r.storedHandoff(t, 1)
	if !slices.Equal(doc.Deterministic.OpenQuestions, []string{asked.Ask.QuestionID}) {
		t.Errorf("questions %v", doc.Deterministic.OpenQuestions)
	}
	if !strings.Contains(r.driver.Specs()[1].Brief, asked.Ask.QuestionID) {
		t.Errorf("brief:\n%s", r.driver.Specs()[1].Brief)
	}
}

func TestALaneAtNoRestCannotHandOff(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, handoffProfile)
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.awaitCharge(t)
	if err := r.subject.HandOff(t.Context(), "lane-1", lanes.HandoffContextThreshold); !errors.Is(err, worker.ErrNoHandoff) {
		t.Errorf("from backoff: %v", err)
	}
	r.stopWorker()
	r.subject.Wait()
	if err := r.subject.HandOff(t.Context(), "lane-2", lanes.HandoffContextThreshold); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("no such lane: %v", err)
	}
}

func TestASecondHandoffAsksNothing(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, r2(&r, narrativeText), completed(0.02))
	if err := r.subject.HandOff(t.Context(), "lane-1", lanes.HandoffContextThreshold); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.HandOff(t.Context(), "lane-1", "again"); !errors.Is(err, worker.ErrNoHandoff) {
		t.Errorf("a second handoff: %v", err)
	}
	close(r.release)
	r.awaitSecondGeneration(t)
	if sent := r.driver.Sent(); len(sent) != 1 {
		t.Errorf("sent %+v", sent)
	}
}

func TestANarrativeNobodyAskedForIsRefused(t *testing.T) {
	t.Parallel()
	r := atWork(t)
	close(r.release)
	if reply := r.callHandoff(narrativeText); reply.OK || reply.Code != lanes.CodeInvalid {
		t.Errorf("reply %+v", reply)
	}
}

func TestAnOutcomeDuringAHandoffEndsTheLane(t *testing.T) {
	t.Parallel()
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(spec ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(spec) }}, completed(0.02))
	r.handOff(t)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("a lane that reported went on: %d sessions", len(specs))
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

func TestAFencedLaneStartsNoNextGeneration(t *testing.T) {
	t.Parallel()
	r := atWork(t, fake.DriverStep{Hold: true})
	r.handOff(t)
	r.eventually("the wait", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
	r.loseLease(t)
	if _, err := r.subject.Beat(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("a fenced lane went on: %d sessions", len(specs))
	}
}

// workerDies ends this worker and starts another on the same journal, with a driver that
// finds what an earlier process left of the session.
func (r *handoffRig) workerDies(t *testing.T, left lanes.Session, script fake.DriverScript) (*worker.Worker, *fake.AgentDriver) {
	t.Helper()
	r.stopWorker()
	r.driver.StopEverySession()
	r.subject.Wait()
	driver := fake.NewAgentDriver()
	driver.Leave("lane-1", 1, left)
	driver.Play(script)
	driver.PlayAfter(r.toldToGoOn())
	config := *r.config
	config.Driver = driver
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		driver.StopEverySession()
		next.Wait()
	})
	return next, driver
}

// A restart reads the deadline from the journal, so a wait counts from the first process.
func TestARestartGoesOnWithAHandoff(t *testing.T) {
	t.Parallel()
	r := atWork(t)
	r.handOff(t)
	r.eventually("the wait", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
	found := lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}
	next, driver := r.workerDies(t, found, script())
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(4 * time.Minute)
	next.Wait()
	if sent := driver.Sent(); len(sent) != 0 {
		t.Errorf("the instruction went twice: %+v", sent)
	}
	record, doc := r.storedHandoff(t, 1)
	if !record.Degraded || doc.Reason != lanes.HandoffContextThreshold {
		t.Errorf("record %+v", record)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeCompleted {
		t.Errorf("outcome %+v", change)
	}
}

// A restart that finds a session reads on, so a narrative a session writes then still counts.
func TestANarrativeReachesTheWorkerThatTookOver(t *testing.T) {
	t.Parallel()
	r := atWork(t)
	r.handOff(t)
	r.eventually("the wait", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
	found := lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}
	token := r.token(t)
	next, _ := r.workerDies(t, found, script())
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: token, Handoff: json.RawMessage(narrativeText)})
	if err != nil {
		t.Fatal(err)
	}
	var reply lanes.Reply
	deadline := time.Now().Add(20 * time.Second)
	for !reply.OK && time.Now().Before(deadline) {
		reply = next.AnswerLane(t.Context(), line)
		time.Sleep(time.Millisecond)
	}
	if !reply.OK {
		t.Fatalf("reply %+v", reply)
	}
	next.Wait()
	record, doc := r.storedHandoff(t, 1)
	if record.Degraded || doc.Narrative == nil {
		t.Errorf("record %+v", record)
	}
}

// A session that went with the worker leaves a handoff nothing can read, and that handoff
// still finishes, because it degrades and cannot block.
func TestAHandoffWhoseSessionWentFinishesOnTheNextPass(t *testing.T) {
	t.Parallel()
	r := atWork(t)
	r.handOff(t)
	r.eventually("the wait", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
	next, driver := r.workerDies(t, lanes.Session{Found: true, Liveness: lanes.LivenessDead}, r.toldToGoOn())
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	next.Wait()
	specs := driver.Specs()
	if len(specs) != 1 || specs[0].Generation != 2 {
		t.Fatalf("specs %+v", specs)
	}
	record, _ := r.storedHandoff(t, 1)
	if !record.Degraded {
		t.Errorf("record %+v", record)
	}
	if !strings.Contains(specs[0].Brief, "left no narrative") {
		t.Errorf("brief:\n%s", specs[0].Brief)
	}
}

func TestADegradedHandoffShowsOnTheLane(t *testing.T) {
	t.Parallel()
	stays := func(*laneRig) fake.DriverScript { return script([]fake.DriverStep{{Bound: true}, {Hold: true}}) }
	r := atWorkBefore(t, stays, fake.DriverStep{Crash: true})
	r.handOff(t)
	r.eventually("the condition", func(view ledger.LaneView) bool {
		return slices.Contains(view.Conditions, string(lanes.HandoffDegraded)) && view.Generation == 2
	})
}
