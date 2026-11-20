package lanes

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Every lane brief tells the lane to wait for its helpers' reports before it reports, so a lane does not end its
// turn while its own agents still run in the background.
func TestEveryLaneBriefWaitsForItsHelpers(t *testing.T) {
	tests := map[string]BriefSpec{
		"a change":  {WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange},
		"a rework":  {WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange, Rework: &ledger.DesiredRework{Reason: "the review failed it"}},
		"a plan":    {WorkOrder: "wo", Title: "Plan it", Kind: workorders.KindPlan},
		"a handoff": {WorkOrder: "wo", Title: "Change it", Kind: workorders.KindChange, Handoff: &Handoff{Generation: 1, Reason: "context"}},
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			brief := Brief(spec)
			for _, want := range []string{"in the foreground", "wait for its report before you call " + ToolLaneReport} {
				if !strings.Contains(brief, want) {
					t.Errorf("the brief leaves out %q", want)
				}
			}
		})
	}
}
