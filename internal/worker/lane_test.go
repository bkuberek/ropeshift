package worker_test

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
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
