package control

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// reviseCriterion is the one criterion a revision's planning session holds, because the definition of ready wants
// at least one and a plan has no test to run.
const reviseCriterion = "The plan is revised in the repository as files, and the person has gone through the decisions it lists in this session."

// PlanRevisions holds what a member does about a plan that decisions have moved past: confirm the approved
// version against them, or start a session that revises the plan (ADR-0030).
type PlanRevisions struct {
	store  ports.Store
	orders *WorkOrders
}

// NewPlanRevisions returns the use cases over a Store.
func NewPlanRevisions(store ports.Store) *PlanRevisions {
	return &PlanRevisions{store: store, orders: NewWorkOrders(store)}
}

// reviseApprovedPlans marks each plan whose approved version a member's decision binds work of as needing revision
// (FR-PLAN-11): it opens the plan's revision with the decision, or adds the decision to the one that stands. A
// reversible decision lists the finished work it reached, which a member reworks in one action, and a one-way
// decision lists none, because it reworked that work. An orchestrator's decision marks nothing, because covered work
// proceeds under policy (FR-PLAN-10), and neither does a decision that binds work no approved plan covers.
func reviseApprovedPlans(tx ports.WriteTx, decision questions.Decision, touched reach, finished []workorders.WorkOrder, oneWay bool, actor ledger.Actor) error {
	if marksNoRevision() || (authority.PartyOf(actor) != authority.PartyPerson && !marksAnyActor()) {
		return nil
	}
	facts := newScopeFacts(tx.Planning(), tx.WorkOrders())
	plans := map[string]bool{}
	for _, order := range touched.orders {
		plan, err := facts.planOf(order.ContractID)
		if err != nil {
			return err
		}
		if plan != "" {
			plans[plan] = true
		}
	}
	for _, plan := range slices.Sorted(maps.Keys(plans)) {
		approved, found, err := approvedVersion(tx, plan)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		var listed []string
		if (!oneWay && !listsNoFinishedWork()) || (oneWay && listsOneWayFinishedWork()) {
			for _, order := range finished {
				if of, err := facts.planOf(order.ContractID); err != nil {
					return err
				} else if of == plan {
					listed = append(listed, order.ID)
				}
			}
		}
		if err := extendRevision(tx, plan, approved, decision.ID, listed, actor); err != nil {
			return err
		}
	}
	return nil
}

// approvedVersion is the newest version of a plan a member approved, and false for a plan none was.
func approvedVersion(tx ports.WriteTx, plan string) (int64, bool, error) {
	version, found, err := lastApproved(tx, plan)
	if errors.Is(err, ports.ErrNotFound) {
		return 0, false, nil
	}
	return version.Version, found && err == nil, err
}

// extendRevision opens a plan's revision with a decision, or adds the decision and the finished work it reached to
// the revision that stands. A decision a revision lists already changes nothing.
func extendRevision(tx ports.WriteTx, plan string, approved int64, decision string, finished []string, actor ledger.Actor) error {
	open, err := tx.Planning().OpenRevision(plan)
	var next planning.Revision
	kind := planning.EventRevisionExtended
	switch {
	case errors.Is(err, ports.ErrNotFound):
		id, err := newID(tx.Now())
		if err != nil {
			return err
		}
		next = planning.Revision{ID: id, PlanID: plan, ApprovedVersion: approved, OpenedAt: tx.Now()}.Extend(decision, finished)
		kind = planning.EventRevisionOpened
	case err != nil:
		return err
	default:
		if extendsNothing() {
			return nil
		}
		if next = open.Extend(decision, finished); reflect.DeepEqual(next, open) {
			return nil
		}
	}
	return saveRevision(tx, next, kind, actor)
}

// saveRevision keeps a revision as it stands, with the event that says so.
func saveRevision(tx ports.WriteTx, revision planning.Revision, kind string, actor ledger.Actor) error {
	if err := tx.Planning().SaveRevision(revision); err != nil {
		return err
	}
	event, err := planning.RevisionEvent(kind, revision, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// revisionWait says why work of a plan waits for it, from the plan's open revision, or nothing for a plan with none.
func revisionWait(planned ports.PlanningReader, plan string) (string, error) {
	if plan == "" || waitsOnNoRevision() {
		return "", nil
	}
	revision, err := planned.OpenRevision(plan)
	if errors.Is(err, ports.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("the plan needs revision: %d decisions since version %d wait for a member to confirm that version or approve a later one",
		len(revision.DecisionIDs), revision.ApprovedVersion), nil
}

// settlePlanWork runs the pass of contract review again for the work of a plan that waits in it, once what held it
// is settled: a member who confirmed a plan's version, or approved a later one, has read the decisions since, so a
// hold a person's one-way decision put on the work ends, and the definition of ready runs on each work order whose
// review is on record.
func settlePlanWork(tx ports.WriteTx, plan string, actor ledger.Actor) error {
	contracts, err := newestContracts(tx.Planning(), plan)
	if err != nil {
		return err
	}
	for _, contract := range contracts {
		orders, err := tx.WorkOrders().ByContract(contract.ID)
		if err != nil {
			return err
		}
		for _, order := range orders {
			if order.State != workorders.ContractReview {
				continue
			}
			if !keepsDecisionHolds() {
				if order, err = releaseDecisionHold(tx, order, actor); err != nil {
					return err
				}
			}
			if order.ReviewedBy == "" || keepsWaitingWork() {
				continue
			}
			if _, err := settleReview(tx, order, actor); err != nil {
				return err
			}
		}
	}
	_, err = passCoveredReviews(tx, actor)
	return err
}

// ConfirmRevision records that the approved version of a plan stands with the decisions a member made since, which
// closes the plan's revision and releases the work that waited for it. It is the same authority as approving a
// plan version, so it takes an authenticated member and never the lane that wrote the plan (FR-PLAN-12).
func (p *PlanRevisions) ConfirmRevision(ctx context.Context, scope ledger.WorkspaceID, plan string, actor ledger.Actor) (planning.Revision, error) {
	if !authority.Allowed(actor, authority.ActionApprovePlan) && !confirmsAnyActor() {
		return planning.Revision{}, workorders.ErrForbidden
	}
	var closed planning.Revision
	err := p.store.Write(ctx, scope, "plan.confirm_revision", func(tx ports.WriteTx) error {
		open, err := tx.Planning().OpenRevision(plan)
		if err != nil {
			return err
		}
		closed = open.Close(authority.Principal(actor), tx.Now(), planning.RevisionConfirmed, "")
		if err := saveRevision(tx, closed, planning.EventRevisionClosed, actor); err != nil {
			return err
		}
		crashpoint.Hit("plan.confirm_revision", "closed")
		return settlePlanWork(tx, plan, actor)
	})
	if err != nil {
		return planning.Revision{}, err
	}
	return closed, nil
}

// closeApprovedRevision ends the revision of a plan whose later version a member approved, naming the approval, and
// releases the work that waited for it. Approving the version is the member's word on the decisions since, which
// the request showed beside the difference. The version's approval never inherits the earlier one, so this closes a
// revision and passes nothing the earlier approval did not cover.
func closeApprovedRevision(tx ports.WriteTx, plan string, approval authority.Approval, member ledger.Actor) error {
	if keepsRevisionOnApproval() {
		return nil
	}
	open, err := tx.Planning().OpenRevision(plan)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	closed := open.Close(authority.Principal(member), tx.Now(), planning.RevisionApproved, approval.ID)
	if err := saveRevision(tx, closed, planning.EventRevisionClosed, member); err != nil {
		return err
	}
	crashpoint.Hit("plan.approve", "revision_closed")
	return settlePlanWork(tx, plan, member)
}

// ReviseSession starts a planning session that revises a plan a revision is open on. It records the `plan` work
// order, in contract review, whose brief lists the decisions since the approved version with their statements and
// scopes and the finished work they reached, and the member goes on as a planning session does: review, claim, and
// start the lane. The client's ID lets a retry find the work order the first attempt recorded. Approving the
// version that session writes closes the revision.
func (p *PlanRevisions) ReviseSession(ctx context.Context, scope ledger.WorkspaceID, plan string, session ReviseSession, actor ledger.Actor) (workorders.WorkOrder, error) {
	if !authority.Allowed(actor, authority.ActionApprovePlan) {
		return workorders.WorkOrder{}, workorders.ErrForbidden
	}
	var title, brief string
	var found workorders.WorkOrder
	err := p.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		existing, err := tx.WorkOrders().Get(session.ID)
		if err == nil {
			found = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		title, brief, err = reviseBrief(tx, plan)
		return err
	})
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	if found.ID != "" {
		return found, nil
	}
	return p.orders.Record(ctx, scope, LedgerWork{
		Order:     workorders.WorkOrder{ID: session.ID, Kind: workorders.KindPlan, Title: title, RepositoryID: session.Repository},
		Statement: brief,
		Criteria:  []planning.Criterion{{ID: "c1", Text: reviseCriterion}},
	}, actor)
}

// ReviseSession is the planning session a revision starts: the work order's ID, which the client generates, and the
// repository it plans in.
type ReviseSession struct {
	ID         string
	Repository string
}

// reviseBrief is the title and the brief of a session that revises a plan: what the plan is, the decisions since its
// approved version with their statements and scopes, and the finished work they reached.
func reviseBrief(tx ports.ReadTx, plan string) (string, string, error) {
	revision, err := tx.Planning().OpenRevision(plan)
	if err != nil {
		return "", "", err
	}
	record, err := tx.Planning().Plan(plan)
	if err != nil {
		return "", "", err
	}
	var text strings.Builder
	fmt.Fprintf(&text, "Revise the plan %q, which a member approved at version %d.\n", record.Title, revision.ApprovedVersion)
	if latest, err := tx.Planning().LatestPlanVersion(plan); err == nil {
		if ref := planFileRef(latest.Records); ref != "" {
			fmt.Fprintf(&text, "The plan's file names it %q.\n", ref)
		}
	}
	text.WriteString("\nThese decisions were made since that version. The plan must reflect each one, or say why it does not:\n\n")
	if !briefsNoDecisions() {
		for _, id := range revision.DecisionIDs {
			decision, err := tx.Questions().Decision(id)
			if err != nil {
				return "", "", err
			}
			how := "reversible"
			if decision.Reversibility == questions.OneWay {
				how = "one-way"
			}
			fmt.Fprintf(&text, "- %s (%s, scope %s): %s\n", decision.ID, how, decision.Scope, decision.Statement)
		}
	}
	if len(revision.Finished) != 0 {
		text.WriteString("\nThese finished work orders were reached by reversible decisions, and may need rework:\n\n")
		for _, work := range revision.Finished {
			fmt.Fprintf(&text, "- %s (decisions %s)\n", work.WorkOrder, strings.Join(work.Decisions, ", "))
		}
	}
	return "Revise " + record.Title, text.String(), nil
}

// DecisionRework starts the rework of one finished work order for the decisions that reached it, which a reversible
// decision lists on its plan's revision and does not start by itself (ADR-0030). The rework names every decision, and
// a rework of that work order that has not finished takes them in place of a second one. It returns the rework, which
// the revision records beside the work. Work that is not finished, work a rework that finished already redid, a
// decision that is not in force, and a decision the plan's open revision does not list for the work are refused.
func (q *Questions) DecisionRework(ctx context.Context, scope ledger.WorkspaceID, workOrder string, decisions []string, actor ledger.Actor) (string, error) {
	if authority.PartyOf(actor) != authority.PartyPerson || !authority.Allowed(actor, authority.ActionRework) {
		return "", workorders.ErrForbidden
	}
	if len(decisions) == 0 {
		return "", fmt.Errorf("%w: rework for a decision names the decisions", workorders.ErrInvalid)
	}
	var rework string
	err := q.store.Write(ctx, scope, "work_order.decision_rework", func(tx ports.WriteTx) error {
		order, err := tx.WorkOrders().Get(workOrder)
		if err != nil {
			return err
		}
		if responseOf(order) != workorders.Finished {
			return fmt.Errorf("%w: work order %s is %s, and only finished work gets rework for a decision", workorders.ErrInvalid, order.ID, order.State)
		}
		if err := refuseReworkedWork(tx, order); err != nil {
			return err
		}
		revision, err := revisionListing(tx, order)
		if err != nil {
			return err
		}
		why, err := decisionReasons(tx, revision, order, decisions)
		if err != nil {
			return err
		}
		made, err := reworkFinished(tx, []workorders.WorkOrder{order}, why, actor)
		if err != nil {
			return err
		}
		if len(made) != 1 {
			return fmt.Errorf("%w: work order %s got no rework", workorders.ErrInvalid, order.ID)
		}
		rework = made[0]
		crashpoint.Hit("work_order.decision_rework", "reworked")
		return recordRework(tx, order, rework, actor)
	})
	return rework, err
}

// refuseReworkedWork refuses the rework of a work order a finished rework redid: the rework is the tip of its
// chain, and reworking the work beside it would fork the chain.
func refuseReworkedWork(tx ports.WriteTx, order workorders.WorkOrder) error {
	rows, err := tx.WorkOrders().List()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ReworkOf == order.ID && responseOf(row) == workorders.Finished {
			return fmt.Errorf("%w: work order %s was redone by %s, which finished: rework that one", workorders.ErrInvalid, order.ID, row.ID)
		}
	}
	return nil
}

// revisionListing is the revision of the plan a finished work order belongs to, when it is open and lists the work
// order, which is what lets a member make rework for the decisions it lists.
func revisionListing(tx ports.WriteTx, order workorders.WorkOrder) (planning.RevisionWork, error) {
	plan, err := newScopeFacts(tx.Planning(), tx.WorkOrders()).planOf(order.ContractID)
	if err != nil {
		return planning.RevisionWork{}, err
	}
	var open planning.Revision
	if plan != "" {
		open, err = tx.Planning().OpenRevision(plan)
	}
	switch {
	case plan == "" || errors.Is(err, ports.ErrNotFound):
		return planning.RevisionWork{}, fmt.Errorf("%w: no open revision lists work order %s", workorders.ErrInvalid, order.ID)
	case err != nil:
		return planning.RevisionWork{}, err
	}
	at := slices.IndexFunc(open.Finished, func(work planning.RevisionWork) bool { return work.WorkOrder == order.ID })
	if at < 0 {
		return planning.RevisionWork{}, fmt.Errorf("%w: the revision of plan %s does not list work order %s", workorders.ErrInvalid, plan, order.ID)
	}
	return open.Finished[at], nil
}

// decisionReasons reads each decision a rework is for, and how it came to bind: answered when it answered a
// question, and recorded otherwise. A decision the revision does not list for the work is refused.
func decisionReasons(tx ports.WriteTx, listed planning.RevisionWork, order workorders.WorkOrder, decisions []string) ([]because, error) {
	var why []because
	for _, id := range slices.Compact(slices.Sorted(slices.Values(decisions))) {
		decision, err := tx.Questions().Decision(id)
		if err != nil {
			return nil, err
		}
		if !decision.Status.InForce() {
			return nil, fmt.Errorf("%w: decision %s is %s, and only a decision in force gets rework", workorders.ErrInvalid, id, decision.Status)
		}
		if !slices.Contains(listed.Decisions, id) {
			return nil, fmt.Errorf("%w: the revision does not list decision %s for work order %s", workorders.ErrInvalid, id, order.ID)
		}
		answered, err := tx.Questions().AnsweredBy(id)
		if err != nil {
			return nil, err
		}
		how := boundRecorded
		if len(answered) != 0 {
			how = boundAnswered
		}
		why = append(why, because{id, how})
	}
	return why, nil
}

// recordRework puts the rework a member made of a finished work order on the revision of its plan that lists the
// work.
func recordRework(tx ports.WriteTx, order workorders.WorkOrder, rework string, actor ledger.Actor) error {
	plan, err := newScopeFacts(tx.Planning(), tx.WorkOrders()).planOf(order.ContractID)
	if err != nil || plan == "" {
		return err
	}
	open, err := tx.Planning().OpenRevision(plan)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if next := open.Reworked(order.ID, rework); !reflect.DeepEqual(next, open) {
		return saveRevision(tx, next, planning.EventRevisionExtended, actor)
	}
	return nil
}
