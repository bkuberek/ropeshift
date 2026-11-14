package delivery_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	now    = int64(1_000_000_000)
	minute = int64(60_000)
)

// took is finished work that took a delivery time, held a lane for part of it, and cost so much.
func took(id string, deliveryMs, laneMs int64, cost float64) delivery.Record {
	return delivery.Record{WorkOrder: id, Outcome: workorders.Done, DeliveryMs: deliveryMs, LaneMs: laneMs, Usage: delivery.Usage{CostUSD: cost}}
}

// confidentOn is a confident estimate that rests on these records.
func confidentOn(records ...delivery.Record) delivery.Estimate {
	return delivery.Estimate{Status: delivery.Estimated, Confidence: delivery.Confident, SampleCount: len(records)}
}

func forecastOf(cap int64, work ...delivery.ForecastWork) []delivery.Forecast {
	return delivery.ForecastOf(delivery.ForecastInput{Plan: "plan-1", PlanDigest: "sha256:plan", Rev: 40, Now: now, Cap: cap, Trials: 2000, Work: work})
}

// forecastFor finds the plan's forecast, or a milestone's.
func forecastFor(t *testing.T, forecasts []delivery.Forecast, milestone string) delivery.Forecast {
	t.Helper()
	for _, f := range forecasts {
		if f.Milestone == milestone {
			return f
		}
	}
	t.Fatalf("no forecast for milestone %q in %+v", milestone, forecasts)
	return delivery.Forecast{}
}

// TestAForecastIsTheSameForTheSameSeed holds FR-EST-8's determinism: the seed is the digest of the plan version's
// digest, the ledger revision, and the estimator version, the forecast records it with its trials, and the same
// input gives the same forecast.
func TestAForecastIsTheSameForTheSameSeed(t *testing.T) {
	records := []delivery.Record{took("d1", 60*minute, 30*minute, 1), took("d2", 180*minute, 60*minute, 3), took("d3", 600*minute, 120*minute, 9)}
	work := []delivery.ForecastWork{
		{WorkOrder: "a", Milestone: "m1", Priority: 2, Estimate: confidentOn(records...), Records: records},
		{WorkOrder: "b", Milestone: "m1", Priority: 1, DependsOn: []string{"a"}, Estimate: confidentOn(records...), Records: records},
		{WorkOrder: "c", Milestone: "m2", Priority: 1, Estimate: confidentOn(records...), Records: records},
	}
	first := forecastOf(1, work...)
	if again := forecastOf(1, slices.Clone(work)...); !reflect.DeepEqual(first, again) {
		t.Fatalf("the same input forecast %+v and %+v", first, again)
	}
	reversed := slices.Clone(work)
	slices.Reverse(reversed)
	if other := forecastOf(1, reversed...); !reflect.DeepEqual(first, other) {
		t.Errorf("the same work in another order forecast %+v", other)
	}
	plan := forecastFor(t, first, "")
	seed := delivery.ForecastSeed("sha256:plan", 40)
	if plan.Seed != seed || !strings.HasPrefix(seed, "sha256:") || plan.Trials != 2000 || plan.Cap != 1 || plan.Rev != 40 || plan.Plan != "plan-1" {
		t.Errorf("the plan's forecast records %+v", plan)
	}
	if delivery.ForecastSeed("sha256:plan", 41) == seed || delivery.ForecastSeed("sha256:other", 40) == seed {
		t.Error("another revision or plan version has the same seed")
	}
	if plan.EndP50 > plan.EndP85 || plan.EndP50 < now+120*minute || plan.EndP85 > now+1800*minute || plan.CostP50USD > plan.CostP85USD {
		t.Errorf("the plan's band %+v is out of what its draws allow", plan)
	}
	if len(first) != 3 || first[0].Milestone != "" || first[1].Milestone != "m1" || first[2].Milestone != "m2" {
		t.Errorf("the forecasts come as %+v", first)
	}
}

// TestWorkRunsInDependencyOrderWithinTheLaneCap runs each trial as FR-EST-8 says: work in dependency order, by
// priority within what is ready, at most the cap at a time, each holding a lane for its lane time and then
// needing the rest of its delivery time without one; and FR-EST-9's rerun with the cap one higher.
func TestWorkRunsInDependencyOrderWithinTheLaneCap(t *testing.T) {
	a, b, c := took("da", 120*minute, 60*minute, 1), took("db", 180*minute, 120*minute, 2), took("dc", 60*minute, 60*minute, 0.5)
	forecasts := forecastOf(1,
		delivery.ForecastWork{WorkOrder: "a", Title: "First", Milestone: "m1", Priority: 2, Estimate: confidentOn(a), Records: []delivery.Record{a}},
		delivery.ForecastWork{WorkOrder: "b", Title: "Second", Milestone: "m1", Priority: 2, DependsOn: []string{"a"}, Estimate: confidentOn(b), Records: []delivery.Record{b}},
		delivery.ForecastWork{WorkOrder: "c", Title: "Aside", Milestone: "m2", Priority: 1, Estimate: confidentOn(c), Records: []delivery.Record{c}},
	)
	// a holds the lane for an hour from the start; c takes it then and ends at two hours, when a ends too; b
	// takes the lane at two hours and ends three hours later.
	plan, first, aside := forecastFor(t, forecasts, ""), forecastFor(t, forecasts, "m1"), forecastFor(t, forecasts, "m2")
	if plan.Status != delivery.Forecasted || plan.EndP50 != now+300*minute || plan.EndP85 != now+300*minute || plan.CostP50USD != 3.5 || plan.CostP85USD != 3.5 {
		t.Errorf("the plan's forecast %+v", plan)
	}
	if first.EndP85 != now+300*minute || first.CostP85USD != 3 || aside.EndP85 != now+120*minute || aside.CostP85USD != 0.5 {
		t.Errorf("the milestones' forecasts %+v and %+v", first, aside)
	}
	if aside.EndP85CapPlusOne != now+60*minute || plan.EndP85CapPlusOne != now+300*minute {
		t.Errorf("with one more lane, the aside ends %d and the plan %d", aside.EndP85CapPlusOne-now, plan.EndP85CapPlusOne-now)
	}
	if plan.Confidence != delivery.Confident || plan.LowerBound != delivery.LowerNone || len(plan.LeftOut) != 0 || len(plan.LowConfidenceWorkOrders) != 0 {
		t.Errorf("a forecast on confident estimates reads %+v", plan)
	}
	if plan.Explanation != "Based on 2000 trials of 3 work orders, at most 1 at a time." {
		t.Errorf("the explanation %q", plan.Explanation)
	}
}

// TestStartedWorkKeepsWhatRemainsOfLongerWork draws a started work order's time only from comparable work that ran
// longer than it has so far, and keeps the remainder; with none longer, it draws any and keeps all of it.
func TestStartedWorkKeepsWhatRemainsOfLongerWork(t *testing.T) {
	records := []delivery.Record{took("short", 60*minute, 0, 1), took("long", 180*minute, 0, 3)}
	under := forecastOf(1, delivery.ForecastWork{WorkOrder: "a", Estimate: confidentOn(records...), Records: records, Started: true, SoFarMs: 120 * minute, SpentUSD: 2})
	if plan := forecastFor(t, under, ""); plan.EndP50 != now+60*minute || plan.EndP85 != now+60*minute || plan.CostP85USD != 1 {
		t.Errorf("started work two hours in forecast %+v", plan)
	}
	past := forecastOf(1, delivery.ForecastWork{WorkOrder: "a", Estimate: confidentOn(records...), Records: records, Started: true, SoFarMs: 300 * minute})
	if plan := forecastFor(t, past, ""); plan.EndP85 != now+180*minute || (plan.EndP50 != now+60*minute && plan.EndP50 != now+180*minute) {
		t.Errorf("started work past every record forecast %+v", plan)
	}
}

// TestAForecastWithThinInputsIsLowConfidenceAndSaysWhy labels a forecast low confidence when an input estimate is
// low or has no history, names those work orders, leaves out work with no history, which takes no time while its
// dependents wait only for its own dependencies, and marks what has no upper bound (FR-EST-8, FR-EST-14).
func TestAForecastWithThinInputsIsLowConfidenceAndSaysWhy(t *testing.T) {
	b := took("db", 120*minute, 60*minute, 2)
	forecasts := forecastOf(2,
		delivery.ForecastWork{WorkOrder: "a", Title: "Unknown work", Milestone: "m1", Estimate: delivery.Estimate{Status: delivery.NoHistory}},
		delivery.ForecastWork{WorkOrder: "b", Title: "Known work", Milestone: "m1", DependsOn: []string{"a"}, Estimate: confidentOn(b), Records: []delivery.Record{b}},
	)
	plan := forecastFor(t, forecasts, "")
	if plan.EndP85 != now+120*minute || plan.Confidence != delivery.Low || plan.LowerBound != delivery.LowerAll ||
		!slices.Equal(plan.LeftOut, []string{"a"}) || !slices.Equal(plan.LowConfidenceWorkOrders, []string{"a"}) {
		t.Errorf("a plan with work of no history forecast %+v", plan)
	}
	if !strings.Contains(plan.Explanation, "Unknown work (a) has no finished history and is left out") || !strings.Contains(plan.Explanation, "lower bounds") {
		t.Errorf("the explanation %q", plan.Explanation)
	}

	open := []delivery.Record{took("d1", 60*minute, 30*minute, 1), took("d2", 90*minute, 30*minute, 1)}
	thin := forecastOf(1, delivery.ForecastWork{WorkOrder: "c", Title: "Thin work", Estimate: delivery.Estimate{Status: delivery.Estimated, Confidence: delivery.Low, SampleCount: 2, MinSamples: 10, P85Open: true}, Records: open})
	plan = forecastFor(t, thin, "")
	if plan.Confidence != delivery.Low || plan.LowerBound != delivery.LowerP85 || len(plan.LeftOut) != 0 || !slices.Equal(plan.LowConfidenceWorkOrders, []string{"c"}) {
		t.Errorf("a plan with an open band forecast %+v", plan)
	}
	if !strings.Contains(plan.Explanation, "Thin work (c) rests on 2 finished records, and a confident estimate needs 10") ||
		!strings.Contains(plan.Explanation, "open above its longest record") {
		t.Errorf("the explanation %q", plan.Explanation)
	}
}

// TestCuttingPersonWaitsToTheMedianShowsWhatThePersonCosts reruns FR-EST-9's second what-if: each drawn record's
// waits for a person cut to the project's median wait.
func TestCuttingPersonWaitsToTheMedianShowsWhatThePersonCosts(t *testing.T) {
	waited := took("dw", 240*minute, 60*minute, 1)
	waited.PersonWaits, waited.PersonMs = []int64{180 * minute}, 180*minute
	forecasts := delivery.ForecastOf(delivery.ForecastInput{
		Plan: "plan-1", PlanDigest: "sha256:plan", Rev: 1, Now: now, Cap: 1, Trials: 100, PersonMedianMs: 60 * minute,
		Work: []delivery.ForecastWork{{WorkOrder: "a", Estimate: confidentOn(waited), Records: []delivery.Record{waited}}},
	})
	if plan := forecastFor(t, forecasts, ""); plan.EndP85 != now+240*minute || plan.EndP85PersonMedian != now+120*minute {
		t.Errorf("with the person's waits cut to the median, the plan ends %d, and %d as it is", plan.EndP85PersonMedian-now, plan.EndP85-now)
	}
}

// TestAMilestoneWithNoHistoryHasNoForecast gives no forecast to a milestone none of whose unfinished work has
// history, and says so, and none to a plan with nothing left (FR-EST-8, FR-EST-14).
func TestAMilestoneWithNoHistoryHasNoForecast(t *testing.T) {
	b := took("db", 60*minute, 60*minute, 1)
	forecasts := forecastOf(1,
		delivery.ForecastWork{WorkOrder: "a", Milestone: "m1", Estimate: delivery.Estimate{Status: delivery.NoHistory}},
		delivery.ForecastWork{WorkOrder: "b", Milestone: "m2", Estimate: confidentOn(b), Records: []delivery.Record{b}},
	)
	none := forecastFor(t, forecasts, "m1")
	if none.Status != delivery.ForecastNoHistory || none.EndP50 != 0 || none.EndP85 != 0 || !slices.Equal(none.LeftOut, []string{"a"}) ||
		none.Explanation != "No forecast: none of the 1 unfinished work order of this milestone has finished history." {
		t.Errorf("a milestone with no history forecast %+v", none)
	}
	if plan := forecastFor(t, forecasts, ""); plan.Status != delivery.Forecasted || plan.EndP85 != now+60*minute {
		t.Errorf("the plan forecast %+v", plan)
	}
	if empty := forecastOf(1); len(empty) != 1 || empty[0].Status != delivery.ForecastFinished || empty[0].Explanation != "Nothing is left to forecast: every work order of this plan has ended." {
		t.Errorf("a plan with nothing left forecast %+v", empty)
	}
}
