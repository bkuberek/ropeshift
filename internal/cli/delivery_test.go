package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
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
}
