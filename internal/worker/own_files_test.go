package worker_test

import (
	"encoding/json"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

const ownWorktree = "/repository-lanes/lane-1"

// runOwning runs an interactive lane whose setup left a cache before its first turn, and whose session then
// edited one file with a tool and made another through its shell.
func runOwning(t *testing.T) *laneRig {
	t.Helper()
	base := newRig(t)
	base.repository = "repo-1"
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", "run_mode = \"tmux\"\n")
	r.dispatched()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step(), r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.atGate(g)
	r.worktrees().ChangeFiles(ownWorktree, ".atl/")
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s", AtMs: 1000})
	r.hook(t, lanes.HookEvent{Event: lanes.EventPreToolUse, Subject: "t-1", Tool: "Edit", Path: ownWorktree + "/docs/a.md", AtMs: 1100})
	r.worktrees().ChangeFiles(ownWorktree, ".atl/", "docs/a.md", "gen/out.go")
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "s", AtMs: 1600})
	g.free()
	r.waitForTheLane(t)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	return r
}

// TestACheckpointHoldsOnlyTheLanesOwnFiles is FR-LANE-20: a file the session edited and one its shell made after its
// first turn are the lane's, and a cache that was there before that turn is reported and never saved.
func TestACheckpointHoldsOnlyTheLanesOwnFiles(t *testing.T) {
	t.Parallel()
	r := runOwning(t)
	paths, scoped := r.worktrees().ScopedSnapshot(lanes.CheckpointRef("lane-1", 1))
	if !scoped || !slices.Equal(paths, []string{"docs/a.md", "gen/out.go"}) {
		t.Errorf("the checkpoint took %q (scoped %v)", paths, scoped)
	}
	var left []string
	for _, event := range r.events() {
		if event.Type != ledger.EventLaneGuarded {
			continue
		}
		var guard ledger.LaneGuard
		if err := json.Unmarshal(event.Data, &guard); err != nil {
			t.Fatal(err)
		}
		left = guard.LeftOut
	}
	if !slices.Equal(left, []string{".atl/"}) {
		t.Errorf("the lane reported %q as left out", left)
	}
}
