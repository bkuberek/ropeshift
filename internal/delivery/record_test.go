package delivery_test

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestASegmentNamesTheStateItWasIn keeps the state beside the kind, which lane time reads.
func TestASegmentNamesTheStateItWasIn(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20).move(30, worker, workorders.TriggerLaneCompleted).move(40, member, workorders.TriggerAcceptancePassed)
	var states []workorders.State
	for _, s := range b.derive(delivery.Input{}).Segments {
		states = append(states, s.State)
	}
	if want := []workorders.State{workorders.Ready, workorders.Claimed, workorders.Running, workorders.Acceptance}; !reflect.DeepEqual(states, want) {
		t.Errorf("states %v, want %v", states, want)
	}
}

// TestADeliveryRecordSummarizesAFinishedTimeline holds FR-EST-4's durations: elapsed is first `ready` to the
// end, delivery leaves out every wait for a lane, lane time is a lane holding a place under the cap, its
// review lanes' included, and person time is the person's waits, each kept by itself.
func TestADeliveryRecordSummarizesAFinishedTimeline(t *testing.T) {
	b := newBook(t).created(0, workorders.Ready).running(10, 20).
		moveWith(50, lane, workorders.Change{Trigger: workorders.TriggerLaneParked, Lane: "lane-1"}).
		moveWith(90, member, workorders.Change{Trigger: workorders.TriggerWakeFired, Lane: "lane-1", Reason: "answer:q1"}).
		running(100, 105).
		move(130, worker, workorders.TriggerLaneCompleted).
		move(160, member, workorders.TriggerRework).
		running(170, 175).
		move(200, worker, workorders.TriggerLaneCompleted).
		move(230, member, workorders.TriggerAcceptancePassed)
	timeline := b.derive(delivery.Input{
		Waits:   []delivery.Wait{{Lane: "lane-1", Record: "q1", Opened: 30, Closed: 90, ClosedBy: member}},
		Reviews: []delivery.ReviewSpan{{WorkOrder: "review-1", Opened: 130, Closed: 150, ClosedBy: system}},
	})
	reviewTimeline := delivery.Timeline{Start: 130, End: 150, Terminal: workorders.Done, Segments: []delivery.Segment{
		{Kind: delivery.Queued, State: workorders.Ready, Round: 1, From: 130, To: 132},
		{Kind: delivery.Queued, State: workorders.Claimed, Round: 1, From: 132, To: 135},
		{Kind: delivery.Working, State: workorders.Running, Round: 1, From: 135, To: 148},
		{Kind: delivery.Integration, State: workorders.Acceptance, Round: 1, From: 148, To: 150},
	}}
	change := ledger.ChangeSize{Files: 2, Added: 30, Removed: 4}
	facts := delivery.Facts{
		Keys:  delivery.Keys{Kind: string(workorders.KindChange), Size: "medium", Agent: "builder", CapabilityClass: "implement", Repository: "repo-1", MergeAuthority: string(ledger.PersonApproves), Criteria: 3},
		Model: "model-x", Milestone: "m1", Plan: "plan-1",
		Usage:   delivery.Usage{InputTokens: 900, OutputTokens: 300, CostUSD: 1.25},
		Change:  &change,
		Reviews: []delivery.Timeline{reviewTimeline},
	}
	record, ok := delivery.RecordOf(timeline, facts)
	if !ok {
		t.Fatal("a finished change has no record")
	}
	if record.Outcome != workorders.Done || record.Started != 0 || record.Ended != 230 || record.Rounds != 2 || record.Version != delivery.Version {
		t.Errorf("record %+v", record)
	}
	// Queued: 0-10, 10-20, 90-100, 100-105, 160-170, 170-175 = 50. Person: 30-50 and 50-90 are one wait of
	// 60, acceptance 130-160 after the review is 10, acceptance 200-230 is 30. Lane: claimed 10-20, 100-105,
	// 170-175 and running 20-50, 105-130, 175-200 = 100, and the review's 132-148 = 16.
	if record.ElapsedMs != 230 || record.DeliveryMs != 180 || record.LaneMs != 116 || record.PersonMs != 100 {
		t.Errorf("elapsed %d, delivery %d, lane %d, person %d", record.ElapsedMs, record.DeliveryMs, record.LaneMs, record.PersonMs)
	}
	if want := []int64{60, 10, 30}; !reflect.DeepEqual(record.PersonWaits, want) {
		t.Errorf("person waits %v, want %v", record.PersonWaits, want)
	}
	if record.ByKind[delivery.Queued] != 50 || record.ByKind[delivery.Working] != 60 || record.ByKind[delivery.Review] != 20 || record.ByKind[delivery.Person] != 100 {
		t.Errorf("by kind %v", record.ByKind)
	}
	if record.Keys != facts.Keys || record.Model != "model-x" || record.Milestone != "m1" || record.Plan != "plan-1" || record.Usage != facts.Usage || record.Change == nil || *record.Change != change {
		t.Errorf("record keeps %+v", record)
	}
}

// TestOnlyFinishedChangesAndReworksHaveARecord leaves out work still under way and every other kind.
func TestOnlyFinishedChangesAndReworksHaveARecord(t *testing.T) {
	finished := delivery.Timeline{Start: 0, End: 10, Terminal: workorders.Canceled, Segments: []delivery.Segment{{Kind: delivery.Queued, State: workorders.Ready, Round: 1, To: 10}}}
	for kind, want := range map[workorders.Kind]bool{workorders.KindChange: true, workorders.KindRework: true, workorders.KindReview: false, workorders.KindPlan: false} {
		if _, ok := delivery.RecordOf(finished, delivery.Facts{Keys: delivery.Keys{Kind: string(kind)}}); ok != want {
			t.Errorf("a finished %s has a record: %v", kind, ok)
		}
	}
	running := finished
	running.Terminal = ""
	if _, ok := delivery.RecordOf(running, delivery.Facts{Keys: delivery.Keys{Kind: string(workorders.KindChange)}}); ok {
		t.Error("work under way has a record")
	}
}
