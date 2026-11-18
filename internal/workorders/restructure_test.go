package workorders

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
)

func aRestructuring() *planning.Restructure {
	return &planning.Restructure{Shape: planning.ShapeWorkspaceFiles, Digest: planning.DigestSHA256 + strings.Repeat("a", 64)}
}

// Only adopting work restructures, and what it carries must be a restructuring that can be kept.
func TestOnlyAdoptingWorkRestructures(t *testing.T) {
	adopting := WorkOrder{ID: "adopt-1", Kind: KindPlan, Adopt: true, Restructure: aRestructuring()}
	kept, err := New(adopting)
	if err != nil || kept.Restructure == nil || kept.Restructure.Shape != planning.ShapeWorkspaceFiles {
		t.Fatalf("adopting work with a restructuring: %+v, %v", kept, err)
	}
	single, err := New(WorkOrder{ID: "adopt-4", Kind: KindPlan, Adopt: true, Restructure: &planning.Restructure{Shape: planning.ShapeSingle, Digest: aRestructuring().Digest}})
	if err != nil || single.Restructure == nil || single.Restructure.Shape != planning.ShapeSingle {
		t.Errorf("adopting work that restructures a single repository: %+v, %v", single, err)
	}
	for name, draft := range map[string]WorkOrder{
		"planning work that does not adopt": {ID: "plan-1", Kind: KindPlan, Restructure: aRestructuring()},
		"a change":                          {ID: "change-1", Kind: KindChange, Restructure: aRestructuring()},
		"a shape no layout has":             {ID: "adopt-2", Kind: KindPlan, Adopt: true, Restructure: &planning.Restructure{Shape: "other", Digest: aRestructuring().Digest}},
		"no digest":                         {ID: "adopt-3", Kind: KindPlan, Adopt: true, Restructure: &planning.Restructure{Shape: planning.ShapePlanned}},
	} {
		if _, err := New(draft); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s was recorded: %v", name, err)
		}
	}
}
