package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/inventory"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-ADOPT-13: a workspace whose ignored folder holds its design repository is consolidated before the adopt lane
// starts: the design repository's history is merged into the lane's branch in one merge commit, with real git.

const goalFile = "+++\nkind = \"goal\"\nid = \"g\"\ntitle = \"A goal\"\nsuccess_measures = [\"one\"]\n+++\n\nWhy.\n"

// newGitConsolidation is a workspace that commits a README, an ignore file for its folder of clones, and rules of
// its own, with a design repository and a code repository in that folder. The design repository commits the same
// three paths with other content, a goal, documents, its own rules, and a code tree.
func newGitConsolidation(t *testing.T) *gitWorkspace {
	t.Helper()
	w := newEmptyGitWorkspace(t)
	w.clone("ws", map[string]string{"README.md": "a workspace\n", ".gitignore": "repos/\nnode_modules/\n", "AGENTS.md": "workspace rules\n"})
	w.clone("ws/repos/design", map[string]string{
		"README.md": "the design\n", ".gitignore": "repos/\ndist/\n*.log\n", "AGENTS.md": "design rules\n", "CLAUDE.md": "design claude\n",
		"docs/vision.md": "# Vision\n", "plan/goal.md": goalFile, "tools/go.mod": "module tools\n", "tools/main.go": "package main\n",
	})
	w.clone("ws/repos/api", map[string]string{"go.mod": "module api\n", "README.md": "api\n"})
	w.begin("ws/repos/api")
	return w
}

// consolidate runs the lane's restructuring for the workspace, as a member's command planned it with the code trees
// it leaves out.
func (w *gitWorkspace) consolidate(leaveOut ...string) *laneRun {
	w.t.Helper()
	request := adoption.Request{Root: w.path("ws"), Depth: 1, LeaveOut: leaveOut}
	return w.runChoosing(request, func(plan planning.AdoptionPlan, chosen *planning.Restructure) {
		if plan.Shape != planning.ShapeConsolidate || plan.Design == nil {
			w.t.Fatalf("the layout reads %s with design %+v", plan.Shape, plan.Design)
		}
		chosen.DesignPath, chosen.DesignRoot = plan.Design.Path, plan.Design.Root
	})
}

// stateOf is what a read of a clone could change: its refs, its index as git reads it and as it is on disk, and
// every file of its working tree.
func stateOf(t *testing.T, w *gitWorkspace, clone string) string {
	t.Helper()
	sum := sha256.New()
	sum.Write([]byte(w.git(clone, "for-each-ref") + "\x00" + w.git(clone, "ls-files", "--stage")))
	index, err := os.ReadFile(filepath.Join(clone, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	sum.Write(index)
	err = filepath.WalkDir(clone, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		raw, err := os.ReadFile(at)
		if err != nil {
			return err
		}
		sum.Write(append([]byte(at+"\x00"), raw...))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func (w *gitWorkspace) readLane(name string) string {
	w.t.Helper()
	raw, err := os.ReadFile(filepath.Join(w.lane, name))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(raw)
}

// A design repository in the workspace's code folder ends in one merge commit whose second parent is the design
// tip, with every design commit reachable, a design file's log read through the merge, the workspace's version of
// each colliding path, the ignore files united, a tree left out absent from the merge and present in its second
// parent, the workspace's identity as it was, and the design repository's clone untouched.
func TestAGitWorkspaceWithADesignRepositoryInItsCodeFolderEndsInOneMergeCommit(t *testing.T) {
	t.Parallel()
	w := newGitConsolidation(t)
	ws, design := w.path("ws"), w.path("ws/repos/design")
	before := stateOf(t, w, design)
	tip := w.git(design, "rev-parse", "HEAD")
	run := w.consolidate("tools")

	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if count := w.git(w.lane, "rev-list", "--count", "--first-parent", w.tip+"..HEAD"); count != "1" {
		t.Fatalf("the branch holds %s commits of its own beyond its base, want the merge alone", count)
	}
	if parents := w.git(w.lane, "log", "-1", "--format=%P"); parents != w.tip+" "+tip {
		t.Fatalf("the merge's parents are %q, want the workspace's tip %s then the design tip %s", parents, w.tip, tip)
	}
	if subject := w.git(w.lane, "log", "-1", "--format=%s"); subject != "chore: import the design history into the workspace" {
		t.Errorf("the merge's subject is %q", subject)
	}
	for _, commit := range strings.Fields(w.git(design, "rev-list", "HEAD")) {
		if _, err := exec.Command("git", "-C", w.lane, "merge-base", "--is-ancestor", commit, "HEAD").CombinedOutput(); err != nil {
			t.Errorf("the design commit %s is not reachable from the merge", commit)
		}
	}
	if got, want := w.git(w.lane, "log", "--format=%H", "--", "docs/vision.md"), w.git(design, "log", "--format=%H", "--", "docs/vision.md"); got != want || got == "" {
		t.Errorf("git log on a design file reads %q through the merge, and %q in the design repository", got, want)
	}
	for name, want := range map[string]string{"README.md": "a workspace\n", "AGENTS.md": "workspace rules\n", "docs/vision.md": "# Vision\n"} {
		if got := w.readLane(name); got != want {
			t.Errorf("%s reads %q, want %q", name, got, want)
		}
	}
	if got := w.git(w.lane, "show", "HEAD^2:README.md"); got != "the design" {
		t.Errorf("the design's README at the second parent reads %q", got)
	}
	if got, want := w.readLane(".gitignore"), "repos/\nnode_modules/\ndist/\n*.log\n/.ropeshift/worktrees/\n"; got != want {
		t.Errorf("the ignore file reads %q, want the workspace's lines, the design's it lacked, and the worktrees line: %q", got, want)
	}
	if tracked := w.git(w.lane, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(tracked, "tools/") {
		t.Errorf("the merge commit holds the tree left out:\n%s", tracked)
	}
	if tracked := w.git(w.lane, "ls-tree", "-r", "--name-only", "HEAD^2"); !strings.Contains(tracked, "tools/go.mod") {
		t.Errorf("the second parent lost the tree left out:\n%s", tracked)
	}
	if root := w.git(w.lane, "rev-list", "--first-parent", "--max-parents=0", "HEAD"); root != w.ids["ws"] {
		t.Errorf("the first-parent root is %s, so the workspace's identity %s changed", root, w.ids["ws"])
	}
	if status := w.git(w.lane, "status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty:\n%s", status)
	}
	raw, err := os.ReadFile(filepath.Join(w.lane, "plan", "repositories.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := repofiles.ParseManifest(raw)
	if err != nil || manifest.ID != "shop" || len(manifest.Repositories) != 1 || manifest.Repositories[0].Name != "api" || manifest.Repositories[0].Path != "repos/api" {
		t.Fatalf("the manifest reads %+v with design repository left out: %v\n%s", manifest, err, raw)
	}
	if makefile := w.readLane("Makefile"); !strings.Contains(makefile, "\n  api=") || strings.Contains(makefile, "design") {
		t.Errorf("the Makefile reads:\n%s", makefile)
	}
	if after := stateOf(t, w, design); after != before {
		t.Error("the design repository changed: its refs, its index, or its working tree differ")
	}

	told := run.restructuredOf()
	if told == nil || told.Commit != w.git(w.lane, "rev-parse", "HEAD") || told.Consolidated == nil || told.Manifest != "repositories:shop" || len(told.Moves) != 0 {
		t.Fatalf("the brief is told %+v", told)
	}
	got := told.Consolidated
	wantCollisions := []planning.Collision{
		{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "AGENTS.md", Kept: planning.KeptWorkspace}, {Path: "README.md", Kept: planning.KeptWorkspace},
	}
	if got.Design != "design" || got.SecondParent != tip || !slices.Equal(got.LeftOut, []string{"tools"}) || !slices.Equal(got.Instructions, []string{"AGENTS.md", "CLAUDE.md"}) ||
		!slices.Equal(got.Collisions, wantCollisions) {
		t.Errorf("the brief is told the consolidation %+v", got)
	}
}

// A retry finds the merge on the lane's branch and makes no second one, whether the design repository has moved on
// or the clones have been moved, and tells the brief what it told before.
func TestAGitConsolidationRepeatedMakesNoSecondMerge(t *testing.T) {
	t.Parallel()
	w := newGitConsolidation(t)
	ws, design := w.path("ws"), w.path("ws/repos/design")
	run := w.consolidate("tools")
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	merge := w.git(w.lane, "rev-parse", "HEAD")
	if told := run.restructuredOf(); told == nil || told.Consolidated == nil {
		t.Fatalf("the brief is told %+v", told)
	}
	first := *run.restructuredOf().Consolidated

	if err := os.WriteFile(filepath.Join(design, "later.md"), []byte("later\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(design, "add", "later.md")
	w.git(design, "commit", "--quiet", "-m", "a commit made after the merge")
	if err := os.Rename(w.path("ws/repos/api"), filepath.Join(w.base, "moved")); err != nil {
		t.Fatal(err)
	}
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("a run after the merge failed: %+v", report)
	}
	if now := w.git(w.lane, "rev-parse", "HEAD"); now != merge {
		t.Errorf("a second run moved the branch from %s to %s", merge, now)
	}
	if count := w.git(w.lane, "rev-list", "--count", "--first-parent", w.tip+"..HEAD"); count != "1" {
		t.Errorf("the branch holds %s commits of its own after a second run", count)
	}
	told := run.restructuredOf()
	if told == nil || told.Commit != merge || told.Consolidated == nil || told.Consolidated.SecondParent != first.SecondParent ||
		!slices.Equal(told.Consolidated.Collisions, first.Collisions) {
		t.Errorf("the brief is told %+v after the retry, and %+v before", told, first)
	}
}

// A crash between the merge and the record of the effect leaves the merge on the branch, and the worker finds it
// there: before the merge it finds nothing, and after it the effect happened.
func TestAGitConsolidationsMergeIsFoundAfterACrash(t *testing.T) {
	t.Parallel()
	w := newGitConsolidation(t)
	ws := w.path("ws")
	run := w.consolidate()
	effect := lanes.NewEffect(lanes.EffectRestructure, "lane-1", 1, w.lane, "")
	if got := run.w.findRestructure(t.Context(), effect); got != lanes.FoundNothing {
		t.Fatalf("before the merge the worker finds %v", got)
	}
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if got := run.w.findRestructure(t.Context(), effect); got != lanes.Found {
		t.Errorf("after the merge the worker finds %v", got)
	}
}

// A design tip whose first-parent root is not the one the plan stored fails the work with layout_changed, and the
// branch is as it was.
func TestAGitConsolidationWhoseDesignRootDiffersFailsWithLayoutChanged(t *testing.T) {
	t.Parallel()
	w := newGitConsolidation(t)
	ws := w.path("ws")
	run := w.runChoosing(adoption.Request{Root: ws, Depth: 1}, func(plan planning.AdoptionPlan, chosen *planning.Restructure) {
		chosen.DesignPath, chosen.DesignRoot = plan.Design.Path, strings.Repeat("a", 40)
	})
	report, failed := run.restructure(t.Context(), ws, w.lane)
	if !failed || report.Reason != workorders.ReasonLayoutChanged || !strings.Contains(report.Detail, strings.Repeat("a", 40)) {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	if now := w.git(w.lane, "rev-parse", "HEAD"); now != w.tip {
		t.Errorf("the branch moved to %s", now)
	}
	if status := w.git(w.lane, "status", "--porcelain"); status != "" {
		t.Errorf("the worktree is dirty:\n%s", status)
	}
}

// A design repository that changed between the plan and the work fails the work with layout_changed, naming what
// differs, and merges nothing.
func TestAGitConsolidationWhoseDesignRepositoryChangedFailsWithLayoutChanged(t *testing.T) {
	t.Parallel()
	w := newGitConsolidation(t)
	ws, design := w.path("ws"), w.path("ws/repos/design")
	run := w.consolidate()
	if err := os.WriteFile(filepath.Join(design, "added.md"), []byte("added\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.git(design, "add", "added.md")
	w.git(design, "commit", "--quiet", "-m", "a commit made after the plan")
	// The plan reads the tip of the remote's default branch, which a clone keeps as a ref of its own.
	w.git(design, "update-ref", "refs/remotes/origin/main", "HEAD")
	report, failed := run.restructure(t.Context(), ws, w.lane)
	if !failed || report.Reason != workorders.ReasonLayoutChanged || !strings.Contains(report.Detail, "takes") {
		t.Fatalf("the restructuring reported %+v, failed %v", report, failed)
	}
	if now := w.git(w.lane, "rev-parse", "HEAD"); now != w.tip {
		t.Errorf("the branch moved to %s", now)
	}
}

// The inventory of the planning repository reads the merge commit, so the session onboards the documents the
// design repository brought, and not the branch's base, which holds none of them.
func TestTheInventoryOfAConsolidatedWorkspaceReadsTheMergeCommit(t *testing.T) {
	t.Parallel()
	w := newGitConsolidation(t)
	ws := w.path("ws")
	run := w.consolidate("tools")
	run.lease.Inventory = true
	run.rt.Inventory = inventory.Git{Env: w.env}
	run.rt.Repository = func(context.Context, string, string) (string, error) { return w.path("ws/repos/api"), nil }
	if report, failed := run.restructure(t.Context(), ws, w.lane); failed {
		t.Fatalf("the restructuring failed: %+v", report)
	}
	if err := run.writeInventory(t.Context(), ws, w.lane); err != nil {
		t.Fatal(err)
	}
	var written planning.Inventory
	if err := json.Unmarshal([]byte(w.readLane(lanes.InventoryFile)), &written); err != nil {
		t.Fatal(err)
	}
	var home planning.RepositoryInventory
	for _, one := range written.Repositories {
		if one.Name == "ws" {
			home = one
		}
	}
	var paths []string
	for _, document := range home.Documents {
		paths = append(paths, document.Path)
	}
	if home.BaseCommit != w.git(w.lane, "rev-parse", "HEAD") || !slices.Contains(paths, "docs/vision.md") || !slices.Contains(paths, "plan/goal.md") {
		t.Errorf("the inventory of the workspace is at %s and lists %v", home.BaseCommit, paths)
	}
}
