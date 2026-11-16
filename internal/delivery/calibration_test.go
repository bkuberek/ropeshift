package delivery_test

import (
	"fmt"
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

// calOutcome is a baselined work order that reached done inside its band at every comparison, ended at a time,
// with a confidence. Each change turns it into the case a test needs.
func calOutcome(id string, confidence delivery.Confidence, ended int64, changes ...func(*delivery.Outcome)) delivery.Outcome {
	yes, one := true, 1.0
	out := delivery.Outcome{
		SubjectKind: delivery.OutcomeOfWorkOrder, Subject: id, Baseline: "b-" + id, Confidence: confidence, Tier: 1, Repository: "repo-1",
		Outcome: workorders.Done, ActualEnd: ended, Ended: ended, EstimatorVersion: 1,
		TimeAtOrBelowP50: &yes, TimeAtOrBelowP85: &yes, TimeRatio: &one, CostAtOrBelowP50: &yes, CostAtOrBelowP85: &yes, CostRatio: &one,
	}
	for _, change := range changes {
		change(&out)
	}
	return out
}

func calMissesP50(o *delivery.Outcome) {
	no := false
	o.TimeAtOrBelowP50, o.CostAtOrBelowP50 = &no, &no
}
func calMissesP85(o *delivery.Outcome) {
	no := false
	o.TimeAtOrBelowP85, o.CostAtOrBelowP85 = &no, &no
}
func calOpen(o *delivery.Outcome) {
	o.P85Open, o.TimeAtOrBelowP85, o.CostAtOrBelowP85 = true, nil, nil
}
func calTier(n int) func(*delivery.Outcome) { return func(o *delivery.Outcome) { o.Tier = n } }
func calVersion(n int) func(*delivery.Outcome) {
	return func(o *delivery.Outcome) { o.EstimatorVersion = n }
}
func calRepository(id string) func(*delivery.Outcome) {
	return func(o *delivery.Outcome) { o.Repository = id }
}
func calRatio(time, cost float64) func(*delivery.Outcome) {
	return func(o *delivery.Outcome) { o.TimeRatio, o.CostRatio = &time, &cost }
}
func calEnded(state workorders.State) func(*delivery.Outcome) {
	return func(o *delivery.Outcome) {
		o.Outcome = state
		o.TimeAtOrBelowP50, o.TimeAtOrBelowP85, o.TimeRatio, o.CostAtOrBelowP50, o.CostAtOrBelowP85, o.CostRatio = nil, nil, nil, nil, nil, nil
	}
}
func calCostIsALowerBound(o *delivery.Outcome) {
	o.ActualCostAtLeast, o.CostAtOrBelowP50, o.CostAtOrBelowP85, o.CostRatio = true, nil, nil, nil
}

// calMany makes n outcomes of one confidence, ended one after another from a time, each changed the same way.
func calMany(prefix string, n int, confidence delivery.Confidence, from int64, changes ...func(*delivery.Outcome)) []delivery.Outcome {
	var out []delivery.Outcome
	for i := range n {
		out = append(out, calOutcome(fmt.Sprintf("%s-%d", prefix, i), confidence, from+int64(i), changes...))
	}
	return out
}

// calRecords are the delivery records of outcomes, as the fold keeps them: one per work order, done unless the
// outcome says otherwise, in the outcome's repository.
func calRecords(outcomes []delivery.Outcome) []delivery.Record {
	var out []delivery.Record
	for _, o := range outcomes {
		if o.SubjectKind == delivery.OutcomeOfWorkOrder {
			out = append(out, delivery.Record{WorkOrder: o.Subject, Keys: delivery.Keys{Repository: o.Repository}, Outcome: o.Outcome, Ended: o.Ended})
		}
	}
	return out
}

func calRate(t *testing.T, name string, got delivery.Rate, n, hits int64, rate float64) {
	t.Helper()
	if got.N != n || got.Hits != hits {
		t.Errorf("%s counts %d of %d, want %d of %d", name, got.Hits, got.N, hits, n)
	}
	switch {
	case rate < 0 && got.Rate != nil:
		t.Errorf("%s shows rate %v from %d outcomes, want none", name, *got.Rate, got.N)
	case rate >= 0 && (got.Rate == nil || math.Abs(*got.Rate-rate) > 1e-9):
		t.Errorf("%s rate %v, want %v", name, got.Rate, rate)
	}
}

func calMedian(t *testing.T, name string, got delivery.MedianRatio, n int64, median float64) {
	t.Helper()
	if got.N != n {
		t.Errorf("%s ratio counts %d, want %d", name, got.N, n)
	}
	switch {
	case median < 0 && got.Median != nil:
		t.Errorf("%s shows median %v, want none", name, *got.Median)
	case median >= 0 && (got.Median == nil || math.Abs(*got.Median-median) > 1e-9):
		t.Errorf("%s median %v, want %v", name, got.Median, median)
	}
}

// TestCalibrationCountsAnOpenP85WhereItBelongs holds FR-EST-11: an outcome whose baseline p85 was open counts in the
// p50 hit rate and the ratio and not in the p85 hit rate, its count shows, and a rate is shown only from 10 outcomes.
func TestCalibrationCountsAnOpenP85WhereItBelongs(t *testing.T) {
	to := 30 * week
	tests := []struct {
		name                                      string
		outcomes                                  []delivery.Outcome
		p50N, p50Hits                             int64
		p50Rate                                   float64
		p85N, p85Hits                             int64
		p85Rate                                   float64
		open, ratioN                              int64
		ratioMedian                               float64
		costP50N, costP85N                        int64
		costP50Rate, costP85Rate, costRatioMedian float64
		costRatioN, total, done, failed, canceled int64
	}{
		{
			name:     "12 outcomes, 4 open: the p85 rate rests on 8, so it shows no rate",
			outcomes: append(calMany("c", 8, delivery.Confident, to-week), calMany("o", 4, delivery.Confident, to-week+100, calOpen)...),
			p50N:     12, p50Hits: 12, p50Rate: 1, p85N: 8, p85Hits: 8, p85Rate: -1, open: 4, ratioN: 12, ratioMedian: 1,
			costP50N: 12, costP85N: 8, costP50Rate: 1, costP85Rate: -1, costRatioN: 12, costRatioMedian: 1, total: 12, done: 12,
		},
		{
			name:     "14 closed and 4 open: the p85 rate shows over the 14",
			outcomes: append(calMany("c", 14, delivery.Confident, to-week, calMissesP85), calMany("o", 4, delivery.Confident, to-week+100, calOpen)...),
			p50N:     18, p50Hits: 18, p50Rate: 1, p85N: 14, p85Hits: 0, p85Rate: 0, open: 4, ratioN: 18, ratioMedian: 1,
			costP50N: 18, costP85N: 14, costP50Rate: 1, costP85Rate: 0, costRatioN: 18, costRatioMedian: 1, total: 18, done: 18,
		},
		{
			name:     "9 outcomes show their counts and no rate",
			outcomes: calMany("c", 9, delivery.Confident, to-week),
			p50N:     9, p50Hits: 9, p50Rate: -1, p85N: 9, p85Hits: 9, p85Rate: -1, open: 0, ratioN: 9, ratioMedian: -1,
			costP50N: 9, costP85N: 9, costP50Rate: -1, costP85Rate: -1, costRatioN: 9, costRatioMedian: -1, total: 9, done: 9,
		},
		{
			name:     "10 outcomes show their rates",
			outcomes: append(calMany("c", 7, delivery.Confident, to-week), calMany("m", 3, delivery.Confident, to-week+100, calMissesP50, calMissesP85)...),
			p50N:     10, p50Hits: 7, p50Rate: 0.7, p85N: 10, p85Hits: 7, p85Rate: 0.7, open: 0, ratioN: 10, ratioMedian: 1,
			costP50N: 10, costP85N: 10, costP50Rate: 0.7, costP85Rate: 0.7, costRatioN: 10, costRatioMedian: 1, total: 10, done: 10,
		},
		{
			name:     "failed and canceled work counts apart and compares nothing",
			outcomes: append(calMany("c", 10, delivery.Confident, to-week), calOutcome("f", delivery.Confident, to-week+200, calEnded(workorders.Failed)), calOutcome("x", delivery.Confident, to-week+201, calEnded(workorders.Canceled))),
			p50N:     10, p50Hits: 10, p50Rate: 1, p85N: 10, p85Hits: 10, p85Rate: 1, open: 0, ratioN: 10, ratioMedian: 1,
			costP50N: 10, costP85N: 10, costP50Rate: 1, costP85Rate: 1, costRatioN: 10, costRatioMedian: 1, total: 12, done: 10, failed: 1, canceled: 1,
		},
		{
			name:     "a cost that is a lower bound compares nothing, so cost counts fewer than time",
			outcomes: append(calMany("c", 10, delivery.Confident, to-week), calMany("b", 3, delivery.Confident, to-week+100, calCostIsALowerBound)...),
			p50N:     13, p50Hits: 13, p50Rate: 1, p85N: 13, p85Hits: 13, p85Rate: 1, open: 0, ratioN: 13, ratioMedian: 1,
			costP50N: 10, costP85N: 10, costP50Rate: 1, costP85Rate: 1, costRatioN: 10, costRatioMedian: 1, total: 13, done: 13,
		},
		{
			name:     "the median ratio is the nearest rank, the lower of two middle values",
			outcomes: append(calMany("lo", 5, delivery.Confident, to-week, calRatio(0.5, 0.5)), calMany("hi", 5, delivery.Confident, to-week+100, calRatio(2, 2))...),
			p50N:     10, p50Hits: 10, p50Rate: 1, p85N: 10, p85Hits: 10, p85Rate: 1, open: 0, ratioN: 10, ratioMedian: 0.5,
			costP50N: 10, costP85N: 10, costP50Rate: 1, costP85Rate: 1, costRatioN: 10, costRatioMedian: 0.5, total: 10, done: 10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := delivery.CalibrationOf(tt.outcomes, calRecords(tt.outcomes), nil, to-2*week, to)
			s := report.Scopes[0].Recent.Confident.Total
			if s.Outcomes != tt.total || s.Done != tt.done || s.Failed != tt.failed || s.Canceled != tt.canceled || s.P85Open != tt.open {
				t.Errorf("counts outcomes %d done %d failed %d canceled %d open %d", s.Outcomes, s.Done, s.Failed, s.Canceled, s.P85Open)
			}
			calRate(t, "time p50", s.Time.P50, tt.p50N, tt.p50Hits, tt.p50Rate)
			calRate(t, "time p85", s.Time.P85, tt.p85N, tt.p85Hits, tt.p85Rate)
			calMedian(t, "time", s.Time.Ratio, tt.ratioN, tt.ratioMedian)
			if tt.costP50N != s.Cost.P50.N || tt.costP85N != s.Cost.P85.N || tt.costRatioN != s.Cost.Ratio.N {
				t.Errorf("cost counts p50 %d p85 %d ratio %d, want %d %d %d", s.Cost.P50.N, s.Cost.P85.N, s.Cost.Ratio.N, tt.costP50N, tt.costP85N, tt.costRatioN)
			}
			if (tt.costP50Rate < 0) != (s.Cost.P50.Rate == nil) || (tt.costP85Rate < 0) != (s.Cost.P85.Rate == nil) || (tt.costRatioMedian < 0) != (s.Cost.Ratio.Median == nil) {
				t.Errorf("cost shows p50 %v p85 %v median %v", s.Cost.P50.Rate, s.Cost.P85.Rate, s.Cost.Ratio.Median)
			}
		})
	}
}

// TestCalibrationKeepsConfidentAndLowApart holds FR-EST-11: a low-confidence outcome never counts in the confident
// rates, which are the headline, and an estimate with no history belongs to neither.
func TestCalibrationKeepsConfidentAndLowApart(t *testing.T) {
	to := 30 * week
	outcomes := slicesConcat(
		calMany("c", 10, delivery.Confident, to-week),
		calMany("l", 10, delivery.Low, to-week, calMissesP50, calMissesP85),
		[]delivery.Outcome{calOutcome("none", "", to-week, calEnded(workorders.Done))},
	)
	recent := delivery.CalibrationOf(outcomes, calRecords(outcomes), nil, to-2*week, to).Scopes[0].Recent
	calRate(t, "confident p50", recent.Confident.Total.Time.P50, 10, 10, 1)
	calRate(t, "low p50", recent.Low.Total.Time.P50, 10, 0, 0)
	calRate(t, "low p85", recent.Low.Total.Time.P85, 10, 0, 0)
	if recent.Confident.Total.Outcomes != 10 || recent.Low.Total.Outcomes != 10 {
		t.Errorf("confident %d and low %d outcomes, want 10 and 10", recent.Confident.Total.Outcomes, recent.Low.Total.Outcomes)
	}
}

// TestCalibrationGroupsByTierAndEstimatorVersion holds FR-EST-11: the same counts by tier and by estimator version,
// each ordered by its key, for confident and low outcomes apart.
func TestCalibrationGroupsByTierAndEstimatorVersion(t *testing.T) {
	to := 30 * week
	outcomes := slicesConcat(
		calMany("t2", 10, delivery.Confident, to-week, calTier(2)),
		calMany("t1", 12, delivery.Confident, to-week+50, calTier(1), calMissesP50),
		calMany("v2", 3, delivery.Confident, to-week+100, calTier(1), calVersion(2)),
		calMany("low", 4, delivery.Low, to-week, calTier(6)),
	)
	recent := delivery.CalibrationOf(outcomes, calRecords(outcomes), nil, to-2*week, to).Scopes[0].Recent
	tiers := recent.Confident.Tiers
	if len(tiers) != 2 || tiers[0].Tier != 1 || tiers[1].Tier != 2 {
		t.Fatalf("confident tiers %+v", tiers)
	}
	if tiers[0].Outcomes != 15 || tiers[1].Outcomes != 10 {
		t.Errorf("tier counts %d and %d, want 15 and 10", tiers[0].Outcomes, tiers[1].Outcomes)
	}
	calRate(t, "tier 1 p50", tiers[0].Time.P50, 15, 3, 0.2)
	calRate(t, "tier 2 p50", tiers[1].Time.P50, 10, 10, 1)
	versions := recent.Confident.Versions
	if len(versions) != 2 || versions[0].Version != 1 || versions[1].Version != 2 || versions[0].Outcomes != 22 || versions[1].Outcomes != 3 {
		t.Errorf("confident versions %+v", versions)
	}
	calRate(t, "version 2 p50", versions[1].Time.P50, 3, 3, -1)
	if low := recent.Low.Tiers; len(low) != 1 || low[0].Tier != 6 || low[0].Outcomes != 4 {
		t.Errorf("low tiers %+v", low)
	}
}

// TestCalibrationReadsTheWindowAndAllTime holds FR-EST-11: over the last window and over all time, an outcome that
// ended before the window counting only in all time, and one that ended at or after the end counting in neither.
func TestCalibrationReadsTheWindowAndAllTime(t *testing.T) {
	to := 30 * week
	from := to - 4*week
	outcomes := slicesConcat(
		calMany("new", 10, delivery.Confident, from+10),
		calMany("old", 5, delivery.Confident, from-3*week, calMissesP50),
		[]delivery.Outcome{calOutcome("later", delivery.Confident, to)},
	)
	report := delivery.CalibrationOf(outcomes, calRecords(outcomes), nil, from, to)
	scope := report.Scopes[0]
	if report.From != from || report.To != to || scope.Recent.From != from || scope.Recent.To != to || scope.AllTime.From != 0 || scope.AllTime.To != to {
		t.Errorf("windows %+v %+v %+v", report, scope.Recent, scope.AllTime)
	}
	calRate(t, "recent p50", scope.Recent.Confident.Total.Time.P50, 10, 10, 1)
	calRate(t, "all time p50", scope.AllTime.Confident.Total.Time.P50, 15, 10, 10.0/15)
}

// TestCalibrationHasAWeeklyLine holds FR-EST-11: twelve weeks ending where the window does, each with the outcomes
// that ended in it, confident and low apart, and a rate only from 10 outcomes in the week.
func TestCalibrationHasAWeeklyLine(t *testing.T) {
	to := 30 * week
	outcomes := slicesConcat(
		calMany("a", 10, delivery.Confident, to-week),
		calMany("b", 3, delivery.Confident, to-2*week, calMissesP50),
		calMany("l", 2, delivery.Low, to-2*week+5),
		[]delivery.Outcome{calOutcome("edge", delivery.Confident, to-12*week), calOutcome("before", delivery.Confident, to-12*week-1)},
	)
	weeks := delivery.CalibrationOf(outcomes, calRecords(outcomes), nil, to-week, to).Scopes[0].Weeks
	if len(weeks) != 12 || weeks[0].From != to-12*week || weeks[11].From != to-week {
		t.Fatalf("weeks %d, first %d, last %d", len(weeks), weeks[0].From, weeks[11].From)
	}
	calRate(t, "last week", weeks[11].Confident.Time.P50, 10, 10, 1)
	calRate(t, "week before", weeks[10].Confident.Time.P50, 3, 0, -1)
	if weeks[10].Low.Outcomes != 2 || weeks[0].Confident.Outcomes != 1 || weeks[1].Confident.Outcomes != 0 {
		t.Errorf("week counts: low %d, first %d, second %d", weeks[10].Low.Outcomes, weeks[0].Confident.Outcomes, weeks[1].Confident.Outcomes)
	}
}

// TestCalibrationReadsEachRepositoryApart holds FR-EST-11: all work first, then each repository in order, each
// holding only its own outcomes.
func TestCalibrationReadsEachRepositoryApart(t *testing.T) {
	to := 30 * week
	outcomes := slicesConcat(
		calMany("w", 10, delivery.Confident, to-week, calRepository("web")),
		calMany("a", 4, delivery.Confident, to-week+100, calRepository("api"), calMissesP50),
	)
	scopes := delivery.CalibrationOf(outcomes, calRecords(outcomes), nil, to-2*week, to).Scopes
	if len(scopes) != 3 || scopes[0].Repository != "" || scopes[1].Repository != "api" || scopes[2].Repository != "web" {
		t.Fatalf("scopes %+v", scopes)
	}
	if scopes[0].Recent.Confident.Total.Outcomes != 14 || scopes[1].Recent.Confident.Total.Outcomes != 4 || scopes[2].Recent.Confident.Total.Outcomes != 10 {
		t.Errorf("counts %d %d %d", scopes[0].Recent.Confident.Total.Outcomes, scopes[1].Recent.Confident.Total.Outcomes, scopes[2].Recent.Confident.Total.Outcomes)
	}
	calRate(t, "api p50", scopes[1].Recent.Confident.Total.Time.P50, 4, 0, -1)
	calRate(t, "web p50", scopes[2].Recent.Confident.Total.Time.P50, 10, 10, 1)
}

// TestCalibrationSharesWorkByHowItWasEstimated holds FR-EST-11: of the work that ended, the share whose baseline
// estimate was confident, low, or absent, absent covering work with no baseline and an estimate with no history,
// and with no share under 10 ended.
func TestCalibrationSharesWorkByHowItWasEstimated(t *testing.T) {
	to := 30 * week
	baselined := slicesConcat(
		calMany("c", 5, delivery.Confident, to-week),
		calMany("l", 3, delivery.Low, to-week),
		[]delivery.Outcome{calOutcome("nohist", "", to-week, calEnded(workorders.Done)), calOutcome("failed", delivery.Confident, to-week, calEnded(workorders.Failed))},
	)
	records := append(calRecords(baselined),
		delivery.Record{WorkOrder: "unbaselined-1", Outcome: workorders.Done, Ended: to - week},
		delivery.Record{WorkOrder: "unbaselined-2", Outcome: workorders.Canceled, Ended: to - week},
		delivery.Record{WorkOrder: "old", Outcome: workorders.Done, Ended: to - 5*week})
	scope := delivery.CalibrationOf(baselined, records, nil, to-2*week, to).Scopes[0]
	share := scope.Recent.Share
	if share.Ended != 12 || share.Confident.Count != 6 || share.Low.Count != 3 || share.None.Count != 3 {
		t.Fatalf("share %+v", share)
	}
	if share.Confident.Share == nil || *share.Confident.Share != 0.5 || *share.Low.Share != 0.25 || *share.None.Share != 0.25 {
		t.Errorf("shares %+v", share)
	}
	if all := scope.AllTime.Share; all.Ended != 13 || all.None.Count != 4 {
		t.Errorf("all time share %+v", all)
	}
	few := delivery.CalibrationOf(baselined[:4], calRecords(baselined[:4]), nil, to-2*week, to).Scopes[0].Recent.Share
	if few.Ended != 4 || few.Confident.Count != 4 || few.Confident.Share != nil || few.None.Share != nil {
		t.Errorf("a share of 4 ended work orders %+v", few)
	}
}

// TestCalibrationPutsEachMilestoneBesideItsForecast holds FR-EST-11: each milestone's baseline forecast against
// the day it ended, confident and low forecasts reported apart, a lower bound date compared nowhere it is open, and
// the newest first.
func TestCalibrationPutsEachMilestoneBesideItsForecast(t *testing.T) {
	to := 30 * week
	yes, no := true, false
	forecasts := map[string]delivery.Forecast{
		"f-1": {ID: "f-1", Plan: "plan-1", Milestone: "m1", Status: delivery.Forecasted, Confidence: delivery.Confident, EndP50: to - 3*week, EndP85: to - 2*week},
		"f-2": {ID: "f-2", Plan: "plan-1", Milestone: "m2", Status: delivery.Forecasted, Confidence: delivery.Low, LowerBound: delivery.LowerP85, EndP50: to - 3*week, EndP85: to - 2*week},
		"f-3": {ID: "f-3", Plan: "plan-1", Milestone: "m3", Status: delivery.ForecastNoHistory},
	}
	milestone := func(name, forecast string, confidence delivery.Confidence, ended int64, state workorders.State, p50, p85 *bool) delivery.Outcome {
		return delivery.Outcome{
			SubjectKind: delivery.OutcomeOfMilestone, Subject: name, Plan: "plan-1", Baseline: "b-1", Forecast: forecast, Confidence: confidence,
			Outcome: state, ActualEnd: ended, Ended: ended, TimeAtOrBelowP50: p50, TimeAtOrBelowP85: p85,
		}
	}
	outcomes := []delivery.Outcome{
		milestone("m1", "f-1", delivery.Confident, to-2*week-10, workorders.Done, &no, &yes),
		milestone("m2", "f-2", delivery.Low, to-week, workorders.Done, &yes, nil),
		milestone("m3", "f-3", "", to-week+5, workorders.Done, nil, nil),
		milestone("m4", "", delivery.Confident, to-10*week, workorders.Failed, nil, nil),
	}
	got := delivery.CalibrationOf(outcomes, nil, forecasts, to-4*week, to).Milestones
	if len(got.Results) != 4 || got.Results[0].Milestone != "m3" || got.Results[1].Milestone != "m2" || got.Results[2].Milestone != "m1" || got.Results[3].Milestone != "m4" {
		t.Fatalf("results %+v", got.Results)
	}
	m1, m2 := got.Results[2], got.Results[1]
	if m1.ForecastP50 != to-3*week || m1.ForecastP85 != to-2*week || m1.ActualEnd != to-2*week-10 || m1.Plan != "plan-1" || m1.Confidence != delivery.Confident ||
		m1.AtOrBelowP50 == nil || *m1.AtOrBelowP50 || m1.AtOrBelowP85 == nil || !*m1.AtOrBelowP85 {
		t.Errorf("m1 %+v", m1)
	}
	if m2.LowerBound != delivery.LowerP85 || m2.AtOrBelowP85 != nil || m2.AtOrBelowP50 == nil || !*m2.AtOrBelowP50 {
		t.Errorf("m2 %+v", m2)
	}
	recent, all := got.Recent, got.AllTime
	if recent.Confident.Outcomes != 1 || recent.Low.Outcomes != 1 || recent.None != 1 || all.Confident.Outcomes != 2 || all.Confident.Failed != 1 {
		t.Errorf("recent %+v all time %+v", recent, all)
	}
	calRate(t, "confident p50", recent.Confident.P50, 1, 0, -1)
	if recent.Low.P85Open != 1 || recent.Low.P85.N != 0 || recent.Low.P50.N != 1 {
		t.Errorf("a p85 date that is a lower bound: %+v", recent.Low)
	}
}

// TestCalibrationPointsExportOnlyTheAllowedLabels holds FR-OBS-15: hit rates, ratios, and the confidence share as
// metrics labeled only by quantile, quantity, tier, and confidence, with a point only where a rate is shown.
func TestCalibrationPointsExportOnlyTheAllowedLabels(t *testing.T) {
	to := 30 * week
	outcomes := slicesConcat(
		calMany("c", 12, delivery.Confident, to-week, calTier(2), calRepository("web")),
		calMany("l", 4, delivery.Low, to-week, calTier(6), calMissesP50),
	)
	points := delivery.CalibrationPoints(outcomes, calRecords(outcomes), to-2*week, to)
	allowed := map[string]bool{"quantile": true, "quantity": true, "tier": true, "confidence": true}
	byName := map[string]int{}
	var headline, lowRate bool
	for _, p := range points {
		byName[p.Name]++
		for key := range p.Labels {
			if !allowed[key] {
				t.Errorf("%s carries %s", p.Name, key)
			}
		}
		if p.Unit != "1" {
			t.Errorf("%s in %s", p.Name, p.Unit)
		}
		if p.Name == "estimate.hit_rate" && p.Labels["confidence"] == "confident" && p.Labels["tier"] == "all" && p.Labels["quantity"] == "time" && p.Labels["quantile"] == "p85" && p.Value == 1 {
			headline = true
		}
		lowRate = lowRate || (p.Labels["confidence"] == "low" && p.Name != "estimate.share")
	}
	if lowRate {
		t.Errorf("4 low-confidence outcomes have a rate point")
	}
	if !headline {
		t.Errorf("no confident p85 time hit rate over all tiers in %+v", points)
	}
	// Confident: p50 and p85 for time and cost, over tier 2 and all tiers, and a ratio for each quantity and tier.
	if byName["estimate.hit_rate"] != 8 || byName["estimate.ratio"] != 4 {
		t.Errorf("points %v", byName)
	}
	// 16 ended work orders: a share for confident, low, and none.
	if byName["estimate.share"] != 3 {
		t.Errorf("share points %v", byName)
	}
	for _, p := range delivery.CalibrationPoints(outcomes[:5], calRecords(outcomes[:5]), to-2*week, to) {
		t.Errorf("5 outcomes export %+v", p)
	}
}

func slicesConcat(parts ...[]delivery.Outcome) []delivery.Outcome {
	var out []delivery.Outcome
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
