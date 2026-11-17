package entry

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/worker"
)

// planned is a lane request in the workspace's project, with the workspace's repository named the planning
// repository, as a lease names it.
func planned(lane, repository string) worker.LaneRequest {
	return worker.LaneRequest{Lane: lane, Repository: repository, Project: "prj-1", PlanningRepository: "repo-workspace"}
}

// ignoreWorktrees makes the workspace ignore its folder for lane worktrees, as a person's commit does. It does
// nothing when the workspace already does.
func (w *worktreeWorkspace) ignoreWorktrees() {
	held, err := os.ReadFile(filepath.Join(w.workspace.Dir, ".gitignore"))
	if err == nil && strings.Contains(string(held), planning.WorktreesLine) {
		return
	}
	w.workspace.Commit(".gitignore", "repos/\n"+planning.WorktreesLine+"\n")
}

// at spells a path as the disk does, the links in the part of it that exists followed, as git names a worktree.
func at(t *testing.T, path string) string {
	t.Helper()
	rest := ""
	for dir := path; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(real, rest)
		}
		if filepath.Dir(dir) == dir {
			t.Fatalf("no part of %s exists", path)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

// userLayer writes the user's profile layer.
func (w *worktreeWorkspace) userLayer(t *testing.T, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.paths.StateDir, profiles.UserFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// realWorkspace is the workspace's path as git spells it.
func (w *worktreeWorkspace) realWorkspace(t *testing.T) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(w.workspace.Dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// With a workspace that ignores /.ropeshift/worktrees/, a new lane worktree of each code clone inside it goes in
// <workspace>/.ropeshift/worktrees/<repo>/, on real git, and leaves the workspace's own status clean. The
// workspace repository's own lane stays outside it, under the worktree directory (FR-PROF-3, FR-PROF-6).
func TestLaneWorktreesOfCodeClonesGoInTheWorkspacesIgnoredFolder(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.ignoreWorktrees()
	workspace := w.realWorkspace(t)
	for _, name := range []string{"api", "web"} {
		lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones[name]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := filepath.Join(workspace, ".ropeshift", "worktrees", name, "lane-1"); at(t, lane.Worktree.Path) != want {
			t.Errorf("%s: the worktree is at %q, want %q", name, lane.Worktree.Path, want)
		}
		if lane.Unignored != "" {
			t.Errorf("%s: the workspace is named unignored: %q", name, lane.Unignored)
		}
		if _, err := os.Stat(filepath.Join(lane.Worktree.Path, "README.txt")); err != nil {
			t.Errorf("%s: the worktree holds no checkout: %v", name, err)
		}
		if !slices.Contains(w.worktrees(w.clones[name]), at(t, lane.Worktree.Path)) {
			t.Errorf("%s: git lists %v", name, w.worktrees(w.clones[name]))
		}
	}
	own, err := w.prov.Prepare(t.Context(), planned("lane-1", w.workspace.Dir))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", filepath.Base(w.workspace.Dir), "lane-1"); own.Worktree.Path != want {
		t.Errorf("the workspace's own lane is at %q, want %q outside the workspace", own.Worktree.Path, want)
	}
	if status := w.workspace.Git("status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("the workspace shows %q after its clones' lanes started", status)
	}
}

// The same workspace without the ignore line puts every worktree under the worktree directory, and the lane's start
// names the line to add, so a lane never lands in a folder the workspace's own git would show as untracked.
func TestAWorkspaceThatDoesNotIgnoreTheFolderGetsNoWorktreesAndSaysWhichLine(t *testing.T) {
	w := newWorktreeWorkspace(t)
	for _, name := range []string{"api", "web"} {
		lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones[name]))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if want := filepath.Join(w.paths.WorktreeDir, "prj-1", name, "lane-1"); lane.Worktree.Path != want {
			t.Errorf("%s: the worktree is at %q, want %q", name, lane.Worktree.Path, want)
		}
		if lane.Unignored != w.workspace.Dir {
			t.Errorf("%s: the unignored workspace is %q, want %q", name, lane.Unignored, w.workspace.Dir)
		}
	}
	if notice := lanes.UnignoredNotice(w.workspace.Dir); !strings.Contains(notice, "add `/.ropeshift/worktrees/` to its .gitignore") {
		t.Errorf("the notice reads %q", notice)
	}
	if _, err := os.Stat(filepath.Join(w.workspace.Dir, ".ropeshift")); err == nil {
		t.Error("a folder was made in the workspace that does not ignore it")
	}
}

// A user root is refused inside a code clone, inside the workspace outside its folder for worktrees, and under that
// folder while the workspace does not ignore it, where the detail names the line to add. Nothing is made. Under the
// folder, once ignored, it works.
func TestAUserRootInTheWorkspaceIsAllowedOnlyUnderItsIgnoredWorktreeFolder(t *testing.T) {
	w := newWorktreeWorkspace(t)
	workspace := w.workspace.Dir
	tests := []struct {
		name     string
		root     string
		ignored  bool
		refused  bool
		wantLine bool
	}{
		// The cases share one workspace, so the one that needs the folder not ignored comes before the line is committed.
		{name: "under the folder, not ignored", root: filepath.Join(workspace, ".ropeshift", "worktrees", "{repo}"), refused: true, wantLine: true},
		{name: "inside a code clone", root: filepath.Join(w.clones["web"], "wt"), ignored: true, refused: true},
		{name: "inside the workspace, outside the folder", root: filepath.Join(workspace, "lanes", "{repo}"), ignored: true, refused: true},
		{name: "under the folder, ignored", root: filepath.Join(workspace, ".ropeshift", "worktrees", "{repo}"), ignored: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.ignored {
				w.ignoreWorktrees()
			}
			w.userRoot(t, tt.root)
			before := w.worktrees(w.clones["api"])
			lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["api"]))
			var inside *worker.WorktreeInsideClone
			switch {
			case !tt.refused && err != nil:
				t.Fatalf("a root under the ignored folder is refused: %v", err)
			case tt.refused && !errors.As(err, &inside):
				t.Fatalf("got %v", err)
			case tt.refused && strings.Contains(inside.Detail(), "add `"+planning.WorktreesLine+"` to the .gitignore") != tt.wantLine:
				t.Errorf("the detail reads %q, and the line to add is wanted: %v", inside.Detail(), tt.wantLine)
			case tt.refused && !slices.Equal(before, w.worktrees(w.clones["api"])):
				t.Errorf("a worktree was made: %v", w.worktrees(w.clones["api"]))
			case !tt.refused && !strings.HasPrefix(at(t, lane.Worktree.Path), filepath.Join(w.realWorkspace(t), ".ropeshift", "worktrees")+string(filepath.Separator)):
				t.Errorf("the worktree is at %q", lane.Worktree.Path)
			}
			if tt.refused {
				for _, made := range []string{filepath.Join(workspace, "lanes"), filepath.Join(workspace, ".ropeshift"), filepath.Join(w.clones["web"], "wt")} {
					if _, err := os.Stat(made); err == nil {
						t.Errorf("%s was created", made)
					}
				}
			}
		})
	}
}

// The user's profile names a project's workspace over what the planning clone says, and an empty one says there is
// none: the same workspace then gives its clones the root outside every clone.
func TestTheUsersProfileDecidesWhichFolderIsTheWorkspace(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.ignoreWorktrees()
	workspace := w.realWorkspace(t)
	w.userLayer(t, "[projects.prj-1]\nworkspace = \"\"\n")
	lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["api"]))
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", "api", "lane-1"); err != nil || lane.Worktree.Path != want {
		t.Errorf("with no workspace the worktree is at %q (%v), want %q", lane.Worktree.Path, err, want)
	}
	// A profile that names the folder finds it with no planning repository named.
	w.userLayer(t, "[projects.prj-1]\nworkspace = \""+w.workspace.Dir+"\"\n")
	request := planned("lane-2", w.clones["web"])
	request.PlanningRepository = ""
	lane, err = w.prov.Prepare(t.Context(), request)
	if want := filepath.Join(workspace, ".ropeshift", "worktrees", "web", "lane-2"); err != nil || at(t, lane.Worktree.Path) != want {
		t.Errorf("with the workspace named the worktree is at %q (%v), want %q", lane.Worktree.Path, err, want)
	}
	// A relative path is a profile error.
	w.userLayer(t, "[projects.prj-1]\nworkspace = \"ws\"\n")
	if _, err := w.prov.Prepare(t.Context(), planned("lane-3", w.clones["web"])); !errors.Is(err, lanes.ErrBadProfile) {
		t.Errorf("a relative workspace gives %v", err)
	}
}

// A lane whose worktree stands under the worktree directory before the workspace ignored the folder goes on in it,
// on real git, makes no second worktree, and its start names where a new one would go. A lane with no worktree takes
// the workspace's folder.
func TestALaneKeepsItsWorktreeWhenTheWorkspaceFolderAppears(t *testing.T) {
	w := newWorktreeWorkspace(t)
	first, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["api"]))
	if err != nil {
		t.Fatal(err)
	}
	w.ignoreWorktrees()
	workspace := w.realWorkspace(t)
	before := w.worktrees(w.clones["api"])
	again := planned("lane-1", w.clones["api"])
	again.Recorded = first.Worktree.Path
	lane, err := w.prov.Prepare(t.Context(), again)
	if err != nil {
		t.Fatal(err)
	}
	would := filepath.Join(workspace, ".ropeshift", "worktrees", "api", "lane-1")
	if lane.Worktree.Path != first.Worktree.Path || lane.Kept == nil || at(t, lane.Kept.Would) != would {
		t.Errorf("the next generation is at %q and keeps %+v, want %q kept and %q named", lane.Worktree.Path, lane.Kept, first.Worktree.Path, would)
	}
	if after := w.worktrees(w.clones["api"]); !slices.Equal(before, after) {
		t.Errorf("a worktree was made: %v, then %v", before, after)
	}
	fresh, err := w.prov.Prepare(t.Context(), planned("lane-2", w.clones["api"]))
	if want := filepath.Join(workspace, ".ropeshift", "worktrees", "api", "lane-2"); err != nil || at(t, fresh.Worktree.Path) != want {
		t.Errorf("a new lane is at %q (%v), want %q", fresh.Worktree.Path, err, want)
	}
}
