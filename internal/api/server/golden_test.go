package server_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

var updateGolden = flag.Bool("update", false, "write the golden JSON the web tests read")

// goldenDir is what the web tests read, so a change to a read model fails here until the file changes
// and fails there until the view does.
var goldenDir = filepath.Join("..", "..", "..", "web", "testdata")

var generatedID = regexp.MustCompile(`\b[0-9A-HJKMNP-TV-Z]{26}\b`)

// steady names each generated ID by where it first appears, because only those differ between runs.
func steady(raw []byte) []byte {
	seen := map[string]string{}
	return generatedID.ReplaceAllFunc(raw, func(id []byte) []byte {
		name, ok := seen[string(id)]
		if !ok {
			name = fmt.Sprintf("generated-%d", len(seen)+1)
			seen[string(id)] = name
		}
		return []byte(name)
	})
}

func pretty(t *testing.T, value any) []byte {
	t.Helper()
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func holdsGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".json")
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: run this test with -update", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s changed: run this test with -update, and read the diff\ngot:\n%s", name, got)
	}
}

// aDetailedWorld is one of everything a view reads, with a planned project whose second version waits
// for approval.
func aDetailedWorld(t *testing.T) *apitest.Daemon {
	t.Helper()
	clock := apitest.NewClock()
	knowledge := fake.NewKnowledgeSource()
	apitest.Documents(knowledge)
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect, Knowledge: []ports.KnowledgeSource{knowledge}})
	w := apitest.NewWorld(t, d)
	project := apitest.Factory(w, clock)
	apitest.Populate(w, clock)
	apitest.Detail(w, clock)
	project.Change()
	return d
}

func readAny(t *testing.T, d *apitest.Daemon, name string) any {
	t.Helper()
	var value any
	if err := d.Client.ReadView(t.Context(), name, "", &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// TestGoldenReadModels writes what the web tests read, from a real ledger on a clock that repeats, so
// a test of the page runs on what the API serves and nothing a test author imagined.
func TestGoldenReadModels(t *testing.T) {
	d := aDetailedWorld(t)
	for _, name := range api.Views {
		t.Run(name, func(t *testing.T) {
			holdsGolden(t, name, steady(pretty(t, readAny(t, d, name))))
		})
	}
}

// TestGoldenDocument writes one document as a page reads it, with the text an attacker could commit, and
// the newest version of a planning document as the ledger stored it.
func TestGoldenDocument(t *testing.T) {
	d := aDetailedWorld(t)
	ref := api.DocumentRef{Path: apitest.WorldSpecPath}
	document, err := d.Client.Document(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "document", pretty(t, document))
	versions, err := d.Client.DocumentVersions(t.Context(), api.DocumentRef{Path: "design.md"})
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "document-versions", pretty(t, api.DocumentVersions{Versions: versions}))
	recorded, err := d.Client.RecordedDocument(t.Context(), "spec:factory-api", 0)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "recorded-document", steady(pretty(t, recorded)))
}

// everyState is what the web test reads: both state tables, the values that stand beside a lane's
// state, and every read model of a ledger that holds one of each.
type everyState struct {
	WorkOrderStates []workorders.State   `json:"work_order_states"`
	LaneStates      []lanes.ActualState  `json:"lane_states"`
	Conditions      []lanes.Condition    `json:"conditions"`
	Liveness        []lanes.Liveness     `json:"liveness"`
	Reach           []lanes.Reachability `json:"reach"`
	Freshness       []string             `json:"freshness"`
	Views           map[string]any       `json:"views"`
}

func aWorldInEveryState(t *testing.T) *apitest.Daemon {
	t.Helper()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	apitest.EveryState(apitest.NewWorld(t, d), clock)
	return d
}

// TestFixtureCoversEveryState walks both state tables into one ledger and writes what it reads back,
// which `every-state.test.tsx` holds to the views. A new state fails here until a walk reaches it and
// a read model shows it, and then fails there until a view has words for it.
func TestFixtureCoversEveryState(t *testing.T) {
	for _, state := range workorders.States() {
		if apitest.WorkWalks[state] == nil {
			t.Fatalf("no walk reaches %s", state)
		}
	}
	d := aWorldInEveryState(t)
	fixture := everyState{
		WorkOrderStates: workorders.States(), LaneStates: lanes.ActualStates, Conditions: lanes.EveryCondition, Liveness: apitest.EveryLiveness,
		Reach: apitest.EveryReach, Freshness: []string{ledger.FreshnessObserved, ledger.FreshnessLastReported}, Views: map[string]any{},
	}
	for _, name := range api.Views {
		fixture.Views[name] = readAny(t, d, name)
	}

	listed := map[string]workorders.State{}
	list := func(orders ...workorders.WorkOrder) {
		for _, order := range orders {
			listed[order.ID] = order.State
		}
	}
	plans := readView[api.Plans](t, d, api.ViewPlans, "")
	list(plans.Unplanned...)
	for _, plan := range plans.Plans {
		list(plan.WorkOrders...)
	}
	for _, line := range readView[api.InProgress](t, d, api.ViewInProgress, "").Items {
		list(line.WorkOrder)
	}
	for _, line := range readView[api.OnHold](t, d, api.ViewOnHold, "").Items {
		list(line.WorkOrder)
	}
	for _, line := range readView[api.Completed](t, d, api.ViewCompleted, "").Items {
		list(line.WorkOrder)
	}
	for _, state := range workorders.States() {
		if id := apitest.WorkIn(state); listed[id] != state {
			t.Errorf("%s stands in no read model as %s: %q", id, state, listed[id])
		}
	}

	floor := readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	lanesByID, fresh := map[string]string{}, map[string]bool{}
	for _, lane := range floor.Lanes {
		lanesByID[lane.ID], fresh[lane.Freshness] = lane.ActualState, true
	}
	for _, state := range lanes.ActualStates {
		if id := apitest.LaneIn(state); lanesByID[id] != string(state) {
			t.Errorf("%s reads %q, want %s", id, lanesByID[id], state)
		}
	}
	for _, kind := range fixture.Freshness {
		if !fresh[kind] {
			t.Errorf("no lane reads %s", kind)
		}
	}
	holdsGolden(t, "every-state", steady(pretty(t, fixture)))
}

// TestGoldenWebRoutes writes the closed list of what the web listener serves, so a web test can hold
// the application's own client to it.
func TestGoldenWebRoutes(t *testing.T) {
	holdsGolden(t, "routes", pretty(t, api.WebRoutes()))
}

// TestGoldenPaneKeys writes the closed list of keys a member may press in a lane's pane, so a web test
// can hold the page's buttons to it.
func TestGoldenPaneKeys(t *testing.T) {
	holdsGolden(t, "pane-keys", pretty(t, api.PaneKeys))
}
