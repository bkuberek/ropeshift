package worker_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/parking"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The work order of these lanes has no estimate and no size, so its hard limit is the medium size's, $25, and the
// headroom of its client's own budget is a tenth of that.
const (
	hardLimit      = 25.0
	underTheLimit  = 10.0
	pastTheLimit   = 30.0
	budgetHeadroom = 2.5
)

// running is a usage report that shows the session's cost so far, in the middle of a turn.
func running(total float64) fake.DriverStep { return fake.DriverStep{Running: total} }

// aToolCall is a call the session's hook denies while a hold stands.
func aToolCall() fake.DriverStep { return fake.DriverStep{Call: func(ports.SessionSpec) {}} }

// spendLane is a lane under the handoff profile, in a run mode, on a work order whose limit is the default.
func spendLane(t *testing.T, mode string) *laneRig {
	t.Helper()
	r := laneRigWith(t, "run_mode = \""+mode+"\"\n"+handoffProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.waitForTheLane(t)
	})
	return r
}

// crossesMidTurn plays a session that works a long turn: it reports its cost, makes a tool call, reports that it
// passed the limit, waits at a gate for the test, and then tries two more tool calls. What a hold denies is what the
// test counts. The turn after it answers the instruction to hand off.
func (r *laneRig) crossesMidTurn(g *gate) {
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, running(underTheLimit), aToolCall(), running(pastTheLimit), g.step(), aToolCall(), aToolCall(), completed(pastTheLimit + 0.2)},
		[]fake.DriverStep{answer, completed(pastTheLimit + 0.3)},
	))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
}

func (r *laneRig) spendWake(t *testing.T) parking.Wake {
	t.Helper()
	return r.wakeOf(t, 1)
}

func (r *laneRig) spendOfWork(t *testing.T) ledger.WorkOrderSpend {
	t.Helper()
	spend, err := control.NewSpend(r.store).WorkOrderSpend(t.Context(), scope, r.id)
	if err != nil {
		t.Fatal(err)
	}
	return spend
}

// atItsHardLimit says the ledger reads the work order as at its hard limit.
func atItsHardLimit(spend ledger.WorkOrderSpend) bool {
	return slices.Contains(spend.Conditions, ledger.ConditionSpendLimit)
}

// firings counts the times a wake fired, from the events about its work order.
func (r *laneRig) firings(wake string) (int, error) {
	count := 0
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, e := range events {
			if e.Type != workorders.EventType(workorders.TriggerWakeFired) {
				continue
			}
			var change workorders.Change
			if err := json.Unmarshal(e.Data, &change); err != nil {
				return err
			}
			if string(change.Payload) == wake {
				count++
			}
		}
		return err
	})
	return count, err
}

// wakeIn reads a wake by its ID, and says so when none was stored.
func (r *laneRig) wakeIn(id string) (parking.Wake, error) {
	var wake parking.Wake
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		wake, err = tx.Wakes().Get(id)
		return err
	})
	return wake, err
}

// raise is a member raising the limit of the lane's work order.
func (r *laneRig) raise(t *testing.T, to float64) {
	t.Helper()
	if _, err := control.NewSpend(r.store).Raise(t.Context(), scope, control.SpendRaise{WorkOrder: r.id, ToUSD: to}, member); err != nil {
		t.Fatal(err)
	}
}

// A lane that crosses its work order's hard limit in the middle of a turn has its next tool calls denied, is asked once
// to hand off, and parks on a raise of the limit or a person, with the handoff for the lane that follows. Nothing else
// is asked of its model: after the report that showed the limit passed, the only call is the handoff's (NFR-COST-2).
func TestALaneCrossingItsHardLimitMidTurnHandsOffAndParks(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"headless", "tmux"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			r := spendLane(t, mode)
			g := newGate()
			r.gates(g)
			r.crossesMidTurn(g)
			r.reach(t, g)
			r.eventually("the next tool call is held", func(ledger.LaneView) bool { return len(r.driver.Holds()) == 1 })
			if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendLimit {
				t.Errorf("the lane reads %q", holds[0])
			}
			instruction := r.awaitSent(t, lanes.ToolLaneHandoff)
			if !instruction.MidTurn {
				t.Errorf("the instruction took the path of a turn that is over: %+v", instruction)
			}
			if r.driver.Denied() != 0 || r.driver.Calls() != 1 {
				t.Fatalf("%d calls ran and %d were denied before the lane went on", r.driver.Calls(), r.driver.Denied())
			}
			close(g.release)
			row := r.awaitOrder(t, workorders.Parked)
			r.subject.Wait()

			if row.ClaimID != "" || row.LeaseWorker != "" {
				t.Errorf("the lease outlived the process: %+v", row)
			}
			if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 1 || denied != 2 {
				t.Errorf("after the report that passed the limit %d calls ran and %d were denied, want none to run and two denied", calls-1, denied)
			}
			if sent := r.driver.Sent(); len(sent) != 1 || sent[0].Text != instruction.Text {
				t.Errorf("the model was called %d times after the report, want the one handoff: %+v", len(sent), sent)
			}
			if specs := r.driver.Specs(); len(specs) != 1 {
				t.Errorf("%d sessions started, and a lane at its limit starts no next generation", len(specs))
			}
			if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
				t.Errorf("wake %+v", wake)
			}
			record, doc := r.storedHandoff(t, 1)
			if record.Degraded || doc.Reason != string(lanes.ParkSpendLimit) || doc.Narrative == nil {
				t.Errorf("handoff %+v %+v", record, doc)
			}
			if spend := r.spendOfWork(t); spend.Charged.ChargedUSD < pastTheLimit || !atItsHardLimit(spend) {
				t.Errorf("the ledger reads %+v, and a lane the worker stopped at its limit is at it in the ledger too", spend)
			}
			// Parked at its limit, the work order stays parked: the ledger does not read its own stop as a raise.
			if err := r.subject.Upload(t.Context()); err != nil {
				t.Fatal(err)
			}
			if row := r.row(); row.State != workorders.Parked {
				t.Errorf("the work order reads %s", row.State)
			}
		})
	}
}

// A raise of the limit above what was spent wakes the lane once, and a second one wakes nothing more.
func TestARaisedLimitWakesTheLaneItParkedOnce(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	g := newGate()
	r.gates(g)
	r.crossesMidTurn(g)
	r.reach(t, g)
	close(g.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()

	// A limit still below what was charged is refused, so it neither wakes the lane nor reads as a raise.
	_, err := control.NewSpend(r.store).Raise(t.Context(), scope, control.SpendRaise{WorkOrder: r.id, ToUSD: hardLimit + 1}, member)
	if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "would stay below charged spend") {
		t.Fatalf("a limit of 26 over spend of 30 was answered %v", err)
	}
	if row := r.row(); row.State != workorders.Parked {
		t.Fatalf("a limit of 26 over spend of 30 woke the lane: %+v", row)
	}
	r.raise(t, 60)
	row := r.row()
	if row.State != workorders.Ready || row.ResumeLaneID != "lane-1" {
		t.Fatalf("a limit of 60 over spend of 30 left %+v", row)
	}
	if wake := r.spendWake(t); wake.Open() || wake.FiredBy == "" {
		t.Errorf("wake %+v", wake)
	}
	r.raise(t, 90)
	if n, err := r.firings(lanes.WakeID("lane-1", 1)); err != nil || n != 1 {
		t.Errorf("the lane woke %d times, want once: %v", n, err)
	}
}

// A lane that ends a turn over the limit parks from rest: nothing is held, because no tool call is under way, and the
// instruction goes to a session that is idle.
func TestALaneWhoseTurnEndsOverItsLimitParksFromRest(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, aToolCall(), completed(pastTheLimit)}, []fake.DriverStep{answer, completed(pastTheLimit + 0.1)}))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if sent := r.driver.Sent(); len(sent) != 1 || sent[0].MidTurn || sent[0].Text != lanes.HandoffInstruction() {
		t.Errorf("sent %+v", sent)
	}
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("a lane at rest was held: %q", holds)
	}
	if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
		t.Errorf("wake %+v", wake)
	}
}

// A lane under its limit is let alone, however much it spent, and a lane on a work order the policy leaves with no hard
// limit is held to none.
func TestALaneUnderItsLimitIsLeftAlone(t *testing.T) {
	t.Parallel()
	for name, limit := range map[string]string{"under the limit": "", "no limit at all": "0"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := spendLane(t, "headless")
			if limit != "" {
				publishSize(t, r, limit)
			}
			spent := underTheLimit
			if limit != "" {
				spent = 400
			}
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, running(spent / 2), aToolCall(), running(spent), r.reports(workorders.OutcomeCompleted), completed(spent)}))
			r.start()
			r.awaitOrder(t, workorders.Acceptance)
			if holds := r.driver.Holds(); len(holds) != 0 {
				t.Errorf("a lane that spent %v was held: %q", spent, holds)
			}
		})
	}
}

// publishSize publishes a version of the spend policy with a size limit for medium work.
func publishSize(t *testing.T, r *laneRig, limit string) {
	t.Helper()
	policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", limit)
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	if _, err := control.NewSpend(r.store).PublishSpendPolicy(t.Context(), scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, member); err != nil {
		t.Fatal(err)
	}
}

// A headless generation starts with its client's own budget cap, which is what remains of the hard limit and the
// headroom's share of the limit beyond it. A tmux lane has no such flag, and a lane with no limit has no cap.
func TestAHeadlessGenerationStartsWithABudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		mode  string
		size  string
		share string
		want  float64
	}{
		{"headless", "headless", "", "", hardLimit + budgetHeadroom},
		{"tmux", "tmux", "", "", 0},
		{"no limit", "headless", "0", "", 0},
		{"a larger headroom, set by a member", "headless", "", "0.3", hardLimit + 0.3*hardLimit},
		{"a headroom of none", "headless", "", "0", hardLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := spendLane(t, tt.mode)
			if tt.size != "" {
				publishSize(t, r, tt.size)
			}
			if tt.share != "" {
				policy, err := authority.DefaultSpendPolicy().Set("budget_flag_headroom_share", tt.share)
				if err != nil {
					t.Fatal(err)
				}
				policy.Version = 3
				if _, err := control.NewSpend(r.store).PublishSpendPolicy(t.Context(), scope, authority.SpendPolicyDraft{Body: policy.Body(), Policy: policy}, member); err != nil {
					t.Fatal(err)
				}
			}
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(1)}))
			r.start()
			r.awaitOrder(t, workorders.Acceptance)
			specs := r.driver.Specs()
			if len(specs) != 1 || fmt.Sprintf("%.4f", specs[0].MaxBudgetUSD) != fmt.Sprintf("%.4f", tt.want) {
				t.Errorf("the generation started with %+v, want a budget of %v", specs, tt.want)
			}
		})
	}
}

// A headless client that ended a turn at its own cap reached the limit and a little more, so the lane parks as at the
// limit. Its session has nothing to ask, so no narrative is asked of it.
func TestAClientThatReachedItsBudgetParksTheLane(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, aToolCall(), {Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: ports.ReasonMaxBudget, TotalUSD: 27.6}}}))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("a session that ended was asked for a narrative: %+v", sent)
	}
	record, doc := r.storedHandoff(t, 1)
	if !record.Degraded || doc.Reason != string(lanes.ParkSpendLimit) {
		t.Errorf("handoff %+v %+v", record, doc)
	}
	if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
		t.Errorf("wake %+v", wake)
	}
}

// A worker that dies after the lane crossed its limit and began to park leaves the wake stored once. The worker that
// takes over finishes the park, a raise wakes the lane once, and no second wake is stored: nothing is lost and nothing
// is doubled (NFR-REL-2). The chaos matrix kills real processes at each step of the same sequence.
func TestAWorkerThatDiesWhileAStopParksLeavesOneWake(t *testing.T) {
	t.Parallel()
	for name, left := range map[string]lanes.Session{
		"a session that outlived it": {Found: true, Liveness: lanes.LivenessAlive, Readable: true},
		"a session that went":        {Found: true, Liveness: lanes.LivenessDead},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := &handoffRig{laneRig: laneRigWith(t, "run_mode = \"headless\"\n"+handoffProfile), release: make(chan struct{}), reached: make(chan struct{})}
			t.Cleanup(func() {
				close(r.release)
				r.driver.StopEverySession()
				r.subject.Wait()
			})
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, running(pastTheLimit), {Do: func(ports.SessionSpec) {
				close(r.reached)
				<-r.release
			}}, completed(pastTheLimit + 0.2)}))
			r.driver.PlayAfter(r.toldToGoOn())
			r.start()
			<-r.reached
			r.eventually("the lane began its stop", func(ledger.LaneView) bool { return len(r.driver.Sent()) == 1 })
			if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
				t.Fatalf("wake %+v", wake)
			}
			next, _ := r.workerDies(t, left, script())
			if _, err := next.Reconcile(t.Context()); err != nil {
				t.Fatal(err)
			}
			r.clock.Advance(4 * time.Minute)
			next.Wait()
			r.awaitOrder(t, workorders.Parked)
			if wake := r.spendWake(t); !wake.Open() || wake.Expr != lanes.SpendLimitWake(r.id) {
				t.Errorf("after the takeover the wake reads %+v", wake)
			}
			r.raise(t, 60)
			if row := r.row(); row.State != workorders.Ready {
				t.Fatalf("a raise left the work order %s", row.State)
			}
			r.raise(t, 90)
			if n, err := r.firings(lanes.WakeID("lane-1", 1)); err != nil || n != 1 {
				t.Errorf("the lane woke %d times, want once: %v", n, err)
			}
			if _, err := r.wakeIn(lanes.WakeID("lane-1", 2)); err == nil {
				t.Error("a second wake was stored for the same stop")
			}
		})
	}
}
