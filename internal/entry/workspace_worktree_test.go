package entry

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
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
		if lane.Fallback.Reason != "" {
			t.Errorf("%s: the root fell back: %+v", name, lane.Fallback)
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
		if lane.Fallback.Workspace != w.workspace.Dir || lane.Fallback.Reason != lanes.FallbackUnignored {
			t.Errorf("%s: the fallback is %+v, want the unignored workspace %q", name, lane.Fallback, w.workspace.Dir)
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

// A link planted in the workspace's ignored folder passes git's ignore check as a path, and a lane there would be
// made wherever the link leads. The lane's folder is judged by where it really is: it falls back to the root outside
// every clone, nothing is made at the link's target, and the doctor says why for that checkout (FR-PROF-3).
func TestALinkInTheWorkspaceFolderLeadsNoLaneOutOfIt(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.ignoreWorktrees()
	elsewhere := filepath.Join(w.home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(w.workspace.Dir, ".ropeshift", "worktrees")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(folder, "api")); err != nil {
		t.Fatal(err)
	}
	lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["api"]))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", "api", "lane-1"); lane.Worktree.Path != want {
		t.Errorf("the worktree is at %q, want %q outside every clone", lane.Worktree.Path, want)
	}
	if lane.Fallback.Reason != lanes.FallbackEscapes || lane.Fallback.Workspace != w.workspace.Dir {
		t.Errorf("the fallback is %+v", lane.Fallback)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("the link's target holds %v (%v)", entries, err)
	}
	// A repository whose folder is no link keeps the workspace root, and the doctor names the one that fell back.
	other, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["web"]))
	if want := filepath.Join(w.realWorkspace(t), ".ropeshift", "worktrees", "web", "lane-1"); err != nil || at(t, other.Worktree.Path) != want {
		t.Errorf("web is at %q (%v), want %q", other.Worktree.Path, err, want)
	}
	w.userLayer(t, "[projects.prj-1]\nworkspace = \""+w.workspace.Dir+"\"\n")
	report, err := worktreeReport(t.Context(), w.paths)
	if err != nil || len(report.Workspaces) != 1 {
		t.Fatalf("the report names %+v (%v)", report.Workspaces, err)
	}
	reasons := map[string]string{}
	for _, root := range report.Workspaces[0].Roots {
		reasons[root.Checkout] = root.Fallback
	}
	if reasons[w.clones["api"]] != lanes.FallbackEscapes || reasons[w.clones["web"]] != "" {
		t.Errorf("the doctor's fallbacks are %v", reasons)
	}
}

// A link planted at the lane's own folder, under a repository folder that is a plain directory, leads the lane out of
// the workspace as a link at the repository's folder does. The lane falls back to the root outside every clone,
// nothing is made at the link's target, and a lane of another name keeps the workspace root (FR-PROF-3).
func TestALinkAtTheLanesOwnFolderLeadsNoLaneOutOfTheWorkspace(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.ignoreWorktrees()
	elsewhere := filepath.Join(w.home, "elsewhere")
	repoFolder := filepath.Join(w.workspace.Dir, ".ropeshift", "worktrees", "api")
	for _, dir := range []string{elsewhere, repoFolder} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(elsewhere, filepath.Join(repoFolder, "lane-1")); err != nil {
		t.Fatal(err)
	}
	lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["api"]))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", "api", "lane-1"); lane.Worktree.Path != want {
		t.Errorf("the worktree is at %q, want %q outside every clone", lane.Worktree.Path, want)
	}
	if lane.Fallback.Reason != lanes.FallbackEscapes || lane.Fallback.Workspace != w.workspace.Dir {
		t.Errorf("the fallback is %+v", lane.Fallback)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("the link's target holds %v (%v)", entries, err)
	}
	other, err := w.prov.Prepare(t.Context(), planned("lane-2", w.clones["api"]))
	if want := filepath.Join(w.realWorkspace(t), ".ropeshift", "worktrees", "api", "lane-2"); err != nil || at(t, other.Worktree.Path) != want {
		t.Errorf("lane-2 is at %q (%v), want %q", other.Worktree.Path, err, want)
	}
}

// A workspace git cannot answer for, here one whose .ropeshift is a link, as git refuses a path beyond a link with
// exit status 128, stops no lane that never uses it: a lane with a root a person set starts, a lane on the built-in
// root starts outside every clone with the reason, and the doctor reports the error as a finding of that workspace
// and still reports the rest (FR-PROF-6).
func TestAWorkspaceGitCannotAnswerForStopsNoLaneAndNotTheDoctor(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.ignoreWorktrees()
	elsewhere := filepath.Join(w.home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(w.workspace.Dir, ".ropeshift")); err != nil {
		t.Fatal(err)
	}
	named := "[projects.prj-1]\nworkspace = \"" + w.workspace.Dir + "\"\n"
	w.userLayer(t, named)
	lane, err := w.prov.Prepare(t.Context(), planned("lane-1", w.clones["api"]))
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", "api", "lane-1"); err != nil || lane.Worktree.Path != want || lane.Fallback.Reason != lanes.FallbackUnreadable {
		t.Errorf("a lane on the built-in root is at %q with %+v (%v), want %q and the reason", lane.Worktree.Path, lane.Fallback, err, want)
	}
	mine := filepath.Join(w.home, "mine")
	w.userLayer(t, "[worktree]\nroot = \""+mine+"/{project}/{repo}\"\n"+named)
	lane, err = w.prov.Prepare(t.Context(), planned("lane-2", w.clones["web"]))
	if want := filepath.Join(mine, "prj-1", "web", "lane-2"); err != nil || at(t, lane.Worktree.Path) != at(t, want) || lane.Fallback.Reason != "" {
		t.Errorf("a lane on a root a person set is at %q with %+v (%v), want %q", lane.Worktree.Path, lane.Fallback, err, want)
	}
	report, err := worktreeReport(t.Context(), w.paths)
	if err != nil || len(report.Workspaces) != 1 {
		t.Fatalf("the doctor fails or names %+v (%v)", report.Workspaces, err)
	}
	if held := report.Workspaces[0]; held.Project != "prj-1" || held.Error == "" || !strings.Contains(held.Error, "128") {
		t.Errorf("the doctor reports the workspace as %+v", held)
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

// The doctor, which reads no ledger, names the workspace the user's profile gives with whether git says it ignores
// the folder for worktrees, and the root each of its checkouts gets, and says which line the workspace lacks.
func TestTheDoctorNamesTheProfilesWorkspaceAndItsIgnoreState(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.userLayer(t, "[projects.prj-1]\nworkspace = \""+w.workspace.Dir+"\"\n")
	report, err := worktreeReport(t.Context(), w.paths)
	if err != nil || len(report.Workspaces) != 1 {
		t.Fatalf("the report names %+v (%v)", report.Workspaces, err)
	}
	held := report.Workspaces[0]
	if held.Project != "prj-1" || held.Found != lanes.WorkspaceFromProfile || !held.Clone || held.Ignored || held.Line != planning.WorktreesLine {
		t.Errorf("an unignored workspace is reported as %+v", held)
	}
	w.ignoreWorktrees()
	if report, err = worktreeReport(t.Context(), w.paths); err != nil {
		t.Fatal(err)
	}
	held = report.Workspaces[0]
	if !held.Ignored || held.Line != "" || len(report.Inside) != 0 {
		t.Errorf("an ignoring workspace is reported as %+v, with %+v inside clones", held, report.Inside)
	}
	roots := map[string]string{}
	for _, root := range held.Roots {
		roots[root.Checkout] = root.Root
	}
	if want := filepath.Join(w.workspace.Dir, ".ropeshift", "worktrees", "api"); roots[w.clones["api"]] != want {
		t.Errorf("the api checkout gets %q, want %q", roots[w.clones["api"]], want)
	}
	if want := filepath.Join(w.paths.WorktreeDir, "prj-1", filepath.Base(w.workspace.Dir)); roots[w.workspace.Dir] != want {
		t.Errorf("the workspace's own checkout gets %q, want %q", roots[w.workspace.Dir], want)
	}
}

// The doctor finds a workspace through the planning clone too: the planning repository the worker's journal holds
// from desired state makes the workspace of a project whose profile names none, and it counts in the roots the
// report gives. A journal that is absent, or names no planning repository, leaves only the profile's workspaces.
func TestTheDoctorFindsAWorkspaceThroughThePlanningCloneFromTheJournal(t *testing.T) {
	w := newWorktreeWorkspace(t)
	w.ignoreWorktrees()
	report, err := worktreeReport(t.Context(), w.paths)
	if err != nil || len(report.Workspaces) != 0 {
		t.Fatalf("with no journal the report names %+v (%v)", report.Workspaces, err)
	}
	journal, err := openJournal(t.Context(), w.paths.StateDir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	state := ledger.DesiredState{Rev: 1, Projects: []ledger.DesiredProject{{ID: "prj-1", Name: "one"}, {ID: "prj-2", Name: "two"}}}
	if err := journal.SaveDesired(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if report, err = worktreeReport(t.Context(), w.paths); err != nil || len(report.Workspaces) != 0 {
		t.Fatalf("a journal that names no planning repository gives %+v (%v)", report.Workspaces, err)
	}
	state.Projects[0].PlanningRepository = "repo-workspace"
	if err := journal.SaveDesired(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if report, err = worktreeReport(t.Context(), w.paths); err != nil || len(report.Workspaces) != 1 {
		t.Fatalf("the report names %+v (%v)", report.Workspaces, err)
	}
	held := report.Workspaces[0]
	if held.Project != "prj-1" || held.Path != w.workspace.Dir || held.Found != lanes.WorkspaceFromPlanning || !held.Clone || !held.Ignored {
		t.Errorf("the workspace is reported as %+v", held)
	}
	roots := map[string]string{}
	for _, root := range held.Roots {
		roots[root.Checkout] = root.Root
	}
	if want := filepath.Join(w.workspace.Dir, ".ropeshift", "worktrees", "web"); roots[w.clones["web"]] != want {
		t.Errorf("the web checkout gets %q, want %q", roots[w.clones["web"]], want)
	}
}

// The doctor only reads the worker's journal: a journal file it cannot write is read all the same, and the file is
// left as it was, byte for byte and in its modification time.
func TestTheDoctorReadsTheJournalWithoutChangingIt(t *testing.T) {
	w := newWorktreeWorkspace(t)
	journal, err := openJournal(t.Context(), w.paths.StateDir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	state := ledger.DesiredState{Rev: 1, Projects: []ledger.DesiredProject{{ID: "prj-1", Name: "one", PlanningRepository: "repo-workspace"}}}
	if err := journal.SaveDesired(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(w.paths.StateDir, api.JournalFileName)
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(file, 0o600) })
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if planned := plannedProjects(t.Context(), w.paths.StateDir); planned["prj-1"] != "repo-workspace" {
		t.Errorf("the doctor reads %v from a journal it cannot write", planned)
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Errorf("the journal file changed (%v)", err)
	}
	if again, err := os.Stat(file); err != nil || !again.ModTime().Equal(info.ModTime()) {
		t.Errorf("the journal file's modification time moved (%v)", err)
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
