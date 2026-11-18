package worker_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/worker"
)

// The report names the root in force with the layer that set it, and for each checkout of every project the
// clone the root resolves inside for that checkout's project and folder.
func TestTheWorktreeReportNamesEachCheckoutTheRootFallsInside(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		root    string
		source  string
		clones  map[string]string
		links   map[string]string
		wantAll []lanes.RootInsideClone
	}{
		{
			name: "a user root inside a registered clone", root: "/work/app/lanes/{project}/{repo}", source: lanes.RootFromUser,
			clones: map[string]string{"prj-1/r1": "/work/app", "prj-2/r2": "/work/web"},
			wantAll: []lanes.RootInsideClone{
				{Project: "prj-1", Checkout: "/work/app", Root: "/work/app/lanes/prj-1/app", Clone: "/work/app"},
				{Project: "prj-2", Checkout: "/work/web", Root: "/work/app/lanes/prj-2/web", Clone: "/work/app"},
			},
		},
		{
			name: "a root that is another project's clone", root: "/work/{repo}", source: lanes.RootFromUser,
			clones: map[string]string{"prj-1/r1": "/work/app", "prj-2/r2": "/work/web"},
			wantAll: []lanes.RootInsideClone{
				{Project: "prj-1", Checkout: "/work/app", Root: "/work/app", Clone: "/work/app"},
				{Project: "prj-2", Checkout: "/work/web", Root: "/work/web", Clone: "/work/web"},
			},
		},
		{
			name: "a relative root, which is relative to each checkout", root: "lanes/{project}", source: lanes.RootFromUser,
			clones: map[string]string{"prj-1/r1": "/work/app"},
			wantAll: []lanes.RootInsideClone{
				{Project: "prj-1", Checkout: "/work/app", Root: "/work/app/lanes/prj-1", Clone: "/work/app"},
			},
		},
		{
			name: "a root that reaches a clone through a link", root: "/links/app/lanes", source: lanes.RootFromUser,
			clones: map[string]string{"prj-1/r1": "/work/app"}, links: map[string]string{"/links/app": "/work/app"},
			wantAll: []lanes.RootInsideClone{
				{Project: "prj-1", Checkout: "/work/app", Root: "/links/app/lanes", Clone: "/work/app"},
			},
		},
		{
			name: "the built-in root, which lies outside every clone", root: "/data/ropeshift/worktrees/{project}/{repo}", source: lanes.RootFromDefault,
			clones: map[string]string{"prj-1/r1": "/work/app", "prj-2/r2": "/work/web"},
		},
		{
			name: "a user root outside every clone", root: "/srv/lanes/{project}/{repo}", source: lanes.RootFromUser,
			clones: map[string]string{"prj-1/r1": "/work/app"},
		},
		{
			name: "a root beside each checkout", root: "../{repo}-lanes", source: lanes.RootFromUser,
			clones: map[string]string{"prj-1/r1": "/work/app"},
		},
		{name: "no checkout", root: "/work/lanes", source: lanes.RootFromUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			machine := fake.NewCheckouts()
			for key, at := range tt.clones {
				project, repository, _ := strings.Cut(key, "/")
				keeping(t, machine, ledger.WorkspaceID(project), repository, at)
			}
			for at, to := range tt.links {
				machine.Link(at, to)
			}
			profile := lanes.Profile{Worktree: lanes.WorktreeSettings{Root: tt.root, RootSource: tt.source}}
			report, err := worker.WorktreeReport(t.Context(), profile, machine, nil)
			if err != nil {
				t.Fatal(err)
			}
			if report.Root != tt.root || report.Source != tt.source {
				t.Errorf("the report names the root %q from %q, want %q from %q", report.Root, report.Source, tt.root, tt.source)
			}
			if !slices.Equal(report.Inside, tt.wantAll) {
				t.Errorf("inside a clone: %+v, want %+v", report.Inside, tt.wantAll)
			}
		})
	}
}

// The report names, for each project that has a workspace on this worker, the workspace, how it was found, whether
// its .ropeshift/worktrees/ folder is ignored, the line to add when it is not, and the root each checkout of the
// project gets. A project with no workspace is not named.
func TestTheWorktreeReportNamesEachProjectsWorkspace(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "plan", "/work/ws")
	keeping(t, machine, "prj-1", "api", "/work/ws/repos/api")
	machine.Ignore("/work/ws", "repos", ".ropeshift/worktrees")
	keeping(t, machine, "prj-2", "api", "/work/plain/api")
	keeping(t, machine, "prj-3", "plan", "/work/ws3")
	keeping(t, machine, "prj-3", "web", "/work/ws3/repos/web")
	machine.Ignore("/work/ws3", "repos")
	keeping(t, machine, "prj-4", "api", "/work/beside/api")
	profile := lanes.Profile{
		Worktree: lanes.WorktreeSettings{Root: "/data/worktrees/{project}/{repo}", RootSource: lanes.RootFromDefault},
		Projects: map[string]lanes.ProjectSettings{"prj-2": {Workspace: named("/work/plain")}},
	}
	report, err := worker.WorktreeReport(t.Context(), profile, machine, map[string]string{"prj-1": "plan", "prj-3": "plan"})
	if err != nil {
		t.Fatal(err)
	}
	want := []lanes.WorkspaceReport{
		{
			Project: "prj-1", Path: "/work/ws", Found: lanes.WorkspaceFromPlanning, Clone: true, Ignored: true,
			Roots: []lanes.CheckoutRoot{{Checkout: "/work/ws/repos/api", Root: "/work/ws/.ropeshift/worktrees/api"}, {Checkout: "/work/ws", Root: "/data/worktrees/prj-1/ws"}},
		},
		{
			Project: "prj-2", Path: "/work/plain", Found: lanes.WorkspaceFromProfile, Ignored: true,
			Roots: []lanes.CheckoutRoot{{Checkout: "/work/plain/api", Root: "/work/plain/.ropeshift/worktrees/api"}},
		},
		{
			Project: "prj-3", Path: "/work/ws3", Found: lanes.WorkspaceFromPlanning, Clone: true, Line: "/.ropeshift/worktrees/",
			Roots: []lanes.CheckoutRoot{{Checkout: "/work/ws3", Root: "/data/worktrees/prj-3/ws3"}, {Checkout: "/work/ws3/repos/web", Root: "/data/worktrees/prj-3/web", Fallback: lanes.FallbackUnignored}},
		},
	}
	if !slices.EqualFunc(report.Workspaces, want, func(a, b lanes.WorkspaceReport) bool {
		return a.Project == b.Project && a.Path == b.Path && a.Found == b.Found && a.Clone == b.Clone && a.Ignored == b.Ignored && a.Line == b.Line && slices.Equal(a.Roots, b.Roots)
	}) {
		t.Errorf("the workspaces are %+v, want %+v", report.Workspaces, want)
	}
	if len(report.Inside) != 0 {
		t.Errorf("a root the workspace allows, or the built-in root, is reported inside a clone: %+v", report.Inside)
	}
}

// A profile with no root says so, because a worker that has none starts no lane.
func TestTheWorktreeReportRefusesAProfileWithNoRoot(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "r1", "/work/app")
	if _, err := worker.WorktreeReport(t.Context(), lanes.Profile{}, machine, nil); err == nil {
		t.Error("a profile with no root was reported")
	}
}
