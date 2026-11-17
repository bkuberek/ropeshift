package repofiles

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func decisionText(front string) []byte {
	return []byte("+++\nkind = \"decision\"\nid = \"adr-0007\"\ntitle = \"Retry budget\"\n" + front + "+++\n\nThe retry budget bounds every call.\n")
}

// A decision record carries a status, and a record that states none is proposed.
func TestParseDecisionStatus(t *testing.T) {
	cases := []struct {
		name    string
		front   string
		want    planning.DecisionStatus
		wantErr error
	}{
		{"proposed", "status = \"proposed\"\n", planning.DecisionProposed, nil},
		{"accepted", "status = \"accepted\"\n", planning.DecisionAccepted, nil},
		{"superseded", "status = \"superseded\"\n", planning.DecisionSuperseded, nil},
		{"rejected", "status = \"rejected\"\n", planning.DecisionRejected, nil},
		{"absent", "", planning.DecisionProposed, nil},
		{"empty", "status = \"\"\n", planning.DecisionProposed, nil},
		{"unknown", "status = \"approved\"\n", "", planning.ErrBadFile},
		{"wrong case", "status = \"Accepted\"\n", "", planning.ErrBadFile},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := ParseFile(decisionText(c.front))
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("error %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if doc.Kind != planning.FileDecision || doc.Decision == nil {
				t.Fatalf("got %+v", doc)
			}
			if doc.Decision.Status != c.want || doc.Decision.ID != "adr-0007" || doc.Decision.Title != "Retry budget" {
				t.Errorf("decision %+v, want status %q", doc.Decision, c.want)
			}
			if doc.Statement != "The retry budget bounds every call.\n" {
				t.Errorf("statement %q", doc.Statement)
			}
			head, ok := doc.Head()
			if !ok || head.Ref() != "decision:adr-0007" || head.Plan != "" {
				t.Fatalf("head %+v, %v", head, ok)
			}
		})
	}
}

// A decision record may name a plan, and it addresses requirements as a spec does: a repeated one is
// dropped, and an empty one is a bad file.
func TestParseDecisionPlanAndRequirements(t *testing.T) {
	doc, err := ParseFile(decisionText("plan = \"payments-v2\"\nrequirements = [\"PAY-13\", \"PAY-13\", \"PAY-12\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Decision.Plan != "payments-v2" || !slices.Equal(doc.Decision.Requirements, []string{"PAY-13", "PAY-12"}) {
		t.Errorf("decision %+v", doc.Decision)
	}
	if _, err := ParseFile(decisionText("requirements = [\"PAY-13\", \"\"]\n")); !errors.Is(err, planning.ErrBadFile) {
		t.Errorf("an empty requirement: %v", err)
	}
	if _, err := ParseFile([]byte("+++\nkind = \"decision\"\nid = \"adr-0007\"\n+++\n\nBody.\n")); !errors.Is(err, planning.ErrBadFile) {
		t.Errorf("a decision with no title: %v", err)
	}
	if _, err := ParseFile([]byte("+++\nkind = \"decision\"\ntitle = \"T\"\n+++\n\nBody.\n")); !errors.Is(err, planning.ErrBadFile) {
		t.Errorf("a decision with no id: %v", err)
	}
}
