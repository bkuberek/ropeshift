package worker_test

import (
	"errors"
	"path"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/worker"
)

// workspaceMachine is a worker that keeps a planning repository's clone at /work/ws and two code clones in it, in
// repos/, the layout the recommended workspace has.
func workspaceMachine(t *testing.T) *fake.Checkouts {
	t.Helper()
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "plan", "/work/ws")
	keeping(t, machine, "prj-1", "api", "/work/ws/repos/api")
	keeping(t, machine, "prj-1", "web", "/work/ws/repos/web")
	return machine
}

func named(path string) *string { return &path }

// The workspace is the folder the user's profile names, else the planning repository's clone when another clone of
// the project lies inside it at a path it ignores, else none. An empty folder says there is none, and a clone
// beside the planning clone, or inside it at a path it does not ignore, makes no workspace (FR-PROF-6).
func TestAProjectsWorkspaceIsFoundFromTheProfileOrThePlanningClone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		setup     func(*fake.Checkouts)
		workspace *string
		planning  string
		want      string
		found     string
	}{
		{name: "the planning clone, with the clones in its ignored folder", setup: func(m *fake.Checkouts) { m.Ignore("/work/ws", "repos") },
			planning: "plan", want: "/work/ws", found: lanes.WorkspaceFromPlanning},
		{name: "the planning clone holds the clones at a path it does not ignore", planning: "plan"},
		{name: "no planning repository is named", setup: func(m *fake.Checkouts) { m.Ignore("/work/ws", "repos") }},
		{name: "a code repository is named the planning repository", setup: func(m *fake.Checkouts) { m.Ignore("/work/ws", "repos") }, planning: "api"},
		{name: "the profile names a folder", workspace: named("/work/other"), planning: "plan", want: "/work/other", found: lanes.WorkspaceFromProfile},
		{name: "the profile names none", setup: func(m *fake.Checkouts) { m.Ignore("/work/ws", "repos") }, workspace: named(""), planning: "plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			machine := workspaceMachine(t)
			if tt.setup != nil {
				tt.setup(machine)
			}
			profile := lanes.Profile{}
			if tt.workspace != nil {
				profile.Projects = map[string]lanes.ProjectSettings{"prj-1": {Workspace: tt.workspace}}
			}
			got, found, err := worker.FindWorkspace(t.Context(), machine, profile, "prj-1", tt.planning)
			if err != nil {
				t.Fatal(err)
			}
			if found != (tt.want != "") || got.Path != tt.want || got.Found != tt.found {
				t.Errorf("the workspace is %+v (found %v), want %q from %q", got, found, tt.want, tt.found)
			}
		})
	}
}

// A clone nothing else of the project lies inside beside the planning clone, as a design repository with its code
// repositories beside it, has no workspace until the clones move inside it.
func TestClonesBesideThePlanningCloneMakeNoWorkspace(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "plan", "/work/design")
	keeping(t, machine, "prj-1", "api", "/work/api")
	machine.Ignore("/work/design", "repos")
	if got, found, err := worker.FindWorkspace(t.Context(), machine, lanes.Profile{}, "prj-1", "plan"); err != nil || found {
		t.Errorf("the workspace is %+v (found %v, %v), want none", got, found, err)
	}
}

// A workspace the profile names that is a plain folder, not a clone, needs no ignore check: its worktree folder
// is never in a repository.
func TestAPlainFolderWorkspaceNeedsNoIgnoreCheck(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "api", "/work/plain/api")
	profile := lanes.Profile{Projects: map[string]lanes.ProjectSettings{"prj-1": {Workspace: named("/work/plain")}}}
	got, found, err := worker.FindWorkspace(t.Context(), machine, profile, "prj-1", "")
	if err != nil || !found || got.Clone || !got.Ignored {
		t.Errorf("the workspace is %+v (found %v, %v), want a plain folder that needs no check", got, found, err)
	}
}

// A clone of a workspace gets the root in the workspace's .ropeshift/worktrees/ folder when the workspace ignores
// it, the workspace itself and a clone outside it keep the root outside every clone, and a workspace that does not
// ignore the folder is no place: the built-in root holds and the start says which line to add.
func TestABuiltInRootInsideAWorkspaceNeedsTheWorkspaceToIgnoreIt(t *testing.T) {
	t.Parallel()
	const outside = "/data/ropeshift/worktrees"
	for _, tt := range []struct {
		name       string
		ignores    []string
		repository string
		want       string
		unignored  string
	}{
		{"a code clone, with the folder ignored", []string{"repos", ".ropeshift/worktrees"}, "/work/ws/repos/api", "/work/ws/.ropeshift/worktrees/api/l1", ""},
		{"the workspace's own lane, with the folder ignored", []string{"repos", ".ropeshift/worktrees"}, "/work/ws", outside + "/prj-1/ws/l1", ""},
		{"a code clone, with the folder not ignored", []string{"repos"}, "/work/ws/repos/api", outside + "/prj-1/api/l1", "/work/ws"},
		{"the workspace's own lane, with the folder not ignored", []string{"repos"}, "/work/ws", outside + "/prj-1/ws/l1", ""},
		{"a clone outside the workspace", []string{"repos", ".ropeshift/worktrees"}, "/work/lib", outside + "/prj-1/lib/l1", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, _ := provisioner(nil)
			source.SetDefaultRoot(outside + "/{project}/{repo}")
			machine := workspaceMachine(t)
			keeping(t, machine, "prj-1", "lib", "/work/lib")
			machine.Ignore("/work/ws", tt.ignores...)
			p.Checkouts = machine
			lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: tt.repository, Project: "prj-1", PlanningRepository: "plan"})
			if err != nil {
				t.Fatal(err)
			}
			if lane.Worktree.Path != tt.want || lane.Fallback.Workspace != tt.unignored || (tt.unignored != "") != (lane.Fallback.Reason == lanes.FallbackUnignored) {
				t.Errorf("the worktree is %q and the fallback %+v, want %q and the unignored workspace %q", lane.Worktree.Path, lane.Fallback, tt.want, tt.unignored)
			}
			if tt.unignored != "" && !strings.Contains(lane.Fallback.Notice(), "/.ropeshift/worktrees/") {
				t.Error("the notice names no line to add")
			}
		})
	}
}

// A workspace root is used only while the lane's folder, with every link followed, stays under the workspace's
// .ropeshift/worktrees/ folder. A link planted in the folder, or a link for .ropeshift itself, that leads out of it
// keeps the root outside every clone for a clone workspace and for a plain folder alike, and the fallback says why.
func TestAWorkspaceRootThatLeadsOutOfTheWorkspaceIsNotUsed(t *testing.T) {
	t.Parallel()
	const outside = "/data/ropeshift/worktrees"
	for _, tt := range []struct {
		name      string
		workspace *string
		ignores   []string
		links     map[string]string
		repo      string
	}{
		{"a link for the repository's folder", nil, []string{"repos", ".ropeshift/worktrees"},
			map[string]string{"/work/ws/.ropeshift/worktrees/api": "/elsewhere"}, "/work/ws/repos/api"},
		{"a link for the lane's own folder", nil, []string{"repos", ".ropeshift/worktrees"},
			map[string]string{"/work/ws/.ropeshift/worktrees/api/l1": "/elsewhere/l1"}, "/work/ws/repos/api"},
		{"a link for the folder of worktrees", nil, []string{"repos", ".ropeshift/worktrees"},
			map[string]string{"/work/ws/.ropeshift/worktrees": "/elsewhere/worktrees"}, "/work/ws/repos/api"},
		{"a link for .ropeshift in a plain folder", named("/work/plain"), nil,
			map[string]string{"/work/plain/.ropeshift": "/elsewhere"}, "/work/plain/api"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, _ := provisioner(nil)
			source.SetDefaultRoot(outside + "/{project}/{repo}")
			machine := workspaceMachine(t)
			keeping(t, machine, "prj-1", "plainapi", "/work/plain/api")
			machine.Ignore("/work/ws", tt.ignores...)
			for at, to := range tt.links {
				machine.Link(at, to)
			}
			if tt.workspace != nil {
				source.PutUser("[projects.prj-1]\nworkspace = \"" + *tt.workspace + "\"\n")
			}
			p.Checkouts = machine
			request := worker.LaneRequest{Lane: "l1", Repository: tt.repo, Project: "prj-1", PlanningRepository: "plan"}
			lane, err := p.Prepare(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if want := outside + "/prj-1/" + path.Base(tt.repo) + "/l1"; lane.Worktree.Path != want {
				t.Errorf("the worktree is at %q, want %q outside every clone", lane.Worktree.Path, want)
			}
			if lane.Fallback.Reason != lanes.FallbackEscapes || !strings.Contains(lane.Fallback.Notice(), "link") {
				t.Errorf("the fallback is %+v: %q", lane.Fallback, lane.Fallback.Notice())
			}
		})
	}
}

// A link that leads nowhere resolves to nothing, so it is judged by where it stands and would pass for a folder in
// the workspace, while git makes the worktree wherever the link points. A lane whose path holds such a link, at its
// own folder, at the repository's folder, or at the folder of worktrees, keeps the root outside every clone.
func TestALinkToAMissingFolderLeadsALaneOutOfTheWorkspace(t *testing.T) {
	t.Parallel()
	const outside = "/data/ropeshift/worktrees"
	for _, link := range []string{
		"/work/ws/.ropeshift/worktrees/api/l1",
		"/work/ws/.ropeshift/worktrees/api",
		"/work/ws/.ropeshift/worktrees",
		"/work/ws/.ropeshift",
	} {
		t.Run(link, func(t *testing.T) {
			t.Parallel()
			p, source, _ := provisioner(nil)
			source.SetDefaultRoot(outside + "/{project}/{repo}")
			machine := workspaceMachine(t)
			machine.Ignore("/work/ws", "repos", ".ropeshift/worktrees")
			machine.Dangle(link)
			p.Checkouts = machine
			lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
			if err != nil {
				t.Fatal(err)
			}
			if want := outside + "/prj-1/api/l1"; lane.Worktree.Path != want {
				t.Errorf("the worktree is at %q, want %q outside every clone", lane.Worktree.Path, want)
			}
			if lane.Fallback.Reason != lanes.FallbackEscapes || !strings.Contains(lane.Fallback.Notice(), "link") {
				t.Errorf("the fallback is %+v: %q", lane.Fallback, lane.Fallback.Notice())
			}
		})
	}
}

// A root a person set wins over the workspace root, so a user layer's root is used as it is.
func TestAUserRootWinsOverTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(nil)
	source.SetDefaultRoot("/data/ropeshift/worktrees/{project}/{repo}")
	source.PutUser("[worktree]\nroot = \"/mine/{project}/{repo}\"\n")
	machine := workspaceMachine(t)
	machine.Ignore("/work/ws", "repos", ".ropeshift/worktrees")
	p.Checkouts = machine
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
	if err != nil || lane.Worktree.Path != "/mine/prj-1/api/l1" {
		t.Errorf("the worktree is %q (%v), want the user's root", lane.Worktree.Path, err)
	}
}

// A root inside the workspace's clone is allowed only under .ropeshift/worktrees/ and only while the workspace
// ignores it. Every other path inside it, and every code clone inside it, is refused, and a root under a folder the
// workspace does not ignore names the line to add.
func TestARootInsideTheWorkspaceIsAllowedOnlyUnderItsIgnoredWorktreeFolder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		root      string
		ignores   []string
		refused   bool
		wantClone string
		wantLine  bool
	}{
		{name: "under the folder, ignored", root: "/work/ws/.ropeshift/worktrees/{repo}", ignores: []string{"repos", ".ropeshift/worktrees"}},
		{name: "under the folder, not ignored", root: "/work/ws/.ropeshift/worktrees/{repo}", ignores: []string{"repos"}, refused: true, wantClone: "/work/ws", wantLine: true},
		{name: "elsewhere in the workspace", root: "/work/ws/lanes/{repo}", ignores: []string{"repos", ".ropeshift/worktrees"}, refused: true, wantClone: "/work/ws"},
		{name: "the workspace's own profile folder", root: "/work/ws/.ropeshift/{repo}", ignores: []string{"repos", ".ropeshift/worktrees"}, refused: true, wantClone: "/work/ws"},
		{name: "inside a code clone", root: "/work/ws/repos/web/wt", ignores: []string{"repos", ".ropeshift/worktrees"}, refused: true, wantClone: "/work/ws"},
		{name: "inside the repository's own code clone, under a worktree folder", root: "/work/ws/repos/api/.ropeshift/worktrees/{repo}", ignores: []string{"repos", ".ropeshift/worktrees"}, refused: true, wantClone: "/work/ws"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, _ := provisioner(nil)
			source.PutUser("[worktree]\nroot = \"" + tt.root + "\"\n")
			machine := workspaceMachine(t)
			machine.Ignore("/work/ws", tt.ignores...)
			p.Checkouts = machine
			_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
			var inside *worker.WorktreeInsideClone
			switch {
			case !tt.refused && err != nil:
				t.Fatalf("a root under the ignored folder is refused: %v", err)
			case tt.refused && !errors.As(err, &inside):
				t.Fatalf("got %v", err)
			case tt.refused && inside.Clone != tt.wantClone:
				t.Errorf("the refusal names the clone %q, want %q", inside.Clone, tt.wantClone)
			case tt.refused && strings.Contains(inside.Detail(), "add `"+planning.WorktreesLine+"` to the .gitignore of /work/ws") != tt.wantLine:
				t.Errorf("the detail %q, and the line to add is wanted: %v", inside.Detail(), tt.wantLine)
			}
		})
	}
}

// A lane with a worktree goes on in it when a workspace root would now put a new one elsewhere, and the start notice
// names where a new one would go.
func TestALaneWithAWorktreeGoesOnInItWhenTheWorkspaceRootAppears(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(nil)
	source.SetDefaultRoot("/data/ropeshift/worktrees/{project}/{repo}")
	machine := workspaceMachine(t)
	p.Checkouts = machine
	first, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
	if err != nil || first.Worktree.Path != "/data/ropeshift/worktrees/prj-1/api/l1" {
		t.Fatalf("the first worktree is %q (%v)", first.Worktree.Path, err)
	}
	machine.Ignore("/work/ws", "repos", ".ropeshift/worktrees")
	again, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan", Branch: first.Worktree.Branch, Recorded: first.Worktree.Path})
	if err != nil {
		t.Fatal(err)
	}
	if again.Worktree.Path != first.Worktree.Path || again.Kept == nil || again.Kept.Would != "/work/ws/.ropeshift/worktrees/api/l1" {
		t.Errorf("the next generation is at %q and keeps %+v", again.Worktree.Path, again.Kept)
	}
	fresh, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l2", Repository: "/work/ws/repos/api", Project: "prj-1", PlanningRepository: "plan"})
	if err != nil || fresh.Worktree.Path != "/work/ws/.ropeshift/worktrees/api/l2" {
		t.Errorf("a new lane is at %q (%v)", fresh.Worktree.Path, err)
	}
}
