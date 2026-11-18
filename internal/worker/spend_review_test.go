package worker_test

import (
	"math"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// holdsBy waits, on the clock a test lives by, for the driver to take this many holds, and says whether it did. It reads
// the driver and no ledger, so it can look while uploads are held.
func (r *laneRig) holdsBy(within time.Duration, want int) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if len(r.driver.Holds()) >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return len(r.driver.Holds()) >= want
}

// A tool call must not start between the report that showed the limit passed and the instruction to hand off, and the
// first step of a park is an upload that can take as long as its bound. The hold is armed before that step, so a slow
// control plane leaves no window (NFR-COST-2).
func TestNoToolCallStartsAfterTheCrossingEvenWithASlowUpload(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	g1, g2 := newGate(), newGate()
	r.gates(g1, g2)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, running(underTheLimit), aToolCall(), g1.step(), running(pastTheLimit), g2.step(), aToolCall(), aToolCall(), completed(pastTheLimit + 0.2)},
		[]fake.DriverStep{answer, completed(pastTheLimit + 0.3)},
	))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.reach(t, g1)
	// The session is bound and its first events are in the ledger before the control plane turns slow.
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	release := r.link.HoldUploads()
	t.Cleanup(release)
	close(g1.release)
	r.reach(t, g2)
	if !r.holdsBy(time.Second, 1) {
		t.Fatal("the lane's next tool call was still allowed a second after the report that showed its limit passed, with the upload that opens a park held")
	}
	if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendLimit {
		t.Errorf("the lane reads %q", holds[0])
	}
	release()
	close(g2.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 1 || denied != 2 {
		t.Errorf("after the report that passed the limit %d calls ran and %d were denied, want none to run and two denied", calls-1, denied)
	}
}

// A lane whose work order's limit its worker cannot read is run for a short while, because a read that failed once says
// nothing, and then held and parked with a reason of its own, because a limit nobody can read is no limit. It asks again
// at each report once a span has passed, and a lane that gets its answer in time is not held.
func TestALaneWhoseAllowanceStaysUnreadableIsHeld(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	r.link.FailLaneFacts.Store(true)
	g1, g2 := newGate(), newGate()
	r.gates(g1, g2)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, g1.step(), running(underTheLimit), g2.step(), aToolCall(), completed(underTheLimit + 0.2)},
		[]fake.DriverStep{answer, completed(underTheLimit + 0.3)},
	))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.reach(t, g1)
	r.clock.Advance(40 * time.Second)
	close(g1.release)
	r.reach(t, g2)
	if !r.holdsBy(2*time.Second, 1) {
		t.Fatal("a lane whose limit stayed unreadable for 40 seconds was not held")
	}
	if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendUnreadable {
		t.Errorf("the lane reads %q", holds[0])
	}
	close(g2.release)
	// It is under its limit, so the ledger wakes it as soon as it can say so.
	r.awaitOrder(t, workorders.Ready)
	r.subject.Wait()
	if row := r.row(); row.ResumeLaneID != "lane-1" {
		t.Errorf("the lane was not left to resume: %+v", row)
	}
	if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 0 || denied != 1 {
		t.Errorf("%d calls ran and %d were denied after the lane was held", calls, denied)
	}
	_, doc := r.storedHandoff(t, 1)
	if doc.Reason != string(lanes.ParkSpendUnreadable) {
		t.Errorf("the handoff reads %q", doc.Reason)
	}
}

// A read that fails and then answers inside the span leaves the lane to run, and a lane that was never in doubt does too.
func TestALaneWhoseAllowanceCameBackInTimeIsNotHeld(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	r.link.FailLaneFacts.Store(true)
	g1 := newGate()
	r.gates(g1)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g1.step(), running(underTheLimit), aToolCall(), r.reports(workorders.OutcomeCompleted), completed(underTheLimit + 0.2)}))
	r.start()
	r.reach(t, g1)
	r.clock.Advance(6 * time.Second)
	r.link.FailLaneFacts.Store(false)
	close(g1.release)
	r.awaitOrder(t, workorders.Acceptance)
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("a lane whose allowance was read in time was held: %q", holds)
	}
}

// A work order already at its limit when a generation starts has no turn to run: the generation parks before its first
// turn, with no process and no model call, and not with a budget cap of its headroom alone (FR-SPEND-5).
func TestAGenerationThatBeginsAtItsLimitParksBeforeItsFirstTurn(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	err := r.store.Write(t.Context(), scope, "spend.fixture", func(tx ports.WriteTx) error {
		if err := tx.Lanes().Start("lane-prior", r.id, 1, ledger.LanePolicies{}); err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: "lane-prior", Number: 1, StartReason: ledger.StartInitial}); err != nil {
			return err
		}
		if _, err := tx.Lanes().ReportUsage("lane-prior", 1, ledger.GenerationUsage{CostUSD: pastTheLimit, KnownTurns: 3}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: member, Subject: ledger.Subject{Kind: "note", ID: "prior"}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, aToolCall(), completed(0.2)}))
	r.start()
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Errorf("a generation with nothing left to spend started %d sessions", len(specs))
	}
	if sent := r.driver.Sent(); len(sent) != 0 || r.driver.Calls() != 0 {
		t.Errorf("a model was called: %+v, %d calls", sent, r.driver.Calls())
	}
	if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
		t.Errorf("wake %+v", wake)
	}
	if record, doc := r.storedHandoff(t, 1); !record.Degraded || doc.Reason != string(lanes.ParkSpendLimit) {
		t.Errorf("handoff %+v %+v", record, doc)
	}
}

// A lane that crosses its limit while the context policy's handoff is in flight is skipped by the stop, which leaves
// the handoff alone. The generation that follows starts at the limit, and parks before its first turn.
func TestALaneThatCrossedDuringAHandoffParksBeforeItsNextGeneration(t *testing.T) {
	t.Parallel()
	g := newGate()
	r := contextLane(t)
	r.gates(g)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(185), running(pastTheLimit), g.step(), completed(pastTheLimit + 0.2)},
		[]fake.DriverStep{answer, completed(pastTheLimit + 0.3)},
	))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.reach(t, g)
	input := r.awaitSent(t, lanes.ToolLaneHandoff)
	r.admit(input)
	close(g.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions started, and the generation after the handoff had nothing left to spend", len(specs))
	}
	if wake := r.wakeOf(t, 2); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
		t.Errorf("the second generation's wake is %+v", wake)
	}
	if generations := r.generations(t); len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff {
		t.Errorf("generations %+v", generations)
	}
}

// A lane resumed on a worker whose journal knows nothing of what it spent before it parked is held to its limit by what the
// ledger holds of that spend: its earlier generation's 20 and the 6 it spends now pass $25, though the worker has seen
// only the 6. The cap its client starts with is what remains after the 20, and a headroom of a tenth of the limit.
func TestALaneResumedOnAnotherWorkerIsHeldByWhatTheLedgerHolds(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	reached := make(chan struct{})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, running(20), {Do: func(ports.SessionSpec) { close(reached) }}, {Hold: true}}))
	r.start()
	<-reached
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	if err := r.subject.Park(t.Context(), "lane-1", lanes.ParkRequest{Trigger: lanes.ParkFleetDrain, Expr: lanes.HumanWake(), SkipNarrative: true}); err != nil {
		t.Fatal(err)
	}
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if spend := r.spendOfWork(t); spend.Charged.ChargedUSD < 20 {
		t.Fatalf("the ledger charged %+v for the parked generation", spend.Charged)
	}
	r.driver.StopEverySession()
	r.wake(t)

	// A second machine on the same control plane, with a journal that never saw the lane, takes the resume.
	second := newGate()
	driver := fake.NewAgentDriver()
	driver.Play(script([]fake.DriverStep{{Bound: true}, running(6), second.step(), aToolCall(), aToolCall(), completed(6.2)}))
	t.Cleanup(driver.StopEverySession)
	other, _, _ := r.otherWorker(t, driver)
	t.Cleanup(other.Wait)
	second.letGoOnFailure(t)
	r.shiftDatabase(t, control.AffinityDelayMs+1000)
	if _, err := control.NewWakes(r.store).WakeTick(t.Context(), scope, member); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := other.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := other.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-second.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the second worker never ran the lane")
	}
	specs := driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions on the second worker", len(specs))
	}
	// 25 less the 20 the first generation was charged, and a tenth of 25 beyond it.
	if want := hardLimit - 20 + budgetHeadroom; math.Abs(specs[0].MaxBudgetUSD-want) > 0.01 {
		t.Errorf("the resumed generation's client starts with a cap of %v, want %v", specs[0].MaxBudgetUSD, want)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(driver.Holds()) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if holds := driver.Holds(); len(holds) != 1 || holds[0] != lanes.ReasonSpendLimit {
		t.Fatalf("the resumed lane passed its limit with the 20 of its earlier generation unknown to its worker, and its holds read %q", holds)
	}
	close(second.release)
	deadline = time.Now().Add(5 * time.Second)
	for driver.Denied() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls, denied := driver.Calls(), driver.Denied(); calls != 0 || denied != 2 {
		t.Errorf("after the report that passed the limit %d calls ran and %d were denied, want none to run and two denied", calls, denied)
	}
}

// What a turn's calls were estimated at is a bound, and the turn's end carries the exact total: a lane that was estimated
// high is charged what its turn cost, and the ledger holds that.
func TestATurnsExactCostReplacesWhatItsCallsWereEstimatedAt(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	publishSize(t, r, "1000")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, running(60), aToolCall(), r.reports(workorders.OutcomeCompleted), completed(12)}))
	r.start()
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	generations := r.generations(t)
	if len(generations) != 1 || math.Abs(generations[0].Usage.CostUSD-12) > 1e-9 {
		t.Errorf("a turn estimated at 60 that cost 12 left the generation at %+v", generations)
	}
	var _ = control.NewSpend
	var _ = authority.ActionRaiseSpendLimit
}
