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
			report, err := worker.WorktreeReport(t.Context(), profile, machine)
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

// A profile with no root says so, because a worker that has none starts no lane.
func TestTheWorktreeReportRefusesAProfileWithNoRoot(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "r1", "/work/app")
	if _, err := worker.WorktreeReport(t.Context(), lanes.Profile{}, machine); err == nil {
		t.Error("a profile with no root was reported")
	}
}
