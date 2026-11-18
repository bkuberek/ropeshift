package worker_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/worker"
)

func provisioner(parent map[string]string) (worker.Provisioner, *fake.ProfileSource, *fake.Worktrees) {
	source := fake.NewProfileSource()
	worktrees := fake.NewWorktrees()
	return worker.Provisioner{
		Profiles:  source,
		Worktrees: worktrees,
		Lookup: func(name string) (string, bool) {
			value, ok := parent[name]
			return value, ok
		},
	}, source, worktrees
}

func TestPrepareOnFakes(t *testing.T) {
	t.Parallel()
	p, _, _ := provisioner(map[string]string{"PATH": "/usr/bin", "CLAUDECODE": "1"})
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"})
	if err != nil {
		t.Fatal(err)
	}
	if lane.Worktree.Path != "/work/app-lanes/l1" || lane.Worktree.Branch != "lane/l1" {
		t.Errorf("worktree %+v", lane.Worktree)
	}
	if want := []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=maintenance.auto", "GIT_CONFIG_VALUE_0=false", "PATH=/usr/bin"}; !slices.Equal(lane.Environment, want) {
		t.Errorf("environment %v, want %v", lane.Environment, want)
	}
}

// keeping puts a clone on the machine and has this worker keep it for a project, as a registration does.
func keeping(t *testing.T, machine *fake.Checkouts, project ledger.WorkspaceID, repository, at string) {
	t.Helper()
	machine.Place(ports.Clone{Path: at, ID: repository, DefaultBranch: "main"})
	if err := machine.Keep(t.Context(), project, repository, at); err != nil {
		t.Fatal(err)
	}
}

// With no root in any layer, a lane's worktree goes under the installation's worktree directory, by project
// and clone folder, and not beside or inside any clone (FR-PROF-3).
func TestAWorktreeGoesUnderTheWorktreeDirectoryByProjectAndRepository(t *testing.T) {
	t.Parallel()
	for project, want := range map[string]string{
		"prj-1": "/data/ropeshift/worktrees/prj-1/api/l1",
		"prj-2": "/data/ropeshift/worktrees/prj-2/api/l1",
		"":      "/data/ropeshift/worktrees/default/api/l1",
	} {
		p, source, _ := provisioner(nil)
		source.SetDefaultRoot("/data/ropeshift/worktrees/{project}/{repo}")
		machine := fake.NewCheckouts()
		keeping(t, machine, "prj-1", "r1", "/work/repos/api")
		keeping(t, machine, "prj-2", "r1", "/work/other/api")
		p.Checkouts = machine
		lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/repos/api", Project: project})
		if err != nil || lane.Worktree.Path != want {
			t.Errorf("project %q: worktree %q (%v), want %q", project, lane.Worktree.Path, err, want)
		}
	}
}

// A root that is a registered clone, or lies inside one, fails the lane before anything exists: through a
// relative root, a root that is another project's clone, and a link that leads into a clone. The refusal
// names the root, the clone, and the layer that set the root, and says how to fix it.
func TestAWorktreeRootInsideAClonePreparesNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		layer       string
		repository  string
		wantRoot    string
		wantClone   string
		wantSource  string
		wantFixWord string
	}{
		{"a relative root inside the repository's own clone", "[worktree]\nroot = \"lanes/{project}\"\n", "/work/app",
			"/work/app/lanes/prj-1", "/work/app", lanes.RootFromRepository, "remove it"},
		{"a root that is another repository's clone", "[worktree]\nroot = \"../web\"\n", "/work/app",
			"/work/web", "/work/web", lanes.RootFromRepository, "remove it"},
		{"a root inside a clone another project keeps", "[worktree]\nroot = \"/work/elsewhere/clone/wt\"\n", "/work/app",
			"/work/elsewhere/clone/wt", "/work/elsewhere/clone", lanes.RootFromRepository, "remove it"},
		{"a link that leads into a clone", "[worktree]\nroot = \"/links/app/wt\"\n", "/work/app",
			"/links/app/wt", "/work/app", lanes.RootFromRepository, "remove it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, worktrees := provisioner(nil)
			machine := fake.NewCheckouts()
			keeping(t, machine, "prj-1", "r1", "/work/app")
			keeping(t, machine, "prj-1", "r2", "/work/web")
			keeping(t, machine, "prj-2", "r3", "/work/elsewhere/clone")
			machine.Link("/links/app", "/work/app")
			p.Checkouts = machine
			source.Put(tt.repository, tt.layer)
			allocated := false
			_, err := p.Prepare(t.Context(), worker.LaneRequest{
				Lane: "l1", Repository: tt.repository, Project: "prj-1",
				Allocate: func(lanes.ResourceSettings) (lanes.Allocation, error) {
					allocated = true
					return lanes.Allocation{}, nil
				},
			})
			var inside *worker.WorktreeInsideClone
			if !errors.As(err, &inside) {
				t.Fatalf("got %v", err)
			}
			if inside.Root != tt.wantRoot || inside.Clone != tt.wantClone || inside.Source != tt.wantSource {
				t.Errorf("refusal %+v", inside)
			}
			detail := inside.Detail()
			for _, want := range []string{tt.wantRoot, tt.wantClone, tt.wantSource, "set `[worktree] root` in your profile to a folder outside every clone, or remove it to use the built-in root"} {
				if !strings.Contains(detail, want) {
					t.Errorf("the detail %q does not name %q", detail, want)
				}
			}
			if allocated {
				t.Error("resources were allocated before the root was refused")
			}
			if _, err := worktrees.Inspect(t.Context(), tt.wantRoot+"/l1"); !errors.Is(err, ports.ErrNotFound) {
				t.Errorf("a worktree was left behind: %v", err)
			}
		})
	}
}

// A root set by the user's layer is named as the user's, and a built-in root that lies inside a clone, which
// a worktree directory a person put there makes, is fixed by moving that directory.
func TestTheRefusalNamesWhereTheRootCameFrom(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(nil)
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "r1", "/work/app")
	p.Checkouts = machine
	source.PutUser("[worktree]\nroot = \"/work/app/wt\"\n")
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Project: "prj-1"})
	var inside *worker.WorktreeInsideClone
	if !errors.As(err, &inside) || inside.Source != lanes.RootFromUser {
		t.Fatalf("got %v", err)
	}

	source.PutUser("")
	source.SetDefaultRoot("/work/app/.worktrees/{project}/{repo}")
	_, err = p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Project: "prj-1"})
	if !errors.As(err, &inside) || inside.Source != lanes.RootFromDefault {
		t.Fatalf("got %v", err)
	}
	if detail := inside.Detail(); !strings.Contains(detail, api.EnvWorktreeDir) || strings.Contains(detail, "remove it") {
		t.Errorf("the detail %q says to remove a root nobody set", detail)
	}
}

// A root beside a clone, or above it, is outside it.
func TestARootBesideOrAboveAClonePasses(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"../{repo}-lanes", "../lanes/{repo}", "/elsewhere/{project}"} {
		p, source, _ := provisioner(nil)
		machine := fake.NewCheckouts()
		keeping(t, machine, "prj-1", "r1", "/work/app")
		p.Checkouts = machine
		source.Put("/work/app", "[worktree]\nroot = \""+root+"\"\n")
		if _, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Project: "prj-1"}); err != nil {
			t.Errorf("root %q: %v", root, err)
		}
	}
}

// A lane that began in a worktree goes on in it when the default root moves, as it does when an installation
// upgrades from the root beside the clone to the one under the worktree directory. Only a worktree not yet made
// takes the new root.
func TestALaneKeepsItsExistingWorktreeWhenTheDefaultRootMoves(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "r1", "/work/app")
	p.Checkouts = machine
	ctx := t.Context()
	request := worker.LaneRequest{Lane: "l1", Repository: "/work/app", Project: "prj-1"}

	// The old default, a folder beside the clone.
	source.SetDefaultRoot("../{repo}-lanes")
	first, err := p.Prepare(ctx, request)
	if err != nil || first.Worktree.Path != "/work/app-lanes/l1" || first.Kept != nil {
		t.Fatalf("the first generation: %+v (%v)", first, err)
	}

	// The upgrade moves the default.
	source.SetDefaultRoot("/data/worktrees/{project}/{repo}")
	next, err := p.Prepare(ctx, request)
	if err != nil || next.Worktree.Path != "/work/app-lanes/l1" {
		t.Fatalf("the next generation: %+v (%v)", next.Worktree, err)
	}
	if next.Kept == nil || next.Kept.Path != "/work/app-lanes/l1" || next.Kept.Would != "/data/worktrees/prj-1/app/l1" || next.Kept.Inside != "" {
		t.Errorf("the kept placement reads %+v", next.Kept)
	}
	if created := worktrees.Created(); len(created) != 1 {
		t.Errorf("the next generation made another worktree: %+v", created)
	}

	// A lane with no worktree yet takes the new root.
	fresh, err := p.Prepare(ctx, worker.LaneRequest{Lane: "l2", Repository: "/work/app", Project: "prj-1"})
	if err != nil || fresh.Worktree.Path != "/data/worktrees/prj-1/app/l2" || fresh.Kept != nil {
		t.Errorf("a new lane: %+v (%v)", fresh, err)
	}
}

// A worktree already made inside a clone stays: the lane in it goes on, with its place reported, because
// refusing it would break work in flight. The same root refuses a lane with no worktree yet.
func TestAnExistingWorktreeInsideACloneIsKeptAndANewOneRefused(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	machine := fake.NewCheckouts()
	keeping(t, machine, "prj-1", "r1", "/work/ws")
	keeping(t, machine, "prj-1", "r2", "/work/ws/repos/api")
	ctx := t.Context()
	// Before the upgrade nothing checked a root, and the old default put the api's lanes inside the workspace.
	source.SetDefaultRoot("../{repo}-lanes")
	request := worker.LaneRequest{Lane: "l1", Repository: "/work/ws/repos/api", Project: "prj-1"}
	if first, err := p.Prepare(ctx, request); err != nil || first.Worktree.Path != "/work/ws/repos/api-lanes/l1" {
		t.Fatalf("the first generation: %+v (%v)", first.Worktree, err)
	}

	p.Checkouts = machine
	next, err := p.Prepare(ctx, request)
	if err != nil || next.Worktree.Path != "/work/ws/repos/api-lanes/l1" {
		t.Fatalf("the next generation: %+v (%v)", next.Worktree, err)
	}
	if next.Kept == nil || next.Kept.Inside != "/work/ws" {
		t.Errorf("the kept placement reads %+v, want it inside /work/ws", next.Kept)
	}
	if created := worktrees.Created(); len(created) != 1 {
		t.Errorf("the next generation made another worktree: %+v", created)
	}
	_, err = p.Prepare(ctx, worker.LaneRequest{Lane: "l2", Repository: "/work/ws/repos/api", Project: "prj-1"})
	var inside *worker.WorktreeInsideClone
	if !errors.As(err, &inside) {
		t.Errorf("a new worktree under the same root: %v", err)
	}
}

// Only a lane's own worktree is kept. A worktree that holds the lane's branch and is someone else's, a person's
// own `git worktree add` or another lane's leftover, is no place for the lane: the start is refused as it was
// before the root moved, and nothing of the other worktree's is reused, so an archive never removes it.
func TestAForeignWorktreeThatHoldsTheBranchIsNeverReused(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		path     string
		recorded string
	}{
		"a worktree a person made, named otherwise":                      {path: "/work/app/mine"},
		"a worktree another lane left, by its name":                      {path: "/work/app-lanes/other"},
		"a worktree of the lane's name that its journal does not record": {path: "/work/app-lanes/l1", recorded: "/work/elsewhere/l1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, source, worktrees := provisioner(nil)
			source.SetDefaultRoot("/data/worktrees/{project}/{repo}")
			if _, err := worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: "/work/app", Path: tt.path, Branch: "lane/l1"}); err != nil {
				t.Fatal(err)
			}
			lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Project: "prj-1", Recorded: tt.recorded})
			if !errors.Is(err, ports.ErrWorktreeConflict) {
				t.Fatalf("got %+v, %v", lane.Worktree, err)
			}
			if created := worktrees.Created(); len(created) != 1 {
				t.Errorf("a second worktree was made: %+v", created)
			}
		})
	}
}

// A lane's own worktree is the one its journal recorded, and a lane with no record is held to the name its
// worktree always has, the lane's.
func TestALaneKeepsTheWorktreeItsJournalRecorded(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	source.SetDefaultRoot("/data/worktrees/{project}/{repo}")
	if _, err := worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: "/work/app", Path: "/work/app-lanes/l1", Branch: "lane/l1"}); err != nil {
		t.Fatal(err)
	}
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Project: "prj-1", Recorded: "/work/app-lanes/l1"})
	if err != nil || lane.Worktree.Path != "/work/app-lanes/l1" || lane.Kept == nil {
		t.Errorf("a recorded worktree: %+v, %v", lane, err)
	}
}

// A review lane has no branch, so its own worktree is the one its journal recorded. One in flight at an upgrade
// goes on in it at its next generation, and the upgrade leaves no second worktree of the same review.
func TestAReviewLaneInFlightKeepsItsRecordedWorktree(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	source.SetDefaultRoot("../{repo}-lanes")
	request := worker.LaneRequest{Lane: "review-1", Repository: "/work/app", Project: "prj-1", Base: "c0ffee", Detached: true}
	first, err := p.Prepare(t.Context(), request)
	if err != nil || first.Worktree.Path != "/work/app-lanes/review-1" {
		t.Fatalf("the first generation: %+v (%v)", first.Worktree, err)
	}
	source.SetDefaultRoot("/data/worktrees/{project}/{repo}")
	request.Recorded = first.Worktree.Path
	next, err := p.Prepare(t.Context(), request)
	if err != nil || next.Worktree.Path != "/work/app-lanes/review-1" || next.Kept == nil || next.Kept.Would != "/data/worktrees/prj-1/app/review-1" {
		t.Fatalf("the next generation: %+v, %+v (%v)", next.Worktree, next.Kept, err)
	}
	if created := worktrees.Created(); len(created) != 1 {
		t.Errorf("a second worktree of the review: %+v", created)
	}
	// With nothing recorded there is nothing to keep, and a worktree that is not the lane's is never taken.
	request.Recorded = "/work/app-lanes/another"
	other, err := p.Prepare(t.Context(), request)
	if err != nil || other.Worktree.Path != "/data/worktrees/prj-1/app/review-1" || other.Kept != nil {
		t.Errorf("a record that names another worktree: %+v, %+v (%v)", other.Worktree, other.Kept, err)
	}
}

// The refusal covers every clone the worker uses, not only the ones it keeps: the repository a lane works in, and
// each repository the daemon reads as a source, which no checkout records.
func TestARootInsideAnUnkeptSourceCloneIsRefused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		layer      string
		sources    []string
		wantClone  string
		repository string
	}{
		{"the lane's own repository, kept by nobody", "[worktree]\nroot = \"lanes\"\n", nil, "/work/app", "/work/app"},
		{"another source clone", "[worktree]\nroot = \"/work/plans/wt\"\n", []string{"/work/plans"}, "/work/plans", "/work/app"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, worktrees := provisioner(nil)
			// The clones are on the machine, and kept in no project.
			machine := fake.NewCheckouts()
			for _, clone := range append([]string{tt.repository}, tt.sources...) {
				machine.Place(ports.Clone{Path: clone, ID: clone})
			}
			p.Checkouts = machine
			p.Sources = tt.sources
			source.Put(tt.repository, tt.layer)
			_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: tt.repository, Project: "prj-1"})
			var inside *worker.WorktreeInsideClone
			if !errors.As(err, &inside) || inside.Clone != tt.wantClone {
				t.Fatalf("got %v, want a refusal for %s", err, tt.wantClone)
			}
			if created := worktrees.Created(); len(created) != 0 {
				t.Errorf("a worktree was made: %+v", created)
			}
		})
	}
}

func TestPrepareRepeats(t *testing.T) {
	t.Parallel()
	p, _, _ := provisioner(nil)
	request := worker.LaneRequest{Lane: "l1", Repository: "/work/app"}
	first, err := p.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.Prepare(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Worktree != second.Worktree || first.ProfileDigest != second.ProfileDigest {
		t.Errorf("first %+v, retry %+v", first, second)
	}
}

func TestPrepareKeepsTheLedgersBranch(t *testing.T) {
	t.Parallel()
	p, _, _ := provisioner(nil)
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Branch: "lane/retry-t1"})
	if err != nil {
		t.Fatal(err)
	}
	if lane.Worktree.Branch != "lane/retry-t1" {
		t.Errorf("branch %s", lane.Worktree.Branch)
	}
}

// TestPrepareLeavesNothingOnABadProfile holds the order: settings before disk.
func TestPrepareLeavesNothingOnABadProfile(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	source.Put("/work/app", "name = unquoted\n")
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"})
	if !errors.Is(err, lanes.ErrBadProfile) {
		t.Fatalf("got %v", err)
	}
	if _, err := worktrees.Inspect(t.Context(), "/work/app-lanes/l1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a worktree was left behind: %v", err)
	}
}

// A repository is code from somewhere else, so it cannot send this machine's work to a remote.
func TestARepositoryCannotTurnPushOn(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	source.Put("/work/app", "[worktree]\npush_on_stop = true\n")
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"})
	if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), "push_on_stop") {
		t.Fatalf("got %v", err)
	}
	if _, err := worktrees.Inspect(t.Context(), "/work/app-lanes/l1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a worktree was left behind: %v", err)
	}
}

// A lane's own layer can come from any caller of the API, so it runs no command, reads no variable of the
// worker's, and places no worktree, and a lane whose layer tries any of them leaves nothing behind.
func TestPrepareRefusesALaneLayerThatRunsReadsOrPlaces(t *testing.T) {
	t.Parallel()
	layers := map[string]string{
		"a hook":       "[hooks]\non_lane_created = [\"/bin/sh\", \"-c\", \"id\"]\n",
		"a credential": "[agent.credentials]\nsecret_ref = \"env:OTHER_KEY\"\n",
		"a root":       "[worktree]\nroot = \"/\"\n",
	}
	for name, overrides := range layers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, _, worktrees := provisioner(map[string]string{"OTHER_KEY": "o"})
			hooks := fake.NewHookRunner()
			p.Hooks = hooks
			lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Overrides: overrides})
			if !errors.Is(err, lanes.ErrBadProfile) {
				t.Fatalf("got %v with environment %v", err, lane.Environment)
			}
			if runs := hooks.Runs(); len(runs) != 0 {
				t.Errorf("a lane's layer ran %+v", runs)
			}
			if created := worktrees.Created(); len(created) != 0 {
				t.Errorf("a lane's layer made %+v", created)
			}
		})
	}
}

// A delegate policy version that sets raise_model_and_effort = false keeps a delegate's lane layer from raising
// the model's capability class past what the layers below give, and the refusal names the version (FR-AUTH-16).
// A lane that runs as no roster agent is given no effort, whatever its layers set, so there is none to raise
// and only its model is compared. The built-in classes map haiku to small, sonnet to standard, and opus to large
// and frontier.
func TestPrepareHoldsADelegatesLaneLayerToARestrictingPolicy(t *testing.T) {
	t.Parallel()
	restricted := &ledger.DelegatedLayer{Delegate: "m1/dlg_01", PolicyVersion: 3, Restricted: true}
	tests := []struct {
		name, repository, overrides string
		refused                     string
	}{
		{"a larger class", "[agent]\nmodel = \"haiku\"\n", "[agent]\nmodel = \"opus\"\n", "agent.model"},
		{"a higher effort, which a lane that runs as no agent never runs", "[agent]\neffort = \"medium\"\n", "[agent]\neffort = \"max\"\n", ""},
		{"a model of no class", "", "[agent]\nmodel = \"opus-preview\"\n", "agent.model"},
		{"from a model of no class", "[agent]\nmodel = \"opus-preview\"\n", "[agent]\nmodel = \"haiku\"\n", "agent.model"},
		{"a lower class and effort", "", "[agent]\nmodel = \"haiku\"\neffort = \"low\"\n", ""},
		{"a lower class with a higher effort, which a lane that runs as no agent never runs", "", "[agent]\nmodel = \"sonnet\"\neffort = \"max\"\n", ""},
		{"the same model and effort", "", "[agent]\nmodel = \"opus\"\neffort = \"high\"\n", ""},
		{"a layer that leaves both alone", "[agent]\nmodel = \"haiku\"\neffort = \"low\"\n", "[lifecycle]\nstall_after = \"30m\"\n", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, source, worktrees := provisioner(nil)
			source.Put("/work/app", tt.repository)
			_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Overrides: tt.overrides, Delegated: restricted})
			if tt.refused == "" {
				if err != nil {
					t.Fatalf("a delegate's %s: %v", tt.name, err)
				}
				return
			}
			if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), "delegate policy version 3") || !strings.Contains(err.Error(), tt.refused) {
				t.Fatalf("a delegate's %s: %v", tt.name, err)
			}
			if created := worktrees.Created(); len(created) != 0 {
				t.Errorf("a refused layer made %+v", created)
			}
		})
	}
}

// A lane that runs as a roster agent runs the model the agent's class maps to, or the agent's own model for the
// driver, at the agent's effort, unless the lane's layer sets either. A restricting policy holds a delegate's
// layer to what the lane would run without it, which is that agent's, whatever the profile's model and effort
// are (FR-AUTH-16). The built-in profile gives opus at high.
func TestPrepareHoldsADelegatesLaneLayerToWhatItsAgentRuns(t *testing.T) {
	t.Parallel()
	restricted := &ledger.DelegatedLayer{Delegate: "m1/dlg_01", PolicyVersion: 3, Restricted: true}
	small := &roster.Agent{Class: roster.ClassSmall, Effort: roster.EffortLow}
	unset := &roster.Agent{Class: roster.ClassSmall}
	tests := []struct {
		name, overrides string
		agent           *roster.Agent
		refused         string
	}{
		{"a lower model that raises the agent's", "[agent]\nmodel = \"sonnet\"\neffort = \"medium\"\n", small, `agent.model from "haiku" to "sonnet"`},
		{"a lower effort that raises the agent's", "[agent]\neffort = \"medium\"\n", small, `agent.effort from "low" to "medium"`},
		{"the agent's own model and effort", "[agent]\nmodel = \"haiku\"\neffort = \"low\"\n", small, ""},
		{"a model the agent names for the driver", "[agent]\nmodel = \"sonnet\"\n", &roster.Agent{Class: roster.ClassSmall, Effort: roster.EffortLow, Models: map[string]string{"claude-code": "opus"}}, ""},
		{"a raise over the agent's own model", "[agent]\nmodel = \"opus-preview\"\n", &roster.Agent{Class: roster.ClassSmall, Effort: roster.EffortLow, Models: map[string]string{"claude-code": "opus"}}, "agent.model"},
		{"the profile's model, which the agent's wins over", "[agent]\nmodel = \"opus\"\n", small, ""},
		// An agent that names no effort runs the CLI's own, which is no lower than low.
		{"low over an agent that names no effort", "[agent]\neffort = \"low\"\n", unset, ""},
		{"medium over an agent that names no effort", "[agent]\neffort = \"medium\"\n", unset, `agent.effort from "" to "medium"`},
		{"max over an agent that names no effort", "[agent]\neffort = \"max\"\n", unset, `agent.effort from "" to "max"`},
		// A lane that runs as no agent is given no effort, so a layer's effort changes nothing it runs. Its model
		// is still compared.
		{"an effort with no agent", "[agent]\neffort = \"max\"\n", nil, ""},
		{"a model with no agent", "[agent]\nmodel = \"opus-preview\"\neffort = \"low\"\n", nil, "agent.model"},
		{"a lower model and effort with no agent", "[agent]\nmodel = \"sonnet\"\neffort = \"medium\"\n", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, _, worktrees := provisioner(nil)
			_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Overrides: tt.overrides, Delegated: restricted, Agent: tt.agent})
			if tt.refused == "" {
				if err != nil {
					t.Fatalf("a delegate's %s: %v", tt.name, err)
				}
				return
			}
			if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), "delegate policy version 3") || !strings.Contains(err.Error(), tt.refused) {
				t.Fatalf("a delegate's %s: %v", tt.name, err)
			}
			if created := worktrees.Created(); len(created) != 0 {
				t.Errorf("a refused layer made %+v", created)
			}
		})
	}
}

// The classes a raise reads are the user's [agent.classes]: a model the user maps to a higher class is a raise
// even where the built-in classes do not name it.
func TestPrepareReadsARaiseOfClassFromTheUsersClasses(t *testing.T) {
	t.Parallel()
	restricted := &ledger.DelegatedLayer{Delegate: "m1/dlg_01", PolicyVersion: 4, Restricted: true}
	p, source, _ := provisioner(nil)
	source.PutUser("[agent.classes]\nsmall = \"haiku\"\nstandard = \"sonnet\"\nlarge = \"opus\"\nfrontier = \"opus-max\"\n")
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Overrides: "[agent]\nmodel = \"opus-max\"\n", Delegated: restricted})
	if !errors.Is(err, lanes.ErrBadProfile) || !strings.Contains(err.Error(), "delegate policy version 4") {
		t.Fatalf("large to frontier: %v", err)
	}
	if _, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Overrides: "[agent]\nmodel = \"sonnet\"\n", Delegated: restricted}); err != nil {
		t.Errorf("large to standard: %v", err)
	}
}

// A member's own lane layer is never restricted, and neither is a delegate's under a version that allows the
// raise, the built-in one included (FR-AUTH-16).
func TestPrepareLetsAMemberOrAnAllowedDelegateRaiseTheModelAndEffort(t *testing.T) {
	t.Parallel()
	const raise = "[agent]\nmodel = \"opus-preview\"\neffort = \"max\"\n"
	for name, delegated := range map[string]*ledger.DelegatedLayer{
		"a member":                        nil,
		"a delegate under an open policy": {Delegate: "m1/dlg_01", PolicyVersion: 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p, source, _ := provisioner(nil)
			source.Put("/work/app", "[agent]\nmodel = \"haiku\"\neffort = \"low\"\n")
			lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Overrides: raise, Delegated: delegated})
			if err != nil {
				t.Fatal(err)
			}
			if lane.Profile.Agent.Model != "opus-preview" || lane.Profile.Agent.Effort != "max" || !lane.LaneModel || !lane.LaneEffort {
				t.Errorf("the lane runs %+v", lane.Profile.Agent)
			}
		})
	}
}

func TestPreparePassesOnlyTheReferencedSecret(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(map[string]string{"ANTHROPIC_API_KEY": "k", "OTHER_KEY": "o"})
	source.Put("/work/app", "[agent.credentials]\nkind = \"api_key\"\nsecret_ref = \"env:ANTHROPIC_API_KEY\"\n")
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ANTHROPIC_API_KEY=k", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=maintenance.auto", "GIT_CONFIG_VALUE_0=false"}; !slices.Equal(lane.Environment, want) {
		t.Errorf("environment %v, want %v", lane.Environment, want)
	}
}

func TestPrepareRefusesASessionToken(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "t"})
	source.Put("/work/app", "[agent.credentials]\nsecret_ref = \"env:CLAUDE_CODE_OAUTH_TOKEN\"\n")
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"})
	if !errors.Is(err, lanes.ErrSessionVariable) {
		t.Errorf("got %v", err)
	}
}

// A review lane's worktree is the commit under review and no branch, so the lane's name makes none and the
// ledger's branch, which a review has none of, is not asked for.
func TestPrepareDetachedMakesNoBranch(t *testing.T) {
	t.Parallel()
	p, _, worktrees := provisioner(nil)
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "review-1", Repository: "/work/app", Base: "c0ffee", Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	if lane.Worktree.Path != "/work/app-lanes/review-1" || lane.Worktree.Branch != "" || !lane.Worktree.Detached || lane.Worktree.Head != "c0ffee" {
		t.Errorf("worktree %+v", lane.Worktree)
	}
	if created := worktrees.Created(); len(created) != 1 || !created[0].Detached || created[0].Base != "c0ffee" {
		t.Errorf("the worker asked for %+v", created)
	}
}

// plainWorktrees is a store that offers a lane's worktree and no more: it would give a review a branch.
type plainWorktrees struct{ ports.Worktrees }

func TestPrepareRefusesADetachedWorktreeFromAStoreThatMakesOnlyBranches(t *testing.T) {
	t.Parallel()
	worktrees := fake.NewWorktrees()
	p := worker.Provisioner{Profiles: fake.NewProfileSource(), Worktrees: plainWorktrees{worktrees}, Lookup: func(string) (string, bool) { return "", false }}
	_, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "review-1", Repository: "/work/app", Base: "c0ffee", Detached: true})
	if !errors.Is(err, worker.ErrNoDetached) {
		t.Fatalf("got %v", err)
	}
	if created := worktrees.Created(); len(created) != 0 {
		t.Errorf("the worker made %+v", created)
	}
	if _, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"}); err != nil {
		t.Errorf("a lane with a branch on the same store: %v", err)
	}
}

// A lane reads its repository's layer at the commit its worktree begins from (FR-PROF-5), and a lane with a layer
// of its own reads the layers below it at that commit too. A review lane's commit is the one under review.
func TestPrepareReadsTheRepositoryLayerAtTheLanesBase(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(nil)
	source.PutAt("/work/app", "c1", "[agent]\nmodel = \"first\"\n")
	source.PutAt("/work/app", "c2", "[agent]\nmodel = \"second\"\n")
	for base, want := range map[string]string{"c1": "first", "c2": "second"} {
		for name, request := range map[string]worker.LaneRequest{
			"a lane":                 {Lane: "l-" + base, Repository: "/work/app", Base: base},
			"a lane with its layer":  {Lane: "o-" + base, Repository: "/work/app", Base: base, Overrides: "[agent]\neffort = \"low\"\n"},
			"a review at the commit": {Lane: "r-" + base, Repository: "/work/app", Base: base, Detached: true},
		} {
			lane, err := p.Prepare(t.Context(), request)
			if err != nil || lane.Profile.Agent.Model != want {
				t.Errorf("%s at %s runs %q (%v), want %q", name, base, lane.Profile.Agent.Model, err, want)
			}
		}
	}
	for _, load := range source.Loaded() {
		if load.Repository != "/work/app" || (load.Base != "c1" && load.Base != "c2") {
			t.Errorf("a layer was read with %+v", load)
		}
	}
}

// A lane with no work of its own has a directory and no repository, so it reads no layer of a repository.
func TestAPrivateLaneReadsNoRepositoryLayer(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(nil)
	source.Put("/state/private/orchestrator", "[agent]\nmodel = \"theirs\"\n")
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{
		Lane: "orchestrator", Repository: "/state/private/orchestrator", Base: "c1", Private: true, Overrides: "[agent]\neffort = \"low\"\n",
	})
	if err != nil || lane.Profile.Agent.Model == "theirs" {
		t.Errorf("the lane runs %q (%v)", lane.Profile.Agent.Model, err)
	}
	if loaded := source.Loaded(); len(loaded) != 0 {
		t.Errorf("a layer was read for %+v", loaded)
	}
}

// arriving is a profile source whose repository lacks a commit until a fetch brings it, as a clone does for a lane
// another machine ran.
type arriving struct {
	*fake.ProfileSource
	worktrees *fake.Worktrees
	commit    string
}

func (a arriving) Load(ctx context.Context, repository, base, overrides string) (lanes.Profile, error) {
	if base == a.commit && len(a.worktrees.Fetched()) == 0 {
		return lanes.Profile{}, fmt.Errorf("%w: %s", ports.ErrNotFound, base)
	}
	return a.ProfileSource.Load(ctx, repository, base, overrides)
}

// A lane that another machine ran begins at a commit this machine fetches before its worktree exists, so a commit
// the clone does not hold is fetched for and read again, and a lane with nothing to fetch fails with the commit
// not found.
func TestPrepareFetchesForABaseTheCloneLacks(t *testing.T) {
	t.Parallel()
	p, source, worktrees := provisioner(nil)
	source.PutAt("/work/app", "c1", "[agent]\nmodel = \"first\"\n")
	p.Profiles = arriving{ProfileSource: source, worktrees: worktrees, commit: "c1"}
	if _, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Base: "c1"}); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a base the clone lacks, with nothing to fetch, gave %v", err)
	}
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app", Base: "c1", Fetch: []string{"refs/heads/lane/l1"}})
	if err != nil || lane.Profile.Agent.Model != "first" {
		t.Errorf("the lane runs %q (%v), want the layer at the fetched commit", lane.Profile.Agent.Model, err)
	}
}
