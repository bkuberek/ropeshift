package server_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

func floorLane(t *testing.T, d *apitest.Daemon, id string) api.FloorLane {
	t.Helper()
	for _, lane := range readView[api.ShopFloor](t, d, api.ViewShopFloor, "").Lanes {
		if lane.ID == id {
			return lane
		}
	}
	t.Fatalf("the floor lists no lane %s", id)
	return api.FloorLane{}
}

func setupNeeds(t *testing.T, d *apitest.Daemon) []api.NeedItem {
	t.Helper()
	var out []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Work != nil && item.Work.Reason == api.ReasonPersonalSetup {
			out = append(out, item)
		}
	}
	return out
}

// Each lane says what its session loaded, and a lane that loaded the member's own setup despite the flags is one item
// of Needs you: it names what leaked and the lane, offers no act, since the one way to stop a running lane is to cancel
// its work, and recommends leaving the lane running. The item goes when the session that loaded it ends, and a lane
// whose session reported nothing says nothing of a clean start.
func TestALaneThatLoadedTheMembersOwnSetupNeedsThem(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	ours := []lanes.SetupServer{{Name: "ropeshift", From: lanes.FromRopeshift}}
	clean := w.Running("wo-clean", "Clean work")
	clean.Upload(ledger.UploadLaneSetup, lanes.Setup{Model: "resolved-model", MCPServers: ours})
	leaked := []lanes.PersonalItem{
		{Kind: lanes.PieceAgent, Name: "reviewer", From: "~/.claude/agents/reviewer.md"},
		{Kind: lanes.PieceMCPServer, Name: "github", From: "user"},
	}
	leaky := w.Running("wo-leaky", "Leaky work")
	leaky.Upload(ledger.UploadLaneSetup, lanes.Setup{
		Model: "resolved-model", MCPServers: append(ours, lanes.SetupServer{Name: "github", From: "user"}), Personal: leaked,
	})
	w.Running("wo-unreported", "Work on a pane")

	if got := floorLane(t, d, clean.ID).Setup; got == nil || got.Generation != 1 || len(got.Personal) != 0 || !slices.Equal(got.MCPServers, ours) {
		t.Errorf("the clean lane runs with %+v", got)
	}
	if got := floorLane(t, d, leaky.ID).Setup; got == nil || !slices.Equal(got.Personal, leaked) {
		t.Errorf("the leaky lane runs with %+v", got)
	}
	if got := floorLane(t, d, "lane-wo-unreported").Setup; got != nil {
		t.Errorf("a lane whose session reported nothing runs with %+v", got)
	}

	needs := setupNeeds(t, d)
	if len(needs) != 1 {
		t.Fatalf("Needs you holds %d personal setup items: %+v", len(needs), needs)
	}
	need := needs[0].Work
	if need.Lane == nil || need.Lane.ID != leaky.ID || need.Actions == nil || len(need.Actions) != 0 || !slices.Equal(need.Personal, leaked) {
		t.Errorf("the item is %+v", need)
	}
	if need.Recommendation != lanes.SetupIgnore || !strings.Contains(need.RecommendationRationale, "MCP server") {
		t.Errorf("an MCP server of the member's leaked, and the item recommends %q: %q", need.Recommendation, need.RecommendationRationale)
	}
	for _, word := range []string{"github", "reviewer", leaky.ID} {
		if !strings.Contains(need.Detail, word) {
			t.Errorf("the item's detail %q does not name %s", need.Detail, word)
		}
	}

	leaky.NextGeneration()
	if needs := setupNeeds(t, d); len(needs) != 0 {
		t.Errorf("the item outlived the session that loaded the setup: %+v", needs)
	}
}

// Agents, skills, and commands only add instructions inside the lane's rules, and the recommendation says so.
func TestALeakOfInstructionsAloneSaysTheyOnlyAddInstructions(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-skill", "Skilled work")
	lane.Upload(ledger.UploadLaneSetup, lanes.Setup{Personal: []lanes.PersonalItem{{Kind: lanes.PieceSkill, Name: "pdf", From: "~/.claude/skills/pdf"}}})
	needs := setupNeeds(t, d)
	if len(needs) != 1 || needs[0].Work.Recommendation != lanes.SetupIgnore || !strings.Contains(needs[0].Work.RecommendationRationale, "only add instructions") {
		t.Errorf("Needs you holds %+v", needs)
	}
}
