package delivery_test

import (
	"reflect"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestAPoolRecordHoldsOnlyWhatThePoolMayHold strips a delivery record to what the installation's pool may hold
// (FR-EST-13): kind, size, capability class, the four durations, and the cost, with the outcome, the end, and
// whether the cost was a lower bound, which the estimator reads. It names no work order, repository, agent,
// model, plan, or milestone, and holds no tokens, waits, or change.
func TestAPoolRecordHoldsOnlyWhatThePoolMayHold(t *testing.T) {
	full := delivery.Record{
		WorkOrder: "wo-1", Version: 3,
		Keys:  delivery.Keys{Kind: "change", Size: "medium", Agent: "implementer", CapabilityClass: "standard", Repository: "repo-1", MergeAuthority: "person_merges", Criteria: 4},
		Model: "a-model", Milestone: "ms-1", Plan: "plan-1", Outcome: workorders.Done, Rounds: 2, Started: 10, Ended: 100,
		ElapsedMs: 90, DeliveryMs: 80, LaneMs: 50, PersonMs: 20, ByKind: map[delivery.Kind]int64{delivery.Person: 20}, PersonWaits: []int64{20},
		Usage:  delivery.Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 1.5, CostLowerBound: true},
		Change: &ledger.ChangeSize{Files: 1, Added: 2}, LaneCapKnown: true,
	}
	pooled := delivery.Pooled([]delivery.Record{full})
	want := delivery.Record{
		Keys:    delivery.Keys{Kind: "change", Size: "medium", CapabilityClass: "standard"},
		Outcome: workorders.Done, Ended: 100, ElapsedMs: 90, DeliveryMs: 80, LaneMs: 50, PersonMs: 20,
		Usage: delivery.Usage{CostUSD: 1.5, CostLowerBound: true},
	}
	if len(pooled) != 1 || !reflect.DeepEqual(pooled[0], want) {
		t.Fatalf("pooled %+v, want %+v", pooled, want)
	}
	if full.WorkOrder != "wo-1" || full.Repository != "repo-1" {
		t.Error("pooling changed the record it was given")
	}
	digest := delivery.PoolDigest(pooled)
	if !strings.HasPrefix(digest, "sha256:") || digest != delivery.PoolDigest(delivery.Pooled([]delivery.Record{full})) {
		t.Errorf("digest %q is not stable", digest)
	}
	if empty := delivery.PoolDigest(nil); empty == "" || empty == digest {
		t.Errorf("sharing nothing digests %q", empty)
	}
	full.DeliveryMs = 81
	if delivery.PoolDigest(delivery.Pooled([]delivery.Record{full})) == digest {
		t.Error("another delivery time has the same digest")
	}
}
