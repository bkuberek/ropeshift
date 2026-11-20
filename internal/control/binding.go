package control

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// How a decision came to bind work, which the title of a rework and the reason of its calibration say.
const (
	boundRecorded   = "recorded"
	boundAnswered   = "answered"
	boundOverturned = "overturned"
)

// because is one decision that reached a work order, and how.
type because struct{ decision, how string }

func (b because) String() string { return fmt.Sprintf("decision %s was %s", b.decision, b.how) }

// responseOf is what a decision does to a work order, which is nothing for a `plan`, `contract_review`, or
// `review` work order: their lanes are told, and they are never moved or reworked.
func responseOf(order workorders.WorkOrder) workorders.Response {
	if reworksAnyKind() {
		order.Kind = workorders.KindChange
	}
	return workorders.ResponseOf(order)
}

// in lists the work a decision reached that has one response.
func (r reach) in(response workorders.Response) []workorders.WorkOrder {
	var out []workorders.WorkOrder
	for _, order := range r.orders {
		if responseOf(order) == response {
			out = append(out, order)
		}
	}
	return out
}

// applyBinding is what recording or answering a decision does to the work it binds, in the transaction that
// records it (ADR-0030). Unstarted work returns to contract review, every lane the decision reaches is told and
// parks when the lane policy says so, finished work gets rework when the decision is one-way, and a notice
// when it is not, and a member's decision inside an approved plan marks the plan as needing revision. A decision
// with no scope in the grammar acts on no work: only a one-way decision of that kind
// still tells the lanes it was found by, as it always did. A scope written before the grammar keeps the reach it
// had, which `covers` reads for lookups, and nothing records one now. A draft decision of a plan binds what its
// approval covers, and nothing else.
func (q *Questions) applyBinding(tx ports.WriteTx, record questions.Decision, how string, actor ledger.Actor) error {
	if !record.Status.InForce() || strings.HasPrefix(record.ID, questions.PlanDecisionPrefix) {
		return nil
	}
	scope, _ := questions.ParseScope(record.Scope)
	oneWay := record.Reversibility == questions.OneWay
	if scope.Zero() && !oneWay {
		return nil
	}
	touched, err := reachOf(tx, record)
	if err != nil {
		return err
	}
	if scope.Zero() {
		touched.orders = nil
	}
	if err := returnUnstarted(tx, touched.in(workorders.Unstarted), record.ID, oneWay, actor); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "held")
	text := questions.BindingNoticeText(record)
	if oneWay {
		text = questions.OneWayNoticeText(record)
	}
	if !tellsNobody() {
		// A lane that asked what the decision answers reads the answer, which names the decision.
		told := touched
		told.lanes = maps.Clone(touched.lanes)
		if how == boundAnswered {
			for lane := range touched.asked {
				delete(told.lanes, lane)
			}
		}
		if err := q.tellDecision(tx, record.ID, "", messages.Clip(text), told, actor); err != nil {
			return err
		}
	}
	crashpoint.Hit("decision.record", "noticed")
	// An orchestrator records a one-way decision only at unattended, where it blocks no work: members review it in Needs
	// you, so the lane policy's rule for a one-way decision does not park a lane for it, and it parks one only as any
	// other decision does (FR-QD-8).
	parksAsOneWay := oneWay && authority.PartyOf(actor) != authority.PartyOrchestrator
	if err := parkAsPolicySays(tx, touched, how, parksAsOneWay, record.ID, actor); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "parked")
	var rework []string
	finished := chainTips(touched.in(workorders.Finished))
	if oneWay || reworksReversible() {
		if rework, err = reworkFinished(tx, finished, []because{{record.ID, how}}, actor); err != nil {
			return err
		}
	}
	if err := recordReached(tx, record.ID, finished, rework, how, actor); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "reworked")
	if err := reviseApprovedPlans(tx, record, touched, finished, oneWay, actor); err != nil {
		return err
	}
	crashpoint.Hit("decision.record", "revised")
	packs, err := packsOfWork(tx, touched.in(workorders.Live))
	if err != nil {
		return err
	}
	if !renewsNoPack() {
		if err := q.renewPacks(tx, packs, actor); err != nil {
			return err
		}
	}
	crashpoint.Hit("decision.record", "bound")
	return nil
}

// returnUnstarted sends ready work back to contract review, where work in `intake`, `planning`, or contract review
// already stands, and holds work in review when a person's one-way decision binds it, so the pass of its review
// reads a choice nobody can take back. Any other decision holds nothing: covered work passes its review again at
// the next tick, with the decision in what it reads. The hold is per work order, and a member's pass of its review,
// a Confirm of the plan's revision, or the approval of a later version of the plan ends it. A plan revision waits
// separately, for the work its decisions bind (FR-PLAN-11).
func returnUnstarted(tx ports.WriteTx, orders []workorders.WorkOrder, decision string, oneWay bool, actor ledger.Actor) error {
	if keepsReadyWork() {
		return nil
	}
	person := (authority.PartyOf(actor) == authority.PartyPerson && oneWay) || holdsReversible()
	for _, order := range orders {
		if order.State == workorders.Ready {
			if err := takeEdge(tx, order.ID, workorders.TriggerBackToReview, actor); err != nil {
				return err
			}
			order.State = workorders.ContractReview
		}
		if person && order.State == workorders.ContractReview {
			if err := holdForDecision(tx, order, decision, actor); err != nil {
				return err
			}
		}
	}
	return nil
}

// derivedHold says a start hold is a reading of facts that a claim and the tick read again, and not a word a member
// or a policy gave. The work a decision returns to review can stand held for any of these, and the hold for the
// decision takes its place: a member's pass ends it, and the next claim or tick reads the facts again and holds the
// work for the same reason if it still stands. No hold is set by a member or a policy today; a reason that is not
// listed is kept as it stands, so a hold of that kind is never replaced.
//
//   - waiting_for_merge, dependencies_diverge, base_unavailable, no_checkout, and continued_branch_missing are read
//     from the repository and the work it waits on, at the claim and on each pass of `RefreshStarts`.
//   - pull_request_pending and forge_unavailable hold a review of a commit, which a decision never moves.
func derivedHold(hold *workorders.StartHold) bool {
	switch hold.Reason {
	case workorders.HoldWaitingForMerge, workorders.HoldDependenciesDiverge, workorders.HoldBaseUnavailable,
		workorders.HoldNoCheckout, workorders.HoldContinuedBranchMissing,
		workorders.HoldPullRequestPending, workorders.HoldForgeUnavailable:
		return true
	}
	return false
}

// holdForDecision keeps work in contract review until a member passes its review again. A hold that already names
// a decision stands, and holding for another leaves it as it is. A hold that is a reading of facts is replaced,
// because a start hold is one reason, and the work must not return to `ready` on the tick with no member's pass.
func holdForDecision(tx ports.WriteTx, order workorders.WorkOrder, decision string, actor ledger.Actor) error {
	if holdsNothing() || order.StartHeld != nil && (!derivedHold(order.StartHeld) || keepsStandingHold()) {
		return nil
	}
	hold := &workorders.StartHold{
		Reason: workorders.HoldDecision,
		Detail: fmt.Sprintf("decision %s binds this work, and a member's review has not read it", decision),
	}
	if err := tx.WorkOrders().Hold(order.ID, hold); err != nil {
		return err
	}
	event, err := workorders.ChangeEvent(workorders.EventStartHeld, order.ID, workorders.Change{Hold: hold}, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// releaseDecisionHold ends the hold a decision put on work in review, and returns the work as it stands.
func releaseDecisionHold(tx ports.WriteTx, order workorders.WorkOrder, actor ledger.Actor) (workorders.WorkOrder, error) {
	if order.StartHeld == nil || order.StartHeld.Reason != workorders.HoldDecision {
		return order, nil
	}
	if err := tx.WorkOrders().Hold(order.ID, nil); err != nil {
		return order, err
	}
	event, err := workorders.ChangeEvent(workorders.EventStartResolved, order.ID, workorders.Change{}, actor)
	if err != nil {
		return order, err
	}
	order.StartHeld = nil
	_, err = tx.Append(event)
	return order, err
}

// parkAsPolicySays asks each lane a decision reached to park when the lane policy says so, `on_one_way_decision`
// for a one-way decision when a policy sets it and `on_binding_decision` otherwise. A lane of a `plan`,
// `contract_review`, or `review` work order is told and never parked, and neither is a lane that asked the question
// a decision answers: the answer is what it waits for, so it reads the answer and goes on.
func parkAsPolicySays(tx ports.WriteTx, touched reach, how string, oneWay bool, decision string, actor ledger.Actor) error {
	if parksNothing() {
		return nil
	}
	policy, err := lanePolicyInForce(tx)
	if err != nil || policy.BindingResponse(oneWay || parksAnyDecision()) != authority.LanePark {
		return err
	}
	reason := "decision " + decision
	if oneWay {
		reason = "one-way decision " + decision
	}
	for _, lane := range slices.Sorted(maps.Keys(touched.lanes)) {
		if touched.asked[lane] && (how == boundAnswered && !parksAskingLanes() || how != boundAnswered && sparesAskersOnOverturn()) {
			continue
		}
		work, err := laneWork(tx, lane)
		if err != nil {
			return err
		}
		if work.Kind != workorders.KindChange && work.Kind != workorders.KindRework {
			continue
		}
		if err := askToPark(tx, lane, touched.lanes[lane], reason, actor); err != nil {
			return err
		}
	}
	return nil
}

// laneWork reads the work order a lane runs, or none for a lane the ledger has no record of.
func laneWork(tx ports.WriteTx, lane string) (workorders.WorkOrder, error) {
	row, err := tx.Lanes().Row(lane)
	if errors.Is(err, ports.ErrNotFound) {
		return workorders.WorkOrder{}, nil
	}
	if err != nil {
		return workorders.WorkOrder{}, err
	}
	order, err := tx.WorkOrders().Get(row.WorkOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return workorders.WorkOrder{}, nil
	}
	return order, err
}

// recordReached records the finished work a decision bound, and the rework it started or that gained it. A
// reversible decision starts none: it lists the finished work, which a member may rework in one action
// (`work decision-rework`).
func recordReached(tx ports.WriteTx, decision string, finished []workorders.WorkOrder, rework []string, how string, actor ledger.Actor) error {
	if len(finished) == 0 {
		return nil
	}
	reached := questions.Reached{DecisionID: decision, Rework: rework, How: how}
	for _, order := range finished {
		reached.WorkOrders = append(reached.WorkOrders, order.ID)
	}
	event, err := questions.RecordEvent(questions.EventDecisionReached, questions.SubjectDecision, decision, reached, actor)
	if err != nil {
		return err
	}
	_, err = tx.Append(event)
	return err
}

// chainTips keeps the latest finished work order of each chain of rework: the work order a later finished rework
// in the list is the rework of is left out, because the rework of the tip covers it. A decision that reaches a
// finished work order and its finished rework makes one rework, of the rework.
func chainTips(finished []workorders.WorkOrder) []workorders.WorkOrder {
	if reworksWholeChain() {
		return finished
	}
	covered := map[string]bool{}
	for _, order := range finished {
		if order.ReworkOf != "" {
			covered[order.ReworkOf] = true
		}
	}
	var tips []workorders.WorkOrder
	for _, order := range finished {
		if !covered[order.ID] {
			tips = append(tips, order)
		}
	}
	return tips
}

// reworkFinished starts one rework of each finished work order, in contract review, where a person reviews it as
// they would any work, naming every decision that reached it. A rework of that work order that has not finished
// is the tip of its chain and gains the decisions instead of a second one beside it: one that has not started
// starts from a pack that holds every decision that binds it, and one a lane has is told and has its pack
// renewed by the reach of the decision. It returns the rework of each work order, started or gained.
func reworkFinished(tx ports.WriteTx, finished []workorders.WorkOrder, why []because, actor ledger.Actor) ([]string, error) {
	if len(finished) == 0 || reworksNothing() {
		return nil, nil
	}
	rows, err := tx.WorkOrders().List()
	if err != nil {
		return nil, err
	}
	// A rework that failed or was canceled is no tip: the work it was to redo is done again by a new one.
	waiting := map[string]string{}
	for _, row := range rows {
		if row.ReworkOf == "" || reworksEachDecision() {
			continue
		}
		switch workorders.ResponseOf(row) {
		case workorders.Unstarted:
			waiting[row.ReworkOf] = row.ID
		case workorders.Live:
			if !branchesTheChain() {
				waiting[row.ReworkOf] = row.ID
			}
		}
	}
	var reasons []string
	for _, reason := range why {
		reasons = append(reasons, reason.String())
	}
	reason := strings.Join(reasons, ", ")
	var out []string
	for _, order := range finished {
		if id, gains := waiting[order.ID]; gains {
			out = append(out, id)
			continue
		}
		id, err := newID(tx.Now())
		if err != nil {
			return nil, err
		}
		if err := insertRework(tx, id, order, reason, actor); err != nil {
			return nil, err
		}
		if err := calibrateLaterDefect(tx, order, reason, actor); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, nil
}

// packsOfWork names the packs of work that holds a lane, which a decision that now binds the work renews so
// the lane reads it at its next generation (FR-CTX-3).
func packsOfWork(tx ports.WriteTx, orders []workorders.WorkOrder) ([]string, error) {
	if len(orders) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, order := range orders {
		want[order.ID] = true
	}
	all, err := tx.Packs().All()
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, pack := range all {
		if want[pack.WorkOrder] && !slices.Contains(ids, pack.ID) {
			ids = append(ids, pack.ID)
		}
	}
	return ids, nil
}
