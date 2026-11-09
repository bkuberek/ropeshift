package contract

import (
	"encoding/json"
	"errors"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// BlockedChecks are the two ways out of `blocked`, which the state diagram draws and only a member
// can take.
var BlockedChecks = []Check{
	{"BK1", "a member sends blocked work back to ready, and its review stands", checkResolvedToReady},
	{"BK2", "a member sends blocked work to planning, which voids its review", checkReplanned},
	{"BK3", "only a member takes work out of blocked", checkOnlyMemberUnblocks},
	{"BK4", "work that is not blocked stays where it is", checkOnlyBlockedUnblocks},
}

// blockedWork is a work order whose lane reported it cannot finish.
func blockedWork(r *Rig) (*statusRig, error) {
	s, err := newStatusRig(r)
	if err != nil {
		return nil, err
	}
	result, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: "no_outcome"}})
	if err != nil {
		return nil, err
	}
	if reply, err := s.upload(ledger.UploadLaneEnded, result); err != nil || len(reply.Rejected) != 0 {
		return nil, Violatedf("the lane did not end: %+v, %v", reply.Rejected, err)
	}
	return s, nil
}

func (s *statusRig) order() (workorders.WorkOrder, error) {
	var row workorders.WorkOrder
	err := s.store.Read(s.r.Ctx, s.r.Scope, func(tx ports.ReadTx) error {
		var err error
		row, err = tx.WorkOrders().Get(s.grant.WorkOrder)
		return err
	})
	return row, err
}

func checkResolvedToReady(r *Rig) error {
	s, err := blockedWork(r)
	if err != nil {
		return err
	}
	before, err := s.order()
	if err != nil {
		return err
	}
	if before.State != workorders.Blocked {
		return Violatedf("state %s", before.State)
	}
	if err := control.NewReviews(s.store).Unblock(r.Ctx, r.Scope, before.ID, workorders.TriggerResolved, "fixed by hand", theMember); err != nil {
		return err
	}
	after, err := s.order()
	if err != nil {
		return err
	}
	if after.State != workorders.Ready || after.ReviewedBy != before.ReviewedBy {
		return Violatedf("after %+v, before %+v", after, before)
	}
	return r.CheckProjection(s.store)
}

func checkReplanned(r *Rig) error {
	s, err := blockedWork(r)
	if err != nil {
		return err
	}
	if err := control.NewReviews(s.store).Unblock(r.Ctx, r.Scope, s.grant.WorkOrder, workorders.TriggerReplan, "the contract is wrong", theMember); err != nil {
		return err
	}
	after, err := s.order()
	if err != nil {
		return err
	}
	if after.State != workorders.Planning || after.ReviewedBy != "" {
		return Violatedf("after %+v", after)
	}
	return r.CheckProjection(s.store)
}

func checkOnlyMemberUnblocks(r *Rig) error {
	s, err := blockedWork(r)
	if err != nil {
		return err
	}
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorOrchestrator, ID: s.auth.WorkerID},
		{Kind: ledger.ActorWorker, ID: s.auth.WorkerID},
		{Kind: ledger.ActorLane, ID: s.lane},
		{Kind: ledger.ActorSystem, ID: "tick"},
	} {
		err := control.NewReviews(s.store).Unblock(r.Ctx, r.Scope, s.grant.WorkOrder, workorders.TriggerResolved, "", actor)
		if !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("%s took work out of blocked: %v", actor.Kind, err)
		}
	}
	return nil
}

func checkOnlyBlockedUnblocks(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	for _, trigger := range []workorders.Trigger{workorders.TriggerResolved, workorders.TriggerReplan} {
		err := control.NewReviews(s.store).Unblock(r.Ctx, r.Scope, s.grant.WorkOrder, trigger, "", theMember)
		if !errors.Is(err, workorders.ErrIllegalMove) {
			return Violatedf("%s moved running work: %v", trigger, err)
		}
	}
	return nil
}
