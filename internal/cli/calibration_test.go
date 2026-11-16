package cli_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestMeasuresEstimatesReadsTheFoldedOutcomes holds FR-EST-11 at the command line: how often finished work landed
// inside its baseline estimates, from the outcomes the delivery fold keeps, with confident and low-confidence work
// apart, a rate only from 10 outcomes, and an open p85 counted where it belongs.
func TestMeasuresEstimatesReadsTheFoldedOutcomes(t *testing.T) {
	s := newSession(t)
	if text := s.must("measures", "estimates"); !strings.Contains(text, "all\twindow\tshare\tended 0\t") || strings.Contains(text, "confident\t") {
		t.Errorf("a project with no outcomes prints:\n%s", text)
	}
	var now int64
	if err := s.daemon.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) (err error) {
		now, err = tx.Now()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	const hour, minute = int64(3_600_000), int64(60_000)
	band := func(confidence delivery.Confidence, open bool) delivery.Estimate {
		return delivery.Estimate{
			ID: "e", Status: delivery.Estimated, Confidence: confidence, Tier: 2, P85Open: open, EstimatorVersion: delivery.EstimatorVersion,
			DeliveryP50Ms: 10 * hour, DeliveryP85Ms: 20 * hour, CostP50USD: 5, CostP85USD: 8,
		}
	}
	err := s.daemon.Store.Fold(t.Context(), apitest.Scope, func(tx ports.FoldTx) error {
		// 12 confident, 4 of them open and 2 of the rest past p50; 3 low confidence, all past p50.
		for i := range 15 {
			confidence, took, open := delivery.Confident, 8*hour, i >= 8 && i < 12
			if i < 2 {
				took = 12 * hour
			}
			if i >= 12 {
				confidence, took = delivery.Low, 12*hour
			}
			id := fmt.Sprintf("wo-%d", i)
			record := delivery.Record{
				WorkOrder: id, Version: delivery.Version, Keys: delivery.Keys{Kind: "change", Repository: "repo-1"}, Outcome: workorders.Done,
				Ended: now - int64(i+1)*minute, DeliveryMs: took, Usage: delivery.Usage{CostUSD: 4},
			}
			timeline := delivery.Timeline{WorkOrder: id, Version: delivery.Version, End: record.Ended, Terminal: workorders.Done, Segments: []delivery.Segment{}}
			if err := tx.Delivery().Save(timeline, &record); err != nil {
				return err
			}
			if err := tx.Delivery().SaveOutcome(delivery.WorkOutcome("bl-"+id, band(confidence, open), record)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	text := s.must("measures", "estimates", "--window", "7d")
	for _, want := range []string{
		"all\twindow\tshare\tended 15\tconfident 12 80.0%\tlow 3 20.0%\tnone 0 0.0%\n",
		"all\twindow\tconfident\tall\toutcomes 12\tdone 12\tfailed 0\tcanceled 0\tp85 open 4\t" +
			"time p50 10 of 12 83.3%\tp85 8 of 8, no rate\tratio 0.80 of 12\t" + "cost p50 12 of 12 100.0%\tp85 8 of 8, no rate\tratio 0.80 of 12\n",
		"all\twindow\tconfident\ttier 2\toutcomes 12\t",
		"all\twindow\tconfident\testimator 1\toutcomes 12\t",
		"all\twindow\tlow\tall\toutcomes 3\tdone 3\tfailed 0\tcanceled 0\tp85 open 0\ttime p50 0 of 3, no rate\tp85 3 of 3, no rate\tratio no median of 3\t",
		"repository repo-1\twindow\tconfident\tall\toutcomes 12\t",
		"all\tall time\tconfident\tall\toutcomes 12\t",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("calibration leaves out %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "all\tweek ") {
		t.Errorf("calibration prints no weekly line:\n%s", text)
	}
	var report delivery.Calibration
	if err := json.Unmarshal([]byte(s.must("measures", "estimates", "--json")), &report); err != nil {
		t.Fatal(err)
	}
	headline := report.Scopes[0].Recent.Confident.Total
	if len(report.Scopes) != 2 || report.Scopes[1].Repository != "repo-1" || headline.Outcomes != 12 || headline.P85Open != 4 || headline.Time.P85.N != 8 || headline.Time.P85.Rate != nil || len(report.Scopes[0].Weeks) != 12 {
		t.Errorf("calibration reads %+v", report)
	}
	for _, args := range [][]string{{"measures", "estimates", "--window", "a fortnight"}, {"measures", "estimates", "extra"}} {
		if code, _, _ := s.run(args...); code != cli.ExitUsage {
			t.Errorf("%v exits %d", args, code)
		}
	}
}

// TestTheCalibrationGoldensPrintAsTheWebTestsRead writes the text `measures estimates` prints for the goldens the
// API test wrote, which the web tests hold a page to.
func TestTheCalibrationGoldensPrintAsTheWebTestsRead(t *testing.T) {
	for _, name := range []string{"calibration", "calibration-empty"} {
		var report delivery.Calibration
		readGolden(t, name+".json", &report)
		holdsText(t, name+".cli.txt", cli.CalibrationText(report))
	}
}
