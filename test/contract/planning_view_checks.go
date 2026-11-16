package contract

import (
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PlanningViewChecks hold what a planning lane sees of delivery estimates, and what nobody else does (FR-EST-12):
// its pack holds the project's delivery history, and no other lane or orchestrator reads an estimate, a forecast,
// or history.
var PlanningViewChecks = []Check{
	{"PV1", "a planning lane's pack holds the project's delivery history last, aggregated with labels and explanations, and the lane reads it by name", checkPlanningPackHoldsHistory},
	{"PV2", "a change lane and a review lane hold no delivery history in their packs, and a read of it by name is refused like a kind nobody named, however much the project has finished", checkOtherLanesReadNoHistory},
}

// readyPlan records ready planning work.
func (r *Rig) readyPlan(s ports.Store) (string, error) {
	ops := r.NewOps(s)
	made, err := ops.Record(r.Ctx, r.Scope, control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: r.ID("plan-wo"), Kind: workorders.KindPlan, Title: "Plan it", RepositoryID: AcceptanceRepository, MaxAttempts: 1,
			Needs: map[string]string{"driver": "claude-code"},
		},
		Statement: "Plan the work.",
		Criteria:  criteriaOf(criterionOne),
	}, r.Actor)
	if err != nil {
		return "", err
	}
	if _, err := ops.Pass(r.Ctx, r.Scope, made.ID, r.Actor); err != nil {
		return "", err
	}
	return made.ID, nil
}

// historyItem finds a pack's delivery history among its items.
func historyItem(pack contextpacks.Pack) (contextpacks.Item, bool) {
	for _, item := range pack.Items {
		if item.Kind == contextpacks.KindDeliveryHistory {
			return item, true
		}
	}
	return contextpacks.Item{}, false
}

func checkPlanningPackHoldsHistory(r *Rig) error {
	s := r.Store()
	// With no finished work the lane still reads that there is none, so it never mistakes a missing item for a missing
	// history.
	empty, err := newStatusRigOn(r, s, r.readyPlan)
	if err != nil {
		return err
	}
	pack, err := r.packOf(s, empty.grant.ClaimID)
	if err != nil {
		return err
	}
	item, held := historyItem(pack)
	if !held || item.Ref != "project" || item.Trust != contextpacks.TrustWorkspace || pack.Items[len(pack.Items)-1] != item {
		return Violatedf("a planning pack with no finished work holds %v", itemRefs(pack))
	}
	if !strings.Contains(pack.Text, "No work has finished in this project yet") {
		return Violatedf("the planning pack does not say there is no history:\n%s", pack.Text)
	}

	// Finished work enters the history once the delivery fold has kept its record.
	if err := r.finishLikeThePlans(s); err != nil {
		return err
	}
	plan, err := newStatusRigOn(r, s, r.readyPlan)
	if err != nil {
		return err
	}
	if pack, err = r.packOf(s, plan.grant.ClaimID); err != nil {
		return err
	}
	if item, held = historyItem(pack); !held || pack.Items[len(pack.Items)-1] != item || item.Indexed {
		return Violatedf("a planning pack holds %v", itemRefs(pack))
	}
	for _, want := range []string{
		"### By kind and size", "### By kind, size, capability class, and repository", "- change, unknown: 1 finished;",
		"Low confidence. Based on 1 finished change of unknown size; a confident estimate needs 10.",
	} {
		if !strings.Contains(pack.Text, want) {
			return Violatedf("the planning pack does not say %q:\n%s", want, pack.Text)
		}
	}
	// The lane reads it by name as it stands, and it is the same figures the pack holds.
	found, err := plan.lookup(contextpacks.KindDeliveryHistory, "project")
	if err != nil {
		return err
	}
	if err := oneRecord(found, contextpacks.KindDeliveryHistory, "project", "- change, unknown: 1 finished;"); err != nil {
		return err
	}
	return r.CheckPacks(s)
}

// readyReview leaves a change's review ready for a lane to claim.
func (r *Rig) readyReview(s ports.Store) (string, error) {
	id, err := r.AwaitingReview(s, []string{criterionOne}, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true)))
	if err != nil {
		return "", err
	}
	reviews, err := r.reviewsOf(s, id)
	if err != nil || len(reviews) != 1 {
		return "", Violatedf("the change's reviews are %+v: %v", reviews, err)
	}
	return reviews[0].ID, nil
}

func checkOtherLanesReadNoHistory(r *Rig) error {
	s := r.Store()
	// A project with finished work, so there is history to leak.
	if err := r.finishLikeThePlans(s); err != nil {
		return err
	}
	for name, ready := range map[string]func(ports.Store) (string, error){"change": r.Ready, "review": r.readyReview} {
		lane, err := newStatusRigOn(r, s, ready)
		if err != nil {
			return err
		}
		pack, err := r.packOf(s, lane.grant.ClaimID)
		if err != nil {
			return err
		}
		if _, held := historyItem(pack); held || strings.Contains(pack.Text, "Delivery history") || strings.Contains(pack.Text, string(contextpacks.KindDeliveryHistory)) {
			return Violatedf("the %s lane's pack holds delivery history: %v\n%s", name, itemRefs(pack), pack.Text)
		}
		if found, err := lane.lookup(contextpacks.KindDeliveryHistory, "project"); !errors.Is(err, workorders.ErrInvalid) || len(found.Records) != 0 {
			return Violatedf("the %s lane read the delivery history: %+v, %v", name, found, err)
		}
		if _, err := lane.lookup(contextpacks.Kind("nobody_named_this"), "project"); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("a kind nobody named read %v", err)
		}
		if _, err := lane.laneRead(func(request *ledger.LaneContextRequest) {
			request.Tool, request.Kind = "context_get", "delivery_history"
		}); !errors.Is(err, workorders.ErrInvalid) {
			return Violatedf("the %s lane's read by tool name read %v", name, err)
		}
	}
	return nil
}
