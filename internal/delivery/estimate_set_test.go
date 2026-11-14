package delivery_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
)

// TestAnEstimateSetDigestsWhatItsEstimatesSay seals a set (ADR-0026): its estimates in work order order, each
// naming the set and when it was made, and a digest of what they say, which neither the order they came in nor
// the IDs they were given changes, so a baseline names exactly what was shown.
func TestAnEstimateSetDigestsWhatItsEstimatesSay(t *testing.T) {
	estimates := []delivery.Estimate{
		{ID: "e-2", WorkOrder: "wo-b", Status: delivery.Estimated, Confidence: delivery.Low, Explanation: "Only 3 finished medium changes."},
		{ID: "e-1", WorkOrder: "wo-a", Status: delivery.NoHistory, Explanation: "No estimate: no large change has finished in this project yet."},
	}
	set := delivery.EstimateSet{ID: "set-1", Plan: "plan-1", PlanVersion: 2, CreatedAt: 42, Estimates: estimates}.Seal()
	if !strings.HasPrefix(set.Digest, "sha256:") {
		t.Fatalf("digest %q", set.Digest)
	}
	if got := []string{set.Estimates[0].WorkOrder, set.Estimates[1].WorkOrder}; !slices.Equal(got, []string{"wo-a", "wo-b"}) {
		t.Errorf("order %v", got)
	}
	for _, e := range set.Estimates {
		if e.SetID != "set-1" || e.CreatedAt != 42 || !e.Current {
			t.Errorf("estimate %+v does not name its set", e)
		}
	}
	if estimates[0].SetID != "" {
		t.Error("sealing changed the estimates it was given")
	}
	again := delivery.EstimateSet{ID: "set-2", CreatedAt: 99, Estimates: []delivery.Estimate{
		{ID: "e-3", WorkOrder: "wo-a", Status: delivery.NoHistory, Explanation: "No estimate: no large change has finished in this project yet."},
		{ID: "e-4", WorkOrder: "wo-b", Status: delivery.Estimated, Confidence: delivery.Low, Explanation: "Only 3 finished medium changes."},
	}}.Seal()
	if again.Digest != set.Digest {
		t.Errorf("the same estimates digest %s and %s", again.Digest, set.Digest)
	}
	estimates[1].Explanation = "No estimate: no medium change has finished in this project yet."
	if other := (delivery.EstimateSet{ID: "set-1", Estimates: estimates}).Seal(); other.Digest == set.Digest {
		t.Error("another explanation has the same digest")
	}
}
