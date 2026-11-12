package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// approvePlanMerge records a member's plan approval as the approval of the planning branch it came from for
// merge, and runs the integration step (ADR-0018, ADR-0020): a planning branch is reviewed by plan approval,
// not by the review agent, and that approval lets ropeshift merge it. It runs once the ledger holds the
// approved version, so the plan's records exist before any merge, and it binds to the commit intake read the
// version at, which is the commit the member approved. Planning work that is not waiting for its merge, and a
// branch that moved since that commit, approve nothing: a moved head has a plan of its own to approve.
func approvePlanMerge(tx ports.WriteTx, version planning.PlanVersion, approval authority.Approval, member ledger.Actor, commit string) error {
	if version.WorkOrder == "" {
		return nil
	}
	order, err := tx.WorkOrders().Get(version.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil || order.Kind != workorders.KindPlan || order.State != workorders.Integrating {
		return err
	}
	f, err := readIntegration(readersOf(tx), order)
	if err != nil {
		return err
	}
	if commit == "" {
		commit = order.PlanCommit
	}
	if f.commit() == "" || f.commit() != commit {
		return nil
	}
	record := f.approvalBy(tx, ledger.ApprovedByMember, member.ID)
	record.Plan, record.PlanVersion = version.PlanID, version.Version
	if err := recordApproval(tx, f, approvedMerge{IntegrationRecord: record, PlanApproval: approval.ID}, member); err != nil {
		return err
	}
	return integrate(tx, order.ID)
}
