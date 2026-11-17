package lanes

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-SPEND-1: the change, rework, and adopt briefs say a lane never waits for CI, a review, or a merge, and
// that it reports instead, because ropeshift waits on the checks with no session.
func TestEveryLaneBriefSaysALaneNeverWaitsForCI(t *testing.T) {
	tests := map[string]BriefSpec{
		"a change":  {WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange},
		"a rework":  {WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange, Rework: &ledger.DesiredRework{Reason: "the review failed it"}},
		"an adopt":  {WorkOrder: "wo", Title: "Adopt it", Kind: workorders.KindPlan, Adopt: true},
		"a plan":    {WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan},
		"a handoff": {WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange, Handoff: &Handoff{Generation: 1, Reason: "context"}},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			brief := Brief(spec)
			for _, want := range []string{"Never wait for CI, a review, or a merge", ToolLaneReport, "Do not sleep, poll, or watch"} {
				if !strings.Contains(brief, want) {
					t.Errorf("the brief leaves out %q", want)
				}
			}
			if strings.Contains(brief, "a CI run") {
				t.Error("the brief offers a CI run as a wait")
			}
		})
	}
}
