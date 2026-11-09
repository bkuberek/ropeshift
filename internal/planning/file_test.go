package planning_test

import (
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func TestParseDocumentRef(t *testing.T) {
	cases := []struct {
		ref  string
		kind planning.FileKind
		id   string
		ok   bool
	}{
		{"spec:checkout-api", planning.FileSpec, "checkout-api", true},
		{"architecture:payments", planning.FileArchitecture, "payments", true},
		{"experience-design:checkout-flow", planning.FileExperienceDesign, "checkout-flow", true},
		{"requirements:payments", planning.FileRequirements, "payments", true},
		{"milestone:m1", planning.FileMilestone, "m1", true},
		// An ID may hold a colon, and only the first one separates the kind.
		{"spec:a:b", planning.FileSpec, "a:b", true},
		{"checkout-api", "", "", false},
		{"spec:", "", "", false},
		{":checkout-api", "", "", false},
		// A contract, a plan, and a goal are records and not documents.
		{"contract:checkout-retry", "", "", false},
		{"plan:payments-v2", "", "", false},
		{"goal:reliable", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.ref, func(t *testing.T) {
			kind, id, ok := planning.ParseDocumentRef(c.ref)
			if kind != c.kind || id != c.id || ok != c.ok {
				t.Errorf("%q reads as %q, %q, %v; want %q, %q, %v", c.ref, kind, id, ok, c.kind, c.id, c.ok)
			}
		})
	}
}

func TestEveryDocumentKindHasAHead(t *testing.T) {
	cases := map[planning.FileKind]planning.Document{
		planning.FileRequirements:     {Requirements: &planning.RequirementsFile{ID: "x", Plan: "p"}},
		planning.FileSpec:             {Spec: &planning.SpecFile{ID: "x", Title: "T", Plan: "p"}},
		planning.FileArchitecture:     {Architecture: &planning.ArchitectureFile{ID: "x", Title: "T", Plan: "p"}},
		planning.FileExperienceDesign: {Experience: &planning.ExperienceDesignFile{ID: "x", Title: "T", Plan: "p"}},
		planning.FileMilestone:        {Milestone: &planning.MilestoneFile{ID: "x", Title: "T", Plan: "p", Priority: 3}},
	}
	for kind, doc := range cases {
		t.Run(string(kind), func(t *testing.T) {
			head, ok := doc.Head()
			if !ok || head.Kind != kind || head.ID != "x" || head.Plan != "p" || head.Ref() != string(kind)+":x" {
				t.Fatalf("head %+v, %v", head, ok)
			}
			if !planning.IsDocumentKind(kind) {
				t.Errorf("%s is no document kind", kind)
			}
			var fields map[string]any
			if err := json.Unmarshal(head.Fields, &fields); err != nil || fields["id"] != "x" {
				t.Errorf("fields %s: %v", head.Fields, err)
			}
		})
	}
	for _, record := range []planning.Document{
		{Goal: &planning.GoalFile{ID: "g"}}, {Plan: &planning.PlanFile{ID: "p"}}, {Contract: &planning.ContractFile{ID: "c"}},
	} {
		if head, ok := record.Head(); ok {
			t.Errorf("a record reads as a document: %+v", head)
		}
	}
	if (planning.DocumentHead{ID: "x"}).Name() != "x" || (planning.DocumentHead{ID: "x", Title: "T"}).Name() != "T" {
		t.Error("a document with no title is called by its ID")
	}
}
