package server_test

import (
	"fmt"
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// aWorldWithHistory is a ledger whose fold kept the finished work of three groups: twelve medium changes in one
// repository, which a confident group needs ten of, seven small ones in another, and four small reworks whose
// p85 is open. It also kept one failed change, which no group counts.
func aWorldWithHistory(t *testing.T) *apitest.Daemon {
	t.Helper()
	d := apitest.StartWith(t, apitest.Options{Clock: apitest.NewClock().Dialect})
	type group struct {
		kind, size, class, repository string
		n                             int
		lowerBound                    bool
	}
	groups := []group{
		{"change", "medium", "implement", "harbor-web", 12, false},
		{"change", "small", "implement", "harbor-api", 7, true},
		{"rework", "small", "implement", "harbor-api", 4, false},
	}
	err := d.Store.Fold(t.Context(), apitest.Scope, func(tx ports.FoldTx) error {
		save := func(id string, g group, outcome workorders.State, i int) error {
			ended := int64(1_790_000_000_000) + int64(i)*hour
			usage := delivery.Usage{InputTokens: 100_000, OutputTokens: 20_000, CostUSD: float64(i + 1), CostLowerBound: g.lowerBound && i == g.n-1}
			record := delivery.Record{
				WorkOrder: id, Version: delivery.Version, Outcome: outcome, Rounds: int64(1 + i%3), Started: ended - 2*hour, Ended: ended, ElapsedMs: 2 * hour,
				DeliveryMs: int64(i+1) * hour, LaneMs: int64(i+1) * hour / 2, ByKind: map[delivery.Kind]int64{}, PersonWaits: []int64{}, Usage: usage,
				Keys:   delivery.Keys{Kind: g.kind, Size: g.size, CapabilityClass: g.class, Repository: g.repository, MergeAuthority: "person_approves", Criteria: 2},
				Change: &ledger.ChangeSize{Files: int64(2 + i), Added: int64(40 + 10*i), Removed: int64(5 + i)},
			}
			timeline := delivery.Timeline{WorkOrder: id, Version: delivery.Version, Start: record.Started, End: ended, Terminal: outcome, Segments: []delivery.Segment{}}
			return tx.Delivery().Save(timeline, &record)
		}
		for _, g := range groups {
			for i := range g.n {
				if err := save(fmt.Sprintf("%s-%s-%02d", g.kind, g.size, i), g, workorders.Done, i); err != nil {
					return err
				}
			}
		}
		return save("change-failed", groups[0], workorders.Failed, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestGoldenHistory writes the delivery history a planning session sees, as the planning pane's History tab reads it:
// groups confident, low with a lower-bound cost, and low with an open p85, and a ledger with none.
func TestGoldenHistory(t *testing.T) {
	t.Parallel()
	d := aWorldWithHistory(t)
	view, err := d.Client.DeliveryHistory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ByKindSize) != 3 || len(view.ByKindSizeClassRepository) != 3 {
		t.Fatalf("the history reads %+v", view)
	}
	holdsGolden(t, "history", steady(pretty(t, view)))
	empty, err := apitest.StartWith(t, apitest.Options{Clock: apitest.NewClock().Dialect}).Client.DeliveryHistory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "history-empty", steady(pretty(t, empty)))
}
