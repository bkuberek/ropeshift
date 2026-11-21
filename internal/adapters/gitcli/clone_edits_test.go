package gitcli_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// personsClone is a clone of a repository with a remote, in which a person changed, staged, deleted, and added files,
// and committed one change they have not pushed.
func personsClone(t *testing.T) *gittest.Repo {
	t.Helper()
	origin := gittest.New(t)
	origin.Commit("plan.md", "the plan\n")
	origin.Commit("notes.md", "notes\n")
	origin.Commit("gone.md", "going\n")
	origin.Commit("both.md", "both\n")
	clone := origin.Clone("person")
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(clone.Dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	clone.Commit("pushed-not.md", "committed here only\n")
	clone.Commit("both.md", "both, committed here\n")
	write("both.md", "both, and changed again\n")
	write("plan.md", "the plan, edited\n")
	write("staged.md", "staged\n")
	clone.Git("add", "staged.md")
	if err := os.Remove(filepath.Join(clone.Dir, "gone.md")); err != nil {
		t.Fatal(err)
	}
	write("docs/new design.md", "a new design\n")
	write(".gitignore", "*.log\n")
	write("build.log", "ignored\n")
	return clone
}

func TestACloneListsWhatIntakeAndLanesCannotRead(t *testing.T) {
	clone := personsClone(t)
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	got, err := worktrees.Edits(t.Context(), clone.Dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []ledger.CloneEdit{
		{Path: ".gitignore", State: ledger.EditUntracked},
		{Path: "both.md", State: ledger.EditUncommitted},
		{Path: "docs/new design.md", State: ledger.EditUntracked},
		{Path: "gone.md", State: ledger.EditUncommitted},
		{Path: "plan.md", State: ledger.EditUncommitted},
		{Path: "pushed-not.md", State: ledger.EditUnpushed},
		{Path: "staged.md", State: ledger.EditUncommitted},
	}
	if !slices.Equal(got.Edits, want) || got.Cut {
		t.Errorf("edits %+v, cut %v", got.Edits, got.Cut)
	}
	if got.Branch != "main" || got.Remote != "origin" || got.Head != clone.Git("rev-parse", "HEAD") {
		t.Errorf("the clone reads %+v", got)
	}
}

func TestACloneReadKeepsAtMostTheLimitAndSaysItCut(t *testing.T) {
	clone := personsClone(t)
	got, err := gitcli.Worktrees{Env: clone.Env()}.Edits(t.Context(), clone.Dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Edits) != 3 || !got.Cut {
		t.Errorf("a read of 3 kept %d, cut %v", len(got.Edits), got.Cut)
	}
}

func TestAPathGitCannotPrintBareIsReadAsItIs(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "readme\n")
	name := "quote\"and\nnewline.md"
	if err := os.WriteFile(filepath.Join(repo.Dir, name), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := gitcli.Worktrees{Env: repo.Env()}.Edits(t.Context(), repo.Dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Edits, []ledger.CloneEdit{{Path: name, State: ledger.EditUntracked}}) || got.Remote != "" {
		t.Errorf("edits %+v", got)
	}
}

func TestACloneWithNoCommitListsItsFiles(t *testing.T) {
	repo := gittest.New(t)
	if err := os.WriteFile(filepath.Join(repo.Dir, "first.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := gitcli.Worktrees{Env: repo.Env()}.Edits(t.Context(), repo.Dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Head != "" || got.Branch != "main" || !slices.Equal(got.Edits, []ledger.CloneEdit{{Path: "first.md", State: ledger.EditUntracked}}) {
		t.Errorf("edits %+v", got)
	}
}

func TestAPathThatHoldsNoCloneHasNoEdits(t *testing.T) {
	gittest.Require(t)
	_, err := gitcli.Worktrees{}.Edits(t.Context(), filepath.Join(t.TempDir(), "nothing"), 10)
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a path with no clone: %v", err)
	}
}

// runShell runs a step's commands as a person pastes them into a shell.
func runShell(t *testing.T, repo *gittest.Repo, lines []string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", strings.Join(lines, "\n"))
	cmd.Dir, cmd.Env = t.TempDir(), repo.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the commands failed: %v\n%s\n%s", err, strings.Join(lines, "\n"), out)
	}
}

// TestTheStepsCommitTheEditsAndLeaveTheCloneAsItWas runs the commands of the commit and copy steps in a shell, as a
// person who copied them does: each makes a branch that holds the files as the clone holds them, and the clone's
// checkout, index, and files are as they were.
func TestTheStepsCommitTheEditsAndLeaveTheCloneAsItWas(t *testing.T) {
	clone := personsClone(t)
	planning := clone.Git("rev-parse", "HEAD~2")
	clone.Branch("lane/lane-1", planning)
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	edits, err := worktrees.Edits(t.Context(), clone.Dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	status, staged, head := clone.Git("status", "--porcelain"), clone.Git("diff", "--cached", "--name-only"), clone.Git("rev-parse", "HEAD")
	steps := ledger.EditSteps(edits, ledger.EditPlace{
		Project: "harbor", Clone: clone.Dir, Stamp: "20261120-153000",
		Planning: []ledger.PlanningBranch{{WorkOrder: "plan-1", Lane: "lane-1", Branch: "lane/lane-1"}},
	})
	if len(steps) != 3 || steps[0].Kind != ledger.StepCommit || steps[2].Kind != ledger.StepCopy {
		t.Fatalf("steps %+v", steps)
	}
	// The commit step's last command pushes, and the copy step's tells the lane; neither runs here.
	runShell(t, clone, steps[0].Commands[:len(steps[0].Commands)-1])
	runShell(t, clone, steps[2].Commands[:len(steps[2].Commands)-1])
	if clone.Git("status", "--porcelain") != status || clone.Git("diff", "--cached", "--name-only") != staged || clone.Git("rev-parse", "HEAD") != head {
		t.Errorf("the commands changed the clone: %s", clone.Git("status", "--porcelain"))
	}
	committed := clone.Git("rev-parse", "edits/20261120-153000")
	if parent := clone.Git("rev-parse", committed+"^"); parent != head {
		t.Errorf("the commit step's branch starts at %s, not %s", parent, head)
	}
	if got := clone.Git("show", committed+":plan.md"); got != "the plan, edited" {
		t.Errorf("the committed plan reads %q", got)
	}
	if got := clone.Git("show", committed+":docs/new design.md"); got != "a new design" {
		t.Errorf("the committed design reads %q", got)
	}
	if _, err := clone.Try("cat-file", "-e", committed+":gone.md"); err == nil {
		t.Error("the commit step kept a file the clone deleted")
	}
	if _, err := clone.Try("cat-file", "-e", committed+":build.log"); err == nil {
		t.Error("the commit step committed an ignored file")
	}
	copied := clone.Git("rev-parse", "edits/20261120-153000-lane-1")
	if parent := clone.Git("rev-parse", copied+"^"); parent != planning {
		t.Errorf("the copy step's branch starts at %s, not the planning branch's %s", parent, planning)
	}
	if got := clone.Git("show", copied+":pushed-not.md"); got != "committed here only" {
		t.Errorf("the copied file reads %q", got)
	}
	if clone.Git("rev-parse", "lane/lane-1") != planning {
		t.Error("the copy step moved the planning branch")
	}
	// A branch that exists already is never overwritten.
	cmd := exec.Command("sh", "-c", strings.Join(steps[0].Commands[:len(steps[0].Commands)-1], "\n"))
	cmd.Dir, cmd.Env = t.TempDir(), clone.Env()
	if err := cmd.Run(); err == nil || clone.Git("rev-parse", "edits/20261120-153000") != committed {
		t.Errorf("a second run moved the branch: %v", err)
	}
	if index := filepath.Join(clone.Dir, ".git", "ropeshift-edits-index"); fileExists(index) {
		t.Error("the commands left their index behind")
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestALanesBranchAndItsBaseEachSayWhatTheyChanged reads a lane's branch against the remote's copy of its base: the
// lane's own commits on one side, and on the other each commit a person pushed, leaving out a commit a merge of
// ropeshift made and the commits a merge brought in.
func TestALanesBranchAndItsBaseEachSayWhatTheyChanged(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	origin.Commit("b.md", "b\n")
	fork := origin.Commit("c.md", "c\n")
	clone := origin.Clone("registered")
	clone.Git("checkout", "--quiet", "-b", "lane/lane-1")
	clone.Commit("a.md", "a, by the lane\n")
	clone.Commit("lane.md", "the lane's own\n")
	clone.Git("checkout", "--quiet", "main")

	origin.Commit("c.md", "c, pushed by a person\n")
	if err := os.WriteFile(filepath.Join(origin.Dir, "b.md"), []byte("b, merged by ropeshift\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	origin.Git("commit", "--quiet", "-am", "Merge the work\n\nRopeshift-Work-Order: wo-9\nRopeshift-Reviewed-Commit: abc")
	origin.Git("checkout", "--quiet", "-b", "side")
	origin.Commit("d.md", "d, on a side branch\n")
	origin.Git("checkout", "--quiet", "main")
	origin.Merge("side")
	clone.Git("fetch", "--quiet", "origin")

	worktrees := gitcli.Worktrees{Env: clone.Env()}
	got, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got.Fork != fork || got.Base != "origin/main" || got.Cut {
		t.Errorf("divergence %+v, want fork %s", got, fork)
	}
	if !slices.Equal(got.Lane, []string{"a.md", "lane.md"}) || !slices.Equal(got.Pushed, []string{"c.md"}) {
		t.Errorf("the lane changed %v and the base %v", got.Lane, got.Pushed)
	}
	if limited, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", nil, 1); err != nil || len(limited.Lane) != 1 || !limited.Cut {
		t.Errorf("a read of one path: %+v, %v", limited, err)
	}
	for _, branch := range []string{"lane/none", "--output=x"} {
		if _, err := worktrees.Diverged(t.Context(), clone.Dir, branch, "main", nil, 100); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("a branch %q: %v", branch, err)
		}
	}
}

// With no remote, the base is the clone's own branch, where a person's commits land.
func TestWithNoRemoteTheBaseIsTheLocalBranch(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	repo.Git("branch", "lane/lane-1")
	repo.Commit("a.md", "a, by a person\n")
	repo.Git("checkout", "--quiet", "lane/lane-1")
	repo.Commit("a.md", "a, by the lane\n")
	repo.Git("checkout", "--quiet", "main")
	got, err := gitcli.Worktrees{Env: repo.Env()}.Diverged(t.Context(), repo.Dir, "lane/lane-1", "main", nil, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got.Base != "main" || !slices.Equal(got.Lane, []string{"a.md"}) || !slices.Equal(got.Pushed, []string{"a.md"}) {
		t.Errorf("divergence %+v", got)
	}
}
