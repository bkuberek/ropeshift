package cli_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestWorkTimelinePrintsWhereTheTimeWent shows a person each segment of a work order's time, and, once it
// ended, its four durations, tokens, and estimated cost (FR-EST-1, FR-EST-4).
func TestWorkTimelinePrintsWhereTheTimeWent(t *testing.T) {
	s := newSession(t)
	s.completed("wo")
	s.must("work", "accept", "wo")
	text := s.must("work", "timeline", "wo")
	for _, want := range []string{"wo\tdone\t", "segment\tqueued\tready\tdispatch\tround 1\t", "elapsed\t", "delivery\t", "lane\t", "person\t", "cost\t$"} {
		if !strings.Contains(text, want) {
			t.Errorf("the timeline leaves out %q:\n%s", want, text)
		}
	}
	var view api.WorkTimeline
	if err := json.Unmarshal([]byte(s.must("work", "timeline", "wo", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if view.Timeline.Terminal != workorders.Done || len(view.Timeline.Segments) == 0 || view.Record == nil || view.Record.Outcome != workorders.Done {
		t.Errorf("the timeline reads %+v with record %+v", view.Timeline, view.Record)
	}
	at := view.Timeline.Start
	for _, segment := range view.Timeline.Segments {
		if segment.From != at {
			t.Errorf("a segment from %d after coverage reached %d: %+v", segment.From, at, view.Timeline)
		}
		at = segment.To
	}
	if at != view.Timeline.End {
		t.Errorf("the segments cover to %d of %d", at, view.Timeline.End)
	}
	if code, _, _ := s.run("work", "timeline", "absent"); code != cli.ExitNotFound {
		t.Errorf("an absent work order exits %d", code)
	}
}

// TestMeasuresDeliveryReadsTheFoldedRecords holds FR-EST-5 at the command line: look back over a window by a
// grouping, from the records the delivery fold keeps.
func TestMeasuresDeliveryReadsTheFoldedRecords(t *testing.T) {
	s := newSession(t)
	s.completed("wo")
	s.must("work", "accept", "wo")
	if _, err := control.NewDeliveryFold(s.daemon.Store).Fold(t.Context(), apitest.Scope); err != nil {
		t.Fatal(err)
	}
	text := s.must("measures", "delivery", "--by", "size", "--window", "30d")
	for _, want := range []string{"all\tdone 1\tfailed 0\tcanceled 0\t", "size\tunknown\tdone 1\t", "share\tall\t"} {
		if !strings.Contains(text, want) {
			t.Errorf("look back leaves out %q:\n%s", want, text)
		}
	}
	var report delivery.LookBack
	if err := json.Unmarshal([]byte(s.must("measures", "delivery", "--json")), &report); err != nil {
		t.Fatal(err)
	}
	if report.By != delivery.ByKind || len(report.Groups) != 2 || report.Groups[0].Done != 1 || report.Groups[0].Elapsed == nil || len(report.Groups[0].Trend) != 12 {
		t.Errorf("look back reads %+v", report)
	}
	if code, _, _ := s.run("measures", "delivery", "--by", "colour"); code != cli.ExitUsage {
		t.Errorf("an unknown grouping exits %d", code)
	}
}

// TestWorkShowPrintsTheCurrentEstimate shows a work order's current estimate with its label and the explanation
// it was made with, and returns the fields behind them with --json (FR-EST-10, FR-EST-14).
func TestWorkShowPrintsTheCurrentEstimate(t *testing.T) {
	s := newSession(t)
	s.completed("wo")
	s.must("work", "accept", "wo")
	if text := s.must("work", "show", "wo"); !strings.Contains(text, "estimate\tno history\n\tNo estimate: no change of unknown size has finished in this project yet") {
		t.Errorf("the first work's estimate:\n%s", text)
	}
	if _, err := control.NewDeliveryFold(s.daemon.Store).Fold(t.Context(), apitest.Scope); err != nil {
		t.Fatal(err)
	}
	s.create("next")
	text := s.must("work", "show", "next")
	for _, want := range []string{"estimate\tlow\tdelivery p50 ", " p85 more than ", "\tcost p50 $", "\n\tLow confidence. Based on 1 finished change of unknown size;"} {
		if !strings.Contains(text, want) {
			t.Errorf("the estimate leaves out %q:\n%s", want, text)
		}
	}
	var detail api.WorkDetail
	if err := json.Unmarshal([]byte(s.must("work", "show", "next", "--json")), &detail); err != nil {
		t.Fatal(err)
	}
	e := detail.Estimate
	if e == nil || e.Status != delivery.Estimated || e.Confidence != delivery.Low || e.SampleCount != 1 || len(e.SampleIDs) != 1 || e.SampleIDs[0] != "wo" ||
		!e.P85Open || e.Explanation == "" || !strings.Contains(text, e.Explanation) || !e.Current || e.SetID == "" {
		t.Errorf("the estimate reads %+v", e)
	}
	// Work made outside a plan records the estimate it was made with as its baseline (FR-EST-10).
	if !strings.Contains(text, "\nbaseline\tlow\tdelivery p50 ") {
		t.Errorf("work made outside a plan shows no baseline:\n%s", text)
	}
	if b := detail.Baseline; b == nil || b.Approval != "" || b.Estimate.ID != e.ID || b.Estimate.Explanation != e.Explanation {
		t.Errorf("the baseline reads %+v beside %+v", b, e)
	}
}

// TestPlanForecastPrintsTheForecastsAndTheirWhatIfs shows a plan version's latest forecasts, the plan's own and each
// milestone's, with their labels, lower bounds, what-ifs, and explanations, and the estimate set they were made
// from, and returns what the API holds with --json (FR-EST-8, FR-EST-9, FR-EST-14).
func TestPlanForecastPrintsTheForecastsAndTheirWhatIfs(t *testing.T) {
	s := newSession(t)
	plan, _ := s.plannedWithADraft()
	if text := s.must("plan", "forecast", plan); !strings.Contains(text, "forecast pending: the daemon's forecast tick has not forecast version 1 yet\n") {
		t.Errorf("before the tick, the forecast reads:\n%s", text)
	}
	var pending api.PlanForecast
	if err := json.Unmarshal([]byte(s.must("plan", "forecast", plan, "--json")), &pending); err != nil || pending.State != api.ForecastPending {
		t.Errorf("before the tick, the API reads %+v: %v", pending, err)
	}
	if _, err := control.NewForecasts(s.daemon.Store).Refresh(t.Context(), apitest.Scope); err != nil {
		t.Fatal(err)
	}
	var made api.PlanForecast
	if err := json.Unmarshal([]byte(s.must("plan", "forecast", "--json")), &made); err != nil {
		t.Fatal(err)
	}
	if made.Plan != plan || made.Version != 1 || made.State != api.ForecastMade || len(made.Forecasts) != 1 || made.Set == nil || made.Forecasts[0].SetID != made.Set.ID || len(made.Set.Estimates) != 1 {
		t.Fatalf("the only plan's forecast reads %+v", made)
	}
	if text := s.must("plan", "forecast", plan); !strings.Contains(text, "forecast\tplan\t") || !strings.Contains(text, "\t"+made.Forecasts[0].Explanation+"\n") ||
		!strings.Contains(text, "estimate\t"+made.Set.Estimates[0].WorkOrder+"\t") {
		t.Errorf("the tick's forecast prints:\n%s", text)
	}

	// A forecast with dates prints its band, its lower bound, and its what-ifs.
	const day = int64(86_400_000)
	at := made.Forecasts[0].CreatedAt + day
	dated := func(milestone string, bound delivery.LowerBound) delivery.Forecast {
		return delivery.Forecast{
			ID: "fc-" + milestone, SetID: made.Set.ID, Plan: plan, PlanVersion: 1, Milestone: milestone, Status: delivery.Forecasted, Rev: 9, Seed: "sha256:seed",
			Trials: 2000, Cap: 2, AsOf: 1_763_100_000_000, PersonMedianMs: 1_800_000, PolicyVersion: 1, EndP50: 1_763_200_000_000, EndP85: 1_763_300_000_000,
			CostP50USD: 3.5, CostP85USD: 6, EndP85CapPlusOne: 1_763_250_000_000, EndP85PersonMedian: 1_763_280_000_000, Confidence: delivery.Low,
			LowConfidenceWorkOrders: []string{"budget-order"}, LeftOut: []string{}, Outside: []string{}, LowerBound: bound,
			Explanation: "Low confidence, from 2000 trials of 1 work order, at most 2 at a time.", Day: delivery.DayOf(at), CreatedAt: at,
		}
	}
	kept := []delivery.Forecast{dated("", delivery.LowerP85), dated("m1", delivery.LowerAll)}
	if err := s.daemon.Store.Forecast(t.Context(), apitest.Scope, func(tx ports.ForecastTx) error { return tx.Forecast().Keep(kept) }); err != nil {
		t.Fatal(err)
	}
	text := s.must("plan", "forecast", plan, "--version", "1")
	for _, want := range []string{
		"plan\t" + plan + "\tversion 1\n",
		"forecast\tplan\tlow\tp50 2025-11-15 09:46 UTC\tp85 2025-11-16 13:33 UTC or later\tcost p50 $3.50 p85 at least $6.00\n",
		"\twith one more lane, p85 2025-11-15 23:40 UTC or later; with each wait for a person cut to 30m0s, p85 2025-11-16 08:00 UTC or later\n",
		"\tLow confidence, from 2000 trials of 1 work order, at most 2 at a time.\n",
		"\tseed sha256:seed, 2000 trials, at most 2 lanes, revision 9, as of 2025-11-14 06:00 UTC\n",
		"forecast\tmilestone m1\tlow\tp50 2025-11-15 09:46 UTC or later\tp85 2025-11-16 13:33 UTC or later\tcost p50 at least $3.50 p85 at least $6.00\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the forecast leaves out %q:\n%s", want, text)
		}
	}
	var view api.PlanForecast
	if err := json.Unmarshal([]byte(s.must("plan", "forecast", plan, "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Forecasts, kept) || view.Set == nil || view.Set.ID != made.Set.ID {
		t.Errorf("the API reads %+v, kept %+v", view, kept)
	}
	for _, args := range [][]string{{"plan", "forecast", plan, "--version", "9"}, {"plan", "forecast", "absent"}} {
		if code, _, _ := s.run(args...); code != cli.ExitNotFound {
			t.Errorf("%v exits %d", args, code)
		}
	}
	if code, _, _ := s.run("plan", "forecast", plan, "--version", "-1"); code != cli.ExitUsage {
		t.Errorf("a version below 1 exits %d", code)
	}
}

// TestPlanShowAndApproveShowTheForecastOrThatItIsPending shows the forecasts a member reads with a plan approval
// (FR-EST-10): "forecast pending" before the forecast tick has forecast the version, and its forecasts after.
func TestPlanShowAndApproveShowTheForecastOrThatItIsPending(t *testing.T) {
	s := newSession(t)
	plan, _ := s.plannedWithADraft()
	const pending = "  forecast pending: the daemon's forecast tick has not forecast version 1 yet\n"
	if text := s.must("plan", "show", plan); !strings.Contains(text, pending) {
		t.Errorf("before the tick, plan show prints:\n%s", text)
	}
	if _, err := control.NewForecasts(s.daemon.Store).Refresh(t.Context(), apitest.Scope); err != nil {
		t.Fatal(err)
	}
	made, err := s.daemon.Client.PlanForecast(t.Context(), plan, 1)
	if err != nil || len(made.Forecasts) != 1 {
		t.Fatalf("the forecast reads %+v: %v", made, err)
	}
	text := s.must("plan", "show", plan)
	if strings.Contains(text, pending) || !strings.Contains(text, "  forecast\tplan\tno capacity\n") || !strings.Contains(text, "  \t"+made.Forecasts[0].Explanation+"\n") {
		t.Errorf("after the tick, plan show prints:\n%s", text)
	}
}

// TestPlanApproveWhileTheForecastIsPendingSaysSo approves a version the forecast tick has not forecast: the member
// reads "forecast pending" before the approval, and the approval stands.
func TestPlanApproveWhileTheForecastIsPendingSaysSo(t *testing.T) {
	s := newSession(t)
	plan, _ := s.plannedWithADraft()
	text := s.must("plan", "approve", plan)
	if !strings.Contains(text, "  forecast pending: the daemon's forecast tick has not forecast version 1 yet\n") || !strings.Contains(text, "approved The plan version 1\n") {
		t.Errorf("approving while the forecast is pending prints:\n%s", text)
	}
}
