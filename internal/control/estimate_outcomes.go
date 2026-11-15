package control

import (
	"errors"
	"slices"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// workOutcome keeps the estimate outcome of a work order whose delivery record the fold kept (FR-EST-11): its
// record against the estimate its baseline holds, when the baseline was recorded before the work ended, and then
// settles the outcome of the milestone it belongs to. Work with no baseline, or one recorded only after it ended,
// which the baseline could not have foretold, has no outcome.
func workOutcome(tx ports.FoldTx, rows ports.DeliveryWriter, record delivery.Record) error {
	baseline, e, err := tx.Estimates().BaselineOf(record.WorkOrder)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		err = rows.ForgetOutcome(delivery.OutcomeOfWorkOrder, "", record.WorkOrder)
	case err != nil:
		return err
	case baseline.CreatedAt > record.Ended:
		err = rows.ForgetOutcome(delivery.OutcomeOfWorkOrder, "", record.WorkOrder)
	default:
		err = rows.SaveOutcome(delivery.WorkOutcome(baseline.ID, e, record))
	}
	if err != nil {
		return err
	}
	return milestoneOutcome(tx, rows, record.Plan, record.Milestone)
}

// milestoneOutcome keeps, or drops, the estimate outcome of one milestone of a plan (FR-EST-11). Its baseline is the
// first of the plan's baselines whose plan version lists it, and it compares only with the forecast that baseline
// holds of it: one recorded while the forecast was pending has none, so the milestone has no outcome. The milestone
// ended when every `change` and `rework` work order of that version's contracts that names it has ended and the
// fold has kept its record; until then, and when the baseline was recorded only after it ended, it has no outcome.
func milestoneOutcome(tx ports.FoldTx, rows ports.DeliveryWriter, plan, milestone string) error {
	if plan == "" || milestone == "" {
		return nil
	}
	forget := func() error { return rows.ForgetOutcome(delivery.OutcomeOfMilestone, plan, milestone) }
	baseline, version, found, err := milestoneBaseline(tx, plan, milestone)
	if err != nil {
		return err
	}
	if !found {
		return forget()
	}
	forecast, shown := baseline.ForecastOf(milestone)
	if !shown {
		return forget()
	}
	orders, err := planWork(tx.WorkOrders(), version)
	if err != nil {
		return err
	}
	var ends []delivery.Record
	seen := map[string]bool{}
	for _, order := range orders {
		if (order.Kind != workorders.KindChange && order.Kind != workorders.KindRework) || seen[order.ID] {
			continue
		}
		seen[order.ID] = true
		contract, err := tx.Planning().Contract(order.ContractID, order.ContractVersion)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && contract.Milestone != milestone) {
			continue
		}
		if err != nil {
			return err
		}
		if !workorders.Terminal(order.State) {
			return forget()
		}
		record, err := rows.Record(order.ID)
		if errors.Is(err, ports.ErrNotFound) {
			return forget()
		}
		if err != nil {
			return err
		}
		ends = append(ends, record)
	}
	if len(ends) == 0 {
		return forget()
	}
	outcome := delivery.MilestoneOutcome(baseline.ID, forecast, ends)
	if baseline.CreatedAt > outcome.Ended {
		return forget()
	}
	return rows.SaveOutcome(outcome)
}

// milestoneBaseline finds the first baseline of a plan whose plan version lists a milestone, with that version.
func milestoneBaseline(tx ports.FoldTx, plan, milestone string) (delivery.Baseline, planning.PlanVersion, bool, error) {
	baselines, err := tx.Estimates().Baselines(plan)
	if err != nil {
		return delivery.Baseline{}, planning.PlanVersion{}, false, err
	}
	for _, baseline := range baselines {
		version, err := tx.Planning().PlanVersion(plan, baseline.PlanVersion)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return delivery.Baseline{}, planning.PlanVersion{}, false, err
		}
		if slices.Contains(milestonesOf(version), milestone) {
			return baseline, version, true, nil
		}
	}
	return delivery.Baseline{}, planning.PlanVersion{}, false, nil
}
