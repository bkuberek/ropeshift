package entry

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// worktreeWorkspace is a workspace on real git whose code repositories are clones inside it, in repos/, the
// layout that put every lane worktree inside the folder of clones before the built-in root moved.
type worktreeWorkspace struct {
	home      string
	paths     api.Paths
	workspace *gittest.Repo
	clones    map[string]string
	prov      worker.Provisioner
}

// newWorktreeWorkspace puts an installation's directories under a temporary home, as ROPESHIFT_STATE_DIR
// does for a test or a second installation, so nothing here names the person's own directories.
func newWorktreeWorkspace(t *testing.T) *worktreeWorkspace {
	t.Helper()
	gittest.Require(t)
	// A short home, because the control socket's path has a limit, and one with its links followed, as a
	// disk that ends /var in a link would otherwise spell each path two ways.
	home := apitest.ShortDir(t)
	t.Setenv(api.EnvStateDir, filepath.Join(home, "state"))
	t.Setenv(api.EnvRunDir, filepath.Join(home, "run"))
	t.Setenv(api.EnvWorktreeDir, "")
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "xdg-data"))
	paths, err := api.ResolvePaths(runtime.GOOS, os.Getenv, home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	w := &worktreeWorkspace{home: home, paths: paths, workspace: gittest.New(t), clones: map[string]string{}}
	w.workspace.Commit(".gitignore", "repos/\n")
	for _, name := range []string{"api", "web"} {
		origin := gittest.New(t)
		origin.Commit("README.txt", name+"\n")
		clone := filepath.Join(w.workspace.Dir, "repos", name)
		if err := os.MkdirAll(filepath.Dir(clone), 0o700); err != nil {
			t.Fatal(err)
		}
		origin.Git("clone", "--quiet", origin.Dir, clone)
		w.clones[name] = clone
	}
	kept := soloCheckouts(paths)
	for name, repository := range map[string]string{"workspace": w.workspace.Dir, "api": w.clones["api"], "web": w.clones["web"]} {
		if err := kept.Keep(t.Context(), "prj-1", "repo-"+name, repository); err != nil {
			t.Fatal(err)
		}
	}
	// A second project keeps a clone of its own, which no lane of the first may land in either.
	other := gittest.New(t)
	other.Commit("README.txt", "other\n")
	if err := kept.Keep(t.Context(), "prj-2", "repo-other", other.Dir); err != nil {
		t.Fatal(err)
	}
	w.clones["other"] = other.Dir
	lanesRuntime, _, err := soloLanes(paths, soloOptions{}, kept, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	w.prov = lanesRuntime.Provisioner
	w.prov.Worktrees = gitcli.Worktrees{Env: w.workspace.Env()}
	return w
}

// userRoot writes the user's layer with a worktree root.
func (w *worktreeWorkspace) userRoot(t *testing.T, root string) {
	t.Helper()
	text := "[worktree]\nroot = \"" + root + "\"\n"
	if err := os.WriteFile(filepath.Join(w.paths.StateDir, profiles.UserFile), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// worktrees lists the worktrees a repository holds, by git.
func (w *worktreeWorkspace) worktrees(repository string) []string {
	var paths []string
	for _, line := range strings.Split(w.workspace.Git("-C", repository, "worktree", "list", "--porcelain"), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, path)
		}
	}
	return paths
}

// With no root in the user's profile, each lane's worktree lands under the installation's worktree
// directory, by project and clone folder, and nowhere in the workspace or any clone (FR-PROF-3). Nothing is
// made in repos/, and the worktree directory is one the checkouts reserve.
func TestLaneWorktreesOfAWorkspaceLandOutsideTheWorkspaceAndEveryClone(t *testing.T) {
	w := newWorktreeWorkspace(t)
	if want := filepath.Join(w.paths.StateDir, "worktrees"); w.paths.WorktreeDir != want {
		t.Fatalf("the worktree directory under an explicit state directory is %q, want %q", w.paths.WorktreeDir, want)
	}
	for _, repository := range []struct{ name, path string }{
		{"workspace", w.workspace.Dir}, {"api", w.clones["api"]}, {"web", w.clones["web"]},
	} {
		lane, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: repository.path, Project: "prj-1"})
		if err != nil {
			t.Fatalf("%s: %v", repository.name, err)
		}
		want := filepath.Join(w.paths.WorktreeDir, "prj-1", filepath.Base(repository.path), "lane-1")
		if lane.Worktree.Path != want {
			t.Errorf("%s: the worktree is at %q, want %q", repository.name, lane.Worktree.Path, want)
		}
		if _, err := os.Stat(filepath.Join(lane.Worktree.Path, "README.txt")); repository.name != "workspace" && err != nil {
			t.Errorf("%s: the worktree holds no checkout: %v", repository.name, err)
		}
		for _, clone := range []string{w.workspace.Dir, w.clones["api"], w.clones["web"]} {
			if resolved, err := filepath.EvalSymlinks(clone); err != nil {
				t.Fatal(err)
			} else if lane.Worktree.Path == resolved || strings.HasPrefix(lane.Worktree.Path, resolved+string(filepath.Separator)) {
				t.Errorf("%s: the worktree %q is inside %q", repository.name, lane.Worktree.Path, clone)
			}
		}
		if !slices.Contains(w.worktrees(repository.path), lane.Worktree.Path) {
			t.Errorf("%s: git lists %v", repository.name, w.worktrees(repository.path))
		}
		if _, reserved, err := soloCheckouts(w.paths).Resolve(t.Context(), lane.Worktree.Path); err != nil || !reserved {
			t.Errorf("%s: the lane's worktree is not reserved (%v): a registration could take it for a clone", repository.name, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(w.workspace.Dir, "repos"))
	if err != nil || len(entries) != 2 {
		t.Errorf("repos/ holds %v (%v), want only the two clones", entries, err)
	}
	// A second project's clone of the same folder name gets a folder of its own.
	if _, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: w.clones["other"], Project: "prj-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(w.paths.WorktreeDir, "prj-2", "repository", "lane-1")); err != nil {
		t.Errorf("the second project's worktree is not in its own folder: %v", err)
	}
}

// A root a person set that is a clone, lies inside one, or reaches inside through a symbolic link fails the
// lane with the new refusal before any worktree exists, and creates nothing in the clone.
func TestAWorktreeRootInsideARegisteredCloneIsRefusedBeforeAnythingIsMade(t *testing.T) {
	w := newWorktreeWorkspace(t)
	link := filepath.Join(w.home, "link")
	if err := os.Symlink(w.clones["api"], link); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		root       string
		repository string
		clone      string
		made       string
	}{
		{"inside the repository's own clone", "lanes/{project}", w.clones["api"], w.clones["api"], filepath.Join(w.clones["api"], "lanes")},
		{"a root that is another clone", w.clones["web"], w.clones["api"], w.clones["web"], ""},
		{"inside another clone", filepath.Join(w.clones["web"], "wt"), w.clones["api"], w.clones["web"], filepath.Join(w.clones["web"], "wt")},
		{"inside the workspace's clone", filepath.Join(w.workspace.Dir, "lanes"), w.clones["api"], w.workspace.Dir, filepath.Join(w.workspace.Dir, "lanes")},
		{"through a symbolic link", filepath.Join(link, "wt"), w.clones["web"], w.clones["api"], filepath.Join(w.clones["api"], "wt")},
		{"inside a clone another project keeps", filepath.Join(w.clones["other"], "wt"), w.clones["api"], w.clones["other"], filepath.Join(w.clones["other"], "wt")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w.userRoot(t, tt.root)
			before := w.worktrees(tt.repository)
			_, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: tt.repository, Project: "prj-1"})
			var inside *worker.WorktreeInsideClone
			if !errors.As(err, &inside) {
				t.Fatalf("got %v", err)
			}
			if inside.Source != lanes.RootFromUser || !strings.Contains(inside.Detail(), "set `[worktree] root` in your profile") {
				t.Errorf("the refusal reads %+v: %s", inside, inside.Detail())
			}
			if after := w.worktrees(tt.repository); !slices.Equal(before, after) {
				t.Errorf("a worktree was made: %v, then %v", before, after)
			}
			if tt.made != "" {
				if _, err := os.Stat(tt.made); err == nil {
					t.Errorf("%s was created in a clone", tt.made)
				}
			}
		})
	}
}

// A root beside every clone, which a person chose, still works, and so does one with the project in it.
func TestAUserRootOutsideEveryCloneStillPlacesTheWorktree(t *testing.T) {
	w := newWorktreeWorkspace(t)
	outside := filepath.Join(w.home, "my-lanes")
	w.userRoot(t, outside+"/{project}/{repo}")
	lane, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: w.clones["api"], Project: "prj-1"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(outside, "prj-1", "api", "lane-1"); lane.Worktree.Path != want {
		t.Errorf("the worktree is at %q, want %q", lane.Worktree.Path, want)
	}
}

// A worktree that holds the lane's branch and is not the lane's own, one a person made inside the clone, is never
// taken as the lane's place: the start is refused as before and nothing of the person's is touched or reused.
func TestAForeignWorktreeOnRealGitIsNeitherReusedNorTouched(t *testing.T) {
	w := newWorktreeWorkspace(t)
	api := w.clones["api"]
	mine := filepath.Join(api, "mine")
	w.workspace.Git("-C", api, "worktree", "add", "--quiet", "-b", "lane/lane-1", mine)
	before := w.worktrees(api)
	_, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: api, Project: "prj-1"})
	if !errors.Is(err, ports.ErrWorktreeConflict) {
		t.Fatalf("got %v", err)
	}
	if after := w.worktrees(api); !slices.Equal(before, after) {
		t.Errorf("the worktrees changed: %v, then %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(mine, "README.txt")); err != nil {
		t.Errorf("the person's worktree was touched: %v", err)
	}
}

// A clone only the daemon's sources know, which no checkout records, is checked like any other: a root inside it
// refuses the lane.
func TestARootInsideACloneNoCheckoutRecordsIsRefused(t *testing.T) {
	w := newWorktreeWorkspace(t)
	source := gittest.New(t)
	source.Commit("README.txt", "source\n")
	w.userRoot(t, "lanes/{project}")
	_, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: source.Dir, Project: "prj-1"})
	var inside *worker.WorktreeInsideClone
	if !errors.As(err, &inside) || inside.Clone != source.Dir {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(source.Dir, "lanes")); err == nil {
		t.Error("a folder was made in the source clone")
	}
}

// An installation upgraded with lanes in flight: each lane's worktree stands where the old default put it, beside
// its clone, which for a clone in a workspace's repos/ is inside the workspace. The next generation of each goes on
// in that worktree, on real git, makes no second worktree, and is not refused for where it stands, while a lane
// with no worktree yet takes the new root.
func TestALaneInFlightAtAnUpgradeGoesOnInItsOldWorktree(t *testing.T) {
	w := newWorktreeWorkspace(t)
	old := map[string]string{}
	for name, clone := range map[string]string{"api": w.clones["api"], "workspace": w.workspace.Dir} {
		old[name] = filepath.Join(filepath.Dir(clone), filepath.Base(clone)+"-lanes", "lane-1")
		if _, err := w.prov.Worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: clone, Path: old[name], Branch: "lane/lane-1"}); err != nil {
			t.Fatal(err)
		}
		// Git names a worktree by its real path, as a disk that spells a temporary directory through a link gives.
		var err error
		if old[name], err = filepath.EvalSymlinks(old[name]); err != nil {
			t.Fatal(err)
		}
	}
	for name, clone := range map[string]string{"api": w.clones["api"], "workspace": w.workspace.Dir} {
		before := w.worktrees(clone)
		lane, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-1", Repository: clone, Project: "prj-1"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lane.Worktree.Path != old[name] {
			t.Errorf("%s: the next generation is at %q, want its old worktree %q", name, lane.Worktree.Path, old[name])
		}
		if lane.Kept == nil || lane.Kept.Path != old[name] || lane.Kept.Would != filepath.Join(w.paths.WorktreeDir, "prj-1", filepath.Base(clone), "lane-1") {
			t.Errorf("%s: the kept placement reads %+v", name, lane.Kept)
		}
		// The api's old worktree lies inside the workspace's clone, which a new one may not, and is said to.
		if wantInside := map[string]string{"api": w.workspace.Dir, "workspace": ""}[name]; lane.Kept != nil && lane.Kept.Inside != wantInside {
			t.Errorf("%s: the kept worktree lies inside %q, want %q", name, lane.Kept.Inside, wantInside)
		}
		if after := w.worktrees(clone); !slices.Equal(before, after) {
			t.Errorf("%s: a worktree was made: %v, then %v", name, before, after)
		}
	}
	if _, err := os.Stat(w.paths.WorktreeDir); err == nil {
		t.Errorf("something was made under the new worktree directory %s", w.paths.WorktreeDir)
	}
	// The api's old worktree lies inside the workspace's clone and is kept, while a new lane there is placed
	// at the new root.
	fresh, err := w.prov.Prepare(t.Context(), worker.LaneRequest{Lane: "lane-2", Repository: w.clones["api"], Project: "prj-1"})
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", "api", "lane-2"); err != nil || fresh.Worktree.Path != want {
		t.Errorf("a new lane is at %q (%v), want %q", fresh.Worktree.Path, err, want)
	}
}
