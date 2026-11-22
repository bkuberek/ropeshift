package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// adoptedToken is the token of a session an earlier worker started.
const adoptedToken = "adopted-session-token"

// reloadChecks is a driver that keeps the spec each reload of a session's settings is checked with.
type reloadChecks struct {
	ports.AgentDriver
	mu    sync.Mutex
	specs []ports.SessionSpec
}

func (d *reloadChecks) SettingsChange(ctx context.Context, spec ports.SessionSpec) *lanes.SettingsChange {
	d.mu.Lock()
	d.specs = append(d.specs, spec)
	d.mu.Unlock()
	return d.AgentDriver.SettingsChange(ctx, spec)
}

func (d *reloadChecks) checked() []ports.SessionSpec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ports.SessionSpec{}, d.specs...)
}

// anAdoptedLane is a lane whose session an earlier worker started at its claim's base and left running, and whose
// session asks about one reload of its settings, with the answer sent on replies.
func anAdoptedLane(t *testing.T, replies chan<- lanes.Reply) (*worldRig, *reloadChecks) {
	t.Helper()
	ctx := t.Context()
	w := newWorldRig(t)
	w.leases = append(w.leases, ledger.DesiredLease{
		WorkOrder: "wo-lane-1", ClaimID: "c-lane-1", Fence: 1, State: string(workorders.Claimed), Lane: "lane-1", BaseCommit: "base-commit",
	})
	if err := w.journal.SaveDesired(ctx, ledger.DesiredState{Rev: 1, Leases: w.leases}); err != nil {
		t.Fatal(err)
	}
	err := w.journal.SaveLane(ctx, lanes.Record{
		ID: "lane-1", WorkOrder: "wo-lane-1", ClaimID: "c-lane-1", Fence: 1, Generation: 1, State: lanes.StateRunning,
		Worktree: "/worktrees/lane-1", TokenDigest: lanes.TokenDigest(adoptedToken),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	checks := &reloadChecks{AgentDriver: w.driver}
	w.config.Driver = checks
	ask := fake.DriverStep{Do: func(ports.SessionSpec) {
		line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: adoptedToken, Reload: true})
		if err != nil {
			t.Error(err)
		}
		replies <- w.subject.AnswerLane(context.Background(), line)
	}}
	w.driver.Play(script([]fake.DriverStep{ask, {Hold: true}}))
	return w, checks
}

// A session a restarted worker adopted was not started by this process, so a reload of its settings is checked with a
// spec read again from the lane: its worktree, the clone its repository is, and its claim's base, which a reload that
// widens nothing passes.
func TestAnAdoptedSessionsReloadIsCheckedAtItsClaimsBase(t *testing.T) {
	t.Parallel()
	replies := make(chan lanes.Reply, 1)
	w, checks := anAdoptedLane(t, replies)
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := answered(t, replies); !reply.OK || reply.Reload != lanes.ReloadAllow {
		t.Errorf("an adopted session's reload that widens nothing was answered %+v", reply)
	}
	specs := checks.checked()
	if len(specs) != 1 {
		t.Fatalf("the reload was checked %d times", len(specs))
	}
	spec := specs[0]
	if spec.Lane != "lane-1" || spec.Dir != "/worktrees/lane-1" || spec.Repository != "/repository" || spec.Base != "base-commit" ||
		spec.SettingsBase != "base-commit" || spec.SettingsUnbased {
		t.Errorf("an adopted session's reload was checked with %+v", spec)
	}
}

// An adopted session whose clone cannot be found has no repository to compare its settings with, so its reload is
// blocked, and nothing is checked.
func TestAnAdoptedSessionWithNoCloneHasItsReloadBlocked(t *testing.T) {
	t.Parallel()
	replies := make(chan lanes.Reply, 1)
	w, checks := anAdoptedLane(t, replies)
	w.config.Repository = func(context.Context, string, string) (string, error) { return "", errors.New("no clone") }
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reply := answered(t, replies); !reply.OK || reply.Reload != lanes.ReloadBlock {
		t.Errorf("an adopted session with no clone had its reload answered %+v", reply)
	}
	if specs := checks.checked(); len(specs) != 0 {
		t.Errorf("a reload with no clone was checked with %+v", specs)
	}
}
