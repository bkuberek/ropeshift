package planning_test

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

// ADR-0013, ADR-0023: a contract the code already delivers has no work orders.
func TestAnExistingContractHasNoWorkOrders(t *testing.T) {
	delivered := planning.ContractFile{ID: "client", Title: "Payments client", Existing: true, Shared: true,
		Criteria: []planning.Criterion{{ID: "c1", Text: "One client", Requirements: []string{"PAY-1"}}}}
	if got, err := delivered.Normalize(); err != nil || !got.Existing {
		t.Fatalf("an existing contract with no work orders: %+v, %v", got, err)
	}
	delivered.WorkOrders = []planning.WorkOrderSpec{{ID: "w1", Title: "Build it"}}
	if _, err := delivered.Normalize(); !errors.Is(err, planning.ErrBadFile) {
		t.Errorf("an existing contract with a work order: %v", err)
	}
}

// ADR-0013: a requirement is open unless the code already meets it, and a met one cites its evidence as
// <repository>:<path>.
func TestAMetRequirementCitesItsEvidence(t *testing.T) {
	file := func(specs ...planning.RequirementSpec) planning.RequirementsFile {
		return planning.RequirementsFile{ID: "payments", Requirements: specs}
	}
	for name, spec := range map[string]planning.RequirementSpec{
		"open by default":                  {ID: "PAY-1", Text: "It retries"},
		"open":                             {ID: "PAY-1", Text: "It retries", Status: planning.RequirementOpen},
		"met with evidence":                {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{"api:internal/retry.go", "api:internal/retry_test.go"}},
		"met in this repository":           {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{".:retry.go"}},
		"met with a path that has a colon": {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{"api:docs/a:b.md"}},
	} {
		if _, err := file(spec).Normalize(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, spec := range map[string]planning.RequirementSpec{
		"met with no evidence":            {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet},
		"a status nobody offers":          {ID: "PAY-1", Text: "It retries", Status: "done"},
		"evidence on an open one":         {ID: "PAY-1", Text: "It retries", Evidence: []string{"api:retry.go"}},
		"evidence with no repository":     {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{"retry.go"}},
		"evidence outside the repository": {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{"api:../secret"}},
		"evidence that is empty":          {ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{""}},
	} {
		if _, err := file(spec).Normalize(); !errors.Is(err, planning.ErrBadFile) {
			t.Errorf("%s: %v", name, err)
		}
	}
	met := planning.RequirementSpec{ID: "PAY-1", Text: "It retries", Status: planning.RequirementMet, Evidence: []string{"api:retry.go"}}
	if !met.Met() || (planning.RequirementSpec{ID: "PAY-1"}).Met() {
		t.Error("Met reads the status")
	}
}

// A requirement that is open keeps the digest it had before the format could say met, and saying met, or
// changing the evidence, is a new version.
func TestARequirementsDigestCoversItsStatusAndEvidence(t *testing.T) {
	open := planning.RequirementSpec{ID: "PAY-1", Text: "It retries"}
	if open.Digest() != planning.RequirementDigest("PAY-1", "It retries") {
		t.Error("an open requirement changed its digest")
	}
	explicit := open
	explicit.Status = planning.RequirementOpen
	if explicit.Digest() != open.Digest() {
		t.Error("open written out is a new version")
	}
	met := open
	met.Status, met.Evidence = planning.RequirementMet, []string{"api:retry.go"}
	other := met
	other.Evidence = []string{"api:retry_test.go"}
	if met.Digest() == open.Digest() || met.Digest() == other.Digest() {
		t.Error("met, and its evidence, are not part of the digest")
	}
}

// ADR-0013: a work order that continues a branch names a branch git would take, and not its own base.
func TestAWorkOrderContinuesABranch(t *testing.T) {
	contract := func(order planning.WorkOrderSpec) planning.ContractFile {
		order.ID, order.Title = "w1", "Finish it"
		return planning.ContractFile{ID: "c", Title: "C", WorkOrders: []planning.WorkOrderSpec{order}}
	}
	if got, err := contract(planning.WorkOrderSpec{Continues: "feature/retry"}).Normalize(); err != nil || got.WorkOrders[0].Continues != "feature/retry" {
		t.Fatalf("continuing a branch: %+v, %v", got, err)
	}
	if _, err := contract(planning.WorkOrderSpec{Continues: "feature/retry", Base: "release/1.2"}).Normalize(); err != nil {
		t.Errorf("continuing a branch onto another base: %v", err)
	}
	for name, order := range map[string]planning.WorkOrderSpec{
		"an option":    {Continues: "--upload-pack=evil"},
		"its own base": {Continues: "release/1.2", Base: "release/1.2"},
	} {
		if _, err := contract(order).Normalize(); !errors.Is(err, planning.ErrBadFile) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAWorkOrderThatContinuesABranchSaysSoInItsDigest(t *testing.T) {
	order := planning.WorkOrderSpec{ID: "w1", Title: "Finish it", Priority: 1, Size: "small"}
	before := planning.WorkOrderPart(order)
	order.Continues = "feature/retry"
	if planning.WorkOrderPart(order).Digest == before.Digest {
		t.Error("continuing a branch is not part of what a person sees of the work")
	}
}

// ADR-0013: a plan records what an adoption left out, each with its reason.
func TestAPlanRecordsWhatItLeftOut(t *testing.T) {
	plan := planning.PlanFile{ID: "p", Title: "P", LeftOut: []planning.LeftOut{
		{Ref: "branch:spike/old-ui", Reason: "An abandoned experiment"},
		{Ref: "branch:wip/docs", Reason: "The person will finish it by hand"},
	}}
	if got, err := plan.Normalize(); err != nil || len(got.LeftOut) != 2 {
		t.Fatalf("a plan with what it left out: %+v, %v", got, err)
	}
	for name, left := range map[string][]planning.LeftOut{
		"no reason": {{Ref: "branch:spike/old-ui"}},
		"no ref":    {{Reason: "Old"}},
		"twice":     {{Ref: "branch:a", Reason: "Old"}, {Ref: "branch:a", Reason: "Older"}},
	} {
		plan.LeftOut = left
		if _, err := plan.Normalize(); !errors.Is(err, planning.ErrBadFile) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// ADR-0023: an existing contract or milestone resolves every dependency on it at once, so nothing waits
// for work it holds.
func TestShapeExpandLeavesOutWhatAlreadyExists(t *testing.T) {
	shape := planning.Shape{
		Milestones: []planning.ShapeMilestone{
			{ID: "m0", Existing: true},
			{ID: "m1", DependsOn: []string{"m0"}},
		},
		Contracts: []planning.ShapeContract{
			// A contract written before the code was found to deliver it keeps the work orders the ledger made.
			{ID: "client", Milestone: "m1", Existing: true, Orders: []planning.ShapeOrder{{ID: "client-old"}}},
			{ID: "base", Milestone: "m0", Orders: []planning.ShapeOrder{{ID: "base-old"}}},
			{ID: "retry", Milestone: "m1", Uses: []string{"client"}, Orders: []planning.ShapeOrder{{ID: "retry-1"}}},
			{ID: "audit", Milestone: "m1", Uses: []string{"retry"}, Orders: []planning.ShapeOrder{{ID: "audit-1"}}},
		},
	}
	edges := shape.Expand()
	want := []planning.ShapeEdge{{Order: "audit-1", DependsOn: "retry-1", Kind: planning.DependencySharedContract}}
	if !slices.Equal(edges, want) {
		t.Errorf("edges %+v, want %+v", edges, want)
	}
}
