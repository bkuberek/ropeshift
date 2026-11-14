package delivery_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestPointsLabelOnlyWhatTheExportAllows holds FR-OBS-15: the delivery measures as metrics, by kind, size,
// capability class, and repository, with segment shares by kind and size, and no label naming a work order.
func TestPointsLabelOnlyWhatTheExportAllows(t *testing.T) {
	to := 20 * week
	a := record("a", "small", workorders.Done, to-1, 100, 60, 1)
	a.CapabilityClass = "implement"
	b := record("b", "small", workorders.Done, to-2, 300, 200, 3)
	b.CapabilityClass = "implement"
	failed := record("c", "large", workorders.Failed, to-3, 500, 500, 5)
	points := delivery.Points([]delivery.Record{a, b, failed}, to-week, to)
	allowed := map[string]bool{"quantile": true, "kind": true, "size": true, "capability_class": true, "repository": true, "segment_kind": true}
	byName := map[string]int{}
	for _, p := range points {
		byName[p.Name]++
		for key := range p.Labels {
			if !allowed[key] {
				t.Errorf("%s carries %s", p.Name, key)
			}
		}
		if p.Name == "delivery.elapsed" && p.Labels["size"] == "small" && p.Labels["quantile"] == "p85" && (p.Value != 300 || p.Unit != "ms" || p.Labels["capability_class"] != "implement") {
			t.Errorf("small elapsed p85 %+v", p)
		}
		if p.Name == "delivery.elapsed" && p.Labels["size"] == "large" {
			t.Errorf("failed work has a duration point: %+v", p)
		}
	}
	// Elapsed and delivery time at p50 and p85 for the one group that finished work; throughput for both groups;
	// a share for each segment kind of the small changes.
	if byName["delivery.elapsed"] != 2 || byName["delivery.time"] != 2 || byName["delivery.throughput"] != 2 || byName["delivery.segment_share"] != 2 {
		t.Errorf("points %v", byName)
	}
}
