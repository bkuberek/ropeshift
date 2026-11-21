package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestAConflictOnRealGitIsFoundAndClearsOnceTheLaneHoldsThePersonsFiles runs a conflict through real git from end to
// end: a lane's branch and a person's uncommitted file reach Needs you, the person runs the commands Keep both changes
// gives, the lane merges that branch keeping both changes, and the next reading finds no conflict.
func TestAConflictOnRealGitIsFoundAndClearsOnceTheLaneHoldsThePersonsFiles(t *testing.T) {
	t.Parallel()
	origin := gittest.New(t)
	origin.Commit("plans/plan.md", "the plan\n")
	clone := origin.Clone("registered")
	git := gitcli.Worktrees{Env: clone.Env()}
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return clone.Dir, nil }}
	d := apitest.StartWith(t, apitest.Options{Starts: starts, LaneBranchPrefix: "lane/", OutsideEdits: control.NewOutsideEdits(starts, git, "lane/")})
	w := apitest.NewWorld(t, d)
	w.Repository("repository-1", ledger.PersonMerges)
	w.Running("wo-1", "Retry within a budget")

	worktree := filepath.Join(t.TempDir(), "lane")
	clone.Git("worktree", "add", "--quiet", "-b", "lane/lane-wo-1", worktree, "origin/main")
	lane := &gittest.Repo{T: t, Dir: worktree, Home: clone.Home}
	lane.Commit("plans/plan.md", "the plan, as the lane wrote it\n")
	if err := os.WriteFile(filepath.Join(clone.Dir, "plans", "plan.md"), []byte("the plan, as the person wrote it\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	items := conflictItems(t, d)
	if len(items) != 1 || len(items[0].Conflict.Paths) != 1 || items[0].Conflict.Paths[0] != (api.ConflictPathView{Path: "plans/plan.md", Person: string(ledger.EditUncommitted)}) {
		t.Fatalf("Needs you holds %+v", items)
	}
	both := items[0].Conflict.Options[0]
	commit := exec.Command("sh", "-c", strings.Join(both.Commands[:len(both.Commands)-1], "\n"))
	commit.Dir, commit.Env = t.TempDir(), clone.Env()
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("the commands of Keep both changes failed: %v\n%s", err, out)
	}
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again := conflictItems(t, d); len(again) != 1 {
		t.Fatalf("the conflict cleared before the lane held the person's files: %+v", again)
	}

	// The lane merges the branch and keeps both changes.
	if _, err := lane.Try("merge", "--no-edit", both.Branch); err == nil {
		t.Fatal("the two changes merged with no conflict")
	}
	if err := os.WriteFile(filepath.Join(worktree, "plans", "plan.md"), []byte("the plan, as the lane and the person wrote it\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lane.Git("add", "plans/plan.md")
	lane.Git("commit", "--quiet", "--no-edit")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Errorf("the conflict stays once the lane's branch holds the person's files: %+v", got)
	}
	if status := clone.Git("status", "--porcelain"); status != "M plans/plan.md" {
		t.Errorf("the person's clone changed: %q", status)
	}
}

// A page that reads the clones often reads them again no sooner than the window Reusing set, and answers with the
// reading it has.
func TestAReadingIsReusedWithinItsWindow(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	d.Config.OutsideEdits.Reusing(time.Hour)
	first, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin"})
	again, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if again.AsOf != first.AsOf || len(again.Clones[0].Edits) != len(first.Clones[0].Edits) {
		t.Errorf("a reading within its window read the clones again: %+v", again)
	}
	if _, err := d.Config.OutsideEdits.Read(t.Context(), d.Store, apitest.Scope, "default"); err != nil {
		t.Fatal(err)
	}
	if fresh, err := d.Client.OutsideEdits(t.Context()); err != nil || len(fresh.Clones[0].Edits) != 0 {
		t.Errorf("the page reuses an older reading than the newest: %+v, %v", fresh, err)
	}
}
