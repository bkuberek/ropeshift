package server_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/version"
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

// steadySet stands a fixed digest in for the digest of the estimate set a plan's approval shows, which covers the
// generated IDs of the work it estimates, and so differs between runs.
func steadySet(view *api.PlanView) {
	if view.Approval != nil && view.Approval.Forecast != nil && view.Approval.Forecast.Set != nil {
		view.Approval.Forecast.Set.Digest = "sha256:steady"
	}
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
	aMeasuredMachine(t, w)
	return d
}

// measuredWorker is a worker ID of the length a worker makes its own: 16 random bytes in hex.
const measuredWorker = "7c41e9a05d2b4f68b3a1e6d29c084f5a"

// aMeasuredMachine joins a second worker that reported every measure the Workers view's Machines table shows, so
// that table holds a full row beside the world's worker, which has reported nothing.
func aMeasuredMachine(t *testing.T, w *apitest.World) {
	t.Helper()
	cpu, load1, load5, load15, pressure := 37.5, 3.42, 2.87, 2.15, 4.2
	total, used, available := int64(64)<<30, int64(41)<<30, int64(23)<<30
	settings, err := lanes.Admission{}.Recommending()
	if err != nil {
		t.Fatal(err)
	}
	recommended, ok := lanes.Recommend(lanes.RecommendInput{Cores: 12, MemoryTotalBytes: &total, Settings: settings})
	if !ok {
		t.Fatal("a machine of 12 cores has no recommendation")
	}
	w.Reporting(measuredWorker, "build-host-07", ledger.ResourceReport{
		IntervalMs: 15_000, AgeMs: 1_000,
		Machine: ledger.MachineResources{
			Cores: 12, CPUPercent: &cpu, Load1: &load1, Load5: &load5, Load15: &load15,
			MemoryTotalBytes: &total, MemoryUsedBytes: &used, MemoryAvailableBytes: &available, MemoryPressureSome: &pressure,
			Volumes: []ledger.VolumeResources{
				{Roles: []string{"state"}, FreeBytes: 182 << 30, TotalBytes: 460 << 30},
				{Roles: []string{"worktrees"}, FreeBytes: 1_310 << 30, TotalBytes: 1_830 << 30},
			},
		},
		Recommendation: &recommended,
		Caps:           ledger.LaneCaps{MaxLanes: recommended.MaxLanes},
		Sessions:       []ledger.SessionResources{},
	})
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
	t.Parallel()
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
	t.Parallel()
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
	WorkOrderStates []workorders.State        `json:"work_order_states"`
	LaneStates      []lanes.ActualState       `json:"lane_states"`
	Conditions      []lanes.Condition         `json:"conditions"`
	Liveness        []lanes.Liveness          `json:"liveness"`
	Reach           []lanes.Reachability      `json:"reach"`
	Freshness       []string                  `json:"freshness"`
	CardStates      []authority.ApprovalState `json:"card_states"`
	DelegateStates  []authority.DelegateState `json:"delegate_states"`
	// ConversationStates are where an assistant's session stands, and MessageStates where a message stands
	// between the ledger and the session it is for.
	ConversationStates []conversations.ActualState `json:"conversation_states"`
	MessageStates      []messages.State            `json:"message_states"`
	Views              map[string]any              `json:"views"`
}

func aWorldInEveryState(t *testing.T) *apitest.Daemon {
	t.Helper()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	apitest.EveryState(w, clock)
	everyConditionOnALane(t, d, w)
	return d
}

// everyConditionOnALane gives a lane of its own to each condition no lane of the world carries. EveryState hands the
// conditions to its lanes one to each in turn, so once there are more conditions than lanes that report one, the
// turn leaves some over. An orchestrator's generation is the only one that awaits a wake, so that one stays with it.
func everyConditionOnALane(t *testing.T, d *apitest.Daemon, w *apitest.World) {
	t.Helper()
	floor := readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	carried := map[string]bool{}
	for _, lane := range floor.Lanes {
		for _, condition := range lane.Conditions {
			carried[condition] = true
		}
	}
	for _, condition := range lanes.EveryCondition {
		if condition == lanes.AwaitingWake || carried[string(condition)] {
			continue
		}
		lane := w.Running("wc-"+string(condition), "A lane that is "+string(condition))
		lane.Status(ledger.LaneStatus{
			ActualState: string(lanes.Idle), Liveness: string(lanes.LivenessAlive), ModelReach: string(lanes.Reachable),
			LedgerReach: string(lanes.Reachable), Conditions: []string{string(condition)},
		})
	}
}

// TestFixtureCoversEveryState walks both state tables into one ledger and writes what it reads back,
// which `every-state.test.tsx` holds to the views. A new state fails here until a walk reaches it and
// a read model shows it, and then fails there until a view has words for it.
func TestFixtureCoversEveryState(t *testing.T) {
	t.Parallel()
	for _, state := range workorders.States() {
		if apitest.WorkWalks[state] == nil {
			t.Fatalf("no walk reaches %s", state)
		}
	}
	d := aWorldInEveryState(t)
	fixture := everyState{
		WorkOrderStates: workorders.States(), LaneStates: lanes.ActualStates, Conditions: lanes.EveryCondition, Liveness: apitest.EveryLiveness,
		Reach: apitest.EveryReach, Freshness: []string{ledger.FreshnessObserved, ledger.FreshnessLastReported},
		CardStates: authority.CardStates(), DelegateStates: authority.DelegateStates(),
		ConversationStates: conversations.ActualStates, MessageStates: messages.States, Views: map[string]any{},
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

// TestGoldenPlan writes a plan as the plan approval page reads it: with its versions, and with the request
// that waits and the one a member decided, which a version asked for by number gives.
func TestGoldenPlan(t *testing.T) {
	d := aDetailedWorld(t)
	var id string
	for _, row := range readView[api.Plans](t, d, api.ViewPlans, "").Plans {
		if row.PendingVersion != 0 {
			id = row.Plan.ID
		}
	}
	if id == "" {
		t.Fatal("the world holds no plan with a version that waits")
	}
	waiting, err := d.Client.Plan(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	steadySet(&waiting)
	holdsGolden(t, "plan", steady(pretty(t, waiting)))
	for _, line := range waiting.Versions {
		if line.State != authority.ApprovalApproved {
			continue
		}
		approved, err := d.Client.PlanVersion(t.Context(), id, line.Version)
		if err != nil {
			t.Fatal(err)
		}
		steadySet(&approved)
		holdsGolden(t, "plan-approved", steady(pretty(t, approved)))
		return
	}
	t.Fatal("the plan has no approved version")
}

// TestGoldenAdoptedApproval writes the request to approve an adopted plan's first version, which the
// ledger holds no plan for yet, as the plan approval page reads it.
func TestGoldenAdoptedApproval(t *testing.T) {
	d, _, _ := aWorld(t)
	adoption := planning.Adoption{
		Exists:    []planning.ExistingRecord{{Ref: "requirement:SHOP-1", Title: "One client", Evidence: []string{"shop:client.go", "shop:client_test.go"}, Cites: []string{"requirement:SHOP-2"}}},
		Continues: []planning.ContinuedWork{{Contract: "contract:retry", WorkOrder: "retry-finish", Title: "Finish the retry", Repo: "shop", Branch: "feature/retry"}},
		New:       []planning.NewRecord{{Ref: "milestone:m2", Title: "Saved carts", WorkOrders: []string{"carts-storage"}}},
		LeftOut:   []planning.LeftOut{{Ref: "branch:spike/old-ui", Reason: "Abandoned in favor of the new checkout"}},
		Changes: []planning.FileChange{
			{Status: planning.ChangeAdded, Path: "plans/plan.md"}, {Status: planning.ChangeMoved, Path: "docs/payments.md", From: "notes/payments.md"},
			{Status: planning.ChangeChanged, Path: "docs/requirements.md"},
		},
	}
	detail, err := json.Marshal(planning.PlanDetail{Title: "Adopt shop", Ref: "shop", Counts: map[string]int{"requirements": 1, "milestone": 2, "contract": 3}, Decisions: 1, Adoption: &adoption, Source: "abc1234"})
	if err != nil {
		t.Fatal(err)
	}
	request := authority.Approval{
		ID: "adopt-approval", Kind: authority.KindPlan, WorkOrder: "adopt-1", Subject: "shop@1", SubjectDigest: "sha256:abc", Summary: "Plan Adopt shop, version 1",
		RequestedBy: "lane:lane-1", State: authority.ApprovalPending, RaisedAt: 1, PlanVersion: 1, Detail: string(detail),
	}
	err = d.Store.Write(t.Context(), apitest.Scope, "test.approval", func(tx ports.WriteTx) error {
		if _, err := tx.Approvals().Insert(request); err != nil {
			return err
		}
		event, err := authority.ApprovalEvent(request, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var shown authority.Approval
	if err := d.Client.Do(t.Context(), http.MethodGet, d.Client.Route("/v1/approvals/adopt-approval"), nil, &shown); err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "approval-adopted", steady(pretty(t, shown)))
}

// TestGoldenRevisedApproval writes the request to approve a later version of a plan that needs revision as Needs
// you holds it, with the decisions made since the approved version beside the difference.
func TestGoldenRevisedApproval(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	source := retrySource("The first draft.")
	approvedPlanWithADecisionOn(t, d, w, source)
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedPlanApproval {
			holdsGolden(t, "needs-you-revised-approval", steady(pretty(t, item)))
			return
		}
	}
	t.Fatal("no request waits for the later version")
}

// TestGoldenReplacedDraftApproval writes the request to approve a later version of a plan whose draft decision a
// member replaced outside the plan, as Needs you holds it: the comparison lists the draft as replaced, and names
// the decision in force.
func TestGoldenReplacedDraftApproval(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	w := apitest.NewWorld(t, d)
	source := retrySource("The first draft.")
	plan := approvedPlanWithADecisionOn(t, d, w, source)
	earlier := derivedDecisionID(plan, 1, "d1")
	if _, err := d.Client.RecordDecision(t.Context(), api.RecordDecision{
		ID: "d-adyen", Status: questions.DecisionDecided, Statement: "Q: Which provider first? A: Adyen", Rationale: "The finance lead chose it.",
		Citations: []string{"PAY-12"}, Scope: "plan:" + plan, Reversibility: questions.Reversible, Categories: []string{questions.CategoryProduct},
		Supersedes: earlier,
	}); err != nil {
		t.Fatal(err)
	}
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	want := []questions.ReplacedDraft{{
		Draft: "d1", Decision: earlier, Status: questions.DecisionReplaced, InForce: "d-adyen", Statement: "Q: Which provider first? A: Adyen",
	}}
	// The page of the version reads the same comparison.
	shown, err := d.Client.PlanVersion(t.Context(), plan, 2)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Approval == nil || !slices.Equal(shown.Approval.Replaced, want) {
		t.Fatalf("the plan approval page lists %+v as replaced, want %+v", shown.Approval, want)
	}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind != api.NeedPlanApproval {
			continue
		}
		if !slices.Equal(item.PlanApproval.Replaced, want) {
			t.Fatalf("the approval lists %+v as replaced, want %+v", item.PlanApproval.Replaced, want)
		}
		// The decision a draft derives is named by a digest of the plan's generated ID, so a fixed name stands for it.
		holdsGolden(t, "needs-you-replaced-draft-approval", bytes.ReplaceAll(steady(pretty(t, item)), []byte(earlier), []byte("pd-derived")))
		return
	}
	t.Fatal("no request waits for the later version")
}

// TestGoldenOutline writes the outline of a planning session as the planning pane reads it.
func TestGoldenOutline(t *testing.T) {
	d, w, _ := aPlanningSession(t)
	askedBy(t, w, "question-blocking", "Hourly or daily?", true)
	outline, err := d.Client.Outline(t.Context(), outlineLane)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "outline", steady(pretty(t, outline)))
	holdsGolden(t, "outline-unread", pretty(t, api.PlanOutline{Unread: "the lane recorded no commit its branch began at"}))
}

// TestGoldenWebRoutes writes the closed list of what the web listener serves, so a web test can hold
// the application's own client to it.
func TestGoldenWebRoutes(t *testing.T) {
	t.Parallel()
	holdsGolden(t, "routes", pretty(t, api.WebRoutes()))
}

// TestGoldenTemplates writes the templates route's list as the Templates view reads it, from a daemon whose build
// version is steady, so the view's tests run on every template the binary embeds.
func TestGoldenTemplates(t *testing.T) {
	t.Parallel()
	list, err := apitest.ExampleTemplates()
	if err != nil {
		t.Fatal(err)
	}
	d := apitest.StartWith(t, apitest.Options{Templates: list, Build: version.Info{Version: "v0.1.0-alpha.16"}})
	got, err := api.NewUnixClient(d.Socket).Templates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "templates", pretty(t, got))
}

// TestGoldenPaneKeys writes the named keys a chord in a lane's pane may end with, so a web test can hold
// the keys the page sends to them.
func TestGoldenPaneKeys(t *testing.T) {
	t.Parallel()
	holdsGolden(t, "pane-keys", pretty(t, api.PaneKeyNames))
}

// A review pins the contract of the work it judges, which puts it under the plan that contract belongs
// to. It is no part of that plan: it counts in no milestone, lies in no layer, is on no critical path,
// and is not planned work in a plan's list, and the plan holds it so it is not listed as unplanned.
func TestAReviewIsNoPartOfThePlanItsContractBelongsTo(t *testing.T) {
	t.Parallel()
	d := aDetailedWorld(t)
	every, err := d.Client.WorkOrders(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	reviews := 0
	for _, order := range every {
		if order.Kind == workorders.KindReview {
			reviews++
		}
	}
	if reviews == 0 {
		t.Fatal("the world holds no review, so this proves nothing")
	}

	plans := readView[api.Plans](t, d, api.ViewPlans, "")
	planned := 0
	for _, plan := range plans.Plans {
		for _, order := range plan.WorkOrders {
			planned++
			if order.Kind == workorders.KindReview {
				t.Errorf("plan %s lists the review %s as its work", plan.Plan.Title, order.ID)
			}
		}
	}
	for _, order := range plans.Unplanned {
		if order.Kind == workorders.KindReview {
			t.Errorf("the review %s is listed as unplanned", order.ID)
		}
	}

	roadmap := readView[api.Roadmap](t, d, api.ViewRoadmap, "")
	counted := 0
	for _, goal := range roadmap.Goals {
		for _, plan := range goal.Plans {
			counted += len(plan.Work)
			if int(plan.Total) != len(plan.Work) {
				t.Errorf("plan %s counts %d work orders and lists %d", plan.Plan.Title, plan.Total, len(plan.Work))
			}
			for _, work := range plan.Work {
				if strings.HasPrefix(work.Title, "Review: ") {
					t.Errorf("plan %s lists %q as work", plan.Plan.Title, work.Title)
				}
			}
		}
	}
	if counted != planned {
		t.Errorf("the roadmap lists %d work orders of plans and the plans view %d", counted, planned)
	}
}

// TestGoldenSettingsReads writes what the projects, repositories, settings, agents, members, and audit
// pages read, from a real ledger, so their tests run on what the API serves.
func TestGoldenSettingsReads(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	in.Checkouts.Place(ports.Clone{Path: "/work/api", ID: "id-api", Remote: "git@example.invalid:acme/api.git", DefaultBranch: "main"})
	in.Checkouts.Place(ports.Clone{Path: "/work/web", Entered: "/srv/code/web", ID: "id-web", Remote: "https://example.invalid/acme/web.git", DefaultBranch: "trunk"})
	project := in.Client.Project(apitest.ProjectName)
	if _, err := project.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}}); err != nil {
		t.Fatal(err)
	}
	dry, err := project.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work", "/nowhere"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	recovery := "[recovery_policy]\nversion = 2\nrestart_stalled = true\nmax_breaker_resets_per_day = 3\nretry_failed = false\nreplan_blocked = true\nmax_attempts = 3\n"
	if _, err := project.PublishPolicy(t.Context(), recovery); err != nil {
		t.Fatal(err)
	}
	assistant := "[assistant]\nversion = 2\nidle_stop = \"30m\"\nwarm_idle_stop = \"2m\"\n"
	if _, err := project.PublishPolicy(t.Context(), assistant); err != nil {
		t.Fatal(err)
	}
	if _, err := project.ChangeRoster(t.Context(), roster.Change{Op: roster.OpSet, Agent: "heavy-implementation", Fields: map[string]string{"effort": "max"}}); err != nil {
		t.Fatal(err)
	}
	reads := map[string]func() (any, error){
		"repositories":       func() (any, error) { return project.Repositories(t.Context()) },
		"repository-dry-run": func() (any, error) { return dry, nil },
		"fleet":              func() (any, error) { return project.Fleet(t.Context()) },
		"roster":             func() (any, error) { return project.Roster(t.Context()) },
		"roster-history":     func() (any, error) { return project.RosterHistory(t.Context(), "") },
		"members":            func() (any, error) { return in.Client.Members(t.Context()) },
		"audit":              func() (any, error) { return project.Audit(t.Context(), api.AuditQuery{Limit: 8}) },
		"policies-assistant": func() (any, error) {
			var out api.PolicyList
			err := in.Client.Do(t.Context(), http.MethodGet, api.InProject(apitest.ProjectName, "/v1/policies?kind=assistant"), nil, &out)
			return out, err
		},
		"policies-recovery": func() (any, error) {
			var out api.PolicyList
			err := in.Client.Do(t.Context(), http.MethodGet, api.InProject(apitest.ProjectName, "/v1/policies?kind=recovery"), nil, &out)
			return out, err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			value, err := read()
			if err != nil {
				t.Fatal(err)
			}
			holdsGolden(t, name, steadyTimes(steady(pretty(t, value))))
		})
	}
}

// TestGoldenEstimatePolicy writes what the estimate policy section reads, from a real ledger: the built-in
// version and a version a member published, which shares the project's delivery history. It reads apart from
// the other settings, whose audit it would otherwise change.
func TestGoldenEstimatePolicy(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	project := in.Client.Project(apitest.ProjectName)
	body := "[estimate_policy]\nversion = 2\nmin_samples = 12\nrecent_samples = 60\nforecast_trials = 1000\nshare_history = true\n"
	if _, err := project.PublishPolicy(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	var out api.PolicyList
	if err := in.Client.Do(t.Context(), http.MethodGet, api.InProject(apitest.ProjectName, "/v1/policies?kind=estimate"), nil, &out); err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "policies-estimate", steadyTimes(steady(pretty(t, out))))
}

// TestGoldenWorkerCaps writes what the Admission section reads, from a real ledger and the recommendation a
// worker computes: a worker's caps while they follow the recommendation, and with a cap a member set above
// it, which the API saves with a warning that says why.
func TestGoldenWorkerCaps(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	credential, epoch := joinWorker(t, d)
	memory := int64(16) << 30
	settings, err := lanes.Admission{}.Recommending()
	if err != nil {
		t.Fatal(err)
	}
	recommended, ok := lanes.Recommend(lanes.RecommendInput{Cores: 8, MemoryTotalBytes: &memory, Settings: settings})
	if !ok {
		t.Fatal("a machine of 8 cores has no recommendation")
	}
	report := aReport()
	report.Recommendation = &recommended
	reportAs(t, d, credential, epoch, report)
	client := capsListener(t, d, fake.NewProfileSource(), aSubscriptionWorker, d.Session)
	following, err := client.LaneCaps(t.Context(), aSubscriptionWorker)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "worker-caps", steadyTimes(steady(pretty(t, following))))
	above, err := client.SetLaneCaps(t.Context(), aSubscriptionWorker, api.SetLaneCaps{MaxLanes: lanesOf(recommended.MaxLanes + 3), MaxLanesPerSignIn: 2})
	if err != nil {
		t.Fatal(err)
	}
	if above.Warning == nil {
		t.Fatalf("a cap above the recommendation of %d saved with no warning: %+v", recommended.MaxLanes, above)
	}
	holdsGolden(t, "worker-caps-above", steadyTimes(steady(pretty(t, above))))
}

var wallTime = regexp.MustCompile(`": 1[0-9]{12}\b`)

// steadyTimes moves the times a harness on the wall clock wrote to a fixed start, kept to the minute
// from the earliest, so a session that lasts an hour still reads as an hour.
func steadyTimes(raw []byte) []byte {
	const start, minute = int64(1_794_000_000_000), int64(60_000)
	at := func(match []byte) int64 {
		value, _ := strconv.ParseInt(string(match[3:]), 10, 64)
		return value
	}
	earliest := int64(0)
	for _, match := range wallTime.FindAll(raw, -1) {
		if earliest == 0 || at(match) < earliest {
			earliest = at(match)
		}
	}
	return wallTime.ReplaceAllFunc(raw, func(match []byte) []byte {
		return fmt.Appendf(nil, "\": %d", start+(at(match)-earliest+minute/2)/minute*minute)
	})
}
