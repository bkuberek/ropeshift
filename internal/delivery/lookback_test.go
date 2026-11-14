package delivery_test

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

const week = int64(7 * 24 * 60 * 60 * 1000)

// record is a finished change of a size, ended at a time, with its elapsed and delivery times and its cost.
func record(id, size string, outcome workorders.State, ended, elapsed, deliveryMs int64, cost float64) delivery.Record {
	return delivery.Record{
		WorkOrder: id, Keys: delivery.Keys{Kind: string(workorders.KindChange), Size: size, Agent: "builder", Repository: "repo-1"},
		Outcome: outcome, Ended: ended, ElapsedMs: elapsed, DeliveryMs: deliveryMs, Usage: delivery.Usage{CostUSD: cost},
		ByKind: map[delivery.Kind]int64{delivery.Working: deliveryMs, delivery.Queued: elapsed - deliveryMs},
	}
}

// TestLookBackReadsTheWindowByGroup holds FR-EST-5: cycle time at p50 and p85 by nearest rank, throughput per
// week, the share of time in each segment kind, and cost, over a window, with failed and canceled work counted
// apart and never in the durations.
func TestLookBackReadsTheWindowByGroup(t *testing.T) {
	to := 20 * week
	from := to - 2*week
	records := []delivery.Record{
		record("a", "small", workorders.Done, from+1, 100, 60, 1.0),
		record("b", "small", workorders.Done, from+2, 300, 200, 3.0),
		record("c", "medium", workorders.Done, from+3, 200, 100, 2.0),
		record("d", "small", workorders.Failed, from+4, 900, 900, 9.0),
		record("e", "medium", workorders.Canceled, from+5, 50, 0, 0),
		record("old", "small", workorders.Done, from-1, 5000, 5000, 50),
	}
	report := delivery.LookBackOf(records, delivery.BySize, from, to)
	if report.By != delivery.BySize || len(report.Groups) != 3 {
		t.Fatalf("groups %+v", report.Groups)
	}
	all, medium, small := report.Groups[0], report.Groups[1], report.Groups[2]
	if all.Key != "" || small.Key != "small" || medium.Key != "medium" {
		t.Fatalf("keys %q %q %q", all.Key, small.Key, medium.Key)
	}
	if all.Done != 3 || all.Failed != 1 || all.Canceled != 1 || all.PerWeek != 1.5 {
		t.Errorf("all counts %+v", all)
	}
	// Done elapsed 100, 200, 300: p50 is the second, p85 the third.
	if *all.Elapsed != (delivery.Quantiles{P50: 200, P85: 300}) || *all.Delivery != (delivery.Quantiles{P50: 100, P85: 200}) {
		t.Errorf("all durations %+v %+v", *all.Elapsed, *all.Delivery)
	}
	if *all.Cost != (delivery.CostQuantiles{P50: 2, P85: 3}) {
		t.Errorf("all cost %+v", *all.Cost)
	}
	if want := map[delivery.Kind]float64{delivery.Working: 360.0 / 600, delivery.Queued: 240.0 / 600}; !reflect.DeepEqual(all.Share, want) {
		t.Errorf("all share %v, want %v", all.Share, want)
	}
	if small.Done != 2 || small.Failed != 1 || *small.Elapsed != (delivery.Quantiles{P50: 100, P85: 300}) {
		t.Errorf("small %+v", small)
	}
	if medium.Done != 1 || medium.Canceled != 1 || medium.PerWeek != 0.5 {
		t.Errorf("medium %+v", medium)
	}
	if len(all.Trend) != 12 || all.Trend[11].From != to-week || all.Trend[11].Done != 0 || all.Trend[10].Done != 3 || all.Trend[9].Done != 1 {
		t.Errorf("trend %+v", all.Trend)
	}
}

// TestLookBackWithNothingFinishedHasNoDurations says so rather than printing zero.
func TestLookBackWithNothingFinishedHasNoDurations(t *testing.T) {
	report := delivery.LookBackOf(nil, delivery.ByKind, 0, week)
	if len(report.Groups) != 1 || report.Groups[0].Elapsed != nil || report.Groups[0].Cost != nil || report.Groups[0].Done != 0 {
		t.Errorf("an empty window reads %+v", report.Groups)
	}
}
