package worker_test

import (
	"fmt"
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

// The lanes of these tests are on work orders whose own limit is the default, $25, and their project has a daily limit of
// $10 that its other lanes have spent most of.
const (
	dailyLimit      = 10.0
	spentByTheOther = 9.5
)

// projectLimitOf publishes the spend policy of a project that has a daily limit, and spends some of the day as the
// project's other lanes did, into a ledger's tally.
func projectLimitOf(t *testing.T, store ports.Store, limit, spent float64) {
	t.Helper()
	projectLimitIn(t, store, scope, limit, spent)
}

// projectLimitIn is the same for a named project of an installation.
func projectLimitIn(t *testing.T, store ports.Store, project ledger.WorkspaceID, limit, spent float64) {
	t.Helper()
	policy, err := authority.DefaultSpendPolicy().Set("project_daily_limit", fmt.Sprint(limit))
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	if _, err := control.NewSpend(store).PublishSpendPolicy(t.Context(), project, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, member); err != nil {
		t.Fatal(err)
	}
	spendInProject(t, store, project, spent)
}

// spendInProject adds to what a project's lanes were charged today.
func spendInProject(t *testing.T, store ports.Store, project ledger.WorkspaceID, usd float64) {
	t.Helper()
	err := store.Write(t.Context(), project, "spend.fixture", func(tx ports.WriteTx) error {
		if _, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: member, Subject: ledger.Subject{Kind: "note", ID: fmt.Sprint("spent-", usd)}, Data: []byte(`{}`)}); err != nil {
			return err
		}
		return tx.Lanes().AddSpend(ledger.SpendScopeWork, usd)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// databaseNow reads the ledger's clock, which is the one a project's day is read by.
func (r *laneRig) databaseNow(t *testing.T) int64 {
	t.Helper()
	var now int64
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		now, err = tx.Now()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return now
}

// databaseDayEnd is when the ledger's clock reads the day over.
func (r *laneRig) databaseDayEnd(t *testing.T) int64 {
	t.Helper()
	return ledger.WindowEnd(ledger.WindowDay, r.databaseNow(t))
}

// toldTheLedger waits for the ledger to hold this much of the generation's spend, which a lane in a project with a limit
// tells it in the middle of a turn and a lane in a project with none tells it when the generation ends.
func (r *laneRig) toldTheLedger(usd float64) {
	r.t.Helper()
	r.eventually("the generation's spend reported", func(view ledger.LaneView) bool {
		return len(view.Generations) > 0 && view.Generations[0].Usage.CostUSD >= usd
	})
}

// A lane whose project passes its daily limit while it works is held at its next tool call and parks on the project's
// limit, with the handoff for the lane that follows: the lane's own work order is far under its limit, and what stops it
// is what the project's lanes spent (FR-SPEND-6). Its wake is the project's limit rising or the day ending.
func TestAProjectPastItsDailyLimitParksARunningLaneAtItsNextToolCall(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"headless", "tmux"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := spendLane(t, mode)
			projectLimitOf(t, r.store, dailyLimit, spentByTheOther)
			g1, g2 := newGate(), newGate()
			r.gates(g1, g2)
			answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
			r.driver.Play(script(
				[]fake.DriverStep{{Bound: true}, running(0.4), aToolCall(), g1.step(), running(1.2), g2.step(), aToolCall(), aToolCall(), completed(1.3)},
				[]fake.DriverStep{answer, completed(1.4)},
			))
			r.driver.PlayAfter(r.toldToGoOn())
			r.start()
			// The first report tells the ledger what the lane spent, and a minute later the next one passes the project's limit.
			r.reach(t, g1)
			r.toldTheLedger(0.4)
			r.clock.Advance(2 * time.Minute)
			close(g1.release)
			r.reach(t, g2)
			r.eventually("the next tool call is held", func(ledger.LaneView) bool { return len(r.driver.Holds()) == 1 })
			if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendLimit {
				t.Errorf("the lane reads %q", holds[0])
			}
			instruction := r.awaitSent(t, lanes.ToolLaneHandoff)
			if !instruction.MidTurn {
				t.Errorf("the instruction took the path of a turn that is over: %+v", instruction)
			}
			close(g2.release)
			r.awaitOrder(t, workorders.Parked)
			r.subject.Wait()
			if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 1 || denied != 2 {
				t.Errorf("after the report that passed the project's limit %d calls ran and %d were denied, want none to run and two denied", calls-1, denied)
			}
			if specs := r.driver.Specs(); len(specs) != 1 {
				t.Errorf("%d sessions started, and a lane a limit parked starts no next generation", len(specs))
			}
			end := r.databaseDayEnd(t)
			if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.ProjectLimitWake(end) || wake.NextEvalAt != end {
				t.Errorf("wake %+v, want the project's limit or the end of the day, %d", wake, end)
			}
			if record, doc := r.storedHandoff(t, 1); record.Degraded || doc.Reason != string(lanes.ParkSpendLimit) || doc.Narrative == nil {
				t.Errorf("handoff %+v %+v", record, doc)
			}
			if spend := r.spendOfWork(t); atItsHardLimit(spend) {
				t.Errorf("the work order reads %+v, and its own limit is not what stopped it", spend)
			}
		})
	}
}

// A project under its limit holds no lane, and the lane keeps the ledger current with what it spends while it works: the
// minute's report is in the project's tally before the generation ends.
func TestAProjectUnderItsDailyLimitHoldsNoLane(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	projectLimitOf(t, r.store, dailyLimit, 1)
	g1, g2 := newGate(), newGate()
	r.gates(g1, g2)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, running(0.4), g1.step(), running(1.2), aToolCall(), g2.step(), r.reports(workorders.OutcomeCompleted), completed(1.3),
	}))
	r.start()
	r.reach(t, g1)
	r.toldTheLedger(0.4)
	r.clock.Advance(2 * time.Minute)
	close(g1.release)
	r.reach(t, g2)
	r.toldTheLedger(1.2)
	var tally float64
	if err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		tally, err = tx.Lanes().SpendSince(ledger.SpendScopeWork, 0)
		return err
	}); err != nil || tally < 2.2-1e-6 {
		t.Errorf("the project's tally reads %v with the lane's 1.2 and the other lanes' 1 told, %v", tally, err)
	}
	close(g2.release)
	r.awaitOrder(t, workorders.Acceptance)
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("a lane in a project with room was held: %q", holds)
	}
}

// A generation that begins in a project already past its limit has no turn to run: it parks before its first turn with
// no process and no model call, on the project's limit, and a day that ends wakes it once.
func TestAGenerationThatBeginsInAHeldProjectParksBeforeItsFirstTurnAndWakesWhenTheDayEnds(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	projectLimitOf(t, r.store, dailyLimit, dailyLimit)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, aToolCall(), completed(0.2)}))
	r.start()
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Errorf("a generation in a held project started %d sessions", len(specs))
	}
	if sent := r.driver.Sent(); len(sent) != 0 || r.driver.Calls() != 0 {
		t.Errorf("a model was called: %+v, %d calls", sent, r.driver.Calls())
	}
	end := r.databaseDayEnd(t)
	wake := r.spendWake(t)
	if !wake.Open() || wake.Expr != lanes.ProjectLimitWake(end) {
		t.Fatalf("wake %+v", wake)
	}
	if record, doc := r.storedHandoff(t, 1); !record.Degraded || doc.Reason != string(lanes.ParkSpendLimit) {
		t.Errorf("handoff %+v %+v", record, doc)
	}
	// Still the day: a tick wakes nothing. The day over: it wakes the lane once, and a tick after that wakes nothing more.
	wakes := control.NewWakes(r.store)
	if fired, err := wakes.WakeTick(t.Context(), scope, member); err != nil || len(fired) != 0 {
		t.Fatalf("a tick before the day ended fired %v, %v", fired, err)
	}
	r.shiftDatabase(t, end-r.databaseNow(t)+1)
	for range 2 {
		if _, err := wakes.WakeTick(t.Context(), scope, member); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := r.firings(wake.ID); err != nil || n != 1 {
		t.Errorf("the wake fired %d times when the day ended, want once: %v", n, err)
	}
	if row := r.row(); row.State != workorders.Ready {
		t.Errorf("the work order reads %s after its day ended", row.State)
	}
}

// A member raising the project's limit wakes the lane it parked once, and the day ending after that wakes nothing.
func TestARaisedProjectLimitWakesTheLaneItParkedOnce(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	projectLimitOf(t, r.store, dailyLimit, dailyLimit)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, aToolCall(), completed(0.2)}))
	r.start()
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	wake := r.spendWake(t)
	spend := control.NewSpend(r.store)
	// A raise that leaves the limit at what is in force lifts nothing, and wakes no one.
	if _, err := spend.RaiseProject(t.Context(), scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: dailyLimit}, member); err == nil {
		t.Fatal("a raise to the limit already in force was taken")
	}
	if n, err := r.firings(wake.ID); err != nil || n != 0 {
		t.Fatalf("a refused raise woke the lane %d times: %v", n, err)
	}
	if _, err := spend.RaiseProject(t.Context(), scope, control.ProjectRaise{Window: ledger.WindowDay, ToUSD: 20}, member); err != nil {
		t.Fatal(err)
	}
	if n, err := r.firings(wake.ID); err != nil || n != 1 {
		t.Fatalf("a raise to $20 woke the lane %d times, want once: %v", n, err)
	}
	r.shiftDatabase(t, r.databaseDayEnd(t)-r.databaseNow(t)+1)
	if _, err := control.NewWakes(r.store).WakeTick(t.Context(), scope, member); err != nil {
		t.Fatal(err)
	}
	if n, err := r.firings(wake.ID); err != nil || n != 1 {
		t.Errorf("the day's end woke the lane again: %d firings, %v", n, err)
	}
}

// A member who sets or lowers a project's limit while lanes work reaches them in their current generation, at their next
// tool call: a lane that started in a project with no limit asks the ledger again about once a minute, and does not wait for
// its generation to end. The first case lowers the limit under what the project already spent. In the second the limit is
// above it, and the lane's own spend, which the ledger had not been told, takes the project past it once the lane reports.
func TestALimitPublishedWhileALaneWorksHoldsItAtItsNextToolCall(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct{ limit float64 }{
		"a limit under what the day spent":                    {limit: 5},
		"a limit the lane's own spend takes the project past": {limit: dailyLimit},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := spendLane(t, "headless")
			spendInProject(t, r.store, scope, spentByTheOther)
			g1, g2 := newGate(), newGate()
			r.gates(g1, g2)
			answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
			r.driver.Play(script(
				[]fake.DriverStep{{Bound: true}, g1.step(), running(1.2), g2.step(), aToolCall(), aToolCall(), completed(1.3)},
				[]fake.DriverStep{answer, completed(1.4)},
			))
			r.driver.PlayAfter(r.toldToGoOn())
			r.start()
			// The generation began in a project with no limit, so its allowance holds none.
			r.reach(t, g1)
			projectLimitOf(t, r.store, tt.limit, 0)
			r.clock.Advance(2 * time.Minute)
			close(g1.release)
			r.reach(t, g2)
			if !r.holdsBy(5*time.Second, 1) {
				t.Fatal("a limit published while the lane worked never reached it: its next tool call was not held")
			}
			if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendLimit {
				t.Errorf("the lane reads %q", holds[0])
			}
			close(g2.release)
			r.awaitOrder(t, workorders.Parked)
			r.subject.Wait()
			if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 0 || denied != 2 {
				t.Errorf("after the report that found the project held %d calls ran and %d were denied, want none to run and two denied", calls, denied)
			}
			end := r.databaseDayEnd(t)
			if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.ProjectLimitWake(end) {
				t.Errorf("wake %+v, want the project's limit or the end of the day, %d", wake, end)
			}
		})
	}
}

// A lane that cannot hear from the control plane for longer than the grace does not keep the allowance it last read, which
// said the project had room: the allowance is unknown, and the lane is held and parked as one whose work order's limit it
// cannot read is, with a reason of its own (FR-SPEND-5).
func TestAProjectAllowanceThatCannotBeRefreshedIsHeldAfterTheGrace(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	projectLimitOf(t, r.store, dailyLimit, 1)
	g1, g2 := newGate(), newGate()
	r.gates(g1, g2)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, g1.step(), running(0.4), g2.step(), aToolCall(), completed(0.5)},
		[]fake.DriverStep{answer, completed(0.6)},
	))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.reach(t, g1)
	r.link.FailLaneFacts.Store(true)
	r.clock.Advance(40 * time.Second)
	close(g1.release)
	r.reach(t, g2)
	if !r.holdsBy(5*time.Second, 1) {
		t.Fatal("a lane whose project allowance stayed unreadable for 40 seconds kept the allowance it last read")
	}
	if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendUnreadable {
		t.Errorf("the lane reads %q", holds[0])
	}
	close(g2.release)
	// Its work order is under its limit and so is its project, so the ledger wakes it as soon as it can say so.
	r.awaitOrder(t, workorders.Ready)
	r.subject.Wait()
	if _, doc := r.storedHandoff(t, 1); doc.Reason != string(lanes.ParkSpendUnreadable) {
		t.Errorf("the handoff reads %q", doc.Reason)
	}
}

// A lane whose reads keep answering is never held for the age of its allowance: the span is how long it may go with no
// answer, and not how long it may go with no event.
func TestAProjectAllowanceThatKeepsBeingReadIsNeverHeldForItsAge(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	projectLimitOf(t, r.store, dailyLimit, 1)
	g1 := newGate()
	r.gates(g1)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g1.step(), running(0.4), aToolCall(), r.reports(workorders.OutcomeCompleted), completed(0.5)}))
	r.start()
	r.reach(t, g1)
	r.clock.Advance(10 * time.Minute)
	close(g1.release)
	r.awaitOrder(t, workorders.Acceptance)
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("a lane the control plane answered was held: %q", holds)
	}
}
