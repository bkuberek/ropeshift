package cli

import (
	"fmt"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
)

// measuresEstimates prints how often finished work landed inside the estimates it was baselined with, over a recent
// window and over all time (FR-EST-11).
func (c *invocation) measuresEstimates(args []string) int {
	set := c.flags()
	window := set.String("window", api.DefaultCalibrationWindow, "how far back the recent window reads, such as 24h or 30d; all time is always shown")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	if _, err := api.ParseWindow(*window); err != nil {
		return c.usageError("%v", err)
	}
	client, exit := c.projectClient()
	if client == nil {
		return exit
	}
	defer client.Close()
	report, err := client.EstimateCalibration(c.ctx, *window)
	return c.finish(err, report, func() string { return calibrationText(report) })
}

// calibrationText prints calibration as lines of tab-separated fields: the window, then for all work and each
// repository the share of work by how it was estimated, the confident standing, which is the headline, and the low
// standing, each over all of its outcomes, by tier, and by estimator version, over the window, over all time, and
// for each week that had outcomes, and last each milestone's forecast beside its actual. A rate that has fewer than
// 10 outcomes behind it prints its counts and says it has no rate.
func calibrationText(report delivery.Calibration) string {
	var out strings.Builder
	fmt.Fprintf(&out, "window\t%s\t%s\n", dayText(report.From), dayText(report.To))
	for _, scope := range report.Scopes {
		name := "all"
		if scope.Repository != "" {
			name = "repository " + scope.Repository
		}
		for _, period := range []struct {
			label string
			p     delivery.CalibrationPeriod
		}{{"window", scope.Recent}, {"all time", scope.AllTime}} {
			s := period.p.Share
			fmt.Fprintf(&out, "%s\t%s\tshare\tended %d\tconfident %s\tlow %s\tnone %s\n", name, period.label, s.Ended, shareText(s.Confident), shareText(s.Low), shareText(s.None))
			writeCalibrated(&out, name+"\t"+period.label+"\tconfident", period.p.Confident)
			writeCalibrated(&out, name+"\t"+period.label+"\tlow", period.p.Low)
		}
		for _, week := range scope.Weeks {
			label := name + "\tweek " + dayText(week.From)
			for _, standing := range []struct {
				confidence string
				s          delivery.Standing
			}{{"confident", week.Confident}, {"low", week.Low}} {
				if standing.s.Outcomes > 0 {
					fmt.Fprintf(&out, "%s\t%s\tall\t%s\n", label, standing.confidence, standingText(standing.s))
				}
			}
		}
	}
	writeMilestones(&out, report.Milestones)
	return out.String()
}

// writeCalibrated prints one confidence's standing over all outcomes, then by tier and by estimator version, each
// line after its prefix. A confidence with no outcomes prints nothing.
func writeCalibrated(out *strings.Builder, prefix string, c delivery.Calibrated) {
	if c.Total.Outcomes == 0 {
		return
	}
	fmt.Fprintf(out, "%s\tall\t%s\n", prefix, standingText(c.Total))
	for _, t := range c.Tiers {
		fmt.Fprintf(out, "%s\ttier %d\t%s\n", prefix, t.Tier, standingText(t.Standing))
	}
	for _, v := range c.Versions {
		fmt.Fprintf(out, "%s\testimator %d\t%s\n", prefix, v.Version, standingText(v.Standing))
	}
}

func standingText(s delivery.Standing) string {
	return fmt.Sprintf("outcomes %d\tdone %d\tfailed %d\tcanceled %d\tp85 open %d\ttime %s\tcost %s",
		s.Outcomes, s.Done, s.Failed, s.Canceled, s.P85Open, comparisonsText(s.Time), comparisonsText(s.Cost))
}

func comparisonsText(c delivery.Comparisons) string {
	ratio := fmt.Sprintf("ratio no median of %d", c.Ratio.N)
	if c.Ratio.Median != nil {
		ratio = fmt.Sprintf("ratio %.2f of %d", *c.Ratio.Median, c.Ratio.N)
	}
	return fmt.Sprintf("p50 %s\tp85 %s\t%s", hitsText(c.P50), hitsText(c.P85), ratio)
}

func hitsText(r delivery.Rate) string {
	if r.Rate == nil {
		return fmt.Sprintf("%d of %d, no rate", r.Hits, r.N)
	}
	return fmt.Sprintf("%d of %d %.1f%%", r.Hits, r.N, *r.Rate*100)
}

func shareText(s delivery.Share) string {
	if s.Share == nil {
		return fmt.Sprint(s.Count)
	}
	return fmt.Sprintf("%d %.1f%%", s.Count, *s.Share*100)
}

// dayText says a time in epoch milliseconds as its UTC day.
func dayText(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.DateOnly) }

// writeMilestones prints how milestone forecasts held, confident and low confidence apart, over the window and over
// all time, and then each milestone's forecast beside the time its last work order ended.
func writeMilestones(out *strings.Builder, m delivery.MilestoneCalibration) {
	for _, period := range []struct {
		label string
		p     delivery.MilestonePeriod
	}{{"window", m.Recent}, {"all time", m.AllTime}} {
		for _, standing := range []struct {
			confidence string
			s          delivery.MilestoneStanding
		}{{"confident", period.p.Confident}, {"low", period.p.Low}} {
			if standing.s.Outcomes > 0 {
				s := standing.s
				fmt.Fprintf(out, "milestones\t%s\t%s\toutcomes %d\tdone %d\tfailed %d\tcanceled %d\tp85 open %d\tp50 %s\tp85 %s\n",
					period.label, standing.confidence, s.Outcomes, s.Done, s.Failed, s.Canceled, s.P85Open, hitsText(s.P50), hitsText(s.P85))
			}
		}
		if period.p.None > 0 {
			fmt.Fprintf(out, "milestones\t%s\tnone\toutcomes %d\n", period.label, period.p.None)
		}
	}
	for _, r := range m.Results {
		confidence := string(r.Confidence)
		if confidence == "" {
			confidence = "none"
		}
		forecast := "no forecast dates"
		if r.ForecastP50 != 0 {
			forecast = fmt.Sprintf("forecast p50 %s\tp85 %s", dateText(r.ForecastP50, r.LowerBound.OpensP50()), dateText(r.ForecastP85, r.LowerBound.OpensP85()))
		}
		fmt.Fprintf(out, "milestone\t%s\t%s\t%s\t%s\t%s\tended %s\tp50 %s\tp85 %s\n", r.Plan, r.Milestone, confidence, r.Outcome, forecast, dateText(r.ActualEnd, false), comparedText(r.AtOrBelowP50), comparedText(r.AtOrBelowP85))
	}
}

// comparedText says whether a milestone ended at or before a forecast date, or that nothing compares.
func comparedText(held *bool) string {
	switch {
	case held == nil:
		return "not compared"
	case *held:
		return "within"
	}
	return "past"
}
