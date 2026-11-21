package worker_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// reload is a session's `ConfigChange` hook asking the worker whether its agent CLI may apply what changed.
func (r *laneRig) reload() lanes.Reply {
	r.t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(r.t), Reload: true})
	if err != nil {
		r.t.Error(err)
	}
	return r.subject.AnswerLane(context.Background(), line)
}

// reloads is a step of a session that changes its own settings as change says, which nil leaves as the driver has them,
// and whose hook then asks about the reload, with the worker's answer sent on replies.
func (r *laneRig) reloads(change *lanes.SettingsChange, replies chan<- lanes.Reply) fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) {
		if change != nil {
			r.driver.ChangeSettings(change)
		}
		replies <- r.reload()
	}}
}

// answered is the worker's answer to a session's reload.
func answered(t *testing.T, replies <-chan lanes.Reply) lanes.Reply {
	t.Helper()
	select {
	case reply := <-replies:
		return reply
	case <-time.After(20 * time.Second):
		t.Fatal("the session's hook never asked about its reload")
		return lanes.Reply{}
	}
}

// A lane whose session changes its own settings in a key that widens what it runs has the reload blocked, so the
// session never applies the new hook or permission: its next tool calls are denied, it is asked once to hand off, and
// it parks on a member's choice with the same security item a held start raises, for the generation that ran.
func TestALaneThatWidensItsSettingsMidSessionIsBlockedAndHeld(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, handoffProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.waitForTheLane(t)
	})
	g := newGate()
	r.gates(g)
	replies := make(chan lanes.Reply, 1)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, aToolCall(), r.reloads(widened(), replies), g.step(), aToolCall(), completed(0.2)},
		[]fake.DriverStep{answer, completed(0.3)},
	))
	r.start()
	if reply := answered(t, replies); !reply.OK || reply.Reload != lanes.ReloadBlock {
		t.Fatalf("a reload that widens what the lane runs was answered %+v", reply)
	}
	r.reach(t, g)
	r.eventually("the next tool call is held", func(ledger.LaneView) bool { return len(r.driver.Holds()) == 1 })
	close(g.release)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()

	if calls, denied := r.driver.Calls(), r.driver.Denied(); calls != 1 || denied != 1 {
		t.Errorf("after the blocked reload %d calls ran and %d were denied, want none to run", calls-1, denied)
	}
	held := r.settingsHeld(t)
	if len(held) != 1 || held[0].Generation != 1 || held[0].Digest != widened().Digest || !slices.Equal(held[0].Keys, []string{"hooks"}) {
		t.Errorf("the ledger holds %+v", held)
	}
	if wake := r.wakeOf(t, 1); !wake.Open() || wake.Expr != lanes.HumanWake() {
		t.Errorf("wake %+v", wake)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions started, and a held lane starts none until a member chooses", len(specs))
	}
	if _, doc := r.storedHandoff(t, 1); doc.Reason != string(lanes.ParkSettings) || doc.Narrative == nil {
		t.Errorf("handoff %+v", doc)
	}
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		holds, err := control.SettingsHolds(tx)
		if err == nil && (len(holds) != 1 || holds[0].Lane != "lane-1" || holds[0].Change.Digest != widened().Digest) {
			t.Errorf("the holds read %+v", holds)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A reload that leaves every key that runs code or widens access as the repository holds it applies, and holds nothing.
func TestAReloadThatWidensNothingApplies(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	replies := make(chan lanes.Reply, 1)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reloads(nil, replies), r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.pass(t)
	if reply := answered(t, replies); !reply.OK || reply.Reload != lanes.ReloadAllow {
		t.Errorf("a reload that widens nothing was answered %+v", reply)
	}
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if held := r.settingsHeld(t); len(held) != 0 {
		t.Errorf("the ledger holds %+v", held)
	}
}

// A session that runs with the settings a member chose for the lane may reload them as they are: the change is the one
// the member read, so the reload applies, and nothing is held again.
func TestAReloadOfTheSettingsAMemberChoseApplies(t *testing.T) {
	t.Parallel()
	r := heldOnSettings(t)
	replies := make(chan lanes.Reply, 1)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reloads(nil, replies), r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.choose(t, lanes.SettingsLane)
	r.pass(t)
	if reply := answered(t, replies); !reply.OK || reply.Reload != lanes.ReloadAllow {
		t.Errorf("a reload of the settings a member chose was answered %+v", reply)
	}
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
	if held := r.settingsHeld(t); len(held) != 1 {
		t.Errorf("the ledger holds %+v", held)
	}
}
