package planning_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func planEntries() []planning.Entry {
	return []planning.Entry{
		{Ref: planning.PlanRef("retry"), Digest: "gitblob:p1"},
		{Ref: "requirements:retry-reqs", Digest: "gitblob:r1", Record: "requirements:retry-reqs"},
		{Ref: "milestone:m1", Digest: "gitblob:m1", Record: "milestone:m1"},
		{Ref: planning.ContractRef("budget"), Digest: "gitblob:c1", Record: "ctr-1"},
	}
}

func TestAPlanDigestDoesNotDependOnTheOrderRecordsAreListed(t *testing.T) {
	forward := planEntries()
	backward := slices.Clone(forward)
	slices.Reverse(backward)
	if planning.PlanDigest(forward) != planning.PlanDigest(backward) {
		t.Fatal("the digest depends on the order of the records")
	}
	if !strings.HasPrefix(planning.PlanDigest(forward), planning.DigestSHA256) {
		t.Fatalf("the digest %q does not say what kind it is", planning.PlanDigest(forward))
	}
}

func TestAPlanDigestChangesWithAnyRecordOfThePlan(t *testing.T) {
	base := planning.PlanDigest(planEntries())
	tests := []struct {
		name   string
		change func([]planning.Entry) []planning.Entry
	}{
		{"the plan file changed", func(e []planning.Entry) []planning.Entry { e[0].Digest = "gitblob:p2"; return e }},
		{"a document changed", func(e []planning.Entry) []planning.Entry { e[1].Digest = "gitblob:r2"; return e }},
		{"a milestone changed", func(e []planning.Entry) []planning.Entry { e[2].Digest = "gitblob:m2"; return e }},
		{"a contract changed", func(e []planning.Entry) []planning.Entry { e[3].Digest = "gitblob:c2"; return e }},
		{"a contract was added", func(e []planning.Entry) []planning.Entry {
			return append(e, planning.Entry{Ref: planning.ContractRef("backoff"), Digest: "gitblob:c3"})
		}},
		{"a document was removed", func(e []planning.Entry) []planning.Entry { return slices.Delete(e, 1, 2) }},
		{"a record was renamed", func(e []planning.Entry) []planning.Entry { e[3].Ref = planning.ContractRef("other"); return e }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if planning.PlanDigest(tt.change(planEntries())) == base {
				t.Fatal("the digest did not move")
			}
		})
	}
}

// The ledger's own ID for a record and a contract's parts say where a record lives and what it holds, and
// neither is a fact about its content, so a plan that moved between ledgers keeps its digest.
func TestAPlanDigestCoversTheRefAndDigestAndNothingElse(t *testing.T) {
	plain := planEntries()
	annotated := planEntries()
	annotated[3].Record = "a-different-id"
	annotated[3].Parts = []planning.Part{planning.CriterionPart(planning.Criterion{ID: "c1", Text: "It retries."})}
	if planning.PlanDigest(plain) != planning.PlanDigest(annotated) {
		t.Fatal("the digest read more than the ref and the digest of each record")
	}
}

// Two lists can only join into the same bytes if the boundary between a ref and a digest is part of
// what is hashed.
func TestAPlanDigestKeepsEachRefAndDigestApart(t *testing.T) {
	a := []planning.Entry{{Ref: "spec:ab", Digest: "c"}}
	b := []planning.Entry{{Ref: "spec:a", Digest: "bc"}}
	if planning.PlanDigest(a) == planning.PlanDigest(b) {
		t.Fatal("two different records have one digest")
	}
}

func TestComparePlansListsRecordsAddedChangedAndRemoved(t *testing.T) {
	approved := planEntries()
	next := []planning.Entry{
		{Ref: planning.PlanRef("retry"), Digest: "gitblob:p2"},
		{Ref: "requirements:retry-reqs", Digest: "gitblob:r1"},
		{Ref: planning.ContractRef("budget"), Digest: "gitblob:c1"},
		{Ref: planning.ContractRef("backoff"), Digest: "gitblob:c9"},
	}
	got := planning.Compare(3, approved, next)
	if got.Base != 3 {
		t.Errorf("base %d, want 3", got.Base)
	}
	if want := []string{"contract:backoff"}; !slices.Equal(got.Added, want) {
		t.Errorf("added %v, want %v", got.Added, want)
	}
	if want := []string{"plan:retry"}; !slices.Equal(got.Changed, want) {
		t.Errorf("changed %v, want %v", got.Changed, want)
	}
	if want := []string{"milestone:m1"}; !slices.Equal(got.Removed, want) {
		t.Errorf("removed %v, want %v", got.Removed, want)
	}
}

func TestComparePlansAgainstNothingApprovedAddsEverything(t *testing.T) {
	got := planning.Compare(0, nil, planEntries())
	if got.Base != 0 || len(got.Changed) != 0 || len(got.Removed) != 0 {
		t.Fatalf("a first version differs from nothing only by what it adds: %+v", got)
	}
	if len(got.Added) != len(planEntries()) {
		t.Fatalf("added %v", got.Added)
	}
}

func TestComparePlansNamesTheCriteriaAndWorkOrdersAContractChanged(t *testing.T) {
	was := planning.Entry{Ref: planning.ContractRef("budget"), Digest: "gitblob:c1", Parts: []planning.Part{
		planning.CriterionPart(planning.Criterion{ID: "c1", Text: "It retries."}),
		planning.CriterionPart(planning.Criterion{ID: "c2", Text: "It stops."}),
		planning.CriterionPart(planning.Criterion{ID: "c3", Text: "It logs."}),
		planning.WorkOrderPart(planning.WorkOrderSpec{ID: "w1", Title: "Add the budget", Size: planning.SizeSmall}),
		planning.WorkOrderPart(planning.WorkOrderSpec{ID: "w2", Title: "Wire it", Size: planning.SizeSmall}),
	}}
	now := planning.Entry{Ref: planning.ContractRef("budget"), Digest: "gitblob:c2", Parts: []planning.Part{
		planning.CriterionPart(planning.Criterion{ID: "c1", Text: "It retries three times."}),
		planning.CriterionPart(planning.Criterion{ID: "c2", Text: "It stops."}),
		planning.CriterionPart(planning.Criterion{ID: "c4", Text: "It reports."}),
		planning.WorkOrderPart(planning.WorkOrderSpec{ID: "w1", Title: "Add the budget", Size: planning.SizeSmall}),
		planning.WorkOrderPart(planning.WorkOrderSpec{ID: "w3", Title: "Document it", Size: planning.SizeSmall}),
	}}
	got := planning.Compare(1, []planning.Entry{was}, []planning.Entry{now})
	if want := []string{"contract:budget"}; !slices.Equal(got.Changed, want) || len(got.Added)+len(got.Removed) != 0 {
		t.Fatalf("difference %+v", got)
	}
	if len(got.Contracts) != 1 {
		t.Fatalf("contracts %+v", got.Contracts)
	}
	c := got.Contracts[0]
	if c.Ref != "contract:budget" {
		t.Errorf("ref %q", c.Ref)
	}
	if want := []string{"c4"}; !slices.Equal(c.Criteria.Added, want) {
		t.Errorf("criteria added %v, want %v", c.Criteria.Added, want)
	}
	if want := []string{"c1"}; !slices.Equal(c.Criteria.Changed, want) {
		t.Errorf("criteria changed %v, want %v", c.Criteria.Changed, want)
	}
	if want := []string{"c3"}; !slices.Equal(c.Criteria.Removed, want) {
		t.Errorf("criteria removed %v, want %v", c.Criteria.Removed, want)
	}
	if want := []string{"w3"}; !slices.Equal(c.WorkOrders.Added, want) {
		t.Errorf("work orders added %v, want %v", c.WorkOrders.Added, want)
	}
	if want := []string{"w2"}; !slices.Equal(c.WorkOrders.Removed, want) {
		t.Errorf("work orders removed %v, want %v", c.WorkOrders.Removed, want)
	}
	if len(c.WorkOrders.Changed) != 0 {
		t.Errorf("work orders changed %v", c.WorkOrders.Changed)
	}
}

func TestComparePlansLeavesAnUnchangedContractOut(t *testing.T) {
	entries := planEntries()
	got := planning.Compare(2, entries, entries)
	if !got.Empty() || len(got.Contracts) != 0 {
		t.Fatalf("a version that changes nothing differs by %+v", got)
	}
}

func TestACoveredRecordIsOneWhoseLedgerIDAndDigestAnEntryHolds(t *testing.T) {
	entries := planEntries()
	if !planning.Covers(entries, "ctr-1", "gitblob:c1") {
		t.Error("the contract the plan lists is not covered")
	}
	if planning.Covers(entries, "ctr-1", "gitblob:c2") {
		t.Error("another digest of that contract is covered")
	}
	if planning.Covers(entries, "ctr-2", "gitblob:c1") {
		t.Error("another contract with that digest is covered")
	}
}

func TestEntriesAreCountedByTheKindOfRecord(t *testing.T) {
	got := planning.CountEntries(planEntries())
	want := map[string]int{"contract": 1, "milestone": 1, "requirements": 1}
	if len(got) != len(want) {
		t.Fatalf("counts %v, want %v", got, want)
	}
	for kind, n := range want {
		if got[kind] != n {
			t.Errorf("%s: %d, want %d", kind, got[kind], n)
		}
	}
}

func TestAWorkOrdersBaseIsPartOfWhatAPersonSeesOfIt(t *testing.T) {
	plain := planning.WorkOrderSpec{ID: "w1", Title: "Add the budget", Size: planning.SizeSmall}
	based := plain
	based.Base = "release/1.2"
	if planning.WorkOrderPart(plain).Digest == planning.WorkOrderPart(based).Digest {
		t.Error("a work order that moved to another base kept its digest")
	}
}

// A work order that names no base keeps the digest it had before the format could name one, so no
// plan reads as changed because ropeshift learned a new key.
func TestAWorkOrderWithNoBaseKeepsItsDigest(t *testing.T) {
	spec := planning.WorkOrderSpec{ID: "w1", Title: "Add the budget", Priority: 2, Size: planning.SizeSmall, Needs: map[string]string{"driver": "claude-code"}}
	want := planning.ContentDigest("work_order", "w1", "Add the budget", "2", planning.SizeSmall, "driver=claude-code")
	if got := planning.WorkOrderPart(spec).Digest; got != want {
		t.Errorf("digest %s, not %s", got, want)
	}
}
