package control

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
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
// the branch has a request of its own for the commit it holds, or gets one once a member accepts that commit
// (askForTheMerge). A planning branch waiting for acceptance is refused the same way as one waiting for its merge,
// because the request would otherwise stand approved for a commit the work no longer holds. A request that names no
// commit, or no such work order, is the plan's approval and nothing else.
func refuseMovedBranch(tx ports.WriteTx, approval authority.Approval) error {
	commit := requestCommit(approval)
	if approval.WorkOrder == "" || commit == "" {
		return nil
	}
	order, err := tx.WorkOrders().Get(approval.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil || order.Kind != workorders.KindPlan || (order.State != workorders.Integrating && order.State != workorders.Acceptance) {
		return err
	}
	held, err := AttemptCommit(tx.Evidence(), order)
	if err != nil || held == "" || held == commit {
		return err
	}
	return fmt.Errorf("%w: the request was raised for commit %s of work order %s, whose branch holds %s now, so it approves nothing the member read",
		authority.ErrStaleDigest, commit, order.ID, held)
}

// askForTheMerge asks for the approval of a planning branch's merge at the commit intake took in, when the work waits in
// integrating and no request can give it: none that is open or approved, for a version the ledger holds, names the
// work order and that commit. A commit that holds the plan the ledger holds makes no version, so nothing else asks
// about it after a send-back, a moved head, or a second branch of the same plan, and the work would wait for an
// approval no request could give. The request is for the newest version of the first plan the commit holds, naming
// the work order and the commit, as offerHeldVersion names them, and says what that version lacks, if anything. It runs
// after voidUnclaimed, so an approval of the branch that no version claimed does not count. A request that stands is
// enough, so reading the commit again asks nothing new.
func askForTheMerge(tx ports.WriteTx, workOrder, commit string, batch ports.ChangeBatch) error {
	order, err := tx.WorkOrders().Get(workOrder)
	if err != nil || order.Kind != workorders.KindPlan || order.State != workorders.Integrating {
		return err
	}
	if held, err := AttemptCommit(tx.Evidence(), order); err != nil || held != commit {
		return err
	}
	if asked, err := mergeAsked(tx.Approvals(), workOrder, commit); err != nil || asked {
		return err
	}
	for _, item := range batch.Records {
		if item.Plan == nil {
			continue
		}
		link, err := tx.Planning().Link(planning.SubjectPlan, item.Key)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		latest, err := tx.Planning().LatestPlanVersion(link.RecordID)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		finish, err := versionFinish(tx, latest)
		if err != nil {
			return err
		}
		return askAboutVersion(tx, latest, planOrigin{workOrder: workOrder, commit: commit}, planning.Unfinished(finish))
	}
	return nil
}

// mergeAsked says a request stands whose approval is, or was, the approval of a work order's merge at a commit: one
// open or approved, for a version the ledger holds, that names the work order and the commit. A request for a branch
// nothing has read binds to no version once intake has read it, so it does not count.
func mergeAsked(approvals ports.ApprovalReader, workOrder, commit string) (bool, error) {
	rows, err := approvals.OfWorkOrder(workOrder)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.Kind == authority.KindPlan && row.Live() && row.Plan != "" && row.WorkOrder == workOrder && requestCommit(row) == commit {
			return true, nil
		}
	}
	return false, nil
}

// unaskedPlans names, for the merge gate, each plan that a request of a planning work order names at the commit its branch
// holds while no request for that plan at that commit is open or approved, with what became of its request: denied,
// superseded, or closed another way. The merge waits only for what its requests say, and a plan whose every request at the
// commit closed says nothing, so without this the branch would merge with a plan nobody approved. A plan the requests name
// only at other commits is not on this commit's account.
func unaskedPlans(requests []authority.Approval, commit string) []string {
	if commit == "" {
		return nil
	}
	type planAsked struct {
		title string
		live  bool
		last  authority.ApprovalState
	}
	asked := map[string]*planAsked{}
	for _, request := range requests {
		if request.Kind != authority.KindPlan || requestCommit(request) != commit {
			continue
		}
		detail := planDetailOf(request)
		plan := asked[detail.Ref]
		if plan == nil {
			plan = &planAsked{}
			asked[detail.Ref] = plan
		}
		plan.title = cmp.Or(plan.title, detail.Title, detail.Ref)
		plan.live = plan.live || request.Live()
		plan.last = request.State
	}
	var out []string
	for _, ref := range slices.Sorted(maps.Keys(asked)) {
		plan := asked[ref]
		if plan.live {
			continue
		}
		became := string(plan.last)
		if plan.last == authority.ApprovalVoid {
			became = "voided"
		}
		out = append(out, fmt.Sprintf("plan %q (its request at %s was %s, and no request for it is open or approved)", plan.title, short(commit), became))
	}
	return out
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

// sendBackBehindMain sends each planning branch that waits in integrating for the approval of an earlier version of a
// plan back for rework on the same branch, when intake reads a new version of the plan from the repository: main changed
// the plan. The branch's request names a version a member can no longer approve, because approving a plan approves the
// one the ledger holds now, and the plan on the branch is behind main's, as a branch whose merge conflicts with main is.
// So it goes back the way a conflict sends it (reworkConflict): it keeps its branch, the ended lane's worktree is taken
// away, and the brief says why and asks the lane to merge main into the branch. The send-back closes the branch's
// request, and the branch's next commit asks for the approval of main's version naming it, once a member accepts it.
func sendBackBehindMain(tx ports.WriteTx, version planning.PlanVersion, title string) error {
	rows, err := tx.Approvals().All()
	if err != nil {
		return err
	}
	sent := map[string]bool{}
	for _, row := range rows {
		if row.Kind != authority.KindPlan || !row.Open() || row.Plan != version.PlanID || row.PlanVersion >= version.Version || row.WorkOrder == "" || sent[row.WorkOrder] {
			continue
		}
		order, err := tx.WorkOrders().Get(row.WorkOrder)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if order.Kind != workorders.KindPlan || order.State != workorders.Integrating {
			continue
		}
		f, err := readIntegration(readersOf(tx), order)
		if err != nil {
			return err
		}
		if f.commit() == "" || f.commit() != requestCommit(row) {
			continue
		}
		sent[order.ID] = true
		if err := reworkBehindMain(tx, f, version, title); err != nil {
			return err
		}
	}
	return nil
}

// denyPlan sends the planning branch a denied plan request names back for rework on the same branch, with the member's
// reason in its brief, while its work waits in acceptance or integrating at the commit the request names. Nothing asks
// again about a plan a member denied, so its branch would wait for an approval no request can give, and approving the
// request of another plan the branch holds would accept it with the denied plan in it. It goes back the way a change of
// the plan on main sends it (reworkBehindMain): it keeps its branch, the ended lane's worktree is taken away, and the
// requests that name it close. A request that names no work order, or a commit the branch no longer holds, denies the
// plan and nothing else.
func denyPlan(tx ports.WriteTx, denied authority.Approval, member ledger.Actor) error {
	if denied.WorkOrder == "" {
		return nil
	}
	order, err := tx.WorkOrders().Get(denied.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil || order.Kind != workorders.KindPlan || (order.State != workorders.Acceptance && order.State != workorders.Integrating) {
		return err
	}
	f, err := readIntegration(readersOf(tx), order)
	if err != nil || f.commit() == "" || f.commit() != requestCommit(denied) {
		return err
	}
	if err := keepBranch(tx, ledger.ForgeRequest{WorkOrder: order.ID, Branch: f.branch.URI, Commit: f.commit()}, member); err != nil {
		return err
	}
	detail := planDetailOf(denied)
	reason := fmt.Sprintf("a member declined the plan %q on %s at %s: %s. Revise the plan on this branch to meet that reason, and commit the change on this branch.",
		cmp.Or(detail.Title, detail.Ref), f.branch.URI, short(f.commit()), cmp.Or(strings.TrimSpace(denied.Reason), "no reason given"))
	_, err = reworkFor(tx, order.ID, workorders.ReworkNote{Reason: reason}, nil, member, func() {})
	return err
}

// reworkBehindMain sends one waiting planning branch back for rework against the plan main holds now. The branch waits in
// integrating for its approval, or in acceptance when it holds another plan whose request would otherwise accept it.
func reworkBehindMain(tx ports.WriteTx, f integrationFacts, version planning.PlanVersion, title string) error {
	if err := keepBranch(tx, ledger.ForgeRequest{WorkOrder: f.order.ID, Branch: f.branch.URI, Commit: f.commit()}, intakeActor); err != nil {
		return err
	}
	reason := fmt.Sprintf("the plan %q changed on %s while this branch waited in %s for its approval: %s holds version %d of it now, "+
		"and the plan on %s at %s is behind it, so approving it could no longer approve this branch's merge. "+
		"Merge the current %s into this branch, bring the plan up to date with %s's, and commit the merge on this branch.",
		title, f.base, f.order.State, f.base, version.Version, f.branch.URI, short(f.commit()), f.base, f.base)
	_, err := reworkFor(tx, f.order.ID, workorders.ReworkNote{Reason: reason}, nil, intakeActor, func() {})
	return err
}
