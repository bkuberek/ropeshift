package contract

import (
	"errors"
	"strings"

	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// PlanningViewChecks hold what a planning lane sees of delivery estimates, and what nobody else does (FR-EST-12):
// its pack holds the project's delivery history, and no other lane or orchestrator reads an estimate, a forecast,
// or history.
var PlanningViewChecks = []Check{
	{"PV1", "a planning lane's pack holds the project's delivery history last, aggregated with labels and explanations, and the lane reads it by name", checkPlanningPackHoldsHistory},
	{"PV3", "once the forecast tick has forecast a plan version a planning lane submitted, the lane's open session is sent its estimates and forecasts with their labels and explanations once, and no other lane and no ended session is sent one", checkPlanningLaneIsToldItsVersion},
	{"PV4", "a change's whole path from claim to merge, over a store whose transactions cannot reach an estimate, a forecast, the delivery records, or the pool, never reaches one", checkNoDecisionReadsAnEstimate},
	{"PV5", "a planning lane's claim, its pack, and its read of the history reach the delivery records of the project and no estimate, forecast, or pool, so the history rests on the project's own work alone", checkPlanningHistoryReadsNothingBeyondTheRecords},
	{"PV6", "a lease that lapses, and its work order's return to ready, read no estimate", blindly(checkLapseNotTransfer)},
	{"PV7", "an admission refused by the work-in-progress limit and the lane caps reads no estimate", blindly(checkEligibility)},
	{"PV8", "ready work offered to a worker is ordered by priority and reads no estimate", blindly(checkPriorityReadsNoEstimate)},
	{"PV9", "a member's approval of a merge, bound to the reviewed commit, reads no estimate", blindly(checkMemberApprovesTheMerge)},
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

// versionFromLane records a newer version of a plan as the plan work order's lane submitted it: the plan's newest
// version again, from that work order.
func (r *Rig) versionFromLane(s ports.Store, plan, workOrder string) (planning.PlanVersion, error) {
	var next planning.PlanVersion
	err := s.Write(r.Ctx, r.Scope, "plan.version_from_lane", func(tx ports.WriteTx) error {
		latest, err := tx.Planning().LatestPlanVersion(plan)
		if err != nil {
			return err
		}
		next = latest
		next.Version, next.Digest, next.WorkOrder = latest.Version+1, r.ID("sha256:version"), workOrder
		if _, err := tx.Append(r.noteDraft(r.ID("note"))); err != nil {
			return err
		}
		return tx.Planning().InsertPlanVersion(next)
	})
	return next, err
}

// toldOf reads what a lane was sent.
func (r *Rig) toldOf(s ports.Store, lane string) ([]messages.Message, error) {
	return control.NewMessages(s, nil).LaneMessages(r.Ctx, r.Scope, lane)
}

func checkPlanningLaneIsToldItsVersion(r *Rig) error {
	s := r.Store()
	files := newApprovalSource()
	files.source.Write("m1.md", []byte(milestoneOf("m1", approvalPlan, 1)))
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Milestone: "m1"}.text()))
	if report, err := r.intake(s, files.source); err != nil || len(report.Problems) != 0 {
		return Violatedf("intake read %+v: %v", report, err)
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	ids, err := r.ordersByFileID(s, "budget")
	if err != nil {
		return err
	}
	// A planning lane runs, and a change lane runs beside it.
	planner, err := newStatusRigOn(r, s, r.readyPlan)
	if err != nil {
		return err
	}
	changer, err := newStatusRigOn(r, s, r.Ready)
	if err != nil {
		return err
	}
	// Version 1 came from files, so no lane submitted it and nobody is told of it.
	if err := r.forecastRuns(s, 1, "after intake"); err != nil {
		return err
	}
	if told, err := r.toldOf(s, planner.lane); err != nil || len(told) != 0 {
		return Violatedf("the planning lane was told of a version no lane submitted: %+v, %v", told, err)
	}

	version, err := r.versionFromLane(s, plan, planner.grant.WorkOrder)
	if err != nil {
		return err
	}
	if told, err := r.toldOf(s, planner.lane); err != nil || len(told) != 0 {
		return Violatedf("the planning lane was told before the tick forecast the version: %+v, %v", told, err)
	}
	if err := r.forecastRuns(s, 1, "after the lane's version"); err != nil {
		return err
	}
	told, err := r.toldOf(s, planner.lane)
	if err != nil || len(told) != 1 {
		return Violatedf("the planning lane was told %+v: %v", told, err)
	}
	notice := told[0]
	if notice.Kind != messages.KindNotice || notice.To.Lane != planner.lane || notice.To.Generation != 1 || notice.State != messages.Queued || notice.From.Kind != string(ledger.ActorSystem) {
		return Violatedf("the notice reads %+v", notice)
	}
	for _, want := range []string{
		"Plan version 2 of Retry budget", "### Forecasts", "- plan: ", "- milestone m1: ", "### Estimates", "- " + ids["budget-order"] + " ",
		"No estimate: no", "you never write an estimate",
	} {
		if !strings.Contains(notice.Body, want) {
			return Violatedf("the notice does not say %q:\n%s", want, notice.Body)
		}
	}
	if told, err := r.toldOf(s, changer.lane); err != nil || len(told) != 0 {
		return Violatedf("a change lane was told of a plan version: %+v, %v", told, err)
	}

	// A tick that forecasts nothing new tells nobody again, and neither does one that forecasts the version again.
	if err := r.forecastRuns(s, 0, "with nothing new"); err != nil {
		return err
	}
	if err := r.finishLikeThePlans(s); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after work like the plan's finished"); err != nil {
		return err
	}
	if told, err := r.toldOf(s, planner.lane); err != nil || len(told) != 1 || told[0].ID != notice.ID {
		return Violatedf("the planning lane was told again: %+v, %v", told, err)
	}

	// A version recorded from the work of any other kind is told to no lane, whatever else holds it.
	if _, err := r.versionFromLane(s, plan, changer.grant.WorkOrder); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "for a version from a change's work"); err != nil {
		return err
	}
	if told, err := r.toldOf(s, changer.lane); err != nil || len(told) != 0 {
		return Violatedf("a change lane was told of a version recorded from its work: %+v, %v", told, err)
	}

	// Its session ends, so a later version it submitted has nobody to tell, and records no message that expires.
	holder := workorders.Holder{Lease: planner.grant.Lease, Lane: planner.lane, Generation: 1}
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, LaneFinished(r.ID("commit"), LaneCheck(criterionOne, true)), ledger.Actor{Kind: ledger.ActorWorker, ID: planner.auth.WorkerID}); err != nil {
		return err
	}
	if _, err := r.versionFromLane(s, plan, planner.grant.WorkOrder); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after the lane ended"); err != nil {
		return err
	}
	if told, err := r.toldOf(s, planner.lane); err != nil || len(told) != 1 {
		return Violatedf("a lane whose session ended was told %+v: %v", told, err)
	}
	_ = version
	return nil
}

// makers are exactly the transactions that make an estimate, and so read the delivery records by right, found by where
// the estimates are made: a work order created, or recorded as ledger work, a contract of no plan that intake reads, the
// plan version intake records, and the rework an overturned decision makes. A retry, a contract review of a plan, and
// intake's settling and proposing make none, so they are not here and read none.
func makers(op string) bool {
	switch op {
	case "work_order.create", "work_order.record", "intake.item", "intake.plan_version", "decision.overturn":
		return true
	}
	return false
}

// blindly runs a check over a store whose decisions cannot reach an estimate, a forecast, the delivery records, or
// the pool, and fails it if any did.
func blindly(check func(*Rig) error) func(*Rig) error {
	return func(r *Rig) error {
		var blind blindStore
		r.Wrap = func(store ports.Store) ports.Store {
			blind = blinded(store, makers)
			return blind
		}
		if err := check(r); err != nil {
			return Violatedf("with no estimate to read: %v (reached: %v)", err, blind.reached())
		}
		if reads := blind.reached(); len(reads) != 0 {
			return Violatedf("a decision reached an estimate: %v", reads)
		}
		return nil
	}
}

var checkNoDecisionReadsAnEstimate = blindly(checkMergeAfterTheReview)

// checkPriorityReadsNoEstimate offers a worker several ready orders and reads their order, which is priority and
// never a band.
func checkPriorityReadsNoEstimate(r *Rig) error {
	s, err := newStatusRigOn(r, r.Store(), r.Ready)
	if err != nil {
		return err
	}
	var want []string
	for _, priority := range []int64{1, 3, 2} {
		made, err := r.NewOps(s.store).Record(r.Ctx, r.Scope, control.LedgerWork{
			Order: workorders.WorkOrder{
				ID: r.ID("ready"), Kind: workorders.KindChange, Title: "Ready work", RepositoryID: AcceptanceRepository, MaxAttempts: 1,
				Priority: priority, Needs: map[string]string{"driver": "claude-code"},
			},
			Statement: "Do the work.",
			Criteria:  criteriaOf(criterionOne),
		}, r.Actor)
		if err != nil {
			return err
		}
		if _, err := r.NewOps(s.store).Pass(r.Ctx, r.Scope, made.ID, r.Actor); err != nil {
			return err
		}
		want = append(want, made.ID)
	}
	offered, err := s.plane.Offered(r.Ctx, s.auth.WorkerID)
	if err != nil {
		return err
	}
	var got []string
	for _, order := range offered {
		got = append(got, order.ID)
	}
	if len(got) != 3 || got[0] != want[1] || got[1] != want[2] || got[2] != want[0] {
		return Violatedf("the worker is offered %v, want priority order %v", got, []string{want[1], want[2], want[0]})
	}
	return nil
}

func checkPlanningHistoryReadsNothingBeyondTheRecords(r *Rig) error {
	// The history is there to read before the store is blinded, so only what the lane's own transactions reach shows.
	if err := r.finishLikeThePlans(r.Store()); err != nil {
		return err
	}
	var blind blindStore
	r.Wrap = func(store ports.Store) ports.Store {
		blind = blindedToEstimates(store, makers)
		return blind
	}
	s := r.Store()
	plan, err := newStatusRigOn(r, s, r.readyPlan)
	if err != nil {
		return err
	}
	if _, err := plan.lookup(contextpacks.KindDeliveryHistory, "project"); err != nil {
		return err
	}
	pack, err := r.packOf(s, plan.grant.ClaimID)
	if err != nil {
		return err
	}
	if _, held := historyItem(pack); !held {
		return Violatedf("the planning pack holds %v", itemRefs(pack))
	}
	if reads := blind.reached(); len(reads) != 0 {
		return Violatedf("the planning lane's history reached more than the project's delivery records: %v", reads)
	}
	return nil
}
