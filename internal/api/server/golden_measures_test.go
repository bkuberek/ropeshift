package server_test

import (
	"fmt"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

const (
	hour = int64(time.Hour / time.Millisecond)
	day  = 24 * hour
)

// finished is one work order's delivery record and the timeline it came from: how it ended, how long ago, the
// time it took, and where that time went.
type finished struct {
	id, kind, size, agent, repository string
	outcome                           workorders.State
	endedAgo                          int64
	elapsed, delivery                 int64
	byKind                            map[delivery.Kind]int64
	cost                              float64
	lowerBound                        bool
}

// aWorldWithDelivery is a ledger whose fold kept the delivery records of changes of two kinds, three sizes, and
// two roster agents in two repositories, that ended over the last five weeks, one of them failed and one
// canceled, each with where its time went and what it cost.
func aWorldWithDelivery(t *testing.T) *apitest.Daemon {
	t.Helper()
	clock := apitest.NewClock()
	d := apitest.StartWith(t, apitest.Options{Clock: clock.Dialect})
	var now int64
	if err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) (err error) {
		now, err = tx.Now()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	work := func(share ...int64) map[delivery.Kind]int64 {
		kinds := []delivery.Kind{delivery.Queued, delivery.Working, delivery.Review, delivery.Person, delivery.Integration}
		out := map[delivery.Kind]int64{}
		for i, ms := range share {
			out[kinds[i]] = ms * hour
		}
		return out
	}
	records := []finished{
		{"wo-1", "change", "small", "standard", "harbor-web", workorders.Done, 2 * day, 9 * hour, 6 * hour, work(1, 5, 1, 2), 1.2, false},
		{"wo-2", "change", "small", "standard", "harbor-web", workorders.Done, 4 * day, 12 * hour, 8 * hour, work(2, 6, 2, 2), 1.8, false},
		{"wo-3", "change", "medium", "standard", "harbor-web", workorders.Done, 9 * day, 30 * hour, 20 * hour, work(2, 16, 3, 9), 4.1, true},
		{"wo-4", "change", "medium", "heavy", "harbor-api", workorders.Done, 11 * day, 26 * hour, 22 * hour, work(1, 20, 2, 3), 6.4, false},
		{"wo-5", "change", "large", "heavy", "harbor-api", workorders.Done, 16 * day, 60 * hour, 44 * hour, work(4, 36, 5, 15), 12.5, false},
		{"wo-6", "rework", "small", "standard", "harbor-web", workorders.Done, 18 * day, 5 * hour, 3 * hour, work(0, 3, 1, 1), 0.7, false},
		{"wo-7", "rework", "medium", "heavy", "harbor-api", workorders.Done, 27 * day, 15 * hour, 10 * hour, work(1, 8, 1, 5), 3.3, false},
		{"wo-8", "change", "medium", "standard", "harbor-web", workorders.Failed, 6 * day, 14 * hour, 12 * hour, work(1, 11, 0, 2), 2.2, false},
		{"wo-9", "change", "small", "standard", "harbor-api", workorders.Canceled, 13 * day, 3 * hour, 1 * hour, work(1, 1, 0, 1), 0.3, false},
	}
	err := d.Store.Fold(t.Context(), apitest.Scope, func(tx ports.FoldTx) error {
		for _, f := range records {
			ended := now - f.endedAgo
			timeline := delivery.Timeline{WorkOrder: f.id, Version: delivery.Version, Start: ended - f.elapsed, End: ended, Terminal: f.outcome, Segments: []delivery.Segment{}}
			record := delivery.Record{
				WorkOrder: f.id, Version: delivery.Version, Keys: delivery.Keys{Kind: f.kind, Size: f.size, Agent: f.agent, Repository: f.repository, Criteria: 2},
				Outcome: f.outcome, Rounds: 1, Started: ended - f.elapsed, Ended: ended, ElapsedMs: f.elapsed, DeliveryMs: f.delivery, LaneMs: f.delivery,
				ByKind: f.byKind, PersonWaits: []int64{}, Usage: delivery.Usage{InputTokens: 120_000, OutputTokens: 30_000, CostUSD: f.cost, CostLowerBound: f.lowerBound},
			}
			if err := tx.Delivery().Save(timeline, &record); err != nil {
				return fmt.Errorf("%s: %w", f.id, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestGoldenDelivery writes delivery look back as the Delivery tab reads it: by kind and by size, from records of work that
// finished, failed, and was canceled, and from a ledger with none.
func TestGoldenDelivery(t *testing.T) {
	t.Parallel()
	d := aWorldWithDelivery(t)
	for name, by := range map[string]string{"delivery": delivery.ByKind, "delivery-size": delivery.BySize} {
		report, err := d.Client.Delivery(t.Context(), "30d", by)
		if err != nil {
			t.Fatal(err)
		}
		holdsGolden(t, name, steady(pretty(t, report)))
	}
	empty, err := apitest.StartWith(t, apitest.Options{Clock: apitest.NewClock().Dialect}).Client.Delivery(t.Context(), api.DefaultDeliveryWindow, delivery.ByKind)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "delivery-empty", steady(pretty(t, empty)))
}
