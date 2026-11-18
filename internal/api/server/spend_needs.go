package server

import (
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// spendStopID names a spend stop's item by its work order and what stopped it, as a CI item is named.
func spendStopID(workOrder string) string { return workOrder + ":" + ledger.ConditionSpendLimit }

// spendNeeds lists the work orders a hard spend limit parked, as items of Needs you (FR-SPEND-7): one for each work
// order however many lanes it parked. It also says which work orders they are, so the plain items a parked work order
// would raise, a resume and a long park, are left to the stop that answers them.
func (h *handler) spendNeeds(tx ports.ReadTx) ([]api.NeedItem, map[string]bool, error) {
	stops, err := control.SpendStops(tx)
	if err != nil {
		return nil, nil, err
	}
	items := make([]api.NeedItem, 0, len(stops))
	stopped := make(map[string]bool, len(stops))
	for _, stop := range stops {
		since, err := enteredAt(tx, stop.WorkOrder)
		if err != nil {
			return nil, nil, err
		}
		stopped[stop.WorkOrder.ID] = true
		items = append(items, api.NeedItem{Kind: api.NeedSpend, ID: spendStopID(stop.WorkOrder.ID), Since: since, Spend: spendNeedOf(stop)})
	}
	return items, stopped, nil
}

// spendNeedOf is what a member reads of a stop, with what the member can do. Resume once is offered only where something
// sizes one more generation, because the use case refuses it otherwise, and Replan is offered for work that has a contract
// to plan again, which a review has not.
func spendNeedOf(stop control.SpendStop) *api.SpendNeed {
	need := &api.SpendNeed{WorkOrder: stop.WorkOrder, Spend: stop.Spend, Band: stop.Band, Lanes: []api.SpendLane{}, Actions: []string{api.ActSpendRaise}}
	if stop.Resume != nil {
		need.Resume = &api.SpendResume{AllowanceUSD: stop.Resume.AllowanceUSD, Basis: stop.Resume.Basis, LimitUSD: stop.Resume.LimitUSD}
		need.Actions = append(need.Actions, api.ActSpendResume)
	}
	need.Actions = append(need.Actions, api.ActSpendBlock)
	if stop.WorkOrder.Kind != workorders.KindReview {
		need.Actions = append(need.Actions, api.ActSpendReplan)
	}
	for _, lane := range stop.Lanes {
		read := api.SpendLane{Lane: lane.Lane, Generation: lane.Generation, Digest: lane.Digest}
		if lane.Handoff != nil {
			read.Handoff = &api.SpendHandoff{
				Generation: lane.Handoff.Generation, Reason: lane.Handoff.Reason, Degraded: lane.Handoff.Degraded, Narrative: lane.Handoff.Narrative,
			}
		}
		need.Lanes = append(need.Lanes, read)
	}
	return need
}
