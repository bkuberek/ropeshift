package control

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/workorders"
)

// TestOnlyPlanningWorkSeesDeliveryHistory holds FR-EST-12 for every kind of work order: change, rework, review, and
// contract review lanes read no history, so a band cannot anchor how a lane works or a reviewer judges.
func TestOnlyPlanningWorkSeesDeliveryHistory(t *testing.T) {
	for _, c := range []struct {
		kind workorders.Kind
		sees bool
	}{
		{workorders.KindPlan, true},
		{workorders.KindChange, false},
		{workorders.KindRework, false},
		{workorders.KindReview, false},
		{workorders.KindContractReview, false},
		{"", false},
	} {
		if got := seesDeliveryHistory(c.kind); got != c.sees {
			t.Errorf("work of kind %q sees history: %v", c.kind, got)
		}
	}
}

// TestAnOrchestratorReadsNoHistoryByName holds the same for a reader with no work order, which is what an
// orchestrator's read is: a read of the history by name is refused as a kind nobody named, and touches no record.
func TestAnOrchestratorReadsNoHistoryByName(t *testing.T) {
	found, err := packRecords{}.history(historyRef, "")
	if !errors.Is(err, workorders.ErrInvalid) || len(found) != 0 {
		t.Errorf("an orchestrator read %+v, %v", found, err)
	}
}
