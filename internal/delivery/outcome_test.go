package delivery_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

// said reads an optional comparison as "yes", "no", or "-" for none.
func said(b *bool) string {
	switch {
	case b == nil:
		return "-"
	case *b:
		return "yes"
	}
	return "no"
}

// ratioOf reads an optional ratio, or -1 for none.
func ratioOf(r *float64) float64 {
	if r == nil {
		return -1
	}
	return *r
}

// TestAWorkOrdersOutcomeComparesItsRecordWithItsBaseline holds FR-EST-11 for one work order: its delivery time and
// cost against its baseline band at p50 and p85 and as a ratio to p50, with no p85 for an open band, no cost where
// a cost is a lower bound, and no comparison for work that did not reach done or an estimate with no history.
func TestAWorkOrdersOutcomeComparesItsRecordWithItsBaseline(t *testing.T) {
	band := delivery.Estimate{
		ID: "e-1", Status: delivery.Estimated, Confidence: delivery.Confident, Tier: 2, EstimatorVersion: delivery.EstimatorVersion,
		DeliveryP50Ms: 4000, DeliveryP85Ms: 8000, CostP50USD: 2, CostP85USD: 4,
	}
	open := band
	open.Confidence, open.P85Open = delivery.Low, true
	atLeast := band
	atLeast.CostAtLeast = true
	zero := band
	zero.DeliveryP50Ms, zero.CostP50USD = 0, 0
	none := delivery.Estimate{ID: "e-2", Status: delivery.NoHistory, EstimatorVersion: delivery.EstimatorVersion}
	record := func(outcome workorders.State, deliveryMs int64, cost float64, lowerBound bool) delivery.Record {
		return delivery.Record{
			WorkOrder: "wo", Keys: delivery.Keys{Repository: "repo"}, Outcome: outcome, Ended: 99, DeliveryMs: deliveryMs,
			Usage: delivery.Usage{CostUSD: cost, CostLowerBound: lowerBound},
		}
	}
	tests := []struct {
		name                               string
		estimate                           delivery.Estimate
		record                             delivery.Record
		timeP50, timeP85, costP50, costP85 string
		timeRatio, costRatio               float64
	}{
		{"at or below p50", band, record(workorders.Done, 4000, 2, false), "yes", "yes", "yes", "yes", 1, 1},
		{"between p50 and p85", band, record(workorders.Done, 6000, 3, false), "no", "yes", "no", "yes", 1.5, 1.5},
		{"past p85", band, record(workorders.Done, 12000, 5, false), "no", "no", "no", "no", 3, 2.5},
		{"an open band has no p85", open, record(workorders.Done, 2000, 1, false), "yes", "-", "yes", "-", 0.5, 0.5},
		{"an actual cost that is a lower bound", band, record(workorders.Done, 2000, 1, true), "yes", "yes", "-", "-", 0.5, -1},
		{"a band whose cost is a lower bound", atLeast, record(workorders.Done, 2000, 5, false), "yes", "yes", "-", "-", 0.5, -1},
		{"a p50 of zero divides nothing", zero, record(workorders.Done, 2000, 1, false), "no", "yes", "no", "yes", -1, -1},
		{"failed work", band, record(workorders.Failed, 2000, 1, false), "-", "-", "-", "-", -1, -1},
		{"canceled work", band, record(workorders.Canceled, 2000, 1, false), "-", "-", "-", "-", -1, -1},
		{"no history", none, record(workorders.Done, 2000, 1, false), "-", "-", "-", "-", -1, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := delivery.WorkOutcome("b-1", tt.estimate, tt.record)
			if o.SubjectKind != delivery.OutcomeOfWorkOrder || o.Subject != "wo" || o.Baseline != "b-1" || o.Estimate != tt.estimate.ID ||
				o.Confidence != tt.estimate.Confidence || o.Tier != tt.estimate.Tier || o.P85Open != tt.estimate.P85Open || o.Repository != "repo" ||
				o.Outcome != tt.record.Outcome || o.ActualDeliveryMs != tt.record.DeliveryMs || o.ActualCostUSD != tt.record.Usage.CostUSD ||
				o.ActualCostAtLeast != tt.record.Usage.CostLowerBound || o.ActualEnd != 99 || o.Ended != 99 || o.EstimatorVersion != delivery.EstimatorVersion {
				t.Errorf("the outcome reads %+v", o)
			}
			got := [4]string{said(o.TimeAtOrBelowP50), said(o.TimeAtOrBelowP85), said(o.CostAtOrBelowP50), said(o.CostAtOrBelowP85)}
			if want := [4]string{tt.timeP50, tt.timeP85, tt.costP50, tt.costP85}; got != want {
				t.Errorf("time p50, p85, cost p50, p85 compare %v, want %v", got, want)
			}
			if ratioOf(o.TimeRatio) != tt.timeRatio || ratioOf(o.CostRatio) != tt.costRatio {
				t.Errorf("the ratios are %v and %v, want %v and %v", ratioOf(o.TimeRatio), ratioOf(o.CostRatio), tt.timeRatio, tt.costRatio)
			}
		})
	}
}

// TestAMilestonesOutcomeComparesItsDateWithItsBaselineForecast holds FR-EST-11 for a milestone: the end of its last
// work order against its baseline forecast's p50 and p85 dates, keeping the forecast's confidence, with no
// comparison for a date that is a lower bound, a forecast with no dates, or a milestone whose work did not all
// reach done.
func TestAMilestonesOutcomeComparesItsDateWithItsBaselineForecast(t *testing.T) {
	forecast := delivery.Forecast{
		ID: "f-1", Plan: "plan", Milestone: "m1", Status: delivery.Forecasted, Confidence: delivery.Low, LowerBound: delivery.LowerNone,
		EndP50: 5000, EndP85: 9000,
	}
	bounded := func(b delivery.LowerBound) delivery.Forecast { f := forecast; f.LowerBound = b; return f }
	noDates := forecast
	noDates.Status, noDates.EndP50, noDates.EndP85 = delivery.ForecastNoHistory, 0, 0
	end := func(at int64, outcome workorders.State) delivery.Record {
		return delivery.Record{WorkOrder: "wo", Outcome: outcome, Ended: at, Usage: delivery.Usage{CostUSD: 1}}
	}
	done := workorders.Done
	tests := []struct {
		name     string
		forecast delivery.Forecast
		ends     []delivery.Record
		outcome  workorders.State
		p50, p85 string
	}{
		{"by p50", forecast, []delivery.Record{end(3000, done), end(5000, done)}, done, "yes", "yes"},
		{"by p85", forecast, []delivery.Record{end(7000, done), end(2000, done)}, done, "no", "yes"},
		{"after p85", forecast, []delivery.Record{end(9500, done)}, done, "no", "no"},
		{"a p85 that is a lower bound", bounded(delivery.LowerP85), []delivery.Record{end(3000, done)}, done, "yes", "-"},
		{"dates that are lower bounds", bounded(delivery.LowerAll), []delivery.Record{end(3000, done)}, done, "-", "-"},
		{"a forecast with no dates", noDates, []delivery.Record{end(3000, done)}, done, "-", "-"},
		{"failed work", forecast, []delivery.Record{end(3000, workorders.Canceled), end(4000, workorders.Failed), end(2000, done)}, workorders.Failed, "-", "-"},
		{"canceled work", forecast, []delivery.Record{end(3000, done), end(4000, workorders.Canceled)}, workorders.Canceled, "-", "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := delivery.MilestoneOutcome("b-1", tt.forecast, tt.ends)
			last := int64(0)
			for _, r := range tt.ends {
				last = max(last, r.Ended)
			}
			if o.SubjectKind != delivery.OutcomeOfMilestone || o.Subject != "m1" || o.Plan != "plan" || o.Baseline != "b-1" || o.Forecast != "f-1" ||
				o.Confidence != delivery.Low || o.Outcome != tt.outcome || o.ActualEnd != last || o.Ended != last || o.ActualCostUSD != float64(len(tt.ends)) {
				t.Errorf("the outcome reads %+v", o)
			}
			if got := [2]string{said(o.TimeAtOrBelowP50), said(o.TimeAtOrBelowP85)}; got != [2]string{tt.p50, tt.p85} {
				t.Errorf("p50 and p85 compare %v, want %s and %s", got, tt.p50, tt.p85)
			}
			if o.TimeRatio != nil || o.CostAtOrBelowP50 != nil || o.CostAtOrBelowP85 != nil || o.CostRatio != nil {
				t.Errorf("a milestone compared more than its date: %+v", o)
			}
		})
	}
}
