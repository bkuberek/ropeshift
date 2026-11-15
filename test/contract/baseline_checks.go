package contract

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// BaselineChecks hold baselines (ADR-0026, FR-EST-10): what a plan approval showed of its version's estimates and
// forecasts, recorded in the approval's transaction, and the estimates work made outside a plan was made with.
var BaselineChecks = []Check{
	{"BL1", "approving a plan version records the estimate set and the forecasts it showed as its baseline, with no forecast while the forecast was pending, and a work order's first baseline stays its own", checkBaselineAtApproval},
	{"BL2", "work made outside a plan records its baseline when it is made, with no approval and no event of its own", checkBaselineAtCreation},
	{"BL3", "approving a planning lane's branch before intake records it records a baseline with no estimates and no forecast, and the set intake makes afterwards is no baseline", checkBaselineOfABranchApproval},
}

// baselinesOf reads every baseline of a plan, and baselineOf a work order's with the estimate it holds.
func (r *Rig) baselinesOf(s ports.Store, plan string) ([]delivery.Baseline, error) {
	var out []delivery.Baseline
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Estimates().Baselines(plan)
		return err
	})
	return out, err
}

func (r *Rig) baselineOf(s ports.Store, order string) (delivery.Baseline, delivery.Estimate, error) {
	var baseline delivery.Baseline
	var e delivery.Estimate
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		baseline, e, err = tx.Estimates().BaselineOf(order)
		return err
	})
	return baseline, e, err
}

// baselineEvents reads the events that recorded baselines.
func (r *Rig) baselineEvents(s ports.Store) ([]ledger.Event, error) {
	events, err := r.Events(s, r.Scope)
	return slices.DeleteFunc(events, func(e ledger.Event) bool { return e.Type != delivery.EventBaselined }), err
}

// shownBy checks a baseline holds what an approval showed: the set and forecasts given, at the revision of the one
// event that recorded it, about it, as the member.
func (r *Rig) shownBy(s ports.Store, b delivery.Baseline, approval authority.Approval, set *delivery.EstimateSet, forecasts []delivery.Forecast) error {
	wantSet, wantDigest := "", ""
	if set != nil {
		wantSet, wantDigest = set.ID, set.Digest
	}
	if b.ID == "" || b.Approval != approval.ID || b.Plan != approval.Plan || b.PlanVersion != approval.PlanVersion || b.SetID != wantSet || b.SetDigest != wantDigest ||
		!reflect.DeepEqual(b.Forecasts, forecasts) || b.CreatedAt == 0 {
		return Violatedf("approval %s of version %d recorded baseline %+v, not set %q with forecasts %+v", approval.ID, approval.PlanVersion, b, wantSet, forecasts)
	}
	events, err := r.baselineEvents(s)
	if err != nil {
		return err
	}
	var said []ledger.Event
	for _, e := range events {
		if e.Subject.ID == b.ID {
			said = append(said, e)
		}
	}
	if len(said) != 1 || said[0].Rev != b.Rev || said[0].Subject.Kind != delivery.SubjectBaseline || said[0].Actor != r.Actor {
		return Violatedf("baseline %s at revision %d was recorded by %+v", b.ID, b.Rev, said)
	}
	var data struct {
		Approval  string   `json:"approval_id"`
		SetID     string   `json:"estimate_set_id"`
		SetDigest string   `json:"estimate_set_digest"`
		Forecasts []string `json:"forecast_ids"`
	}
	if err := json.Unmarshal(said[0].Data, &data); err != nil {
		return err
	}
	ids := []string{}
	for _, f := range forecasts {
		ids = append(ids, f.ID)
	}
	if data.Approval != approval.ID || data.SetID != wantSet || data.SetDigest != wantDigest || !slices.Equal(data.Forecasts, ids) {
		return Violatedf("the event of baseline %s says %+v", b.ID, data)
	}
	return nil
}

func checkBaselineAtApproval(r *Rig) error {
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
	budget, held, err := r.contractTitled(s, "budget")
	if err != nil || !held {
		return Violatedf("the contract: %v", err)
	}
	planned, err := r.ordersOfContract(s, budget.ID)
	if err != nil || len(planned) != 1 {
		return Violatedf("the plan's work %v: %v", planned, err)
	}
	shown, intakeSet, err := r.currentOf(s, planned[0])
	if err != nil {
		return err
	}

	// Before the forecast tick forecasts the version, its approval shows the version's estimates and "forecast
	// pending", and records a baseline of that set with no forecast.
	first, err := r.theOpenPlanApprovalFor(s, "Retry budget")
	if err != nil {
		return err
	}
	if _, err := r.approve(s, first, true, r.Actor); err != nil {
		return err
	}
	baselines, err := r.baselinesOf(s, plan)
	if err != nil || len(baselines) != 1 {
		return Violatedf("after the first approval, the plan's baselines are %+v: %v", baselines, err)
	}
	if err := r.shownBy(s, baselines[0], first, &intakeSet, []delivery.Forecast{}); err != nil {
		return err
	}
	baseline, e, err := r.baselineOf(s, planned[0])
	if err != nil || baseline.ID != baselines[0].ID || e.ID != shown.ID || e.Explanation != shown.Explanation {
		return Violatedf("the plan's work has baseline %+v with %+v, not %+v: %v", baseline, e, shown, err)
	}

	// A retry of the approval records nothing more.
	if _, err := r.approve(s, first, true, r.Actor); err != nil {
		return err
	}
	if again, err := r.baselinesOf(s, plan); err != nil || len(again) != 1 {
		return Violatedf("after a retry, the plan's baselines are %+v: %v", again, err)
	}

	// A version that adds work asks for approval again. Once the tick has forecast it, its approval shows those
	// forecasts and the set they were made from, and records them.
	files.source.Write("budget.md", []byte(placedContract{ID: "budget", Plan: approvalPlan, Milestone: "m1", Orders: []orderIn{{ID: "budget-order"}, {ID: "budget-more", Size: "large"}}}.text()))
	if _, err := r.intake(s, files.source); err != nil {
		return err
	}
	if planned, err = r.ordersOfContract(s, budget.ID); err != nil || len(planned) != 2 {
		return Violatedf("the plan's work after a change %v: %v", planned, err)
	}
	if err := r.forecastRuns(s, 1, "after version 2"); err != nil {
		return err
	}
	latest, _, _, err := r.forecastsOf(s, plan, 2, "m1")
	if err != nil || len(latest) != 2 {
		return Violatedf("version 2's forecasts are %+v: %v", latest, err)
	}
	forecastSet, _, err := r.estimates(s, latest[0].SetID)
	if err != nil {
		return err
	}
	second, err := r.theOpenPlanApprovalFor(s, "Retry budget")
	if err != nil || second.PlanVersion != 2 {
		return Violatedf("the request for version 2 is %+v: %v", second, err)
	}
	if _, err := r.approve(s, second, true, r.Actor); err != nil {
		return err
	}
	if baselines, err = r.baselinesOf(s, plan); err != nil || len(baselines) != 2 || baselines[0].ID == baselines[1].ID {
		return Violatedf("after the second approval, the plan's baselines are %+v: %v", baselines, err)
	}
	if err := r.shownBy(s, baselines[1], second, &forecastSet, latest); err != nil {
		return err
	}

	// The work version 1 covered keeps its first baseline, and the work version 2 added takes the second's.
	for _, order := range planned {
		baseline, e, err := r.baselineOf(s, order)
		if err != nil {
			return err
		}
		want := baselines[1].ID
		if order == shown.WorkOrder {
			want = baselines[0].ID
		}
		if baseline.ID != want || e.WorkOrder != order || e.SetID != baseline.SetID {
			return Violatedf("work %s has baseline %s with %+v, not %s", order, baseline.ID, e, want)
		}
	}

	// A forecast the tick keeps later the same day replaces the day's, and the baseline keeps what it showed.
	if err := r.registerWorker(s); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "once a worker serves the project"); err != nil {
		return err
	}
	moved, _, _, err := r.forecastsOf(s, plan, 2, "m1")
	if err != nil || moved[0].ID == latest[0].ID || moved[0].Day != latest[0].Day {
		return Violatedf("the day's forecast is now %+v, after %+v: %v", moved, latest, err)
	}
	if baselines, err = r.baselinesOf(s, plan); err != nil || !reflect.DeepEqual(baselines[1].Forecasts, latest) {
		return Violatedf("after the day's forecast moved, the baseline holds %+v: %v", baselines, err)
	}
	return r.CheckProjection(s)
}

func checkBaselineAtCreation(r *Rig) error {
	s := r.Store()
	before, err := r.baselineEvents(s)
	if err != nil {
		return err
	}
	work := control.LedgerWork{
		Order:     workorders.WorkOrder{ID: r.ID("wo-open"), Kind: workorders.KindChange, Title: "Do it later", RepositoryID: AcceptanceRepository, MaxAttempts: 1},
		Statement: "Do the work later.",
		Criteria:  []planning.Criterion{{ID: criterionOne, Text: "It holds: " + criterionOne}},
	}
	created, err := r.NewOps(s).Record(r.Ctx, r.Scope, work, r.Actor)
	if err != nil {
		return err
	}
	made, set, err := r.currentOf(s, created.ID)
	if err != nil {
		return err
	}
	baseline, e, err := r.baselineOf(s, created.ID)
	if err != nil {
		return err
	}
	if baseline.ID == "" || baseline.Approval != "" || baseline.Plan != "" || baseline.SetID != set.ID || baseline.SetDigest != set.Digest ||
		baseline.Rev != set.Rev || len(baseline.Forecasts) != 0 || !reflect.DeepEqual(e, made) {
		return Violatedf("work made outside a plan has baseline %+v with %+v, made %+v in %+v", baseline, e, made, set)
	}
	after, err := r.baselineEvents(s)
	if err != nil || len(after) != len(before) {
		return Violatedf("making work appended baseline events %+v: %v", after, err)
	}

	// A refresh that estimates the work again keeps a new current estimate, and the baseline stays the first.
	if err := r.finishLikeThePlans(s); err != nil {
		return err
	}
	if _, err := control.NewEstimates(s).Refresh(r.Ctx, r.Scope); err != nil {
		return err
	}
	current, _, err := r.currentOf(s, created.ID)
	if err != nil || current.ID == made.ID {
		return Violatedf("the refresh left the current estimate %+v: %v", current, err)
	}
	if again, e, err := r.baselineOf(s, created.ID); err != nil || again.ID != baseline.ID || e.ID != made.ID {
		return Violatedf("after a refresh, the baseline is %+v with %+v: %v", again, e, err)
	}
	if _, _, err := r.baselineOf(s, r.ID("absent")); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("work with no baseline read: %v", err)
	}
	return nil
}

func checkBaselineOfABranchApproval(r *Rig) error {
	s := r.Store()
	files := laneFiles("The first draft.")
	_, pending, err := r.aLanePlanAsksForApproval(s, files)
	if err != nil {
		return err
	}
	if _, err := r.approve(s, pending, true, theMember); err != nil {
		return err
	}
	events, err := r.baselineEvents(s)
	if err != nil || len(events) != 1 || events[0].Actor != theMember {
		return Violatedf("approving the branch recorded baselines %+v: %v", events, err)
	}
	var asked []string
	if _, err := control.NewIntake(s).TakeInPlans(r.Ctx, r.Scope, openFrom(acceptedSource(files), &asked)); err != nil {
		return err
	}
	plan, err := r.planID(s, "Retry budget")
	if err != nil {
		return err
	}
	order, err := r.orderOfContract(s, "budget")
	if err != nil {
		return err
	}
	if _, _, err := r.currentOf(s, order.ID); err != nil {
		return Violatedf("intake estimated nothing of the branch's work: %v", err)
	}
	if _, _, err := r.baselineOf(s, order.ID); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("the branch's work has a baseline nobody was shown: %v", err)
	}
	if after, err := r.baselineEvents(s); err != nil || len(after) != 1 {
		return Violatedf("intake recorded baselines %+v: %v", after, err)
	}
	baselines, err := r.baselinesOf(s, plan)
	if err != nil {
		return err
	}
	for _, b := range baselines {
		if b.SetID != "" || len(b.Forecasts) != 0 || b.Approval != pending.ID {
			return Violatedf("the branch's approval recorded %+v", b)
		}
	}
	return nil
}
