package server_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const tracedPlan = `+++
kind = "plan"
id = "traced"
title = "Traced"
+++

How.
`

const tracedRequirements = `+++
kind = "requirements"
id = "traced"
title = "What it must do"
plan = "traced"

[[requirement]]
id = "R-1"
text = "It retries."

[[requirement]]
id = "R-2"
text = "It stops."

[[requirement]]
id = "R-3"
text = "It reports."
+++

Why.
`

const tracedContract = `+++
kind = "contract"
id = "budget"
title = "The budget"
plan = "traced"

[[criteria]]
id = "c1"
text = "It retries twice"
requirements = ["R-1"]

[[criteria]]
id = "c2"
text = "It stops at the budget"
requirements = ["R-1", "R-2"]

[[work_orders]]
id = "budget-order"
title = "Add the budget"
+++

The budget.
`

// documentsWorld serves a knowledge source beside a work source intake reads, so the documents view holds
// a live file that is also a ledger record, and one that is not.
func documentsWorld(t *testing.T) (*apitest.Daemon, *apitest.World, *fake.KnowledgeSource) {
	t.Helper()
	clock := apitest.NewClock()
	knowledge := fake.NewKnowledgeSource()
	knowledge.Write("spec.md", "# The API\n")
	knowledge.Write("design.md", "# The retry budget\n")
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect, Knowledge: []ports.KnowledgeSource{knowledge}})
	return d, apitest.NewWorld(t, d), knowledge
}

func planDocuments(t *testing.T, d *apitest.Daemon, title string) api.PlanDocuments {
	t.Helper()
	for _, plan := range readView[api.Documents](t, d, api.ViewDocuments, "").Plans {
		if plan.Plan.Title == title {
			return plan
		}
	}
	t.Fatalf("the documents view holds no plan titled %s", title)
	return api.PlanDocuments{}
}

func recorded(t *testing.T, plan api.PlanDocuments, id string) api.RecordedDocument {
	t.Helper()
	for _, document := range plan.Documents {
		if document.ID == id {
			return document
		}
	}
	t.Fatalf("%s holds no document %s: %+v", plan.Plan.Title, id, plan.Documents)
	return api.RecordedDocument{}
}

func TestTheDocumentsViewListsAPlansRecordedDocumentsWithEveryVersion(t *testing.T) {
	d, w, _ := documentsWorld(t)
	source := retrySource("The first draft.")
	w.Planned(source)
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	spec := recorded(t, planDocuments(t, d, "Retry budget"), "spec:api")
	if spec.Kind != "spec" || spec.Title != "The API" || spec.Path != "spec.md" || len(spec.Versions) != 2 {
		t.Fatalf("spec %+v", spec)
	}
	newest, first := spec.Versions[0], spec.Versions[1]
	if newest.Version != 2 || first.Version != 1 || newest.Digest == first.Digest || newest.Digest == "" {
		t.Errorf("versions %+v", spec.Versions)
	}
	if first.Source != "spec.md@3" || newest.Source == first.Source || first.RecordedAt == 0 || newest.RecordedAt < first.RecordedAt {
		t.Errorf("where and when %+v", spec.Versions)
	}
}

func TestTheDocumentsViewSaysWhereEachVersionStandsInReview(t *testing.T) {
	d, w, _ := documentsWorld(t)
	source := retrySource("The first draft.")
	w.Planned(source)
	spec := recorded(t, planDocuments(t, d, "Retry budget"), "spec:api")
	if got := spec.Versions[0]; got.Review != api.ReviewPending || got.PlanVersion != 1 {
		t.Errorf("a version an open approval lists reads %+v", got)
	}
	w.ApprovePlan("Retry budget")
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	spec = recorded(t, planDocuments(t, d, "Retry budget"), "spec:api")
	if got := spec.Versions[0]; got.Review != api.ReviewPending || got.PlanVersion != 2 {
		t.Errorf("the new version reads %+v", got)
	}
	if got := spec.Versions[1]; got.Review != api.ReviewApproved || got.PlanVersion != 1 {
		t.Errorf("the approved version reads %+v", got)
	}

	// A denied request covers nothing, so the version it listed stands in no review.
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: false, Reason: "Not yet"}); err != nil {
		t.Fatal(err)
	}
	spec = recorded(t, planDocuments(t, d, "Retry budget"), "spec:api")
	if got := spec.Versions[0]; got.Review != api.ReviewNone || got.PlanVersion != 0 {
		t.Errorf("a version only a denied request listed reads %+v", got)
	}
}

func TestTheDocumentsViewTracesEachRequirementToItsCriteriaAndWork(t *testing.T) {
	d, w, _ := documentsWorld(t)
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(tracedPlan))
	source.Write("requirements.md", []byte(tracedRequirements))
	source.Write("budget.md", []byte(tracedContract))
	w.Planned(source)
	plan := planDocuments(t, d, "Traced")
	var keys []string
	for _, trace := range plan.Requirements {
		keys = append(keys, trace.Key)
		if trace.Document != "requirements:traced" || trace.Text == "" || trace.Criteria == nil {
			t.Errorf("trace %+v", trace)
		}
	}
	if !slices.Equal(keys, []string{"R-1", "R-2", "R-3"}) {
		t.Fatalf("requirements %v", keys)
	}
	criteriaOf := func(trace api.RequirementTrace) []string {
		var out []string
		for _, criterion := range trace.Criteria {
			out = append(out, criterion.Criterion)
			if criterion.ContractTitle != "The budget" || criterion.ContractVersion != 1 || criterion.Text == "" || len(criterion.WorkOrders) != 1 {
				t.Errorf("criterion %+v", criterion)
				continue
			}
			if order := criterion.WorkOrders[0]; order.Title != "Add the budget" || order.State != workorders.ContractReview || order.ID == "" {
				t.Errorf("work %+v", order)
			}
		}
		return out
	}
	if got := criteriaOf(plan.Requirements[0]); !slices.Equal(got, []string{"c1", "c2"}) {
		t.Errorf("R-1 is served by %v", got)
	}
	if got := criteriaOf(plan.Requirements[1]); !slices.Equal(got, []string{"c2"}) {
		t.Errorf("R-2 is served by %v", got)
	}
	if got := plan.Requirements[2].Criteria; len(got) != 0 {
		t.Errorf("R-3 is served by %+v", got)
	}
}

// A live file a plan recorded names its record, so a page lists it once, from the ledger, and keeps the
// live listing for every file that is not one.
func TestTheDocumentsViewMarksALiveFileAPlanRecorded(t *testing.T) {
	d, w, _ := documentsWorld(t)
	w.Planned(retrySource("The first draft."))
	marks := map[string]string{}
	for _, document := range readView[api.Documents](t, d, api.ViewDocuments, "").Documents {
		marks[document.Path] = document.Record
	}
	if marks["spec.md"] != "spec:api" || marks["design.md"] != "" {
		t.Errorf("marks %v", marks)
	}
}

// A live file the ledger holds as a goal, a plan, or a contract has a page of its own, so it names that
// record too, and a page lists it there and not among the repository's other files.
func TestTheDocumentsViewMarksALiveFileTheLedgerHoldsAsAGoalPlanOrContract(t *testing.T) {
	d, w, knowledge := documentsWorld(t)
	goal := "+++\nkind = \"goal\"\nid = \"reliable\"\ntitle = \"A reliable API\"\n+++\n\nWhy.\n"
	source := retrySource("The first draft.")
	source.Write("goal.md", []byte(goal))
	knowledge.Write("goal.md", goal)
	knowledge.Write("plan.md", retryPlan)
	knowledge.Write("budget.md", "+++\nkind = \"contract\"\n+++\n")
	knowledge.Write("README.md", "# The service\n")
	w.Planned(source)
	marks := map[string]string{}
	for _, document := range readView[api.Documents](t, d, api.ViewDocuments, "").Documents {
		marks[document.Path] = document.Record
	}
	for path, kind := range map[string]string{"goal.md": "goal:", "plan.md": "plan:", "budget.md": "contract:"} {
		if !strings.HasPrefix(marks[path], kind) {
			t.Errorf("%s is marked %q, not as a %s record", path, marks[path], kind)
		}
	}
	if marks["README.md"] != "" || marks["design.md"] != "" {
		t.Errorf("a file the ledger does not hold is marked: %v", marks)
	}
}

func TestARecordedVersionKeepsItsBodyAfterTheFileMovesOn(t *testing.T) {
	d, w, _ := documentsWorld(t)
	source := retrySource("The first draft.")
	w.Planned(source)
	source.Write("spec.md", []byte("+++\nkind = \"spec\"\nid = \"api\"\ntitle = \"The API\"\nplan = \"retry\"\n+++\n\nThe second draft.\n"))
	w.Planned(source)
	first, err := d.Client.RecordedDocument(t.Context(), "spec:api", 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Body != "The first draft.\n" || first.Version.Version != 1 || first.Document.ID != "spec:api" || len(first.Document.Versions) != 2 ||
		first.Plan == nil || first.Plan.Title != "Retry budget" || first.AsOf == 0 {
		t.Errorf("version 1 reads %+v", first)
	}
	newest, err := d.Client.RecordedDocument(t.Context(), "spec:api", 0)
	if err != nil {
		t.Fatal(err)
	}
	if newest.Body != "The second draft.\n" || newest.Version.Version != 2 || newest.Version.Review != api.ReviewPending {
		t.Errorf("the newest version reads %+v", newest)
	}
	for _, missing := range []struct {
		id      string
		version int64
	}{{"spec:absent", 0}, {"spec:api", 9}} {
		var refused *api.Error
		if _, err := d.Client.RecordedDocument(t.Context(), missing.id, missing.version); !errors.As(err, &refused) || refused.Detail.Code != api.CodeNotFound {
			t.Errorf("%s at %d: %v", missing.id, missing.version, err)
		}
	}
}
