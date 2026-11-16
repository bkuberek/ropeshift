package delivery_test

import (
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/delivery"
)

// noticeSet is an estimate set of three work orders: a confident one, a low one whose band is open and whose cost is a
// lower bound, and one with no history. Each estimate draws on records named by ID, and one on the pool.
func noticeSet() delivery.EstimateSet {
	return delivery.EstimateSet{ID: "set-1", Plan: "plan-1", PlanVersion: 2, Estimates: []delivery.Estimate{
		{WorkOrder: "wo-a", Status: delivery.Estimated, Confidence: delivery.Confident, Tier: 1, SampleCount: 12, SampleIDs: []string{"done-secret-1"},
			DeliveryP50Ms: 2 * hour, DeliveryP85Ms: 5 * hour, CostP50USD: 1.25, CostP85USD: 3.5, Explanation: "Based on 12 finished medium changes."},
		{WorkOrder: "wo-b", Status: delivery.Estimated, Confidence: delivery.Low, Tier: 6, SampleCount: 5, PoolSamples: 4, P85Open: true, CostAtLeast: true,
			DeliveryP50Ms: hour, DeliveryP85Ms: hour, CostP50USD: 2, CostP85USD: 2,
			Explanation: "Low confidence. Based on 5 finished large changes; a confident estimate needs 10."},
		{WorkOrder: "wo-c", Status: delivery.NoHistory, Explanation: "No estimate: no small change has finished in this project yet."},
	}}
}

func noticeForecasts() []delivery.Forecast {
	end := time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC).UnixMilli()
	return []delivery.Forecast{
		{Plan: "plan-1", Status: delivery.Forecasted, Confidence: delivery.Low, EndP50: end, EndP85: end + 24*hour, CostP50USD: 6, CostP85USD: 9.5,
			LowerBound: delivery.LowerP85, Explanation: "Low confidence. Rests on the estimates of wo-b."},
		{Plan: "plan-1", Milestone: "m1", Status: delivery.ForecastNoHistory, Explanation: "No forecast: none of its work has history."},
	}
}

// TestAForecastThatStatesNoBoundHasNoneInItsLine holds that a date with no stated bound is no lower bound, as the
// outcome that compares the forecast reads it, so its line never says "or later" or "at least".
func TestAForecastThatStatesNoBoundHasNoneInItsLine(t *testing.T) {
	end := time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC).UnixMilli()
	forecasts := []delivery.Forecast{{Plan: "plan-1", Status: delivery.Forecasted, Confidence: delivery.Confident, EndP50: end, EndP85: end + 24*hour, CostP50USD: 6, CostP85USD: 9.5, Explanation: "From 2000 trials."}}
	text := delivery.VersionNotice("Retry budget", 2, nil, forecasts, nil)
	want := "- plan: confident; p50 2026-11-20 14:30 UTC, p85 2026-11-21 14:30 UTC; cost p50 $6.00, p85 $9.50. From 2000 trials."
	if !strings.Contains(text, want) {
		t.Errorf("the notice does not say %q:\n%s", want, text)
	}
}

// TestAVersionNoticeSaysEachEstimateAndForecastWithItsLabelAndExplanation holds FR-EST-12: what the planning lane is
// sent holds every forecast and every estimate of the version it submitted, each with its confidence label and the
// explanation stored with it, a lower bound as "or later", "more than", and "at least", and the work by title.
func TestAVersionNoticeSaysEachEstimateAndForecastWithItsLabelAndExplanation(t *testing.T) {
	set := noticeSet()
	text := delivery.VersionNotice("Retry budget", 2, &set, noticeForecasts(), map[string]string{"wo-a": "Add the budget", "wo-b": "Wire the config", "wo-c": "Write the docs"})
	for _, want := range []string{
		"Plan version 2 of Retry budget",
		"estimates drawn from finished work, and no promise; each explanation says what it drew on",
		"- plan: low confidence; p50 2026-11-20 14:30 UTC, p85 2026-11-21 14:30 UTC or later; cost p50 $6.00, p85 at least $9.50. Low confidence. Rests on the estimates of wo-b.",
		"- milestone m1: no history. No forecast: none of its work has history.",
		`- wo-a "Add the budget": confident; delivery p50 2h, p85 5h; cost p50 $1.25, p85 $3.50. Based on 12 finished medium changes.`,
		`- wo-b "Wire the config": low confidence; delivery p50 1h, p85 more than 1h; cost at least p50 $2.00, p85 more than $2.00. Low confidence. Based on 5 finished large changes; a confident estimate needs 10.`,
		`- wo-c "Write the docs": no history. No estimate: no small change has finished in this project yet.`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the notice does not say %q:\n%s", want, text)
		}
	}
	// One estimate drew on the installation pool, so no line before its own explanation says the work was this project's.
	if strings.Contains(text, "this project's finished work") {
		t.Errorf("the notice says every estimate drew on this project's own work, which a pooled one did not:\n%s", text)
	}
	for _, leak := range []string{"done-secret-1", "set-1", "pool"} {
		if strings.Contains(text, leak) {
			t.Errorf("the notice says %q, which no estimate's label or explanation does:\n%s", leak, text)
		}
	}
}

// TestAVersionNoticeBeforeAForecastSaysTheForecastIsPending says what a version with no forecast reads, and what one
// with no estimate set reads, so a lane never takes a missing section for a good one.
func TestAVersionNoticeBeforeAForecastSaysTheForecastIsPending(t *testing.T) {
	set := noticeSet()
	text := delivery.VersionNotice("Retry budget", 2, &set, nil, nil)
	if !strings.Contains(text, "Forecast pending") || !strings.Contains(text, "- wo-a: confident;") {
		t.Errorf("a version with no forecast reads:\n%s", text)
	}
	text = delivery.VersionNotice("Retry budget", 2, nil, noticeForecasts()[:1], nil)
	if !strings.Contains(text, "No work of this version has an estimate") || strings.Contains(text, "Forecast pending") {
		t.Errorf("a version with no estimate set reads:\n%s", text)
	}
}
