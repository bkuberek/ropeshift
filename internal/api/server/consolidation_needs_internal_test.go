package server

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A merged consolidation whose base lacks the design tip is work that waits for a member in Needs you, under its
// own reason, naming the branch and the missing commit, with no action: it clears when a check finds the tip.
func TestAHistoryGapIsWorkThatNamesTheBranchAndTheMissingCommit(t *testing.T) {
	gap := control.HistoryGap{
		WorkOrder: workorders.WorkOrder{ID: "adopt-1", Kind: workorders.KindPlan, Title: "Adopt shop", State: workorders.Done, Adopt: true},
		Check:     ledger.ConsolidationCheck{Branch: "lane/adopt", Base: "main", BaseCommit: "base-squashed", DesignTip: "design-tip"},
		Since:     42,
	}
	item := historyNeed(gap)
	if item.Kind != api.NeedWork || item.ID != "adopt-1" || item.Since != 42 || item.Work == nil {
		t.Fatalf("the item reads %+v", item)
	}
	work := item.Work
	if work.Reason != api.ReasonHistoryMissing || work.WorkOrder == nil || work.WorkOrder.ID != "adopt-1" || work.Actions == nil || len(work.Actions) != 0 {
		t.Errorf("the work reads %+v", work)
	}
	for _, want := range []string{"lane/adopt", "main", "base-squashed", "design-tip", "with a merge commit"} {
		if !strings.Contains(work.Detail, want) {
			t.Errorf("the detail lacks %q: %s", want, work.Detail)
		}
	}
	notice := ledger.NewNotice("w", item.Kind, work.Reason, item.ID, item.Since)
	if notice.Reason != api.ReasonHistoryMissing || slices.ContainsFunc([]string{"lane/adopt", "design-tip"}, func(name string) bool { return strings.Contains(notice.Text, name) }) {
		t.Errorf("the notice reads %+v", notice)
	}
}

// A merged consolidation whose base could not be checked waits under a reason of its own, saying why.
func TestAnUncheckedBaseIsWorkThatSaysWhy(t *testing.T) {
	gap := control.HistoryGap{
		WorkOrder: workorders.WorkOrder{ID: "adopt-1", Kind: workorders.KindPlan, Title: "Adopt shop", State: workorders.Done, Adopt: true},
		Check:     ledger.ConsolidationCheck{Branch: "lane/adopt", Base: "main", Unread: "the plan approval names no design tip"},
		Since:     7,
	}
	item := historyNeed(gap)
	if item.Work == nil || item.Work.Reason != api.ReasonHistoryUnread || !strings.Contains(item.Work.Detail, "the plan approval names no design tip") || !strings.Contains(item.Work.Detail, "lane/adopt") {
		t.Errorf("the item reads %+v", item.Work)
	}
}
