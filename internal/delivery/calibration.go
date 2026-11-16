package delivery

import (
	"cmp"
	"maps"
	"slices"
	"strconv"

	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MinCalibrationOutcomes is how many outcomes a rate needs before it shows. A rate over fewer is noise that reads as
// a result, so a group shows its counts and no rate (FR-EST-11).
const MinCalibrationOutcomes = 10

// Rate is how often something held among the outcomes that could be compared: N of them were, Hits of those held.
// The rate itself is absent under MinCalibrationOutcomes compared.
type Rate struct {
	N    int64    `json:"n"`
	Hits int64    `json:"hits"`
	Rate *float64 `json:"rate,omitempty"`
}

// MedianRatio is the median of actual over p50 among N outcomes with a ratio, by nearest rank. The median is absent
// under MinCalibrationOutcomes ratios.
type MedianRatio struct {
	N      int64    `json:"n"`
	Median *float64 `json:"median,omitempty"`
}

// Comparisons are what one quantity, delivery time or cost, did against its baseline: the share at or below the
// p50 band (calibrated near 0.5), the share at or below the p85 band (near 0.85), and the median ratio of actual to
// p50 (near 1). An outcome with no comparison for a measure is left out of that measure's N, so an open p85 counts
// in the p50 rate and the ratio and not in the p85 rate.
type Comparisons struct {
	P50   Rate        `json:"p50"`
	P85   Rate        `json:"p85"`
	Ratio MedianRatio `json:"ratio"`
}

// Standing is the calibration of one group of work-order outcomes. Outcomes counts all of them, Done, Failed, and
// Canceled by how the work ended, and P85Open the finished work whose baseline p85 was open, which the p85 rate
// leaves out. Only finished work compares.
type Standing struct {
	Outcomes int64       `json:"outcomes"`
	Done     int64       `json:"done"`
	Failed   int64       `json:"failed"`
	Canceled int64       `json:"canceled"`
	P85Open  int64       `json:"p85_open"`
	Time     Comparisons `json:"time"`
	Cost     Comparisons `json:"cost"`
}

// TierStanding is a Standing of the outcomes whose estimate came from one tier.
type TierStanding struct {
	Tier int `json:"tier"`
	Standing
}

// VersionStanding is a Standing of the outcomes whose estimate one estimator version made.
type VersionStanding struct {
	Version int `json:"estimator_version"`
	Standing
}

// Calibrated is one confidence's outcomes over one period: all together, then by tier, then by estimator version,
// each in order of its key.
type Calibrated struct {
	Total    Standing          `json:"total"`
	Tiers    []TierStanding    `json:"tiers"`
	Versions []VersionStanding `json:"estimator_versions"`
}

// Share is a count of ended work and its share of all that ended, absent under MinCalibrationOutcomes ended.
type Share struct {
	Count int64    `json:"count"`
	Share *float64 `json:"share,omitempty"`
}

// ConfidenceShare is how the work that ended in a period was estimated: with a confident baseline estimate, a
// low-confidence one, or none, which covers work with no baseline and an estimate with no history.
type ConfidenceShare struct {
	Ended     int64 `json:"ended"`
	Confident Share `json:"confident"`
	Low       Share `json:"low"`
	None      Share `json:"none"`
}

// CalibrationPeriod is calibration over one stretch of time, from From to To. Confident is the headline and Low
// stands apart, so low-confidence outcomes never blur whether the main estimator is calibrated.
type CalibrationPeriod struct {
	From      int64           `json:"from"`
	To        int64           `json:"to"`
	Share     ConfidenceShare `json:"share"`
	Confident Calibrated      `json:"confident"`
	Low       Calibrated      `json:"low"`
}

// CalibrationWeek is one week of the weekly line, from From: the work that ended in it, confident and low apart.
type CalibrationWeek struct {
	From      int64    `json:"from"`
	Confident Standing `json:"confident"`
	Low       Standing `json:"low"`
}

// CalibrationScope is calibration over one repository's work, or over all of the project's work when Repository is
// empty: the recent window, all time, and the twelve weeks up to the window's end.
type CalibrationScope struct {
	Repository string            `json:"repository,omitempty"`
	Recent     CalibrationPeriod `json:"recent"`
	AllTime    CalibrationPeriod `json:"all_time"`
	Weeks      []CalibrationWeek `json:"weeks"`
}

// MilestoneResult is one milestone's baseline forecast beside when it ended. ForecastP50 and ForecastP85 are the
// dates the forecast gave, absent for a forecast with no dates, and LowerBound says which were lower bounds, which
// no comparison covers. AtOrBelowP50 and AtOrBelowP85 are absent where nothing compares.
type MilestoneResult struct {
	Plan         string           `json:"plan_id"`
	Milestone    string           `json:"milestone_id"`
	Confidence   Confidence       `json:"confidence,omitempty"`
	Outcome      workorders.State `json:"outcome"`
	ForecastP50  int64            `json:"forecast_p50,omitempty"`
	ForecastP85  int64            `json:"forecast_p85,omitempty"`
	LowerBound   LowerBound       `json:"lower_bound,omitempty"`
	ActualEnd    int64            `json:"actual_end"`
	AtOrBelowP50 *bool            `json:"at_or_below_p50"`
	AtOrBelowP85 *bool            `json:"at_or_below_p85"`
}

// MilestoneStanding is the calibration of one confidence's milestone forecasts: their counts, and how often the
// milestone ended at or before the forecast's p50 and p85 dates. P85Open counts the finished milestones whose p85
// date was a lower bound, which the p85 rate leaves out.
type MilestoneStanding struct {
	Outcomes int64 `json:"outcomes"`
	Done     int64 `json:"done"`
	Failed   int64 `json:"failed"`
	Canceled int64 `json:"canceled"`
	P85Open  int64 `json:"p85_open"`
	P50      Rate  `json:"p50"`
	P85      Rate  `json:"p85"`
}

// MilestonePeriod is milestone forecasts over one stretch of time, confident and low apart. None counts the
// milestones whose forecast had no confidence, which had no dates to compare.
type MilestonePeriod struct {
	From      int64             `json:"from"`
	To        int64             `json:"to"`
	Confident MilestoneStanding `json:"confident"`
	Low       MilestoneStanding `json:"low"`
	None      int64             `json:"none"`
}

// MilestoneCalibration is every milestone forecast against its actual, the newest first, with the calibration of the
// recent window and of all time.
type MilestoneCalibration struct {
	Recent  MilestonePeriod   `json:"recent"`
	AllTime MilestonePeriod   `json:"all_time"`
	Results []MilestoneResult `json:"results"`
}

// Calibration is how often finished work landed inside the estimates it was baselined with (FR-EST-11), read from
// the project's own outcomes alone: all work and then each repository, over the window from From to To and over all
// time, with a weekly line, and each milestone's forecast against its actual.
type Calibration struct {
	From       int64                `json:"from"`
	To         int64                `json:"to"`
	Scopes     []CalibrationScope   `json:"scopes"`
	Milestones MilestoneCalibration `json:"milestones"`
}

// CalibrationOf reads a project's estimate outcomes and delivery records, with the forecasts its baselines hold by
// ID, over the window from From up to To. Work counts where it ended. Records say what ended, so the share of work
// estimated with confidence counts work that has no outcome as having none.
func CalibrationOf(outcomes []Outcome, records []Record, forecasts map[string]Forecast, from, to int64) Calibration {
	var work, milestones []Outcome
	for _, o := range outcomes {
		if o.SubjectKind == OutcomeOfMilestone {
			milestones = append(milestones, o)
		} else {
			work = append(work, o)
		}
	}
	repositories := map[string]bool{}
	for _, o := range work {
		repositories[o.Repository] = true
	}
	for _, r := range records {
		repositories[r.Repository] = true
	}
	delete(repositories, "")
	out := Calibration{From: from, To: to, Milestones: milestonesOf(milestones, forecasts, from, to)}
	for _, repository := range append([]string{""}, slices.Sorted(maps.Keys(repositories))...) {
		out.Scopes = append(out.Scopes, scopeOf(repository, work, records, from, to))
	}
	return out
}

func scopeOf(repository string, work []Outcome, records []Record, from, to int64) CalibrationScope {
	var outcomes []Outcome
	for _, o := range work {
		if repository == "" || o.Repository == repository {
			outcomes = append(outcomes, o)
		}
	}
	var ended []Record
	for _, r := range records {
		if repository == "" || r.Repository == repository {
			ended = append(ended, r)
		}
	}
	scope := CalibrationScope{
		Repository: repository, Recent: periodOf(outcomes, ended, from, to), AllTime: periodOf(outcomes, ended, 0, to),
		Weeks: make([]CalibrationWeek, 0, trendWeeks),
	}
	for i := range trendWeeks {
		start := to - int64(trendWeeks-i)*weekMs
		week := endedIn(outcomes, start, start+weekMs)
		scope.Weeks = append(scope.Weeks, CalibrationWeek{
			From: start, Confident: standingOf(withConfidence(week, Confident)), Low: standingOf(withConfidence(week, Low)),
		})
	}
	return scope
}

// periodOf calibrates the outcomes, and shares the records, that ended from From up to To.
func periodOf(outcomes []Outcome, records []Record, from, to int64) CalibrationPeriod {
	in := endedIn(outcomes, from, to)
	return CalibrationPeriod{
		From: from, To: to, Share: shareOf(outcomes, records, from, to),
		Confident: calibratedOf(withConfidence(in, Confident)), Low: calibratedOf(withConfidence(in, Low)),
	}
}

func endedIn(outcomes []Outcome, from, to int64) []Outcome {
	var out []Outcome
	for _, o := range outcomes {
		if o.Ended >= from && o.Ended < to {
			out = append(out, o)
		}
	}
	return out
}

func withConfidence(outcomes []Outcome, confidence Confidence) []Outcome {
	var out []Outcome
	for _, o := range outcomes {
		if o.Confidence == confidence {
			out = append(out, o)
		}
	}
	return out
}

// shareOf counts the work that ended in the period by the confidence of its baseline estimate, which only an outcome
// holds: work with no outcome, or an outcome with no confidence, has none.
func shareOf(outcomes []Outcome, records []Record, from, to int64) ConfidenceShare {
	confidence := map[string]Confidence{}
	for _, o := range outcomes {
		confidence[o.Subject] = o.Confidence
	}
	var out ConfidenceShare
	var confident, low int64
	for _, r := range records {
		if r.Ended < from || r.Ended >= to {
			continue
		}
		out.Ended++
		switch confidence[r.WorkOrder] {
		case Confident:
			confident++
		case Low:
			low++
		}
	}
	out.Confident, out.Low, out.None = shareCount(confident, out.Ended), shareCount(low, out.Ended), shareCount(out.Ended-confident-low, out.Ended)
	return out
}

func shareCount(count, of int64) Share {
	s := Share{Count: count}
	if of >= MinCalibrationOutcomes {
		share := float64(count) / float64(of)
		s.Share = &share
	}
	return s
}

func calibratedOf(outcomes []Outcome) Calibrated {
	out := Calibrated{Total: standingOf(outcomes), Tiers: []TierStanding{}, Versions: []VersionStanding{}}
	tiers, versions := map[int][]Outcome{}, map[int][]Outcome{}
	for _, o := range outcomes {
		tiers[o.Tier] = append(tiers[o.Tier], o)
		versions[o.EstimatorVersion] = append(versions[o.EstimatorVersion], o)
	}
	for _, tier := range slices.Sorted(maps.Keys(tiers)) {
		out.Tiers = append(out.Tiers, TierStanding{Tier: tier, Standing: standingOf(tiers[tier])})
	}
	for _, version := range slices.Sorted(maps.Keys(versions)) {
		out.Versions = append(out.Versions, VersionStanding{Version: version, Standing: standingOf(versions[version])})
	}
	return out
}

// tally collects the comparisons of one quantity across outcomes.
type tally struct {
	p50, p85 Rate
	ratios   []float64
}

func (t *tally) add(p50, p85 *bool, ratio *float64) {
	if p50 != nil {
		t.p50.N++
		if *p50 {
			t.p50.Hits++
		}
	}
	if p85 != nil {
		t.p85.N++
		if *p85 {
			t.p85.Hits++
		}
	}
	if ratio != nil {
		t.ratios = append(t.ratios, *ratio)
	}
}

func (t *tally) comparisons() Comparisons {
	out := Comparisons{P50: rateOf(t.p50), P85: rateOf(t.p85), Ratio: MedianRatio{N: int64(len(t.ratios))}}
	if len(t.ratios) >= MinCalibrationOutcomes {
		slices.Sort(t.ratios)
		median := t.ratios[rank(len(t.ratios), 0.5)]
		out.Ratio.Median = &median
	}
	return out
}

// rateOf shows the rate of counts that reached the minimum.
func rateOf(r Rate) Rate {
	if r.N >= MinCalibrationOutcomes {
		rate := float64(r.Hits) / float64(r.N)
		r.Rate = &rate
	}
	return r
}

func standingOf(outcomes []Outcome) Standing {
	var s Standing
	var time, cost tally
	for _, o := range outcomes {
		s.Outcomes++
		switch o.Outcome {
		case workorders.Done:
			s.Done++
			if o.P85Open {
				s.P85Open++
			}
		case workorders.Failed:
			s.Failed++
		case workorders.Canceled:
			s.Canceled++
		}
		time.add(o.TimeAtOrBelowP50, o.TimeAtOrBelowP85, o.TimeRatio)
		cost.add(o.CostAtOrBelowP50, o.CostAtOrBelowP85, o.CostRatio)
	}
	s.Time, s.Cost = time.comparisons(), cost.comparisons()
	return s
}

// milestonesOf sets each milestone outcome beside the forecast its baseline holds, and calibrates the forecasts
// apart by confidence.
func milestonesOf(outcomes []Outcome, forecasts map[string]Forecast, from, to int64) MilestoneCalibration {
	out := MilestoneCalibration{Results: []MilestoneResult{}}
	var ended []Outcome
	for _, o := range outcomes {
		if o.Ended < to {
			ended = append(ended, o)
		}
	}
	slices.SortFunc(ended, func(a, b Outcome) int {
		return cmp.Or(cmp.Compare(b.Ended, a.Ended), cmp.Compare(a.Plan, b.Plan), cmp.Compare(a.Subject, b.Subject))
	})
	for _, o := range ended {
		f := forecasts[o.Forecast]
		result := MilestoneResult{
			Plan: o.Plan, Milestone: o.Subject, Confidence: o.Confidence, Outcome: o.Outcome, ActualEnd: o.ActualEnd,
			AtOrBelowP50: o.TimeAtOrBelowP50, AtOrBelowP85: o.TimeAtOrBelowP85,
		}
		if f.Status == Forecasted {
			result.ForecastP50, result.ForecastP85, result.LowerBound = f.EndP50, f.EndP85, f.LowerBound
		}
		out.Results = append(out.Results, result)
	}
	out.Recent, out.AllTime = milestonePeriodOf(ended, from, to), milestonePeriodOf(ended, 0, to)
	return out
}

func milestonePeriodOf(outcomes []Outcome, from, to int64) MilestonePeriod {
	out := MilestonePeriod{From: from, To: to}
	in := endedIn(outcomes, from, to)
	out.Confident, out.Low = milestoneStandingOf(withConfidence(in, Confident)), milestoneStandingOf(withConfidence(in, Low))
	out.None = int64(len(withConfidence(in, "")))
	return out
}

func milestoneStandingOf(outcomes []Outcome) MilestoneStanding {
	var s MilestoneStanding
	for _, o := range outcomes {
		s.Outcomes++
		switch o.Outcome {
		case workorders.Done:
			s.Done++
			if o.TimeAtOrBelowP50 != nil && o.TimeAtOrBelowP85 == nil {
				s.P85Open++
			}
		case workorders.Failed:
			s.Failed++
		case workorders.Canceled:
			s.Canceled++
		}
		if o.TimeAtOrBelowP50 != nil {
			s.P50.N++
			if *o.TimeAtOrBelowP50 {
				s.P50.Hits++
			}
		}
		if o.TimeAtOrBelowP85 != nil {
			s.P85.N++
			if *o.TimeAtOrBelowP85 {
				s.P85.Hits++
			}
		}
	}
	s.P50, s.P85 = rateOf(s.P50), rateOf(s.P85)
	return s
}

// CalibrationPoints lists the calibration of all of a project's work over a window as the measures tick exports it
// (FR-OBS-15): the p50 and p85 hit rates and the median ratio, for time and for cost, by tier and over every tier,
// for confident and low-confidence outcomes apart, and the share of ended work by confidence. A point exists only
// where the rate is shown, so a group under MinCalibrationOutcomes exports none. No label names a work order, a
// repository, an estimator version, or a person.
func CalibrationPoints(outcomes []Outcome, records []Record, from, to int64) []measures.Point {
	var work []Outcome
	for _, o := range outcomes {
		if o.SubjectKind != OutcomeOfMilestone {
			work = append(work, o)
		}
	}
	period := periodOf(work, records, from, to)
	var out []measures.Point
	for _, confidence := range []struct {
		name string
		by   Calibrated
	}{{string(Confident), period.Confident}, {string(Low), period.Low}} {
		groups := []struct {
			tier string
			s    Standing
		}{{"all", confidence.by.Total}}
		for _, t := range confidence.by.Tiers {
			groups = append(groups, struct {
				tier string
				s    Standing
			}{strconv.Itoa(t.Tier), t.Standing})
		}
		for _, g := range groups {
			for _, q := range []struct {
				name string
				c    Comparisons
			}{{"time", g.s.Time}, {"cost", g.s.Cost}} {
				labels := func(extra ...string) map[string]string {
					l := map[string]string{"quantity": q.name, "tier": g.tier, "confidence": confidence.name}
					if len(extra) == 1 {
						l["quantile"] = extra[0]
					}
					return l
				}
				for _, rate := range []struct {
					quantile string
					r        Rate
				}{{"p50", q.c.P50}, {"p85", q.c.P85}} {
					if rate.r.Rate != nil {
						out = append(out, measures.Point{Name: "estimate.hit_rate", Unit: "1", Value: *rate.r.Rate, Labels: labels(rate.quantile)})
					}
				}
				if q.c.Ratio.Median != nil {
					out = append(out, measures.Point{Name: "estimate.ratio", Unit: "1", Value: *q.c.Ratio.Median, Labels: labels()})
				}
			}
		}
	}
	for _, share := range []struct {
		name string
		s    Share
	}{{string(Confident), period.Share.Confident}, {string(Low), period.Share.Low}, {"none", period.Share.None}} {
		if share.s.Share != nil {
			out = append(out, measures.Point{Name: "estimate.share", Unit: "1", Value: *share.s.Share, Labels: map[string]string{"confidence": share.name}})
		}
	}
	return out
}
