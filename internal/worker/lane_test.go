package worker_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
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
	if len(lane.Environment) != 1 || lane.Environment[0] != "PATH=/usr/bin" {
		t.Errorf("environment %v", lane.Environment)
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

func TestPreparePassesOnlyTheReferencedSecret(t *testing.T) {
	t.Parallel()
	p, source, _ := provisioner(map[string]string{"ANTHROPIC_API_KEY": "k", "OTHER_KEY": "o"})
	source.Put("/work/app", "[agent.credentials]\nkind = \"api_key\"\nsecret_ref = \"env:ANTHROPIC_API_KEY\"\n")
	lane, err := p.Prepare(t.Context(), worker.LaneRequest{Lane: "l1", Repository: "/work/app"})
	if err != nil {
		t.Fatal(err)
	}
	if len(lane.Environment) != 1 || lane.Environment[0] != "ANTHROPIC_API_KEY=k" {
		t.Errorf("environment %v", lane.Environment)
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
