package control

import (
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// approvePlanMerge records a member's plan approval as the approval of the planning branch it came from for
// merge, and runs the integration step (ADR-0018, ADR-0020): a planning branch is reviewed by plan approval,
// not by the review agent, and that approval lets ropeshift merge it. It runs once the ledger holds the
// approved version, so the plan's records exist before any merge, and it binds to the commit the member approved:
// the commit the request named, or the work order's plan commit for a request that names none. Planning work that is
// not waiting for its merge, and a branch that moved since that commit, approve nothing: a moved head has a plan of
// its own to approve. The branch is the work order the approval names, which is the version's own except for a
// version made from the repository, whose request intake raised again for the work order whose commit finished the
// plan (offerHeldVersion).
func approvePlanMerge(tx ports.WriteTx, version planning.PlanVersion, approval authority.Approval, member ledger.Actor, commit string) error {
	workOrder := approval.WorkOrder
	if workOrder == "" {
		workOrder = version.WorkOrder
	}
	if workOrder == "" {
		return nil
	}
	order, err := tx.WorkOrders().Get(workOrder)
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

// requestCommit is the commit a request to approve a plan was raised for. A request that names a work order names the
// commit its branch held, or a file read at that commit, as "path@commit".
func requestCommit(approval authority.Approval) string {
	source := planDetailOf(approval).Source
	if at := strings.LastIndex(source, "@"); at >= 0 {
		return source[at+1:]
	}
	return source
}

// refuseMovedBranch refuses a plan approval whose request was raised for a commit of the work order it names that is
// not the one the work order's branch holds now. Approving a plan approves the merge of the branch it came from at the
// commit the member was shown, so a request that named another commit approves the merge of nothing the member read:
// the branch has a request of its own for the commit it holds. Only a planning branch waiting for its merge has a
// merge to approve; a request that names no commit, or no such work order, is the plan's approval and nothing else.
func refuseMovedBranch(tx ports.WriteTx, approval authority.Approval) error {
	commit := requestCommit(approval)
	if approval.WorkOrder == "" || commit == "" {
		return nil
	}
	order, err := tx.WorkOrders().Get(approval.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil || order.Kind != workorders.KindPlan || order.State != workorders.Integrating {
		return err
	}
	held, err := AttemptCommit(tx.Evidence(), order)
	if err != nil || held == "" || held == commit {
		return err
	}
	return fmt.Errorf("%w: the request was raised for commit %s of work order %s, whose branch holds %s now, so it approves nothing the member read",
		authority.ErrStaleDigest, commit, order.ID, held)
}

// supersedePlanRequests closes the requests to approve a plan that name a work order that goes back for rework. Each was
// raised for a commit of the work's branch, and the attempt that follows makes another, which asks for its own approval
// when its plan is new; one that stayed open could be approved after the attempt that follows was accepted. A request
// that only says what a placeholder plan lacks stays, because it cannot be approved and the version that finishes the
// plan replaces it (offerHeldVersion).
func supersedePlanRequests(tx ports.WriteTx, workOrder string) error {
	rows, err := tx.Approvals().OfWorkOrder(workOrder)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || !row.Open() || len(planDetailOf(row).Unfinished) != 0 {
			continue
		}
		next, err := row.Supersede()
		if err != nil {
			return err
		}
		if err := noteApproval(tx, next, row.State, intakeActor); err != nil {
			return err
		}
	}
	return nil
}
