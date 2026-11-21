package server_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
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
		if item.Kind == api.NeedPersonalSetup {
			out = append(out, item)
		}
	}
	return out
}

func needFor(t *testing.T, needs []api.NeedItem, name string) *api.PersonalSetupNeed {
	t.Helper()
	for _, item := range needs {
		if item.PersonalSetup != nil && item.PersonalSetup.Piece.Name == name {
			return item.PersonalSetup
		}
	}
	t.Fatalf("no item names %s among %+v", name, needs)
	return nil
}

func laneIDs(need *api.PersonalSetupNeed) []string {
	ids := make([]string, 0, len(need.Lanes))
	for _, lane := range need.Lanes {
		ids = append(ids, lane.ID)
	}
	slices.Sort(ids)
	return ids
}

var (
	reviewer = lanes.PersonalItem{Kind: lanes.PieceAgent, Name: "reviewer", From: "~/.claude/agents/reviewer.md"}
	github   = lanes.PersonalItem{Kind: lanes.PieceMCPServer, Name: "github", From: "user"}
)

// Each lane says what its session loaded, and a lane whose session reported nothing says nothing of a clean start.
func TestEachLaneSaysWhatItsSessionLoaded(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	ours := []lanes.SetupServer{{Name: "ropeshift", From: lanes.FromRopeshift}}
	clean := w.Running("wo-clean", "Clean work")
	clean.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Model: "resolved-model", MCPServers: ours})
	leaky := w.Running("wo-leaky", "Leaky work")
	leaky.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Personal: []lanes.PersonalItem{reviewer}})
	w.Running("wo-unreported", "Work on a pane")
	if got := floorLane(t, d, clean.ID).Setup; got == nil || got.Generation != 1 || got.Version != "2.1.330" || len(got.Personal) != 0 || !slices.Equal(got.MCPServers, ours) {
		t.Errorf("the clean lane runs with %+v", got)
	}
	if got := floorLane(t, d, leaky.ID).Setup; got == nil || !slices.Equal(got.Personal, []lanes.PersonalItem{reviewer}) {
		t.Errorf("the leaky lane runs with %+v", got)
	}
	if got := floorLane(t, d, "lane-wo-unreported").Setup; got != nil {
		t.Errorf("a lane whose session reported nothing runs with %+v", got)
	}
}

// A piece of the member's own setup that leaked is one Needs you item for each machine, CLI version, and piece, listing
// the lanes it reached. It recommends accepting an agent, a skill, or a command, and a separate configuration directory
// for an MCP server or a plugin, and Accept is the act it offers. Lanes keep running while it waits.
func TestALeakIsOneItemForEachMachineVersionAndPiece(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	first := w.Running("wo-first", "First work")
	first.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Personal: []lanes.PersonalItem{reviewer, github}})
	second := w.Running("wo-second", "Second work")
	second.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Personal: []lanes.PersonalItem{reviewer}})
	older := w.Running("wo-older", "Older work")
	older.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.329", Personal: []lanes.PersonalItem{reviewer}})

	needs := setupNeeds(t, d)
	if len(needs) != 3 {
		t.Fatalf("Needs you holds %d personal setup items, want reviewer on two versions and github on one: %+v", len(needs), needs)
	}
	var agent, server, earlier *api.PersonalSetupNeed
	for _, item := range needs {
		need := item.PersonalSetup
		switch {
		case need.Piece.Name == "reviewer" && need.Version == "2.1.330":
			agent = need
		case need.Piece.Name == "reviewer":
			earlier = need
		default:
			server = need
		}
		if need.Worker != apitest.WorldWorker || !slices.Equal(need.Actions, api.PersonalSetupActions) || need.RecommendationRationale == "" {
			t.Errorf("the item is %+v", need)
		}
	}
	if agent == nil || !slices.Equal(laneIDs(agent), []string{first.ID, second.ID}) || agent.Piece != reviewer || agent.Recommendation != lanes.SetupAccept {
		t.Errorf("the agent's item is %+v", agent)
	}
	if earlier == nil || !slices.Equal(laneIDs(earlier), []string{older.ID}) {
		t.Errorf("the earlier version's item is %+v", earlier)
	}
	if server == nil || !slices.Equal(laneIDs(server), []string{first.ID}) || server.Version != "2.1.330" || server.Recommendation != lanes.SetupSeparateDirectory {
		t.Errorf("the MCP server's item is %+v", server)
	}
	if at := slices.IndexFunc(agent.Lanes, func(lane api.SetupLane) bool { return lane.ID == first.ID }); at < 0 || agent.Lanes[at].WorkOrder != "wo-first" || agent.Lanes[at].Title != "First work" {
		t.Errorf("the item names its lanes as %+v", agent.Lanes)
	}
}

// Accept is recorded on the worker for its version, so the same piece is not raised again on that machine and version,
// and it is raised again on a version that was not accepted. Only a member accepts.
func TestAcceptingALeakRecordsItForTheMachineAndVersion(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-first", "First work")
	lane.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Personal: []lanes.PersonalItem{reviewer, github}})
	accept := api.AcceptPersonalSetup{Version: "2.1.330", Kind: reviewer.Kind, Name: reviewer.Name}
	if err := d.Client.AcceptPersonalSetup(t.Context(), apitest.WorldWorker, accept); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.AcceptPersonalSetup(t.Context(), apitest.WorldWorker, accept); err != nil {
		t.Errorf("accepting again: %v", err)
	}
	if page, err := d.Client.Audit(t.Context(), api.AuditQuery{Kind: ledger.EventPersonalSetupAccepted, Limit: 10}); err != nil || len(page.Events) != 1 {
		t.Errorf("accepting a piece twice recorded %+v (%v), want one acceptance", page.Events, err)
	}
	needs := setupNeeds(t, d)
	if len(needs) != 1 || needs[0].PersonalSetup.Piece.Name != "github" {
		t.Errorf("after the agent was accepted, Needs you holds %+v", needs)
	}
	later := w.Running("wo-later", "Later work")
	later.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.331", Personal: []lanes.PersonalItem{reviewer}})
	if need := needFor(t, setupNeeds(t, d), "reviewer"); need.Version != "2.1.331" || !slices.Equal(laneIDs(need), []string{later.ID}) {
		t.Errorf("a version that was not accepted raises %+v", need)
	}
	if err := d.Client.AcceptPersonalSetup(t.Context(), apitest.WorldWorker, api.AcceptPersonalSetup{Version: "2.1.330", Kind: reviewer.Kind}); err == nil {
		t.Errorf("an acceptance that names no piece was taken")
	}
	if err := d.Client.AcceptPersonalSetup(t.Context(), "no-such-worker", accept); err == nil {
		t.Errorf("an acceptance for a worker the project never saw was taken")
	}
}

// An item lasts while a lane that is not ended runs with the piece, so a lane that ends, or whose next session loads
// clean once the member removed the piece from their own setup, takes the item with it.
func TestALeakGoesWithTheLanesItReached(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-first", "First work")
	lane.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Personal: []lanes.PersonalItem{reviewer}})
	if len(setupNeeds(t, d)) != 1 {
		t.Fatal("the leak is not raised")
	}
	lane.NextGeneration()
	lane.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330"})
	if needs := setupNeeds(t, d); len(needs) != 0 {
		t.Errorf("a lane whose next session loaded clean still raises %+v", needs)
	}
	other := w.Running("wo-other", "Other work")
	other.Upload(ledger.UploadLaneSetup, lanes.Setup{Version: "2.1.330", Personal: []lanes.PersonalItem{reviewer}})
	other.End(workorders.OutcomeCompleted, "")
	if needs := setupNeeds(t, d); len(needs) != 0 {
		t.Errorf("an ended lane still raises %+v", needs)
	}
}

// What a session reports it loaded is a stranger's text, which the repository's and the member's files shape, so a lane
// records it as plain text on one line, with the shapes of credentials taken out, each name held to a bound, and each
// list to a count.
func TestALanesSetupIsRecordedAsBoundedPlainText(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo-noisy", "Noisy work")
	skills := make([]string, 0, 2*lanes.MaxSetupEntries)
	for i := range 2 * lanes.MaxSetupEntries {
		skills = append(skills, fmt.Sprintf("skill-%d", i))
	}
	lane.Upload(ledger.UploadLaneSetup, lanes.Setup{
		Version: "2.1.330\x1b[31m", Model: "resolved\nmodel", Skills: skills,
		MCPServers: []lanes.SetupServer{{Name: "docs\x1b]8;;https://link.example.test\x07", From: "api_key=sk-live-0123456789abcdef"}},
		Personal: []lanes.PersonalItem{{
			Kind: lanes.PieceAgent, Name: strings.Repeat("a", 4*lanes.MaxSetupText), From: "https://member:hunter2hunter2@example.test/agents",
		}},
	})
	got := floorLane(t, d, lane.ID).Setup
	if got == nil {
		t.Fatal("the lane records no setup")
	}
	if got.Version != "2.1.330" || got.Model != "resolved model" || len(got.Skills) != lanes.MaxSetupEntries || got.Skills[0] != "skill-0" {
		t.Errorf("the lane records the version %q, the model %q, and %d skills", got.Version, got.Model, len(got.Skills))
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{`\u001b`, "sk-live-0123456789abcdef", "hunter2hunter2", "link.example.test"} {
		if strings.Contains(string(raw), kept) {
			t.Errorf("the lane records %q: %s", kept, raw)
		}
	}
	if len(got.Personal) != 1 || len(got.Personal[0].Name) > lanes.MaxSetupText {
		t.Errorf("the lane records the personal pieces %+v", got.Personal)
	}
}
