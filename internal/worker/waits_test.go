package worker_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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

const waitProfile = profileAt + "[lifecycle]\nhandoff_timeout = \"30ms\"\n"

// publishSpend puts a spend policy in force, as a member's `policy publish` does.
func (r *laneRig) publishSpend(t *testing.T, policy authority.SpendPolicy) {
	t.Helper()
	draft := authority.SpendPolicyDraft{Body: fmt.Sprintf("%+v", policy), Policy: policy}
	if _, err := control.NewSpend(r.store).PublishSpendPolicy(t.Context(), scope, draft, member); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// clockAtDatabaseTime sets the worker's clock to the time the database keeps, as a daemon on one machine has them,
// so a wake that names a time on the worker's clock means that time to the ledger.
func (r *laneRig) clockAtDatabaseTime() {
	r.t.Helper()
	r.clock.Advance(time.Duration(time.Now().UnixMilli()-r.clock.Now().UnixMilli()) * time.Millisecond)
}

// reconcileUntil runs the worker's pass until a work order gets where a test says, which is how the daemon's
// own loop reaches it.
func (r *laneRig) reconcileUntil(t *testing.T, state workorders.State) workorders.WorkOrder {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var row workorders.WorkOrder
	for time.Now().Before(deadline) {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := r.subject.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		if row = r.row(); row.State == state {
			return row
		}
		r.clock.Advance(time.Millisecond)
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("work order %s, want %s; the lane reads %+v; the worker reported %v", row.State, state, r.read(), r.problems())
	return row
}

// FR-SPEND-1: a wait past the bound parks the lane within one pass, with a wake that fires at the end of the
// wait or when a person resumes it, and the lane's process is stopped.
func TestALaneWaitPastTheBoundParksTheLane(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	r.clockAtDatabaseTime()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.atGate(g)
	at := r.clock.Now().Add(30 * time.Minute).UnixMilli()

	reply := r.wait(t, 30)
	if !reply.OK || !reply.Parking {
		t.Fatalf("a 30 minute wait answers %+v", reply)
	}
	g.free()
	row := r.reconcileUntil(t, workorders.Parked)
	r.subject.Wait()
	if row.ClaimID != "" || row.LeaseWorker != "" {
		t.Errorf("the lease outlived the process: %+v", row)
	}
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.WaitWake(at) || !wake.Open() {
		t.Errorf("wake %+v, want %s", wake, lanes.WaitWake(at))
	}
	generations := r.generations(t)
	if len(generations) != 1 || generations[0].EndReason != workorders.EndParked {
		t.Errorf("generations %+v", generations)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil || record.State != lanes.StateParked || record.TokenDigest != "" {
		t.Errorf("the lane's session outlived its park: %+v, %v", record, err)
	}
}

// A wait inside the bound stays live, as it always did.
func TestALaneWaitInsideTheBoundStaysLive(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.atGate(g)
	if reply := r.wait(t, 5); !reply.OK || reply.Parking {
		t.Fatalf("a 5 minute wait answers %+v", reply)
	}
	for range 5 {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(superviseStep)
	}
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("a lane inside the bound reads %s", view.ActualState)
	}
}

// FR-SPEND-24: the bound is the project's spend policy, and a new version takes effect for the next lane.
func TestTheSpendPolicyMovesTheLiveWaitBound(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	policy := authority.DefaultSpendPolicy()
	policy.Version, policy.MaxLiveWait = 2, "10m"
	r.publishSpend(t, policy)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.atGate(g)
	if reply := r.wait(t, 9); !reply.OK || reply.Parking {
		t.Fatalf("a 9 minute wait under a 10 minute bound answers %+v", reply)
	}
	if reply := r.wait(t, 11); !reply.OK || !reply.Parking {
		t.Fatalf("an 11 minute wait under a 10 minute bound answers %+v", reply)
	}
}

// The compiled policy of a generation carries the project's wait settings, which the hook reads with no worker.
func TestAGenerationStartsWithTheProjectsWaitRules(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	policy := authority.DefaultSpendPolicy()
	policy.Version, policy.MaxToolSleep = 2, "90s"
	policy.WaitRulesOff = []string{"W4"}
	policy.WaitRulePatterns = map[string][]string{"W3": {"kubectl rollout status"}}
	r.publishSpend(t, policy)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.atGate(g)
	waits := r.driver.Specs()[0].Waits
	if waits.MaxToolSleepMs != 90_000 || len(waits.Off) != 1 || waits.Off[0] != "W4" || waits.Patterns["W3"][0] != "kubectl rollout status" {
		t.Errorf("the session started with %+v", waits)
	}
	if waits.Version != lanes.WaitRulesVersion {
		t.Errorf("the rule set reads version %d", waits.Version)
	}
}

// A hook that denied a wait tells the worker which rule did, and the ledger keeps a `wait_denied` event with the
// rule, the lane, and the generation, and no command text (FR-OBS-5).
func TestAWaitDenialIsAnEventWithNoCommand(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), completed(0.01)}))
	r.atGate(g)
	r.denyWait(t, "W3", "toolu_1")
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	var found []workorders.Change
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, e := range events {
			if e.Type != "work_order."+ledger.UploadWaitDenied {
				continue
			}
			var change workorders.Change
			if err := json.Unmarshal(e.Data, &change); err != nil {
				return err
			}
			found = append(found, change)
		}
		return err
	})
	if err != nil || len(found) != 1 {
		t.Fatalf("events %+v, %v", found, err)
	}
	var payload struct {
		Rule string `json:"rule"`
	}
	if err := json.Unmarshal(found[0].Payload, &payload); err != nil || payload.Rule != "W3" {
		t.Errorf("the payload reads %s, %v", found[0].Payload, err)
	}
	if found[0].Lane != "lane-1" || found[0].Generation != 1 {
		t.Errorf("the event names lane %q generation %d", found[0].Lane, found[0].Generation)
	}
	if strings.Contains(string(found[0].Payload), "watch") {
		t.Errorf("the event holds a command: %s", found[0].Payload)
	}
}

// denyWait is a hook that refused a call under a wait rule, reaching the worker as the hook sends it.
func (r *laneRig) denyWait(t *testing.T, rule, call string) {
	t.Helper()
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: r.token(t),
		Hook: &lanes.HookEvent{Event: "PreToolUse", Subject: call, Tool: "Bash", Detail: lanes.DetailDenied, WaitRule: rule, AtMs: r.clock.Now().UnixMilli()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(context.Background(), line); !reply.OK {
		t.Fatal(reply.Message)
	}
}

// polling is a session that sleeps and polls CI, is denied each time, then declares a long wait and ends its turn,
// as a lane told to wait on nothing does.
func (r *laneRig) polling() fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) {
		r.denyWait(r.t, "W2", "toolu_1")
		r.denyWait(r.t, "W1", "toolu_2")
		if reply := r.wait(r.t, 45); !reply.OK || !reply.Parking {
			r.t.Errorf("the wait answers %+v", reply)
		}
	}}
}

// NFR-COST-1: a lane that sleeps and polls is denied, ends its turn, and parks, and no model call follows the
// park: the narrative is the one instruction it is sent, no second generation starts, and the lane holds nothing.
func TestALaneThatPollsIsDeniedParksAndCostsNothingAfter(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	r.clockAtDatabaseTime()
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.polling(), completed(0.01)}, []fake.DriverStep{completed(0.01)}))
	r.start()
	r.reconcileUntil(t, workorders.Parked)
	r.subject.Wait()
	sent := r.driver.Sent()
	if len(sent) != 1 || sent[0].Text != lanes.HandoffInstruction() {
		t.Errorf("a parked lane was sent %+v", sent)
	}
	before := len(r.driver.Sent())
	for range 5 {
		r.clock.Advance(time.Minute)
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if specs := r.driver.Specs(); len(specs) != 1 || len(r.driver.Sent()) != before {
		t.Errorf("a model call followed the park: %d sessions, %d sends", len(specs), len(r.driver.Sent()))
	}
	if held, err := r.journal.Allocations(t.Context()); err != nil || len(held) != 0 {
		t.Errorf("a parked lane holds %+v, %v", held, err)
	}
}
