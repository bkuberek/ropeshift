package delivery

import "ropeshift.com/ropeshift/internal/workorders"

// OutcomeKind names what an estimate outcome measures: a work order against its baseline estimate, or a milestone
// against its baseline forecast.
type OutcomeKind string

// The kinds of estimate outcome.
const (
	OutcomeOfWorkOrder OutcomeKind = "work_order"
	OutcomeOfMilestone OutcomeKind = "milestone"
)

// Outcome is one baselined work order or milestone that ended, against its baseline (ADR-0026, FR-EST-11), as
// calibration reads it. Subject is the work order or the milestone; Plan is a milestone's plan. Baseline names the
// baseline, and Estimate or Forecast what of it the outcome compares with; Confidence, Tier, P85Open, and
// EstimatorVersion are that estimate's or forecast's, Confidence empty for an estimate with no history. Outcome is
// how the subject ended; ActualDeliveryMs, ActualCostUSD, and ActualCostAtLeast are what it took, and ActualEnd when
// it ended, a milestone when its last work order did. Each comparison is null where none holds: for work that did
// not reach `done`, an estimate with no history, and a forecast with no dates; for the p85 of an open band; for a
// cost when the actual cost or the band's is a lower bound; for a ratio over a p50 of zero; and for a forecast's
// date where it is a lower bound. A milestone's outcome compares its date alone.
type Outcome struct {
	SubjectKind       OutcomeKind      `json:"subject_kind"`
	Subject           string           `json:"subject_id"`
	Plan              string           `json:"plan_id,omitempty"`
	Baseline          string           `json:"baseline_id"`
	Estimate          string           `json:"estimate_id,omitempty"`
	Forecast          string           `json:"forecast_id,omitempty"`
	Confidence        Confidence       `json:"confidence,omitempty"`
	Tier              int              `json:"tier,omitempty"`
	P85Open           bool             `json:"p85_open,omitempty"`
	Repository        string           `json:"repository_id,omitempty"`
	Outcome           workorders.State `json:"outcome"`
	ActualDeliveryMs  int64            `json:"actual_delivery_ms"`
	ActualCostUSD     float64          `json:"actual_cost_usd"`
	ActualCostAtLeast bool             `json:"actual_cost_at_least,omitempty"`
	ActualEnd         int64            `json:"actual_end"`
	TimeAtOrBelowP50  *bool            `json:"time_at_or_below_p50"`
	TimeAtOrBelowP85  *bool            `json:"time_at_or_below_p85"`
	TimeRatio         *float64         `json:"time_ratio"`
	CostAtOrBelowP50  *bool            `json:"cost_at_or_below_p50"`
	CostAtOrBelowP85  *bool            `json:"cost_at_or_below_p85"`
	CostRatio         *float64         `json:"cost_ratio"`
	EstimatorVersion  int              `json:"estimator_version,omitempty"`
	Ended             int64            `json:"ended_at"`
}

// WorkOutcome compares a finished work order's delivery record with the estimate its baseline holds (FR-EST-11):
// its delivery time and cost, whether each was at or below the p50 and the p85, and its ratio to the p50. Work that
// ended `failed` or `canceled`, and an estimate with no history, compare nothing. An open band has no p85, so its
// p85 comparisons are null, for time and for cost. A cost compares only when neither the actual cost nor the band's
// is a lower bound, because a lower bound can read as at or below a band it exceeded.
func WorkOutcome(baseline string, e Estimate, r Record) Outcome {
	out := Outcome{
		SubjectKind: OutcomeOfWorkOrder, Subject: r.WorkOrder, Baseline: baseline, Estimate: e.ID, Confidence: e.Confidence, Tier: e.Tier,
		P85Open: e.P85Open, Repository: r.Repository, Outcome: r.Outcome, ActualDeliveryMs: r.DeliveryMs, ActualCostUSD: r.Usage.CostUSD,
		ActualCostAtLeast: r.Usage.LowerBound(), ActualEnd: r.Ended, EstimatorVersion: e.EstimatorVersion, Ended: r.Ended,
	}
	if r.Outcome != workorders.Done || e.Status != Estimated {
		return out
	}
	out.TimeAtOrBelowP50 = comparison(r.DeliveryMs <= e.DeliveryP50Ms)
	if !e.P85Open {
		out.TimeAtOrBelowP85 = comparison(r.DeliveryMs <= e.DeliveryP85Ms)
	}
	out.TimeRatio = ratio(float64(r.DeliveryMs), float64(e.DeliveryP50Ms))
	if r.Usage.LowerBound() || e.CostAtLeast {
		return out
	}
	out.CostAtOrBelowP50 = comparison(r.Usage.CostUSD <= e.CostP50USD)
	if !e.P85Open {
		out.CostAtOrBelowP85 = comparison(r.Usage.CostUSD <= e.CostP85USD)
	}
	out.CostRatio = ratio(r.Usage.CostUSD, e.CostP50USD)
	return out
}

// MilestoneOutcome compares when a milestone's last work order ended with the forecast its baseline holds
// (FR-EST-11), from the delivery records of all of its work, keeping the forecast's confidence. A milestone ended
// `done` when all of its work did, and otherwise `failed` when any of it failed, or else `canceled`; only `done`
// compares. A forecast with no dates compares nothing, and a date that is a lower bound, read "or later", has no
// comparison, as an open p85 has none: with `lower_bound` `p85` the p85 date compares nothing, and with `all`
// neither date does. Its cost is what its work cost, and compares nothing, because a forecast's cost is of what
// remained when it ran.
func MilestoneOutcome(baseline string, f Forecast, ends []Record) Outcome {
	out := Outcome{
		SubjectKind: OutcomeOfMilestone, Subject: f.Milestone, Plan: f.Plan, Baseline: baseline, Forecast: f.ID, Confidence: f.Confidence,
		Outcome: workorders.Done,
	}
	for _, r := range ends {
		out.ActualEnd = max(out.ActualEnd, r.Ended)
		out.ActualCostUSD += r.Usage.CostUSD
		out.ActualCostAtLeast = out.ActualCostAtLeast || r.Usage.LowerBound()
		switch {
		case r.Outcome == workorders.Failed:
			out.Outcome = workorders.Failed
		case r.Outcome != workorders.Done && out.Outcome == workorders.Done:
			out.Outcome = r.Outcome
		}
	}
	out.Ended = out.ActualEnd
	if out.Outcome != workorders.Done || f.Status != Forecasted {
		return out
	}
	if !f.LowerBound.OpensP50() {
		out.TimeAtOrBelowP50 = comparison(out.ActualEnd <= f.EndP50)
	}
	if !f.LowerBound.OpensP85() {
		out.TimeAtOrBelowP85 = comparison(out.ActualEnd <= f.EndP85)
	}
	return out
}

// comparison is one comparison that holds, true or false.
func comparison(b bool) *bool { return &b }

// ratio is actual over a p50, or null over a p50 of zero, which no actual divides.
func ratio(actual, p50 float64) *float64 {
	if p50 <= 0 {
		return nil
	}
	r := actual / p50
	return &r
}
