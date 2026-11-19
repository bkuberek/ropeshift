package contract

import (
	"reflect"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// OutcomeChecks hold estimate outcomes (ADR-0026, FR-EST-11): each baselined work order and milestone that ended,
// against its baseline, as the delivery fold derives them.
var OutcomeChecks = []Check{
	{"EO1", "when baselined work ends the delivery fold writes its outcome against its baseline, failed work compares nothing until a retry drops it, and a refold writes the same outcomes", checkWorkOutcomes},
	{"EO2", "a milestone's outcome compares when its last work ended with the forecast of the first baseline that covered it, and one first covered while its forecast was pending has none", checkMilestoneOutcomes},
}

// outcomes reads every estimate outcome the fold keeps, by subject.
func (r *Rig) outcomes(s ports.Store) (map[string]delivery.Outcome, error) {
	out := map[string]delivery.Outcome{}
	err := s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		all, err := tx.Deliveries().Outcomes()
		for _, o := range all {
			out[string(o.SubjectKind)+":"+o.Plan+":"+o.Subject] = o
		}
		return err
	})
	return out, err
}

func workKey(id string) string { return string(delivery.OutcomeOfWorkOrder) + "::" + id }
func milestoneKey(plan, id string) string {
	return string(delivery.OutcomeOfMilestone) + ":" + plan + ":" + id
}

// comparesNothing says an outcome holds no comparison.
func comparesNothing(o delivery.Outcome) bool {
	return o.TimeAtOrBelowP50 == nil && o.TimeAtOrBelowP85 == nil && o.TimeRatio == nil && o.CostAtOrBelowP50 == nil &&
		o.CostAtOrBelowP85 == nil && o.CostRatio == nil
}

// outcomeFor checks a work order's outcome is its delivery record against its baseline estimate.
func (r *Rig) outcomeFor(s ports.Store, id string, outcomes map[string]delivery.Outcome) (delivery.Outcome, error) {
	o, held := outcomes[workKey(id)]
	if !held {
		return o, Violatedf("work %s that ended has no outcome among %+v", id, outcomes)
	}
	baseline, e, err := r.baselineOf(s, id)
	if err != nil {
		return o, err
	}
	_, record, _, err := r.stored(s, id)
	if err != nil || record == nil {
		return o, Violatedf("work %s has no record: %v", id, err)
	}
	if want := delivery.WorkOutcome(baseline.ID, e, *record); !reflect.DeepEqual(o, want) {
		return o, Violatedf("work %s has outcome %+v, not %+v", id, o, want)
	}
	return o, nil
}

func checkWorkOutcomes(r *Rig) error {
	s := r.Store()
	fold := control.NewDeliveryFold(s)

	// Work made outside a plan with no history ends: its outcome holds its baseline's estimate, which has none, and
	// compares nothing.
	first, err := r.finished(s)
	if err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	outcomes, err := r.outcomes(s)
	if err != nil {
		return err
	}
	o, err := r.outcomeFor(s, first, outcomes)
	if err != nil {
		return err
	}
	if o.Confidence != "" || o.Outcome != workorders.Done || !comparesNothing(o) || o.Baseline == "" || o.Estimate == "" {
		return Violatedf("work with no history has outcome %+v", o)
	}

	// Work like it, made once the fold kept its record, has a low estimate with an open band, and its outcome
	// compares its p50 and its ratio and leaves its p85 null.
	r.DB.ShiftClock(1_000)
	second, err := r.finished(s)
	if err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	if outcomes, err = r.outcomes(s); err != nil {
		return err
	}
	if o, err = r.outcomeFor(s, second, outcomes); err != nil {
		return err
	}
	if o.Confidence != delivery.Low || !o.P85Open || o.TimeAtOrBelowP50 == nil || o.TimeAtOrBelowP85 != nil || o.CostAtOrBelowP85 != nil {
		return Violatedf("work with one comparable record has outcome %+v", o)
	}

	// Failed work is an outcome that compares nothing, and a retry drops it until the work ends again.
	failed, err := r.Ready(s)
	if err != nil {
		return err
	}
	holder, err := r.Running(s, failed, "a")
	if err != nil {
		return err
	}
	if err := r.NewOps(s).EndLane(r.Ctx, r.Scope, holder, laneResult(workorders.OutcomeFailed, workorders.ReasonCrashed), r.worker("a")); err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	if outcomes, err = r.outcomes(s); err != nil {
		return err
	}
	if o, err = r.outcomeFor(s, failed, outcomes); err != nil {
		return err
	}
	if o.Outcome != workorders.Failed || !comparesNothing(o) {
		return Violatedf("failed work has outcome %+v", o)
	}
	if _, err := control.NewRecoveries(s).Retry(r.Ctx, r.Scope, failed, "the network came back", theMember); err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	if outcomes, err = r.outcomes(s); err != nil {
		return err
	}
	if o, held := outcomes[workKey(failed)]; held {
		return Violatedf("retried work kept outcome %+v", o)
	}

	// A new derivation version drops every outcome, an earlier version's included, folds the log again, and writes
	// the same outcomes.
	err = s.Fold(r.Ctx, r.Scope, func(tx ports.FoldTx) error {
		rows := tx.Delivery()
		stale := delivery.Outcome{SubjectKind: delivery.OutcomeOfWorkOrder, Subject: r.ID("gone"), Outcome: workorders.Done}
		if err := rows.SaveOutcome(stale); err != nil {
			return err
		}
		state, err := rows.FoldState()
		if err != nil {
			return err
		}
		state.Version--
		return rows.SaveFoldState(state)
	})
	if err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	refolded, err := r.outcomes(s)
	if err != nil || !reflect.DeepEqual(refolded, outcomes) {
		return Violatedf("a refold wrote outcomes %+v, not %+v: %v", refolded, outcomes, err)
	}
	return nil
}

// finishPlanned runs a plan's ready work order to done.
func (r *Rig) finishPlanned(s ports.Store, id string) error {
	if err := r.anotherAttempt(s, id, LaneFinished(r.Commit(), LaneCheck(criterionOne, true))); err != nil {
		return err
	}
	_, err := r.NewOps(s).Accept(r.Ctx, r.Scope, id, r.Actor)
	return err
}

func checkMilestoneOutcomes(r *Rig) error {
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

	// Version 1 is approved while its forecast is pending, so m1's first baseline holds no forecast of it.
	first, err := r.theOpenPlanApprovalFor(s, "Retry budget")
	if err != nil {
		return err
	}
	if _, err := r.approve(s, first, true, r.Actor); err != nil {
		return err
	}

	// Version 2 adds m2 and its work. Once work like the plan's has finished and a worker serves the project, the
	// tick forecasts both milestones with dates, and approving version 2 records those forecasts.
	files.source.Write("m2.md", []byte(milestoneOf("m2", approvalPlan, 2)))
	files.source.Write("extra.md", []byte(placedContract{ID: "extra", Plan: approvalPlan, Milestone: "m2"}.text()))
	if report, err := r.intake(s, files.source); err != nil || len(report.Problems) != 0 {
		return Violatedf("intake read version 2 as %+v: %v", report, err)
	}
	if err := r.registerWorker(s); err != nil {
		return err
	}
	if err := r.finishLikeThePlans(s); err != nil {
		return err
	}
	if err := r.forecastRuns(s, 1, "after version 2"); err != nil {
		return err
	}
	second, err := r.theOpenPlanApprovalFor(s, "Retry budget")
	if err != nil || second.PlanVersion != 2 {
		return Violatedf("the request for version 2 is %+v: %v", second, err)
	}
	if _, err := r.approve(s, second, true, r.Actor); err != nil {
		return err
	}
	baselines, err := r.baselinesOf(s, plan)
	if err != nil || len(baselines) != 2 {
		return Violatedf("the plan's baselines are %+v: %v", baselines, err)
	}
	m2, shown := baselines[1].ForecastOf("m2")
	if !shown || m2.Status != delivery.Forecasted {
		return Violatedf("version 2's baseline holds %+v", baselines[1].Forecasts)
	}

	// Each milestone's work ends. m2 is measured against version 2's forecast of it; m1, first covered by version
	// 1's baseline, which showed no forecast, has no outcome, though its work has one.
	budget, err := r.orderOfContract(s, "budget")
	if err != nil {
		return err
	}
	extra, err := r.orderOfContract(s, "extra")
	if err != nil {
		return err
	}
	fold := control.NewDeliveryFold(s)
	if err := r.finishPlanned(s, extra.ID); err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	outcomes, err := r.outcomes(s)
	if err != nil {
		return err
	}
	o, held := outcomes[milestoneKey(plan, "m2")]
	if !held {
		return Violatedf("m2, whose work ended, has no outcome among %+v", outcomes)
	}
	_, record, _, err := r.stored(s, extra.ID)
	if err != nil || record == nil {
		return Violatedf("m2's work has no record: %v", err)
	}
	if want := delivery.MilestoneOutcome(baselines[1].ID, m2, []delivery.Record{*record}); !reflect.DeepEqual(o, want) || o.TimeAtOrBelowP50 == nil {
		return Violatedf("m2 has outcome %+v, not %+v", o, want)
	}
	if _, err := r.outcomeFor(s, extra.ID, outcomes); err != nil {
		return err
	}
	if err := r.finishPlanned(s, budget.ID); err != nil {
		return err
	}
	if _, err := fold.Fold(r.Ctx, r.Scope); err != nil {
		return err
	}
	if outcomes, err = r.outcomes(s); err != nil {
		return err
	}
	if o, held := outcomes[milestoneKey(plan, "m1")]; held {
		return Violatedf("m1, first baselined with no forecast, has outcome %+v", o)
	}
	o, err = r.outcomeFor(s, budget.ID, outcomes)
	if err != nil {
		return err
	}
	if o.Baseline != baselines[0].ID {
		return Violatedf("m1's work is measured against baseline %s, not version 1's %s", o.Baseline, baselines[0].ID)
	}
	return nil
}
