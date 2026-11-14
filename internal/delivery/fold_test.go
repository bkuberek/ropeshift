package delivery_test

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestEveryTriggerHasOneTarget lets the fold follow work in progress from a transition's trigger alone.
func TestEveryTriggerHasOneTarget(t *testing.T) {
	for _, edge := range workorders.Transitions() {
		if to, ok := delivery.Target(edge.Trigger); !ok || to != edge.To {
			t.Errorf("%s from %s leads to %s, and Target says %s", edge.Trigger, edge.From, edge.To, to)
		}
	}
	if _, ok := delivery.Target("no_such_trigger"); ok {
		t.Error("an unknown trigger has a target")
	}
}

// TestTheFoldKeepsWhenWorkInProgressWasFull holds the spans that make a wait for a lane `lane_cap` when the
// project's work-in-progress cap was full: claimed, running, and parked work counts, and a cap of zero caps
// nothing.
func TestTheFoldKeepsWhenWorkInProgressWasFull(t *testing.T) {
	f := delivery.FoldState{WIPLimit: 2}
	var spans []delivery.Span
	keep := func(span delivery.Span, ended bool) {
		if ended {
			spans = append(spans, span)
		}
	}
	keep(f.Move("a", workorders.Claimed, 10))
	keep(f.Move("b", workorders.Ready, 15))
	keep(f.Move("b", workorders.Claimed, 20))
	if f.FullSince != 20 {
		t.Fatalf("full since %d", f.FullSince)
	}
	keep(f.Move("a", workorders.Running, 25))
	keep(f.Move("a", workorders.Parked, 28))
	keep(f.Move("b", workorders.Acceptance, 30))
	keep(f.Move("c", workorders.Claimed, 40))
	keep(f.Limit(1, 50))
	keep(f.Limit(0, 60))
	keep(f.Limit(3, 70))
	if want := []delivery.Span{{From: 20, To: 30}, {From: 40, To: 60}}; !reflect.DeepEqual(spans, want) {
		t.Errorf("full spans %+v, want %+v", spans, want)
	}
	if got := f.Open(80); len(got) != 0 {
		t.Errorf("with room under the cap, open %+v", got)
	}
	keep(f.Move("d", workorders.Claimed, 90))
	if got := f.Open(99); !reflect.DeepEqual(got, []delivery.Span{{From: 90, To: 99}}) {
		t.Errorf("a cap full now reads %+v", got)
	}
	keep(f.Move("d", workorders.Canceled, 100))
	if f.FullSince != 0 || len(spans) != 3 || spans[2] != (delivery.Span{From: 90, To: 100}) {
		t.Errorf("a cancel ended the full span: %+v, since %d", spans, f.FullSince)
	}
}
