package delivery_test

import (
	"fmt"
	"math/rand/v2"
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
	return delivery.Estimate{Status: delivery.Estimated, Confidence: delivery.Confident, SampleCount: len(records), MinSamples: 10}
}

// lowOn is a low-confidence estimate that rests on these records, open below six.
func lowOn(records ...delivery.Record) delivery.Estimate {
	return delivery.Estimate{Status: delivery.Estimated, Confidence: delivery.Low, SampleCount: len(records), MinSamples: 10, P85Open: len(records) < 6}
}

// forecastPolicy is the estimate policy the forecasts here run under.
var forecastPolicy = delivery.EstimatePolicy{Version: 3, MinSamples: 10, RecentSamples: 50, ForecastTrials: 2000}

func forecastIn(cap int64, work ...delivery.ForecastWork) delivery.ForecastInput {
	return delivery.ForecastInput{Plan: "plan-1", PlanDigest: "sha256:plan", Rev: 40, Now: now, Cap: cap, Policy: forecastPolicy, Work: work}
}

func forecastOf(cap int64, work ...delivery.ForecastWork) []delivery.Forecast {
	return delivery.ForecastOf(forecastIn(cap, work...))
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
// digest, the ledger revision, and the estimator version; the forecast records it with its trials, the time it
// ran from, the median wait, and the policy version; and the same input gives the same forecast.
func TestAForecastIsTheSameForTheSameSeed(t *testing.T) {
	records := []delivery.Record{took("d1", 60*minute, 30*minute, 1), took("d2", 180*minute, 60*minute, 3), took("d3", 600*minute, 120*minute, 9)}
	work := []delivery.ForecastWork{
		{WorkOrder: "a", Milestone: "m1", Priority: 2, Estimate: confidentOn(records...), Records: records},
		{WorkOrder: "b", Milestone: "m1", Priority: 1, DependsOn: []string{"a"}, Estimate: confidentOn(records...), Records: records},
		{WorkOrder: "c", Milestone: "m2", Priority: 1, Estimate: confidentOn(records...), Records: records},
	}
	in := forecastIn(1, work...)
	in.PersonMedianMs = 30 * minute
	first := delivery.ForecastOf(in)
	again := in
	again.Work = slices.Clone(work)
	slices.Reverse(again.Work)
	if other := delivery.ForecastOf(again); !reflect.DeepEqual(first, other) {
		t.Fatalf("the same work in another order forecast %+v and %+v", first, other)
	}
	plan := forecastFor(t, first, "")
	seed := delivery.ForecastSeed("sha256:plan", 40)
	if plan.Seed != seed || !strings.HasPrefix(seed, "sha256:") || plan.Trials != 2000 || plan.Cap != 1 || plan.Rev != 40 || plan.Plan != "plan-1" ||
		plan.AsOf != now || plan.PersonMedianMs != 30*minute || plan.PolicyVersion != 3 {
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
	many := forecastIn(1, work...)
	many.Policy.ForecastTrials = 1_000_000
	if f := delivery.ForecastOf(many)[0]; f.Trials != delivery.MaxForecastTrials {
		t.Errorf("a policy of a million trials ran %d", f.Trials)
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

// TestAMilestoneSharesItsLanesWithTheWholePlan labels a milestone from all of the plan's unfinished work, because
// under a lane cap any of it can take a lane first: higher-priority work elsewhere with no history makes its
// date a lower bound, and low-confidence work elsewhere makes it low confidence.
func TestAMilestoneSharesItsLanesWithTheWholePlan(t *testing.T) {
	y := took("dy", 60*minute, 60*minute, 1)
	aside := delivery.ForecastWork{WorkOrder: "y", Milestone: "m2", Priority: 1, Estimate: confidentOn(y), Records: []delivery.Record{y}}
	unknown := forecastOf(1, delivery.ForecastWork{WorkOrder: "x", Milestone: "m1", Priority: 5, Estimate: delivery.Estimate{Status: delivery.NoHistory}}, aside)
	if m2 := forecastFor(t, unknown, "m2"); m2.Confidence != delivery.Low || m2.LowerBound != delivery.LowerAll || !slices.Equal(m2.LeftOut, []string{"x"}) {
		t.Errorf("a milestone behind higher-priority work with no history reads %+v", m2)
	}
	x := took("dx", 600*minute, 600*minute, 5)
	thin := forecastOf(1, delivery.ForecastWork{WorkOrder: "x", Milestone: "m1", Priority: 5, Estimate: lowOn(x), Records: []delivery.Record{x}}, aside)
	m2 := forecastFor(t, thin, "m2")
	if m2.EndP85 != now+660*minute || m2.Confidence != delivery.Low || m2.LowerBound != delivery.LowerP85 || !slices.Equal(m2.LowConfidenceWorkOrders, []string{"x"}) {
		t.Errorf("a milestone behind higher-priority low-confidence work reads %+v", m2)
	}
	if m2.CostP85USD != 1 {
		t.Errorf("the milestone's cost %v counts work beyond its own", m2.CostP85USD)
	}
}

// TestStartedWorkKeepsWhatRemainsOfLongerWork draws a started work order's time only from comparable work that ran
// longer than it has so far, keeps the remainder, and labels the forecast by how many ran longer; with none
// longer, it draws any and keeps all of it, and its time has no bound.
func TestStartedWorkKeepsWhatRemainsOfLongerWork(t *testing.T) {
	records := []delivery.Record{took("short", 60*minute, 0, 1), took("long", 180*minute, 0, 3)}
	under := forecastOf(1, delivery.ForecastWork{WorkOrder: "a", Title: "Under way", Estimate: confidentOn(records...), Records: records, Started: true, SoFarMs: 120 * minute, SpentUSD: 2})
	plan := forecastFor(t, under, "")
	if plan.EndP50 != now+60*minute || plan.EndP85 != now+60*minute || plan.CostP85USD != 1 {
		t.Errorf("started work two hours in forecast %+v", plan)
	}
	if plan.Confidence != delivery.Low || plan.LowerBound != delivery.LowerP85 || !strings.Contains(plan.Explanation, "Under way (a) has run 2h so far, and 1 comparable record ran longer") {
		t.Errorf("started work with one longer record reads %+v", plan)
	}
	past := forecastOf(1, delivery.ForecastWork{WorkOrder: "a", Title: "Under way", Estimate: confidentOn(records...), Records: records, Started: true, SoFarMs: 300 * minute})
	plan = forecastFor(t, past, "")
	if plan.EndP85 != now+180*minute || (plan.EndP50 != now+60*minute && plan.EndP50 != now+180*minute) {
		t.Errorf("started work past every record forecast %+v", plan)
	}
	if plan.Confidence != delivery.Low || plan.LowerBound != delivery.LowerAll || !strings.Contains(plan.Explanation, "longer than every comparable record") {
		t.Errorf("started work past every record reads %+v", plan)
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
	if !strings.HasPrefix(plan.Explanation, "Low confidence, from 2000 trials of 1 work order, at most 2 at a time.") ||
		!strings.Contains(plan.Explanation, "Unknown work (a) has no finished history and is left out") || !strings.Contains(plan.Explanation, "lower bounds") {
		t.Errorf("the explanation %q", plan.Explanation)
	}

	open := []delivery.Record{took("d1", 60*minute, 30*minute, 1), took("d2", 90*minute, 30*minute, 1)}
	thin := forecastOf(1, delivery.ForecastWork{WorkOrder: "c", Title: "Thin work", Estimate: lowOn(open...), Records: open})
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
// waits for a person cut to the project's median wait. Started work is left as it is, because a record does not
// say which of its waits are already past, and pooled records, which keep no waits, cannot be cut; the
// explanation says both.
func TestCuttingPersonWaitsToTheMedianShowsWhatThePersonCosts(t *testing.T) {
	waited := took("dw", 240*minute, 60*minute, 1)
	waited.PersonWaits, waited.PersonMs = []int64{180 * minute}, 180*minute
	in := forecastIn(1, delivery.ForecastWork{WorkOrder: "a", Estimate: confidentOn(waited), Records: []delivery.Record{waited}})
	in.PersonMedianMs = 60 * minute
	if plan := delivery.ForecastOf(in)[0]; plan.EndP85 != now+240*minute || plan.EndP85PersonMedian != now+120*minute {
		t.Errorf("with the person's waits cut to the median, the plan ends %d, and %d as it is", plan.EndP85PersonMedian-now, plan.EndP85-now)
	}
	in.Work[0].Started, in.Work[0].SoFarMs = true, 200*minute
	plan := delivery.ForecastOf(in)[0]
	if plan.EndP85 != now+40*minute || plan.EndP85PersonMedian != now+40*minute || !strings.Contains(plan.Explanation, "leaves started work as it is") {
		t.Errorf("started work past its wait reads %+v", plan)
	}
	pooled := took("", 240*minute, 60*minute, 1)
	pooled.PersonMs = 180 * minute
	in = forecastIn(1, delivery.ForecastWork{WorkOrder: "b", Estimate: confidentOn(pooled), Records: []delivery.Record{pooled}})
	in.PersonMedianMs = 60 * minute
	if plan := delivery.ForecastOf(in)[0]; plan.EndP85PersonMedian != now+240*minute || !strings.Contains(plan.Explanation, "pool, which keeps no waits") {
		t.Errorf("a pooled draw reads %+v", plan)
	}
}

// TestTheWhatIfsShareTheirDraws reruns each trial's own draws: one work order with a thousand different records,
// over three trials, ends the same with one more lane and with no waits to cut.
func TestTheWhatIfsShareTheirDraws(t *testing.T) {
	var records []delivery.Record
	for i := range 1000 {
		records = append(records, took("d", int64(i+1)*minute, int64(i+1)*minute, float64(i)))
	}
	in := forecastIn(1, delivery.ForecastWork{WorkOrder: "a", Estimate: confidentOn(records...), Records: records})
	in.Policy.ForecastTrials, in.PersonMedianMs = 3, 5*minute
	plan := delivery.ForecastOf(in)[0]
	if plan.EndP85CapPlusOne != plan.EndP85 || plan.EndP85PersonMedian != plan.EndP85 {
		t.Errorf("the reruns drew apart: %d, %d, and %d", plan.EndP85-now, plan.EndP85CapPlusOne-now, plan.EndP85PersonMedian-now)
	}
}

// TestAMilestoneWithNoHistoryHasNoForecast gives no forecast to a milestone none of whose unfinished work has
// history, and says so; a finished row to a milestone whose work all ended; and none to a plan with nothing left
// (FR-EST-8, FR-EST-14).
func TestAMilestoneWithNoHistoryHasNoForecast(t *testing.T) {
	b := took("db", 60*minute, 60*minute, 1)
	in := forecastIn(1,
		delivery.ForecastWork{WorkOrder: "a", Milestone: "m1", Estimate: delivery.Estimate{Status: delivery.NoHistory}},
		delivery.ForecastWork{WorkOrder: "b", Milestone: "m2", Estimate: confidentOn(b), Records: []delivery.Record{b}},
	)
	in.Milestones = []string{"m3", "m1", "m2"}
	forecasts := delivery.ForecastOf(in)
	none := forecastFor(t, forecasts, "m1")
	if none.Status != delivery.ForecastNoHistory || none.EndP50 != 0 || none.EndP85 != 0 || !slices.Equal(none.LeftOut, []string{"a"}) ||
		none.Explanation != "No forecast: none of the 1 unfinished work order of this milestone has finished history." {
		t.Errorf("a milestone with no history forecast %+v", none)
	}
	if plan := forecastFor(t, forecasts, ""); plan.Status != delivery.Forecasted || plan.EndP85 != now+60*minute {
		t.Errorf("the plan forecast %+v", plan)
	}
	if done := forecastFor(t, forecasts, "m3"); done.Status != delivery.ForecastFinished || done.Explanation != "Nothing is left to forecast: every work order of this milestone has ended." {
		t.Errorf("a milestone whose work all ended forecast %+v", done)
	}
	if empty := forecastOf(1); len(empty) != 1 || empty[0].Status != delivery.ForecastFinished || empty[0].Explanation != "Nothing is left to forecast: every work order of this plan has ended." {
		t.Errorf("a plan with nothing left forecast %+v", empty)
	}
}

// TestACapOfZeroIsNoCap follows the product's rule that a lane cap of zero caps nothing: the work runs as its
// dependencies allow, all at once, and a milestone's labels rest only on its own work and what it waits on, which
// nothing else can delay. Only a project no worker serves has no lane, and no date.
func TestACapOfZeroIsNoCap(t *testing.T) {
	a, b, c := took("da", 60*minute, 60*minute, 1), took("db", 120*minute, 120*minute, 2), took("dc", 90*minute, 90*minute, 3)
	work := []delivery.ForecastWork{
		{WorkOrder: "a", Milestone: "m1", Priority: 1, Estimate: confidentOn(a), Records: []delivery.Record{a}},
		{WorkOrder: "b", Milestone: "m1", Priority: 1, DependsOn: []string{"a"}, Estimate: confidentOn(b), Records: []delivery.Record{b}},
		{WorkOrder: "c", Milestone: "m2", Priority: 9, Estimate: confidentOn(c), Records: []delivery.Record{c}},
		{WorkOrder: "x", Milestone: "m3", Priority: 9, Estimate: delivery.Estimate{Status: delivery.NoHistory}},
	}
	forecasts := forecastOf(0, work...)
	plan, first, aside := forecastFor(t, forecasts, ""), forecastFor(t, forecasts, "m1"), forecastFor(t, forecasts, "m2")
	if plan.EndP85 != now+180*minute || first.EndP85 != now+180*minute || aside.EndP85 != now+90*minute || plan.EndP85CapPlusOne != plan.EndP85 || plan.Cap != 0 {
		t.Errorf("with no cap, the plan ends %d, m1 %d, and m2 %d", plan.EndP85-now, first.EndP85-now, aside.EndP85-now)
	}
	if first.Confidence != delivery.Confident || first.LowerBound != delivery.LowerNone || len(first.LeftOut) != 0 {
		t.Errorf("with no cap, work elsewhere with no history labels m1 %+v", first)
	}
	if plan.Confidence != delivery.Low || plan.LowerBound != delivery.LowerAll || !slices.Equal(plan.LeftOut, []string{"x"}) {
		t.Errorf("with no cap, the plan reads %+v", plan)
	}
	if !strings.HasPrefix(plan.Explanation, "Low confidence, from 2000 trials of 3 work orders, with no lane cap.") {
		t.Errorf("the explanation %q", plan.Explanation)
	}
	in := forecastIn(2, work...)
	in.NoWorker = true
	if unserved := delivery.ForecastOf(in)[0]; unserved.Status != delivery.ForecastNoCapacity || unserved.EndP50 != 0 ||
		unserved.Explanation != "No forecast: the project has no lane to run its work, because no worker serves it." {
		t.Errorf("with no worker, the plan forecast %+v", unserved)
	}
}

// TestUnfinishedWorkOutsideThePlanMakesDatesLowerBounds lists the unfinished work outside the plan that its work
// waits on, which the trials do not run, and makes the dates lower bounds; a dependency that ended counts for
// nothing.
func TestUnfinishedWorkOutsideThePlanMakesDatesLowerBounds(t *testing.T) {
	b := took("db", 60*minute, 60*minute, 1)
	in := forecastIn(1, delivery.ForecastWork{WorkOrder: "b", Title: "Inside", Estimate: confidentOn(b), Records: []delivery.Record{b}, DependsOn: []string{"ext", "ended"}})
	in.Outside = []string{"ext"}
	plan := delivery.ForecastOf(in)[0]
	if plan.LowerBound != delivery.LowerAll || !slices.Equal(plan.Outside, []string{"ext"}) || plan.EndP85 != now+60*minute ||
		!strings.Contains(plan.Explanation, "waits on unfinished work outside this plan (ext)") {
		t.Errorf("a plan waiting on work outside it reads %+v", plan)
	}
}

// TestRepeatsAndSelfDependenciesChangeNothing counts a work order listed twice once, and ignores a dependency of a
// work order on itself, which would otherwise hold it forever.
func TestRepeatsAndSelfDependenciesChangeNothing(t *testing.T) {
	records := []delivery.Record{took("d1", 60*minute, 30*minute, 1), took("d2", 120*minute, 60*minute, 2)}
	one := delivery.ForecastWork{WorkOrder: "a", Estimate: confidentOn(records...), Records: records}
	alone := forecastOf(1, one)
	looped := one
	looped.DependsOn = []string{"a"}
	if twice := forecastOf(1, one, looped); !reflect.DeepEqual(twice, alone) {
		t.Errorf("a repeated, self-dependent work order forecast %+v, alone %+v", twice, alone)
	}
	if self := forecastOf(1, looped); !reflect.DeepEqual(self, alone) {
		t.Errorf("a self-dependent work order forecast %+v, alone %+v", self, alone)
	}
}

// BenchmarkForecastOfALargePlan measures one forecast of a generated plan of 400 unfinished work orders in 20
// milestones, each depending on up to two earlier ones and drawing from 30 records, at the default 2,000 trials
// under a cap of 4 lanes, which is what the forecast tick runs off the request path.
func BenchmarkForecastOfALargePlan(b *testing.B) {
	rng := rand.New(rand.NewPCG(20261114, 400))
	records := make([]delivery.Record, 30)
	for i := range records {
		lane := int64(10+rng.IntN(240)) * minute
		records[i] = took(fmt.Sprintf("d%d", i), lane+int64(rng.IntN(600))*minute, lane, float64(1+rng.IntN(20)))
	}
	work := make([]delivery.ForecastWork, 400)
	milestones := make([]string, 20)
	for i := range milestones {
		milestones[i] = fmt.Sprintf("m%02d", i)
	}
	for i := range work {
		w := delivery.ForecastWork{WorkOrder: fmt.Sprintf("wo%03d", i), Milestone: milestones[i/20], Priority: int64(rng.IntN(5)), Estimate: confidentOn(records...), Records: records}
		for range rng.IntN(3) {
			if i > 0 {
				w.DependsOn = append(w.DependsOn, fmt.Sprintf("wo%03d", rng.IntN(i)))
			}
		}
		work[i] = w
	}
	in := forecastIn(4, work...)
	in.Milestones, in.Policy.ForecastTrials = milestones, 2000
	for b.Loop() {
		if forecasts := delivery.ForecastOf(in); len(forecasts) != 21 || forecasts[0].Status != delivery.Forecasted {
			b.Fatalf("the plan's forecasts are %+v", forecasts[0])
		}
	}
}
