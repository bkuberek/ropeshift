package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ApprovalShows reads what a plan version's approval shows of its estimates and forecasts (FR-EST-10): the latest
// forecasts the forecast tick kept of the version, with the estimate set they were made from, or, before the tick
// has forecast it, the version's newest set and no forecast, which reads "forecast pending". A version forecast
// with no open work rests on no set, and a version intake has not recorded has neither.
func ApprovalShows(estimates ports.EstimateReader, forecasts ports.ForecastReader, plan string, version int64) (*delivery.EstimateSet, []delivery.Forecast, error) {
	latest, err := forecasts.Latest(plan, version)
	if err != nil {
		return nil, nil, err
	}
	if len(latest) != 0 {
		if latest[0].SetID == "" {
			return nil, latest, nil
		}
		set, err := estimates.Set(latest[0].SetID)
		if err != nil {
			return nil, nil, err
		}
		return &set, latest, nil
	}
	newest, err := estimates.NewestOf(plan, version)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil, latest, nil
	case err != nil:
		return nil, nil, err
	}
	return &newest, latest, nil
}

// baselineApproval records what a plan approval showed as its baseline, in the approval's transaction, with the
// event that says so, as the member who approved (FR-EST-10). An approval that showed "forecast pending" records a
// baseline with no forecast, so its milestones take no forecast outcome. An approval of a planning lane's branch,
// decided before intake records the version, showed no estimate and no forecast, because the ledger held no work
// of the version to estimate, so it records a baseline with neither: its work takes no estimate outcome from it,
// and the set intake makes when it records the version afterwards is no baseline, because nobody was shown it.
func baselineApproval(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	baseline := delivery.Baseline{Approval: approval.ID, Plan: approval.Plan, PlanVersion: approval.PlanVersion, Forecasts: []delivery.Forecast{}, CreatedAt: tx.Now()}
	if approval.Plan != "" {
		set, shown, err := ApprovalShows(tx.Estimates(), tx.Forecasts(), approval.Plan, approval.PlanVersion)
		if err != nil {
			return err
		}
		if set != nil {
			baseline.SetID, baseline.SetDigest = set.ID, set.Digest
		}
		baseline.Forecasts = append(baseline.Forecasts, shown...)
	}
	var err error
	if baseline.ID, err = newID(baseline.CreatedAt); err != nil {
		return err
	}
	draft, err := delivery.BaselineEvent(baseline, member)
	if err != nil {
		return err
	}
	event, err := tx.Append(draft)
	if err != nil {
		return err
	}
	baseline.Rev = event.Rev
	return tx.Estimates().AddBaseline(baseline)
}

// baselineMade records the estimates work made outside a plan was made with as its baseline, in the transaction
// that made it (FR-EST-10). It has no approval, and rides on that transaction's events as the set does.
func baselineMade(tx ports.WriteTx, set delivery.EstimateSet) error {
	baseline := delivery.Baseline{SetID: set.ID, SetDigest: set.Digest, Forecasts: []delivery.Forecast{}, Rev: set.Rev, CreatedAt: set.CreatedAt}
	var err error
	if baseline.ID, err = newID(set.CreatedAt); err != nil {
		return err
	}
	return tx.Estimates().AddBaseline(baseline)
}
