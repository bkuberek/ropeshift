package checkouts

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// workspaceWithLanes is a workspace on real git that ignores its code folder and its folder for lane worktrees,
// holds a clone of one code repository in repos/, and holds a lane worktree of that clone and of itself under
// .ropeshift/worktrees/.
func workspaceWithLanes(t *testing.T) (*gittest.Repo, []string) {
	t.Helper()
	workspace := gittest.New(t)
	workspace.Commit(".gitignore", "repos/\n/.ropeshift/worktrees/\n")
	workspace.Commit(".ropeshift/profile.toml", "name = \"workspace\"\n")
	origin := gittest.New(t)
	origin.Commit("README.md", "api\n")
	clone := filepath.Join(workspace.Dir, "repos", "api")
	if err := os.MkdirAll(filepath.Dir(clone), 0o700); err != nil {
		t.Fatal(err)
	}
	origin.Git("clone", "--quiet", origin.Dir, clone)
	var worktrees []string
	for _, of := range []struct{ repository, at string }{
		{workspace.Dir, filepath.Join(workspace.Dir, ".ropeshift", "worktrees", "workspace", "lane-1")},
		{clone, filepath.Join(workspace.Dir, ".ropeshift", "worktrees", "api", "lane-1")},
	} {
		workspace.Git("-C", of.repository, "worktree", "add", "--quiet", "-b", "lane/lane-1", of.at)
		worktrees = append(worktrees, of.at)
	}
	return workspace, worktrees
}

// A workspace's ignored .ropeshift/worktrees/ folder is no ignored directory to search for clones: git lists it,
// and the list leaves it out, with every other .ropeshift folder (FR-PROF-6).
func TestIgnoredDirectoriesLeaveOutTheFolderOfLaneWorktrees(t *testing.T) {
	workspace, _ := workspaceWithLanes(t)
	got, err := layoutsOf(workspace).IgnoredDirectories(t.Context(), workspace.Dir)
	if want := []string{"repos"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ignored directories read %v, want %v: %v", got, want, err)
	}
}

// No scan of a workspace finds a lane worktree as a clone, and a path inside a .ropeshift folder is refused by
// name, so neither a registration nor an adoption takes a lane worktree for a repository.
func TestNoScanOrNameFindsALaneWorktreeAsAClone(t *testing.T) {
	workspace, worktrees := workspaceWithLanes(t)
	kept := layoutsOf(workspace)
	found, _, err := kept.Find(t.Context(), []string{workspace.Dir}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, clone := range found {
		if strings.Contains(clone.Path, ".ropeshift") {
			t.Errorf("the scan found a lane worktree: %s", clone.Path)
		}
	}
	for _, at := range append([]string{filepath.Join(workspace.Dir, ".ropeshift", "worktrees")}, worktrees...) {
		found, unread, err := kept.Find(t.Context(), []string{at}, 4)
		if err != nil || len(found) != 0 || len(unread) != 1 || !strings.Contains(unread[0].Reason, ".ropeshift") {
			t.Errorf("%s: found %v and unread %v (%v)", at, found, unread, err)
		}
	}
}

// Inside the workspace's clone, a path is allowed only under its ignored .ropeshift/worktrees/ folder, judged by
// where the path really leads: a link in that folder that leads into a code clone gets no allowance, and a folder
// the workspace does not ignore gets none either.
func TestTheWorkspaceFolderAllowanceFollowsLinksAndTheIgnoreRule(t *testing.T) {
	workspace, _ := workspaceWithLanes(t)
	kept := layoutsOf(workspace)
	api := filepath.Join(workspace.Dir, "repos", "api")
	clones := []string{workspace.Dir, api}
	folder := filepath.Join(workspace.Dir, ".ropeshift", "worktrees")
	link := filepath.Join(folder, "sneak")
	if err := os.Symlink(api, link); err != nil {
		t.Fatal(err)
	}
	for name, one := range map[string]struct {
		target    string
		workspace string
		inside    bool
	}{
		"under the ignored folder":     {filepath.Join(folder, "api", "lane-2"), workspace.Dir, false},
		"with no workspace named":      {filepath.Join(folder, "api", "lane-2"), "", true},
		"through a link into a clone":  {filepath.Join(link, "lane-2"), workspace.Dir, true},
		"elsewhere in the workspace":   {filepath.Join(workspace.Dir, "lanes", "api"), workspace.Dir, true},
		"the workspace's profile file": {filepath.Join(workspace.Dir, ".ropeshift", "x"), workspace.Dir, true},
	} {
		if _, inside, err := ports.InsideClone(t.Context(), kept, clones, one.target, one.workspace); err != nil || inside != one.inside {
			t.Errorf("%s: inside %v (%v), want %v", name, inside, err, one.inside)
		}
	}
	workspace.Commit(".gitignore", "repos/\n")
	if _, inside, err := ports.InsideClone(t.Context(), kept, clones, filepath.Join(folder, "api", "lane-2"), workspace.Dir); err != nil || !inside {
		t.Errorf("a folder the workspace does not ignore is allowed: inside %v (%v)", inside, err)
	}
}

// A lane worktree under a .ropeshift folder is reserved, as the installation's own directories are, so a
// registration refuses it, and a clone beside it is not.
func TestALaneWorktreeInAWorkspaceIsReserved(t *testing.T) {
	workspace, worktrees := workspaceWithLanes(t)
	kept := layoutsOf(workspace)
	for _, at := range worktrees {
		if _, reserved, err := kept.Resolve(t.Context(), at); err != nil || !reserved {
			t.Errorf("%s is not reserved (%v)", at, err)
		}
	}
	if _, reserved, err := kept.Resolve(t.Context(), filepath.Join(workspace.Dir, "repos", "api")); err != nil || reserved {
		t.Errorf("a code clone is reserved (%v)", err)
	}
	if !ports.InRopeshiftFolder("/ws/.ropeshift/worktrees/api") || ports.InRopeshiftFolder("/ws/repos/ropeshift") || ports.InRopeshiftFolder("/ws/.ropeshift-old") {
		t.Error("InRopeshiftFolder matches a component other than .ropeshift, or misses it")
	}
}
