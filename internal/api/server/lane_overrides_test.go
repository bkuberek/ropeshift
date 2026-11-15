package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// claimOf reads a claim as the ledger holds it.
func claimOf(t *testing.T, d *apitest.Daemon, id string) workorders.Claim {
	t.Helper()
	var claim workorders.Claim
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		var err error
		claim, err = tx.Claims().Find(id)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return claim
}

// claimWork creates a work order, sends it to review, and claims it, so a lane can start on it. A claim runs
// one lane, so each start a test makes and keeps needs its own.
func claimWork(t *testing.T, d *talkDaemon, id, claim string) {
	t.Helper()
	ctx := t.Context()
	if _, err := d.client.CreateWorkOrder(ctx, d.workIn(id, "Add the retry budget")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Review(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewLeases(d.Store).Claim(ctx, apitest.Scope, workorders.ClaimRequest{
		WorkOrder: id, ClaimID: claim, Worker: d.auth.WorkerID, TTLMs: apitest.LeaseTTL,
	}, talkMember); err != nil {
		t.Fatal(err)
	}
}

// TestNoCallerStartsALaneWhoseOverridesRunReadOrPlace holds every caller's lane start, an outside
// delegate's, the assistant's, and a member's, to overrides that name no hook, no credential, no file to
// read, and no place for the worktree or its branch, that set no review bound or ports per lane above the
// floor, which the worker compares with layers the route does not hold, that spell no key two ways, and to
// overrides that are TOML at all. A key counts in every spelling the profile reads as it. The refusal
// answers before the ledger keeps anything, and a member's overrides of any other key still start the lane.
func TestNoCallerStartsALaneWhoseOverridesRunReadOrPlace(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	_, assistant, _ := d.assistant(t)
	outside := asDelegate(t, d.socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	claimWork(t, d, "wo-lanes", "claim-lanes")

	callers := []struct {
		name   string
		client *api.ProjectClient
	}{{"an outside delegate", outside}, {"the assistant", assistant}, {"a member", d.client}}
	refused := []struct{ name, overrides string }{
		{"a hook", "[hooks]\non_lane_created = [\"/bin/sh\", \"-c\", \"id\"]\n"},
		{"a dotted hook", "hooks.on_lane_created = [\"id\"]\n"},
		{"a credential", "agent.credentials.secret_ref = \"env:HOME\"\n"},
		{"a credential's table", "[agent.credentials]\nkind = \"api_key\"\n"},
		{"a root", "worktree = { root = \"/\" }\n"},
		{"a branch prefix", "[worktree]\nbranch_prefix = \"\"\n"},
		{"an instructions file", "[agent]\ninstructions_file = \"/etc/passwd\"\n"},
		{"review rounds", "[review]\nmax_review_rounds = 9\n"},
		{"two review rounds", "[review]\nmax_review_rounds = 2\n"},
		{"the default review rounds", "[review]\nmax_review_rounds = 3\n"},
		{"no review rounds, which reads as the default", "[review]\nmax_review_rounds = 0\n"},
		{"review rounds that are no integer", "[review]\nmax_review_rounds = 1.0\n"},
		{"review rounds that are text", "[review]\nmax_review_rounds = \"1\"\n"},
		{"a table of review rounds", "[review.max_review_rounds]\nlimit = 1\n"},
		{"ports", "resources.ports_per_lane = 10000\n"},
		{"five ports", "[resources]\nports_per_lane = 5\n"},
		{"one port", "resources.ports_per_lane = 1\n"},
		{"a negative count of ports", "resources.ports_per_lane = -1\n"},
		{"ports that are no integer", "resources.ports_per_lane = 0.0\n"},
		{"ports that are text", "resources.ports_per_lane = \"0\"\n"},
		// The profile's decoder takes a key for a setting under Unicode case folding.
		{"a capitalized hook table", "[Hooks]\non_lane_created = [\"id\"]\n"},
		{"an upper-case hook table", "[HOOKS]\non_lane_created = [\"id\"]\n"},
		{"a hook spelled with the Kelvin sign", "[\"hooKs\"]\non_lane_created = [\"id\"]\n"},
		{"a hook spelled with the long s", "[\"hookſ\"]\non_lane_created = [\"id\"]\n"},
		{"an upper-case root", "[worktree]\nROOT = \"/\"\n"},
		{"a capitalized credential's table", "[Agent.Credentials]\nkind = \"api_key\"\n"},
		{"upper-case review rounds", "[Review]\nMAX_REVIEW_ROUNDS = 9\n"},
		{"upper-case ports", "Resources.PORTS_PER_LANE = 5\n"},
		{"a key spelled two ways", "[agent]\nmodel = \"haiku\"\nMODEL = \"opus\"\n"},
		{"a table spelled two ways", "[agent]\nmodel = \"haiku\"\n[Agent]\neffort = \"low\"\n"},
		{"no TOML", "[hooks\n"},
	}
	for _, caller := range callers {
		for _, layer := range refused {
			_, err := caller.client.StartLane(ctx, "wo-lanes", api.StartLane{LaneID: "lane-1", Overrides: layer.overrides})
			t.Run(caller.name+" names "+layer.name, func(t *testing.T) {
				refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
			})
		}
	}
	if claim := claimOf(t, d.Daemon, "claim-lanes"); claim.Lane != "" || claim.Overrides != "" {
		t.Fatalf("a refused start left the claim %+v", claim)
	}

	const chosen = "[agent]\nmodel = \"haiku\"\neffort = \"low\"\n"
	if _, err := d.client.StartLane(ctx, "wo-lanes", api.StartLane{LaneID: "lane-1", Overrides: chosen}); err != nil {
		t.Fatalf("a member's model and effort: %v", err)
	}
	if claim := claimOf(t, d.Daemon, "claim-lanes"); claim.Lane != "lane-1" || claim.Overrides != chosen {
		t.Errorf("the claim holds %+v", claim)
	}
}

// TestAMembersOverridesThatCanOnlyAddOrNarrowStartTheLane holds the route to what it can judge alone. A
// lane's gates join those of the layers below it, so overrides that set agent.gates, even to an empty
// list, can only add a gate, and the route keeps them. A review bound of one and no ports are already the
// floor, whatever the layers below say. The claim keeps the overrides as they were sent.
func TestAMembersOverridesThatCanOnlyAddOrNarrowStartTheLane(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	accepted := []struct{ name, overrides string }{
		{"a gate", "[agent]\ngates = [\"Edit\"]\n"},
		{"a dotted gate", "agent.gates = [\"Bash(git push *)\"]\n"},
		{"no gates", "[agent]\ngates = []\n"},
		{"no ports", "resources.ports_per_lane = 0\n"},
		{"one round of review", "[review]\nmax_review_rounds = 1\n"},
		{"the floors with a gate and a model", "[agent]\nmodel = \"haiku\"\ngates = [\"Edit\"]\n[review]\nmax_review_rounds = 1\n[resources]\nports_per_lane = 0\n"},
	}
	for i, layer := range accepted {
		t.Run(layer.name, func(t *testing.T) {
			id, claim := fmt.Sprintf("wo-narrow-%d", i), fmt.Sprintf("claim-narrow-%d", i)
			claimWork(t, d, id, claim)
			if _, err := d.client.StartLane(ctx, id, api.StartLane{LaneID: "lane-1", Overrides: layer.overrides}); err != nil {
				t.Fatalf("a member's %s: %v", layer.name, err)
			}
			if got := claimOf(t, d.Daemon, claim); got.Lane != "lane-1" || got.Overrides != layer.overrides {
				t.Errorf("the claim holds %+v", got)
			}
		})
	}
}
