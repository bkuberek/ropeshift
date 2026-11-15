package server_test

import (
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

// TestNoCallerStartsALaneWhoseOverridesRunReadOrPlace holds every caller's lane start, an outside
// delegate's, the assistant's, and a member's, to overrides that name no hook, no credential, and no place
// for the worktree or its branch, and to overrides that are TOML at all. The refusal answers before the
// ledger keeps anything, and a member's overrides of any other key still start the lane.
func TestNoCallerStartsALaneWhoseOverridesRunReadOrPlace(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	_, assistant, _ := d.assistant(t)
	outside := asDelegate(t, d.socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	if _, err := d.client.CreateWorkOrder(ctx, d.workIn("wo-lanes", "Add the retry budget")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Review(ctx, "wo-lanes"); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewLeases(d.Store).Claim(ctx, apitest.Scope, workorders.ClaimRequest{
		WorkOrder: "wo-lanes", ClaimID: "claim-lanes", Worker: d.auth.WorkerID, TTLMs: apitest.LeaseTTL,
	}, talkMember); err != nil {
		t.Fatal(err)
	}

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
