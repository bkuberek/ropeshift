package server_test

import (
	"fmt"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// estimateFrom is the estimate a medium change gets from n finished medium changes that took one hour more each,
// the first at least one hour, with the cost of each its hours in dollars. A cost lower bound on one of them reads
// as at least.
func estimateFrom(work string, n int, atLeast bool, rev int64) delivery.Estimate {
	keys := delivery.Keys{Kind: string(workorders.KindChange), Size: "medium", CapabilityClass: "implement", Repository: "harbor-web", MergeAuthority: "person_approves", Criteria: 3}
	var records []delivery.Record
	for i := range n {
		usage := delivery.Usage{CostUSD: float64(i + 1)}
		usage.CostLowerBound = atLeast && i == n-1
		records = append(records, delivery.Record{WorkOrder: fmt.Sprintf("done-%02d", i), Keys: keys, Outcome: workorders.Done, Ended: int64(1000 + i), DeliveryMs: int64(i+1) * hour, Usage: usage})
	}
	policy := delivery.DefaultEstimatePolicy()
	policy.Version = 1
	e := delivery.EstimateOf(delivery.EstimateInput{WorkOrder: work, Keys: keys, Records: records, Policy: policy, Rev: rev, NoPool: true})
	e.ID = fmt.Sprintf("estimate-%d-%s", rev, work)
	return e
}

// TestGoldenEstimates writes what a work order and a plan approval show of estimates: a work order's current
// estimate beside its baseline, a low estimate whose cost is a lower bound, a low one whose p85 is open, one with no
// history, and a work order with none, and a plan version whose approval shows an estimate set of each kind with the
// forecasts made from it.
func TestGoldenEstimates(t *testing.T) {
	d := aDetailedWorld(t)
	var plan string
	for _, row := range readView[api.Plans](t, d, api.ViewPlans, "").Plans {
		if row.Plan.Title == "The factory floor" {
			plan = row.Plan.ID
		}
	}
	waiting, err := d.Client.Plan(t.Context(), plan)
	if err != nil || waiting.Approval == nil || waiting.Approval.Forecast == nil || waiting.Approval.Forecast.Set == nil || len(waiting.Approval.Forecast.Set.Estimates) < 4 {
		t.Fatalf("the world's plan reads %+v: %v", waiting, err)
	}
	version := waiting.Approval.Approval.PlanVersion
	var open []string
	for _, e := range waiting.Approval.Forecast.Set.Estimates {
		open = append(open, e.WorkOrder)
	}
	// The work the version's estimates are of: confident now and low when baselined, low with a lower-bound cost, low
	// with an open p85, and with no history.
	earlier := delivery.EstimateSet{ID: "set-baseline", Plan: plan, PlanVersion: version, Rev: 400, EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, CreatedAt: 1_763_000_000_000,
		Estimates: []delivery.Estimate{estimateFrom(open[3], 4, false, 400)}}.Seal()
	newest := delivery.EstimateSet{ID: "set-newest", Plan: plan, PlanVersion: version, Rev: 412, EstimatorVersion: delivery.EstimatorVersion, PolicyVersion: 1, CreatedAt: 1_763_100_000_000,
		Estimates: []delivery.Estimate{estimateFrom(open[3], 12, false, 412), estimateFrom(open[1], 7, true, 412), estimateFrom(open[2], 3, false, 412), estimateFrom(open[0], 0, false, 412)}}.Seal()
	kept := forecastsOf(plan, newest.ID, newest.CreatedAt)
	for i := range kept {
		kept[i].PlanVersion = version
	}
	err = d.Store.Forecast(t.Context(), apitest.Scope, func(tx ports.ForecastTx) error {
		if err := tx.EstimateSets().Add(earlier); err != nil {
			return err
		}
		if err := tx.EstimateSets().AddBaseline(delivery.Baseline{ID: "baseline-1", Plan: plan, PlanVersion: version, SetID: earlier.ID, SetDigest: earlier.Digest, Forecasts: []delivery.Forecast{}, Rev: 400, CreatedAt: earlier.CreatedAt}); err != nil {
			return err
		}
		if err := tx.EstimateSets().Add(newest); err != nil {
			return err
		}
		return tx.Forecast().Keep(kept)
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, work := range map[string]string{"work-estimate": open[3], "work-estimate-low": open[1], "work-estimate-open": open[2], "work-estimate-none": open[0]} {
		read, err := d.Client.WorkEstimate(t.Context(), work)
		if err != nil || read.Estimate == nil {
			t.Fatalf("%s: work %s reads %+v: %v", name, work, read, err)
		}
		holdsGolden(t, name, steady(pretty(t, read)))
	}
	holdsGolden(t, "work-estimate-absent", pretty(t, api.WorkEstimate{}))

	shown, err := d.Client.PlanVersion(t.Context(), plan, version)
	if err != nil || shown.Approval == nil || shown.Approval.Forecast == nil || shown.Approval.Forecast.State != api.ForecastMade || len(shown.Approval.Forecast.Set.Estimates) != 4 {
		t.Fatalf("the plan approval reads %+v: %v", shown.Approval, err)
	}
	steadySet(&shown)
	holdsGolden(t, "plan-estimates", steady(pretty(t, shown)))
}
