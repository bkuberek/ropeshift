package worker_test

import (
	"fmt"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// aToolCallStarts is a session's hook saying a tool call is about to start, which reaches the worker after the hook has
// decided the call, as a real one does.
func (r *laneRig) aToolCallStarts(t *testing.T, subject string) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventPreToolUse, Subject: subject, Tool: "Bash", AtMs: r.clock.Now().UnixMilli()})
}

// tookTheUsage waits for the worker to have taken what a session said up to its first usage report. The worker reads a
// stream in order and a usage report leaves a measurement of the session's context, so the measurement shows that the
// reports before it, a cost among them, were taken, which a test that changes a limit afterwards needs.
func (r *laneRig) tookTheUsage(t *testing.T) {
	t.Helper()
	r.eventually("the first usage report taken", func(ledger.LaneView) bool {
		return len(r.telemetry.Measurements(ledger.MetricContextTokens)) > 0
	})
}

// unpricedLimit is a limit a member sets while a lane works, and what the lane parks on when it stops there.
type unpricedLimit struct {
	// arrange is what the ledger holds when the lane starts, and publish is what a member changes while it works.
	arrange, publish func(t *testing.T, r *laneRig)
	// before is what the session reports before the member changes the limit.
	before []fake.DriverStep
	// wake is the expression the lane's wake carries.
	wake func(t *testing.T, r *laneRig) string
}

// unpricedTrigger is what tells the worker a call is coming, in a session whose calls report no cost it can price: no
// running total moves, so none of the reports that carry a cost reaches the worker.
type unpricedTrigger struct {
	// report is what the session says after the member changed the limit, and ask is what a test does while the session
	// waits for it, as a session's hook would.
	report []fake.DriverStep
	ask    func(t *testing.T, r *laneRig)
}

// A lane whose calls report no cost it can price is held at its next tool call once its limit is passed, and does not wait
// for its turn to end to learn it. The session's running total stays where it was, as one whose calls cannot be priced does
// and as a pane whose cost line does not change does, so what shows the lane its limit is not what it spent: a member lowered
// its work order's limit under what it had spent, or its project's limit under what the day spent. The lane reads the limit
// again when a usage report or a tool call's hook says work goes on, and parks on the limit it found (NFR-COST-2, FR-SPEND-5).
func TestALimitPassedWhileTheCallsReportNoPriceableCostHoldsTheNextToolCall(t *testing.T) {
	t.Parallel()
	limits := map[string]unpricedLimit{
		"the work order's limit": {
			before:  []fake.DriverStep{running(0.4)},
			publish: func(t *testing.T, r *laneRig) { publishSize(t, r, "0.3") },
			wake:    func(_ *testing.T, r *laneRig) string { return lanes.SpendLimitWake(r.id) },
		},
		"the project's limit": {
			arrange: func(t *testing.T, r *laneRig) { spendInProject(t, r.store, scope, spentByTheOther) },
			publish: func(t *testing.T, r *laneRig) { projectLimitOf(t, r.store, 5, 0) },
			wake:    func(t *testing.T, r *laneRig) string { return lanes.ProjectLimitWake(r.databaseDayEnd(t)) },
		},
	}
	triggers := map[string]unpricedTrigger{
		"a usage report": {report: []fake.DriverStep{tokens(3)}},
		"a tool call's hook": {ask: func(t *testing.T, r *laneRig) {
			r.aToolCallStarts(t, "t-1")
		}},
	}
	for limitName, limit := range limits {
		for triggerName, trigger := range triggers {
			for _, mode := range []string{"headless", "tmux"} {
				t.Run(limitName+" by "+triggerName+" in "+mode, func(t *testing.T) {
					t.Parallel()
					r := spendLane(t, mode)
					if limit.arrange != nil {
						limit.arrange(t, r)
					}
					g1, g2 := newGate(), newGate()
					r.gates(g1, g2)
					answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
					first := append([]fake.DriverStep{{Bound: true}}, limit.before...)
					first = append(first, tokens(1), g1.step())
					first = append(first, trigger.report...)
					first = append(first, g2.step(), aToolCall(), aToolCall(), completed(0.5))
					r.driver.Play(script(first, []fake.DriverStep{answer, completed(0.6)}))
					r.driver.PlayAfter(r.toldToGoOn())
					r.start()
					r.reach(t, g1)
					r.tookTheUsage(t)
					limit.publish(t, r)
					r.clock.Advance(2 * time.Minute)
					if trigger.ask != nil {
						trigger.ask(t, r)
					}
					close(g1.release)
					r.reach(t, g2)
					if !r.holdsBy(5*time.Second, 1) {
						t.Fatal("the lane's next tool call was not held, though its limit was passed and its turn had not ended")
					}
					if holds := r.driver.Holds(); holds[0] != lanes.ReasonSpendLimit {
						t.Errorf("the lane reads %q", holds[0])
					}
					close(g2.release)
					r.awaitOrder(t, workorders.Parked)
					r.subject.Wait()
					if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 0 || denied != 2 {
						t.Errorf("after the limit was passed %d calls ran and %d were denied, want none to run and two denied", calls, denied)
					}
					if wake := r.spendWake(t); !wake.Open() || wake.Expr != limit.wake(t, r) {
						t.Errorf("wake %+v, want %q", wake, limit.wake(t, r))
					}
					if _, doc := r.storedHandoff(t, 1); doc.Reason != string(lanes.ParkSpendLimit) {
						t.Errorf("the handoff reads %q", doc.Reason)
					}
				})
			}
		}
	}
}

// A lane under its limit does not read its allowance again for each of the tool calls it makes: the reads keep the cadence
// the lane's other reports have, so a burst of calls costs the control plane at most one read.
func TestToolCallsOfALaneUnderItsLimitCostTheControlPlaneOneReadInASpan(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	g1, g2 := newGate(), newGate()
	r.gates(g1, g2)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g1.step(), g2.step(), r.reports(workorders.OutcomeCompleted), completed(0.5)}))
	r.start()
	r.reach(t, g1)
	reads := r.link.LaneFactsReads.Load()
	r.clock.Advance(2 * time.Minute)
	for i := range 50 {
		r.aToolCallStarts(t, fmt.Sprint("first-", i))
	}
	r.eventually("the allowance read once", func(ledger.LaneView) bool { return r.link.LaneFactsReads.Load() > reads })
	close(g1.release)
	r.reach(t, g2)
	for i := range 50 {
		r.aToolCallStarts(t, fmt.Sprint("second-", i))
	}
	close(g2.release)
	r.awaitOrder(t, workorders.Acceptance)
	if got := r.link.LaneFactsReads.Load() - reads; got != 1 {
		t.Errorf("100 tool calls within a span of the clock cost the control plane %d reads of the lane's facts, want 1", got)
	}
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("a lane under its limit was held: %q", holds)
	}
}
