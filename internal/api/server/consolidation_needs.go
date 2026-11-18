package server

import (
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
)

// historyNeeds lists, as work that waits for a member, every merged consolidation whose base was last found without
// the design tip it imported (FR-ADOPT-13).
func historyNeeds(tx ports.ReadTx) ([]api.NeedItem, error) {
	gaps, err := control.HistoryGaps(tx)
	if err != nil {
		return nil, err
	}
	items := make([]api.NeedItem, 0, len(gaps))
	for _, gap := range gaps {
		items = append(items, historyNeed(gap))
	}
	return items, nil
}

// historyNeed is one history gap as Needs you shows it, naming the branch and the missing commit. It offers no
// action: merging the tip into the base clears it at the next check.
func historyNeed(gap control.HistoryGap) api.NeedItem {
	order := gap.WorkOrder
	return api.NeedItem{
		Kind: api.NeedWork, ID: order.ID, Since: gap.Since,
		Work: &api.WorkNeed{Reason: api.ReasonHistoryMissing, Detail: gap.Detail(), WorkOrder: &order, Actions: []string{}},
	}
}
