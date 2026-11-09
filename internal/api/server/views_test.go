package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// aWorld lays out one of everything that reaches a person, each through the path that makes it, with
// time between them, so that wait time has an order to hold.
func aWorld(t *testing.T) (*apitest.Daemon, *apitest.World, *apitest.Clock) {
	t.Helper()
	clock := apitest.NewClock()
	knowledge := fake.NewKnowledgeSource()
	knowledge.Write("design.md", "# The retry budget\n")
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect, Knowledge: []ports.KnowledgeSource{knowledge}})
	w := apitest.NewWorld(t, d)
	apitest.Populate(w, clock)
	return d, w, clock
}

func readView[T any](t *testing.T, d *apitest.Daemon, name, category string) T {
	t.Helper()
	var out T
	if err := d.Client.ReadView(t.Context(), name, category, &out); err != nil {
		t.Fatalf("view %s: %v", name, err)
	}
	return out
}

func TestEveryViewHasAReadModelAndAStamp(t *testing.T) {
	d, _, _ := aWorld(t)
	for _, name := range api.Views {
		t.Run(name, func(t *testing.T) {
			got := readView[api.Stamp](t, d, name, "")
			if got.AsOf == 0 || got.Revision == 0 {
				t.Errorf("stamp %+v", got)
			}
		})
	}
}

// What a page reads is what every surface reads, so no secret may stand in it: a member's session, a
// worker's credential, or anything the agent CLIs sign in with, which the ledger never holds.
func TestNoViewHoldsASecret(t *testing.T) {
	d, w, _ := aWorld(t)
	for _, name := range api.Views {
		t.Run(name, func(t *testing.T) {
			var raw json.RawMessage
			if err := d.Client.ReadView(t.Context(), name, "", &raw); err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			for label, secret := range map[string]string{"a session": d.Session, "a worker's credential": w.Credential()} {
				if secret == "" {
					t.Fatalf("no %s to look for", label)
				}
				if strings.Contains(text, secret) {
					t.Errorf("the view holds %s", label)
				}
			}
			for _, word := range []string{"proof_digest", "credential", "api_key", "oauth", "access_token", "refresh_token"} {
				if strings.Contains(text, word) {
					t.Errorf("the view names %s", word)
				}
			}
		})
	}
}

// A read needs no session on the socket (D26).
func TestAViewOnTheSocketNeedsNoSession(t *testing.T) {
	d := apitest.Start(t)
	anonymous := api.NewUnixClient(d.Socket)
	defer anonymous.Close()
	for _, name := range api.Views {
		var out api.Stamp
		if err := anonymous.ReadView(t.Context(), name, "", &out); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestNeedsYouPutsTheLongestWaitFirst(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	if len(view.Items) == 0 {
		t.Fatal("nothing needs a person")
	}
	for i := 1; i < len(view.Items); i++ {
		if view.Items[i-1].Since > view.Items[i].Since {
			t.Errorf("item %d waited %d, and the one before it %d", i, view.Items[i].Since, view.Items[i-1].Since)
		}
	}
	var kinds []string
	for _, item := range view.Items {
		kinds = append(kinds, item.Kind+":"+reason(item))
	}
	want := []string{
		"work:lapsed", "work:blocked", "effect:", "work:acceptance", "approval:", "questions:", "work:human_resume", "work:stalled", "work:tripped",
	}
	if !slices.Equal(kinds, want) {
		t.Errorf("order %v, want %v", kinds, want)
	}
}

func reason(item api.NeedItem) string {
	if item.Work != nil {
		return item.Work.Reason
	}
	return ""
}

func needOf(t *testing.T, view api.NeedsYou, kind string) api.NeedItem {
	t.Helper()
	for _, item := range view.Items {
		if item.Kind == kind {
			return item
		}
	}
	t.Fatalf("no %s among %+v", kind, view.Items)
	return api.NeedItem{}
}

func workNeed(t *testing.T, view api.NeedsYou, why string) *api.WorkNeed {
	t.Helper()
	for _, item := range view.Items {
		if item.Work != nil && item.Work.Reason == why {
			return item.Work
		}
	}
	t.Fatalf("no work waits for %s", why)
	return nil
}

func TestNeedsYouCarriesWhatItTakesToAnswer(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	batch := needOf(t, view, api.NeedQuestions).Batch
	if batch == nil || len(batch.Items) != 2 {
		t.Fatalf("batch %+v", batch)
	}
	first := batch.Items[0]
	if len(first.Options) != 2 || first.Recommendation == "" || !first.Blocking {
		t.Errorf("question %+v", first)
	}
	approval := needOf(t, view, api.NeedApproval).Approval
	if approval == nil || approval.SubjectDigest == "" || approval.Summary == "" {
		t.Errorf("approval %+v", approval)
	}
	effect := needOf(t, view, api.NeedEffect).Effect
	if effect == nil || effect.Key == "" || !slices.Equal(effect.Offers, []ledger.EffectOutcome{ledger.EffectHappened, ledger.EffectNotHappened, ledger.EffectRetry}) {
		t.Errorf("effect %+v", effect)
	}
	cases := map[string][]string{
		api.ReasonLapsed:     {api.ActReassign, api.ActCancel},
		api.ReasonBlocked:    {api.ActResolve, api.ActReplan, api.ActCancel},
		api.ReasonTripped:    {api.ActReset, api.ActCancel},
		api.ReasonStalled:    {api.ActRespawn, api.ActCancel},
		api.ReasonAcceptance: {api.ActAccept, api.ActRework},
		api.ReasonResume:     {api.ActResume},
	}
	for why, actions := range cases {
		if got := workNeed(t, view, why); !slices.Equal(got.Actions, actions) {
			t.Errorf("%s offers %v, want %v", why, got.Actions, actions)
		}
	}
}

func TestSomethingAnsweredLeavesNeedsYou(t *testing.T) {
	d, _, _ := aWorld(t)
	ctx := t.Context()
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	effect := needOf(t, view, api.NeedEffect)
	if _, err := d.Client.DecideEffect(ctx, effect.ID, api.DecideEffect{Outcome: ledger.EffectHappened}); err != nil {
		t.Fatal(err)
	}
	approval := needOf(t, view, api.NeedApproval).Approval
	if _, err := d.Client.DecideApproval(ctx, approval.ID, api.DecideApproval{Digest: approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	for i, item := range needOf(t, view, api.NeedQuestions).Batch.Items {
		if _, err := d.Client.AnswerQuestion(ctx, item.ID, api.AnswerQuestion{ID: "answer-" + string(rune('a'+i)), Answer: "Keep the budget"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind != api.NeedWork {
			t.Errorf("%s stayed after its answer: %+v", item.Kind, item)
		}
	}
}

// An effect a person cannot run again does not offer that.
func TestAnEffectOfAnEndedLaneOffersTwoAnswers(t *testing.T) {
	d, w, _ := aWorld(t)
	lane := w.Running("wo-gone", "Clean up")
	lane.Effect("lane-wo-gone/1/hook/lane_created", "hook", 1)
	lane.End(workorders.OutcomeFailed, "crashed")
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedEffect && item.Effect.Lane == lane.ID {
			if slices.Contains(item.Effect.Offers, ledger.EffectRetry) {
				t.Errorf("offers %v", item.Effect.Offers)
			}
			return
		}
	}
	t.Fatal("the effect left the view")
}

func TestRoadmapStatesProgress(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Roadmap](t, d, api.ViewRoadmap, "")
	if len(view.Goals) < 2 {
		t.Fatalf("groups %+v", view.Goals)
	}
	goal := view.Goals[0]
	if goal.Goal == nil || goal.Goal.ID != "goal-1" || len(goal.Plans) != 1 {
		t.Fatalf("group %+v", goal)
	}
	if plan := goal.Plans[0]; plan.Total != 1 || plan.States[workorders.Running] != 1 {
		t.Errorf("plan %+v", plan)
	}
	if view.Goals[len(view.Goals)-1].Goal != nil {
		t.Error("plans under no goal stand last, with no goal")
	}
}

func TestPlansListEachContractVersion(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Plans](t, d, api.ViewPlans, "")
	for _, plan := range view.Plans {
		if plan.Goal != nil && plan.Goal.ID == "goal-1" {
			if len(plan.Contracts) != 1 || len(plan.Contracts[0].Versions) != 2 || len(plan.WorkOrders) != 1 {
				t.Errorf("plan %+v", plan)
			}
			return
		}
	}
	t.Fatal("no plan names the goal")
}

func TestPlansShowAReviewAndWhatItLeftOpen(t *testing.T) {
	d, w, _ := aWorld(t)
	plan := readView[api.Plans](t, d, api.ViewPlans, "").Plans[0]
	review, err := d.Client.ReviewPlan(t.Context(), plan.Plan.ID, api.ReviewPlan{ID: "review-1"})
	if err != nil {
		t.Fatal(err)
	}
	_ = w
	view := readView[api.Plans](t, d, api.ViewPlans, "")
	for _, row := range view.Plans {
		if row.Plan.ID != plan.Plan.ID {
			continue
		}
		if len(row.Reviews) != 1 || row.Reviews[0].WorkOrder.ID != review.ID {
			t.Errorf("reviews %+v", row.Reviews)
		}
		return
	}
	t.Fatal("the plan left the view")
}

func TestDocumentsListWithNoBody(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Documents](t, d, api.ViewDocuments, "")
	if len(view.Documents) != 1 || view.Documents[0].Path != "design.md" || view.Documents[0].Body != "" {
		t.Errorf("documents %+v", view.Documents)
	}
}

func TestDocumentsSayWhatKindEachIs(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Documents](t, d, api.ViewDocuments, "")
	if len(view.Documents) != 1 || view.Documents[0].Kind != planning.DocumentDesign {
		t.Errorf("documents %+v", view.Documents)
	}
}

// slowSource stands for a source that runs a subprocess for each file. It reads the ledger while it
// works, which a one-connection store can only serve if no read is open around it.
type slowSource struct {
	ports.KnowledgeSource
	store ports.Store
}

func (s slowSource) List(ctx context.Context) ([]ports.KnowledgeDocument, error) {
	err := s.store.Read(ctx, apitest.Scope, func(ports.ReadTx) error { return nil })
	return nil, err
}

// A source runs git for each file, which is no work to do inside a read of the ledger.
func TestDocumentsListOutsideAnyRead(t *testing.T) {
	d := apitest.Start(t)
	source := &slowSource{KnowledgeSource: fake.NewKnowledgeSource()}
	source.store = d.Store
	config := d.Config
	config.Knowledge = []ports.KnowledgeSource{source}
	recorder := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, api.ViewPath(api.ViewDocuments), nil)
	server.New(config).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
}

func TestDecisionsCarryTheirLineage(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Decisions](t, d, api.ViewDecisions, "")
	lines := map[string]api.DecisionLine{}
	for _, line := range view.Decisions {
		lines[line.ID] = line
	}
	first, second := lines["decision-1"], lines["decision-2"]
	want := []string{"decision-1", "decision-2"}
	if !slices.Equal(first.Lineage, want) || !slices.Equal(second.Lineage, want) {
		t.Errorf("lineages %v and %v", first.Lineage, second.Lineage)
	}
	if first.SupersededBy != "decision-2" || second.SupersededBy != "" {
		t.Errorf("superseded by %q and %q", first.SupersededBy, second.SupersededBy)
	}
}

func TestOpenQuestionsFilterByCategory(t *testing.T) {
	d, _, _ := aWorld(t)
	all := readView[api.Questions](t, d, api.ViewQuestions, "")
	if len(all.Questions) != 2 || !slices.Contains(all.Categories, questions.CategoryProduct) {
		t.Fatalf("view %+v", all)
	}
	if none := readView[api.Questions](t, d, api.ViewQuestions, "no-such-category"); len(none.Questions) != 0 {
		t.Errorf("a category nothing holds found %+v", none.Questions)
	}
	if some := readView[api.Questions](t, d, api.ViewQuestions, questions.CategoryProduct); len(some.Questions) != 2 {
		t.Errorf("found %+v", some.Questions)
	}
}

// A filter comes from a link, so its text is a name to match and never a pattern to run.
func TestACategoryFilterIsNeverAPattern(t *testing.T) {
	d, _, _ := aWorld(t)
	for _, category := range []string{"%", "pro%", "produc_", `product"%`, `\`} {
		t.Run(category, func(t *testing.T) {
			if found := readView[api.Questions](t, d, api.ViewQuestions, category); len(found.Questions) != 0 {
				t.Errorf("questions found %d", len(found.Questions))
			}
			if found := readView[api.Decisions](t, d, api.ViewDecisions, category); len(found.Decisions) != 0 {
				t.Errorf("decisions found %d", len(found.Decisions))
			}
		})
	}
}

func TestOpenQuestionsLeaveOutAnsweredOnes(t *testing.T) {
	d, _, _ := aWorld(t)
	all := readView[api.Questions](t, d, api.ViewQuestions, "")
	if _, err := d.Client.AnswerQuestion(t.Context(), all.Questions[0].ID, api.AnswerQuestion{ID: "answered", Answer: "Yes"}); err != nil {
		t.Fatal(err)
	}
	if left := readView[api.Questions](t, d, api.ViewQuestions, ""); len(left.Questions) != 1 {
		t.Errorf("left %+v", left.Questions)
	}
}

func TestInProgressHoldsClaimedAndRunningWork(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.InProgress](t, d, api.ViewInProgress, "")
	var ids []string
	for _, item := range view.Items {
		ids = append(ids, item.WorkOrder.ID)
		if item.Lane == nil {
			t.Errorf("%s has no lane", item.WorkOrder.ID)
		}
	}
	for _, want := range []string{"wo-running", "wo-tripped", "wo-stalled", "wo-effect"} {
		if !slices.Contains(ids, want) {
			t.Errorf("%s is missing from %v", want, ids)
		}
	}
	for _, unwanted := range []string{"wo-parked", "wo-blocked", "wo-accept", "wo-lapsed"} {
		if slices.Contains(ids, unwanted) {
			t.Errorf("%s stands in %v", unwanted, ids)
		}
	}
}

func TestOnHoldSaysWhatEachWaitsFor(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.OnHold](t, d, api.ViewOnHold, "")
	if len(view.Items) != 1 {
		t.Fatalf("items %+v", view.Items)
	}
	item := view.Items[0]
	if item.WorkOrder.ID != "wo-parked" || len(item.Wakes) != 1 || item.Wakes[0].Expression != lanes.QuestionWake([]string{"question-1"}) || item.Since == 0 {
		t.Errorf("item %+v", item)
	}
}

// A lane parked on a question says which question in Needs you, so a person reads what would move it
// before resuming it by hand.
func TestAParkedLaneSaysWhatItWaitsFor(t *testing.T) {
	d, _, _ := aWorld(t)
	work := workNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""), api.ReasonResume)
	if len(work.Wakes) != 1 || work.Wakes[0].Expression != lanes.QuestionWake([]string{"question-1"}) || work.Wakes[0].ID == "" {
		t.Errorf("wakes %+v", work.Wakes)
	}
	for _, other := range []string{api.ReasonLapsed, api.ReasonBlocked, api.ReasonAcceptance} {
		if wakes := workNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""), other).Wakes; len(wakes) != 0 {
			t.Errorf("%s carries wakes %+v", other, wakes)
		}
	}
}

func TestStuckListsTheDesignsFive(t *testing.T) {
	d, _, clock := aWorld(t)
	clock.Advance(25 * time.Hour)
	view := readView[api.Stuck](t, d, api.ViewStuck, "")
	seen := map[string]bool{}
	for _, item := range view.Items {
		seen[item.Reason] = true
		if item.Since == 0 || item.ID == "" {
			t.Errorf("item %+v", item)
		}
	}
	for _, want := range []string{api.ReasonLapsed, api.ReasonBlocked, api.ReasonTripped, api.ReasonStalled, api.ReasonLongParked} {
		if !seen[want] {
			t.Errorf("no %s in %v", want, seen)
		}
	}
}

// A parked lane that waits on a person and has waited long is one thing, and it keeps its way out.
func TestALongParkedLaneStandsOnceInNeedsYou(t *testing.T) {
	d, _, clock := aWorld(t)
	clock.Advance(25 * time.Hour)
	var found []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.ID == "wo-parked" {
			found = append(found, item)
		}
	}
	if len(found) != 1 {
		t.Fatalf("items %+v", found)
	}
	if work := found[0].Work; work.Reason != api.ReasonLongParked || !slices.Equal(work.Actions, []string{api.ActResume, api.ActCancel}) || len(work.Wakes) != 1 {
		t.Errorf("work %+v", work)
	}
}

func TestAParkedLaneIsNotStuckBeforeItsTime(t *testing.T) {
	d, _, _ := aWorld(t)
	for _, item := range readView[api.Stuck](t, d, api.ViewStuck, "").Items {
		if item.Reason == api.ReasonLongParked {
			t.Errorf("item %+v", item)
		}
	}
}

func TestCompletedHoldsOutcomesWithArtifacts(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Completed](t, d, api.ViewCompleted, "")
	if len(view.Items) != 1 {
		t.Fatalf("items %+v", view.Items)
	}
	item := view.Items[0]
	if item.WorkOrder.ID != "wo-accept" || item.Standing.State != workorders.Acceptance || len(item.Standing.Artifacts) == 0 {
		t.Errorf("item %+v", item)
	}
}

// Work that ended is outcomes to a person, whichever way it ended, and each says why when it can.
func TestCompletedHoldsEveryWayWorkEnds(t *testing.T) {
	d, w, clock := aWorld(t)
	w.Running("wo-failed", "Try the impossible").End(workorders.OutcomeFailed, "crashed")
	clock.Advance(time.Minute)
	w.Recorded("wo-canceled", "Drop the old plan")
	w.Cancel("wo-canceled")
	clock.Advance(time.Minute)
	w.Running("wo-done", "Finish the job").End(workorders.OutcomeCompleted, "")
	if _, err := d.Client.Accept(t.Context(), "wo-done"); err != nil {
		t.Fatal(err)
	}
	view := readView[api.Completed](t, d, api.ViewCompleted, "")
	got := map[string]api.CompletedWork{}
	var order []string
	for _, item := range view.Items {
		got[item.WorkOrder.ID] = item
		order = append(order, item.WorkOrder.ID)
		if item.Since == 0 {
			t.Errorf("%s says no time", item.WorkOrder.ID)
		}
	}
	for id, state := range map[string]workorders.State{
		"wo-failed": workorders.Failed, "wo-canceled": workorders.Canceled, "wo-done": workorders.Done, "wo-accept": workorders.Acceptance,
	} {
		if got[id].WorkOrder.State != state {
			t.Errorf("%s reads %q, want %q", id, got[id].WorkOrder.State, state)
		}
	}
	if got["wo-failed"].Reason != "crashed" {
		t.Errorf("the failure says %q", got["wo-failed"].Reason)
	}
	if want := []string{"wo-done", "wo-canceled", "wo-failed", "wo-accept"}; !slices.Equal(order, want) {
		t.Errorf("order %v, want the newest end first", order)
	}
}

func TestInProgressSaysSinceWhenTheLongestFirst(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.InProgress](t, d, api.ViewInProgress, "")
	for i, item := range view.Items {
		if item.Since == 0 {
			t.Errorf("%s says no time", item.WorkOrder.ID)
		}
		if i > 0 && view.Items[i-1].Since > item.Since {
			t.Errorf("%s stands after a later start", item.WorkOrder.ID)
		}
	}
}

// Work with no contract belongs to no plan, and it must stand somewhere a person can find it.
func TestPlansListWorkNoPlanHolds(t *testing.T) {
	d, w, _ := aWorld(t)
	w.Created("wo-loose", "Not planned yet")
	view := readView[api.Plans](t, d, api.ViewPlans, "")
	if len(view.Unplanned) != 1 || view.Unplanned[0].ID != "wo-loose" {
		t.Fatalf("unplanned %+v", view.Unplanned)
	}
	for _, plan := range view.Plans {
		for _, order := range plan.WorkOrders {
			if order.ID == "wo-loose" {
				t.Errorf("plan %s holds it", plan.Plan.ID)
			}
		}
	}
}

// Recording a review makes a plan and a contract to carry it, which is how the ledger holds work and
// nothing a person planned. A review stands under the plan it reads.
func TestAReviewAddsNoPlanToWhatAPersonReads(t *testing.T) {
	d, _, _ := aWorld(t)
	before := readView[api.Plans](t, d, api.ViewPlans, "")
	plan := before.Plans[0]
	if _, err := d.Client.ReviewPlan(t.Context(), plan.Plan.ID, api.ReviewPlan{ID: "review-1"}); err != nil {
		t.Fatal(err)
	}
	after := readView[api.Plans](t, d, api.ViewPlans, "")
	if len(after.Plans) != len(before.Plans) {
		t.Errorf("%d plans became %d", len(before.Plans), len(after.Plans))
	}
	planned := 0
	for _, group := range readView[api.Roadmap](t, d, api.ViewRoadmap, "").Goals {
		planned += len(group.Plans)
	}
	if planned != len(before.Plans) {
		t.Errorf("the roadmap holds %d plans, want %d", planned, len(before.Plans))
	}
}

func TestPlansLeaveOutOfUnplannedWhatAPlanHolds(t *testing.T) {
	d, _, _ := aWorld(t)
	plan := readView[api.Plans](t, d, api.ViewPlans, "").Plans[0]
	if _, err := d.Client.ReviewPlan(t.Context(), plan.Plan.ID, api.ReviewPlan{ID: "review-1"}); err != nil {
		t.Fatal(err)
	}
	if left := readView[api.Plans](t, d, api.ViewPlans, "").Unplanned; len(left) != 0 {
		t.Errorf("unplanned %+v", left)
	}
}

func TestShopFloorSeparatesObservedFromLastReported(t *testing.T) {
	d, _, clock := aWorld(t)
	view := readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	if len(view.Workers) != 1 || view.Workers[0].Worker.ID != apitest.WorldWorker {
		t.Fatalf("workers %+v", view.Workers)
	}
	if len(view.Lanes) == 0 {
		t.Fatal("no lanes")
	}
	clock.Advance(time.Hour)
	later := readView[api.ShopFloor](t, d, api.ViewShopFloor, "")
	if got := later.Workers[0].Freshness; got != ledger.FreshnessLastReported {
		t.Errorf("a worker silent for an hour reads %s", got)
	}
}

// A lane's ID says nothing to a person, so the shop floor names the work each lane runs.
func TestShopFloorNamesTheWorkOfEachLane(t *testing.T) {
	d, _, _ := aWorld(t)
	titles := map[string]string{}
	var raw struct {
		Lanes []struct {
			ID    string `json:"id"`
			Title string `json:"work_order_title"`
		} `json:"lanes"`
	}
	if err := d.Client.ReadView(t.Context(), api.ViewShopFloor, "", &raw); err != nil {
		t.Fatal(err)
	}
	for _, lane := range raw.Lanes {
		titles[lane.ID] = lane.Title
	}
	for lane, want := range map[string]string{"lane-wo-running": "Write the docs", "lane-wo-parked": "Wait for the check"} {
		if titles[lane] != want {
			t.Errorf("%s names %q, want %q", lane, titles[lane], want)
		}
	}
}

func TestOperationsCountTheFleet(t *testing.T) {
	d, _, _ := aWorld(t)
	view := readView[api.Operations](t, d, api.ViewOperations, "")
	if view.Workers != 1 || view.WorkOrders[workorders.Running] == 0 || view.Lanes["working"] == 0 {
		t.Errorf("view %+v", view)
	}
	if view.Waiting != int64(len(readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items)) {
		t.Errorf("waiting %d", view.Waiting)
	}
}
