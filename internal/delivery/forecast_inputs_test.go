package delivery_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestThePersonMedianReadsTheRecentRecords holds the median a forecast's what-if cuts waits for a person to: the
// median of every wait for a person in the policy's most recent records, by nearest rank, and zero with none.
func TestThePersonMedianReadsTheRecentRecords(t *testing.T) {
	waited := func(id string, ended int64, waits ...int64) delivery.Record {
		return delivery.Record{WorkOrder: id, Outcome: workorders.Done, Ended: ended, PersonWaits: waits}
	}
	policy := delivery.EstimatePolicy{Version: 1, MinSamples: 5, RecentSamples: 5, ForecastTrials: 10}
	records := []delivery.Record{
		waited("oldest", 1, 9000, 9000, 9000, 9000), waited("a", 2, 10, 70), waited("b", 3, 40), waited("c", 4),
		waited("d", 5, 50, 60), waited("e", 6, 20, 30),
	}
	if median := delivery.PersonMedianMs(records, policy); median != 40 {
		t.Errorf("the median of the five newest records' waits is %d", median)
	}
	slices.Reverse(records)
	if median := delivery.PersonMedianMs(records, policy); median != 40 {
		t.Errorf("records read in another order give the median %d", median)
	}
	if median := delivery.PersonMedianMs([]delivery.Record{waited("a", 1), waited("b", 2)}, policy); median != 0 {
		t.Errorf("records with no wait for a person give the median %d", median)
	}
}

// TestProgressIsTheTimeAWorkOrderHasHadSoFar reads, from an unfinished timeline, the delivery time it has had,
// which leaves out its waits for a lane; the lane time its lanes and its review lanes held; and what they cost.
// Work that has only waited for a lane has not started.
func TestProgressIsTheTimeAWorkOrderHasHadSoFar(t *testing.T) {
	segment := func(kind delivery.Kind, state workorders.State, from, to int64) delivery.Segment {
		return delivery.Segment{Kind: kind, State: state, Round: 1, From: from, To: to}
	}
	timeline := delivery.Timeline{WorkOrder: "wo", Start: 0, End: 60 * minute, Segments: []delivery.Segment{
		segment(delivery.Queued, workorders.Ready, 0, 10*minute),
		segment(delivery.Held, workorders.Ready, 10*minute, 15*minute),
		segment(delivery.Queued, workorders.Claimed, 15*minute, 18*minute),
		segment(delivery.Working, workorders.Running, 18*minute, 40*minute),
		segment(delivery.Person, workorders.Acceptance, 40*minute, 60*minute),
	}}
	review := delivery.Timeline{WorkOrder: "review", Start: 41 * minute, End: 60 * minute, Segments: []delivery.Segment{
		segment(delivery.Working, workorders.Running, 41*minute, 48*minute),
		segment(delivery.Queued, workorders.Ready, 48*minute, 60*minute),
	}}
	got := delivery.ProgressOf(timeline, delivery.Facts{Reviews: []delivery.Timeline{review}, Usage: delivery.Usage{CostUSD: 1.25}})
	want := delivery.Progress{Started: true, DeliveryMs: 47 * minute, LaneMs: 32 * minute, CostUSD: 1.25}
	if got != want {
		t.Errorf("the progress reads %+v, want %+v", got, want)
	}
	waiting := delivery.Timeline{WorkOrder: "wo", Start: 0, End: 30 * minute, Segments: []delivery.Segment{segment(delivery.Queued, workorders.Ready, 0, 30*minute)}}
	if got := delivery.ProgressOf(waiting, delivery.Facts{}); got.Started || got.DeliveryMs != 0 {
		t.Errorf("work that only waited for a lane reads %+v", got)
	}
	if got := delivery.ProgressOf(delivery.Timeline{WorkOrder: "wo", Segments: []delivery.Segment{}}, delivery.Facts{}); got != (delivery.Progress{}) {
		t.Errorf("work that was never ready reads %+v", got)
	}
}

// TestTheForecastDigestLeavesOutTheTime holds what a tick compares to skip a forecast: everything a forecast is
// made from except the revision, the time it runs from, and the time and cost started work has had so far, which
// move with every event; and the work in any order.
func TestTheForecastDigestLeavesOutTheTime(t *testing.T) {
	records := []delivery.Record{took("d1", 60*minute, 30*minute, 1), took("d2", 180*minute, 60*minute, 3)}
	work := []delivery.ForecastWork{
		{WorkOrder: "a", Title: "First", Milestone: "m1", Priority: 2, Estimate: confidentOn(records...), Records: records, Started: true, SoFarMs: 10 * minute},
		{WorkOrder: "b", Milestone: "m1", DependsOn: []string{"a"}, Estimate: confidentOn(records...), Records: records},
	}
	in := forecastIn(2, work...)
	in.Milestones, in.PersonMedianMs = []string{"m1"}, 5*minute
	digest := delivery.ForecastDigest(in)
	later := in
	later.Rev, later.Now = 90, now+3_600_000
	later.Work = slices.Clone(work)
	later.Work[0].SoFarMs, later.Work[0].LaneSoFarMs, later.Work[0].SpentUSD = 70*minute, 40*minute, 2
	later.Work[1].Estimate.ID, later.Work[1].Estimate.Rev, later.Work[1].Estimate.SetID = "e-2", 90, "set-2"
	slices.Reverse(later.Work)
	if delivery.ForecastDigest(later) != digest {
		t.Error("a later revision, time, and progress changed the digest")
	}
	changes := map[string]func(*delivery.ForecastInput){
		"cap":          func(in *delivery.ForecastInput) { in.Cap = 3 },
		"no worker":    func(in *delivery.ForecastInput) { in.NoWorker = true },
		"policy":       func(in *delivery.ForecastInput) { in.Policy.Version = 4 },
		"median":       func(in *delivery.ForecastInput) { in.PersonMedianMs = 6 * minute },
		"plan version": func(in *delivery.ForecastInput) { in.PlanDigest = "sha256:other" },
		"milestones":   func(in *delivery.ForecastInput) { in.Milestones = []string{"m1", "m2"} },
		"outside":      func(in *delivery.ForecastInput) { in.Outside = []string{"x"} },
		"started":      func(in *delivery.ForecastInput) { in.Work[1].Started = true },
		"priority":     func(in *delivery.ForecastInput) { in.Work[1].Priority = 5 },
		"dependency":   func(in *delivery.ForecastInput) { in.Work[1].DependsOn = nil },
		"estimate":     func(in *delivery.ForecastInput) { in.Work[1].Estimate.Confidence = delivery.Low },
		"records":      func(in *delivery.ForecastInput) { in.Work[1].Records = records[:1] },
		"a work order": func(in *delivery.ForecastInput) { in.Work = in.Work[:1] },
	}
	for name, change := range changes {
		changed := in
		changed.Work = slices.Clone(work)
		change(&changed)
		if delivery.ForecastDigest(changed) == digest {
			t.Errorf("a change of %s left the digest as it was", name)
		}
	}
}
