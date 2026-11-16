package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// forecastsOf are the forecasts the golden keeps for the factory plan: its own, with a lower bound on the
// p85, and each of its milestones: one confident, one low with every result open, and one with no history.
func forecastsOf(plan, set string, at int64) []delivery.Forecast {
	const at85, at50 = int64(1_763_300_000_000), int64(1_763_200_000_000)
	base := func(milestone string, status delivery.ForecastStatus) delivery.Forecast {
		return delivery.Forecast{
			ID: "forecast-" + milestone, SetID: set, Plan: plan, PlanVersion: 6, Milestone: milestone, Status: status, Rev: 412, Seed: "sha256:forecast-seed",
			Trials: 2000, Cap: 2, AsOf: 1_763_100_000_000, PersonMedianMs: 1_800_000, PolicyVersion: 1, LowConfidenceWorkOrders: []string{}, LeftOut: []string{}, Outside: []string{},
			LowerBound: delivery.LowerNone, Day: delivery.DayOf(at), CreatedAt: at,
		}
	}
	plans := base("", delivery.Forecasted)
	plans.Confidence, plans.LowerBound = delivery.Low, delivery.LowerP85
	plans.EndP50, plans.EndP85, plans.CostP50USD, plans.CostP85USD = at50, at85, 18.4, 31.2
	plans.EndP85CapPlusOne, plans.EndP85PersonMedian = 1_763_250_000_000, 1_763_280_000_000
	plans.LowConfidenceWorkOrders = []string{"wo-3", "wo-7"}
	plans.Explanation = "Low confidence, from 2000 trials of 7 work orders, at most 2 at a time. 2 work orders have too few comparable outcomes: wo-3 rests on 4 finished changes, and a confident estimate needs 10."

	confident := base("build", delivery.Forecasted)
	confident.Confidence = delivery.Confident
	confident.EndP50, confident.EndP85, confident.CostP50USD, confident.CostP85USD = 1_763_150_000_000, 1_763_210_000_000, 6.1, 9.8
	confident.EndP85CapPlusOne, confident.EndP85PersonMedian = 1_763_190_000_000, 1_763_200_000_000
	confident.Explanation = "From 2000 trials of 3 work orders, at most 2 at a time, each estimated from at least 10 finished comparable changes."

	open := base("ship", delivery.Forecasted)
	open.Cap, open.Confidence, open.LowerBound = 0, delivery.Low, delivery.LowerAll
	open.EndP50, open.EndP85, open.CostP50USD, open.CostP85USD = 1_763_400_000_000, 1_763_700_000_000, 9.5, 14
	open.EndP85CapPlusOne, open.EndP85PersonMedian = 1_763_700_000_000, 1_763_650_000_000
	open.LowConfidenceWorkOrders, open.LeftOut = []string{"wo-9"}, []string{"wo-10"}
	open.Explanation = "Low confidence, from 2000 trials of 2 work orders, with no lane cap. It leaves out 1 work order with no history, so every date and cost is a lower bound."

	none := base("guide", delivery.ForecastNoHistory)
	none.Explanation = "No forecast: none of the 2 unfinished work orders has finished comparable work."
	return []delivery.Forecast{plans, confident, open, none}
}

// TestGoldenForecast writes a plan version's forecast as the plan page and the Roadmap read it: pending before
// the daemon's forecast tick has run, with forecasts after it, and superseded once a newer version replaced it.
func TestGoldenForecast(t *testing.T) {
	d := aDetailedWorld(t)
	var plan string
	for _, row := range readView[api.Plans](t, d, api.ViewPlans, "").Plans {
		if row.Plan.Title == "The factory floor" {
			plan = row.Plan.ID
		}
	}
	if plan == "" {
		t.Fatal("the world holds no factory plan")
	}
	pending, err := d.Client.PlanForecast(t.Context(), plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State != api.ForecastPending {
		t.Fatalf("before the tick the forecast reads %+v", pending)
	}
	pending.Set.Digest = "sha256:steady"
	holdsGolden(t, "forecast-pending", steady(pretty(t, pending)))

	forecastTick(t, d)
	made, err := d.Client.PlanForecast(t.Context(), plan, 0)
	if err != nil || made.State != api.ForecastMade || made.Set == nil || len(made.Forecasts) == 0 {
		t.Fatalf("after the tick the forecast reads %+v: %v", made, err)
	}
	// The tick's forecasts hold dates of the machine's own clock. These hold fixed ones, kept on the day the tick's
	// were, so each takes the place of one.
	kept := forecastsOf(plan, made.Set.ID, made.Forecasts[0].CreatedAt)
	for i := range kept {
		kept[i].PlanVersion = made.Version
	}
	if err := d.Store.Forecast(t.Context(), apitest.Scope, func(tx ports.ForecastTx) error { return tx.Forecast().Keep(kept) }); err != nil {
		t.Fatal(err)
	}
	if made, err = d.Client.PlanForecast(t.Context(), plan, 0); err != nil || len(made.Forecasts) != len(kept) {
		t.Fatalf("after keeping, the forecast reads %+v: %v", made, err)
	}
	made.Set.Digest = "sha256:steady"
	holdsGolden(t, "forecast", steady(pretty(t, made)))

	older, newer := approvedThenChanged(t)
	superseded, err := older.Client.PlanForecast(t.Context(), newer, 1)
	if err != nil || superseded.State != api.ForecastSuperseded {
		t.Fatalf("a replaced version reads %+v: %v", superseded, err)
	}
	superseded.Set.Digest = "sha256:steady"
	holdsGolden(t, "forecast-superseded", steady(pretty(t, superseded)))
}
