package worker_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	ownWorktree = "/repository-lanes/lane-1"
	cacheFile   = ".atl/registry.md"
)

// owningLane is an interactive lane at its first turn, whose setup left a cache in a folder git tracks nothing in.
func owningLane(t *testing.T) (*laneRig, *gate) {
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
	r.worktrees().ChangeFiles(ownWorktree, cacheFile)
	r.worktrees().UntrackedFolders(ownWorktree, ".atl/")
	return r, g
}

// endOwning has the session edit one file with a tool, make another through its shell, and end.
func (r *laneRig) endOwning(t *testing.T, g *gate) {
	t.Helper()
	r.hook(t, lanes.HookEvent{Event: lanes.EventPreToolUse, Subject: "t-1", Tool: "Edit", Path: ownWorktree + "/docs/a.md", AtMs: 1100})
	r.worktrees().ChangeFiles(ownWorktree, cacheFile, "docs/a.md", "gen/out.go")
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "s", AtMs: 1600})
	g.free()
	r.waitForTheLane(t)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// runOwning runs an owning lane whose first prompt reached the worker as it was submitted.
func runOwning(t *testing.T) *laneRig {
	t.Helper()
	r, g := owningLane(t)
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s", AtMs: 1000})
	r.endOwning(t, g)
	return r
}

func (r *laneRig) lastGuard(t *testing.T) ledger.LaneGuard {
	t.Helper()
	var guard ledger.LaneGuard
	for _, event := range r.events() {
		if event.Type == ledger.EventLaneGuarded {
			if err := json.Unmarshal(event.Data, &guard); err != nil {
				t.Fatal(err)
			}
		}
	}
	return guard
}

// TestACheckpointHoldsOnlyTheLanesOwnFiles is FR-LANE-20: a file the session edited and one its shell made after its
// first turn are the lane's, and a cache that was there before that turn is reported, by its folder, and never saved
// with the lane's work.
func TestACheckpointHoldsOnlyTheLanesOwnFiles(t *testing.T) {
	t.Parallel()
	r := runOwning(t)
	paths, scoped := r.worktrees().ScopedSnapshot(lanes.CheckpointRef("lane-1", 1))
	if !scoped || !slices.Equal(paths, []string{"docs/a.md", "gen/out.go"}) {
		t.Errorf("the checkpoint took %q (scoped %v)", paths, scoped)
	}
	if left := r.lastGuard(t).LeftOut; !slices.Equal(left, []string{".atl/"}) {
		t.Errorf("the lane reported %q as left out", left)
	}
}

// TestFilesLeftOutAreKeptOnALocalRef saves what the lane left out before anything can remove its worktree, so a wrong
// reading can be undone and a decision to commit a path still has its file. The ref is never pushed.
func TestFilesLeftOutAreKeptOnALocalRef(t *testing.T) {
	t.Parallel()
	r := runOwning(t)
	ref := lanes.LeftRef("lane-1", 1)
	if paths, scoped := r.worktrees().ScopedSnapshot(ref); !scoped || !slices.Equal(paths, []string{cacheFile}) {
		t.Errorf("the left-out ref took %q (scoped %v)", paths, scoped)
	}
	if guard := r.lastGuard(t); guard.LeftRef != ref {
		t.Errorf("the guard names the ref %q, want %q", guard.LeftRef, ref)
	}
	if slices.Contains(r.worktrees().Pushed(), ref) {
		t.Errorf("the left-out ref was pushed: %v", r.worktrees().Pushed())
	}
}

// savesEverything holds that a lane with no reading of its start leaves nothing out: its checkpoint is the whole
// worktree.
func (r *laneRig) savesEverything(t *testing.T) {
	t.Helper()
	checkpoint := lanes.CheckpointRef("lane-1", 1)
	if paths, scoped := r.worktrees().ScopedSnapshot(checkpoint); scoped {
		t.Errorf("the checkpoint took only %q", paths)
	}
	if r.worktrees().Snapshots()[checkpoint] != ownWorktree {
		t.Errorf("no checkpoint holds the worktree: %v", r.worktrees().Snapshots())
	}
	if left := r.lastGuard(t).LeftOut; len(left) != 0 {
		t.Errorf("the lane left out %q", left)
	}
}

// TestAFailedFirstReadingLeavesTheLaneOwningEverything is a reading that fails at the first prompt: no later prompt
// takes one, because by then the session's shell may have made files that would read as there before.
func TestAFailedFirstReadingLeavesTheLaneOwningEverything(t *testing.T) {
	t.Parallel()
	r, g := owningLane(t)
	r.worktrees().FailSurvey(errors.New("the worktree is busy"))
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s", AtMs: 1000})
	r.worktrees().FailSurvey(nil)
	r.worktrees().ChangeFiles(ownWorktree, cacheFile, "gen/out.go")
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s-2", AtMs: 1050})
	r.endOwning(t, g)
	r.savesEverything(t)
}

// spoolAll leaves sealed lines in the spool, in order, as hooks whose acks came too late leave them, and drains
// nothing.
func (r *laneRig) spoolAll(t *testing.T, events ...lanes.HookEvent) {
	t.Helper()
	var raw []byte
	for _, event := range events {
		raw = append(raw, sealedLine(t, r.token(t), event)...)
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(filepath.Join(r.dir, "spool"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestASpooledFirstTurnLeavesALaterPromptUnread is a first turn whose every hook missed its ack, so the spool holds its
// prompt, a call whose shell made a file, and its end, when the session's next prompt reaches the worker live. The spool
// is taken before that prompt is read, so it reads as a second prompt, and the lane owns every change.
func TestASpooledFirstTurnLeavesALaterPromptUnread(t *testing.T) {
	t.Parallel()
	r, g := owningLane(t)
	r.spoolAll(t,
		lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: lanes.EventUserPromptSubmit, Subject: "s", AtMs: 1000},
		lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: lanes.EventPreToolUse, Subject: "t-0", Tool: "Bash", AtMs: 1010},
		lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: lanes.EventStop, Subject: "s", AtMs: 1040},
	)
	r.worktrees().ChangeFiles(ownWorktree, cacheFile, "gen/out.go")
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s-2", AtMs: 1050})
	r.endOwning(t, g)
	r.savesEverything(t)
}

// TestAReadingIsNeverTakenAfterTheSessionBeganWork is a first prompt whose hook never reached the worker: the next
// prompt it hears of comes after a call, and takes no reading.
func TestAReadingIsNeverTakenAfterTheSessionBeganWork(t *testing.T) {
	t.Parallel()
	r, g := owningLane(t)
	r.hook(t, lanes.HookEvent{Event: lanes.EventPreToolUse, Subject: "t-0", Tool: "Bash", AtMs: 1000})
	r.worktrees().ChangeFiles(ownWorktree, cacheFile, "gen/out.go")
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s-2", AtMs: 1050})
	r.endOwning(t, g)
	r.savesEverything(t)
}
