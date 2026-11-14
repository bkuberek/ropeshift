package delivery_test

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestUnionJoinsWhatOverlapsOrTouches(t *testing.T) {
	got := delivery.Union([]delivery.Span{{From: 30, To: 40}, {From: 0, To: 10}}, []delivery.Span{{From: 10, To: 15}, {From: 35, To: 50}, {From: 60, To: 60}})
	if want := []delivery.Span{{From: 0, To: 15}, {From: 30, To: 50}}; !reflect.DeepEqual(got, want) {
		t.Errorf("union %+v, want %+v", got, want)
	}
}

func TestIntersectKeepsWhatEverySetHolds(t *testing.T) {
	got := delivery.Intersect([]delivery.Span{{From: 0, To: 50}, {From: 60, To: 90}}, []delivery.Span{{From: 20, To: 70}, {From: 80, To: 100}})
	if want := []delivery.Span{{From: 20, To: 50}, {From: 60, To: 70}, {From: 80, To: 90}}; !reflect.DeepEqual(got, want) {
		t.Errorf("intersect %+v, want %+v", got, want)
	}
	if got := delivery.Intersect(); len(got) != 0 {
		t.Errorf("no sets at all hold %+v", got)
	}
	if got := delivery.Intersect([]delivery.Span{{From: 0, To: 10}}, nil); len(got) != 0 {
		t.Errorf("a set with nothing in it holds %+v", got)
	}
}

// TestAWorkerIsFullWhileAnyOfItsCapsStands reads a worker's capacity intervals as the spans it could start no
// lane in: either cap, each to its end, or to its last report while it stands (FR-OBS-14).
func TestAWorkerIsFullWhileAnyOfItsCapsStands(t *testing.T) {
	got := delivery.Full([]ledger.CapacityInterval{
		{Worker: "w1", Cap: ledger.CapMaxLanes, StartedAt: 10, SeenAt: 30, EndedAt: 40},
		{Worker: "w1", Cap: ledger.CapMaxLanesPerSignIn, StartedAt: 35, SeenAt: 45, EndedAt: 50},
		{Worker: "w1", Cap: ledger.CapMaxLanes, StartedAt: 70, SeenAt: 80},
		{Worker: "w2", Cap: ledger.CapMaxLanes, StartedAt: 0, SeenAt: 100, EndedAt: 100},
	})
	want := map[string][]delivery.Span{"w1": {{From: 10, To: 50}, {From: 70, To: 80}}, "w2": {{From: 0, To: 100}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("full %+v, want %+v", got, want)
	}
}
