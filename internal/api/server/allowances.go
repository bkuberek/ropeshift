package server

import (
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// archivable says a member may ask for a lane's worktree to be removed, and the worker will: the lane ended,
// and the worktree still stands. The use case also takes a request for a lane whose attempt ended before its
// worker reported it, and for a parked lane, but a worker removes only the worktree of a lane that ended, and a
// parked lane's holds the checkpoint it resumes from, so a view offers neither. A lane that ended is not one
// that runs, so the use case never refuses what this offers.
func archivable(row ledger.LaneRow) bool {
	return row.Status.ActualState == ledger.StateEnded && !row.Archived
}

// allowances says what a member can do to each of the work orders now. The state machine says which actions
// the state allows, and the claims and the lanes say the two things a state does not: whether a claim already
// asked for its lane, and whether a lane left a worktree to remove.
func allowances(tx ports.ReadTx, orders []workorders.WorkOrder) (map[string]api.WorkAllowance, error) {
	byDefault, err := control.DefaultAttempts(tx.Policies())
	if err != nil {
		return nil, err
	}
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]workorders.WorkOrder, len(orders))
	for _, order := range orders {
		byID[order.ID] = order
	}
	removable := map[string]bool{}
	for _, row := range rows {
		if _, ok := byID[row.WorkOrder]; ok && archivable(row) {
			removable[row.WorkOrder] = true
		}
	}
	out := make(map[string]api.WorkAllowance, len(orders))
	for _, order := range orders {
		facts := workorders.Facts{Removable: removable[order.ID]}
		if order.State == workorders.Claimed {
			claim, err := tx.Claims().Find(order.ClaimID)
			if err != nil && !isNotFound(err) {
				return nil, err
			}
			facts.Dispatched = err == nil && claim.Lane != ""
		}
		allowance := api.WorkAllowance{Actions: []string{}, ExtraAttempt: order.State == workorders.Failed && !order.AttemptsLeft(byDefault)}
		for _, action := range workorders.Allowed(order.State, facts) {
			allowance.Actions = append(allowance.Actions, string(action))
		}
		out[order.ID] = allowance
	}
	return out, nil
}
