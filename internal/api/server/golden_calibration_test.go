package server_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// estimated is one baselined work order that ended: how it ended, how long ago, the delivery time and cost it took,
// and the estimate its baseline held.
type estimated struct {
	id, repository string
	confidence     delivery.Confidence
	tier           int
	open           bool
	outcome        workorders.State
	endedAgo       int64
	took           int64
	cost           float64
}

// aWorldWithCalibration is a ledger whose fold kept the delivery records and estimate outcomes of work that ended
// over the last two months in two repositories: confident work that landed inside, between, and past its band, work
// with an open p85, low-confidence work, work that failed, work whose estimate had no history, and work with no
// baseline, and two milestones, one forecast with confidence and one with its p85 date a lower bound.
func aWorldWithCalibration(t *testing.T) *apitest.Daemon {
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
	var work []estimated
	for i := range 12 {
		took := []int64{8, 9, 12, 25}[i%4] * hour
		work = append(work, estimated{fmt.Sprintf("wo-web-%d", i), "harbor-web", delivery.Confident, 2, false, workorders.Done, int64(i+1) * 2 * day, took, 3 + float64(i%4)*2})
	}
	for i := range 5 {
		work = append(work, estimated{fmt.Sprintf("wo-api-%d", i), "harbor-api", delivery.Confident, 1, i == 0, workorders.Done, int64(i+1) * 3 * day, int64(7+i*4) * hour, 4})
	}
	for i := range 4 {
		work = append(work, estimated{fmt.Sprintf("wo-low-%d", i), "harbor-web", delivery.Low, 6, false, workorders.Done, int64(i+1) * 5 * day, int64(10+i*6) * hour, 6})
	}
	work = append(work,
		estimated{"wo-failed", "harbor-api", delivery.Confident, 1, false, workorders.Failed, 4 * day, 3 * hour, 1},
		estimated{"wo-none", "harbor-web", "", 0, false, workorders.Done, 6 * day, 11 * hour, 5},
		estimated{"wo-old", "harbor-web", delivery.Confident, 2, false, workorders.Done, 45 * day, 30 * hour, 5},
	)
	band := func(w estimated) delivery.Estimate {
		e := delivery.Estimate{
			ID: "e-" + w.id, Status: delivery.Estimated, Confidence: w.confidence, Tier: w.tier, P85Open: w.open, EstimatorVersion: delivery.EstimatorVersion,
			DeliveryP50Ms: 10 * hour, DeliveryP85Ms: 20 * hour, CostP50USD: 5, CostP85USD: 8,
		}
		if w.confidence == "" {
			e.Status, e.Tier = delivery.NoHistory, 0
		}
		return e
	}
	forecasts := []delivery.Forecast{
		{ID: "f-1", Plan: "plan-1", Milestone: "m1", Status: delivery.Forecasted, Confidence: delivery.Confident, EndP50: now - 7*day, EndP85: now - 3*day},
		{ID: "f-2", Plan: "plan-1", Milestone: "m2", Status: delivery.Forecasted, Confidence: delivery.Low, LowerBound: delivery.LowerP85, EndP50: now - 20*day, EndP85: now - 9*day},
	}
	err := d.Store.Fold(t.Context(), apitest.Scope, func(tx ports.FoldTx) error {
		for _, w := range work {
			ended := now - w.endedAgo
			record := delivery.Record{
				WorkOrder: w.id, Version: delivery.Version, Keys: delivery.Keys{Kind: "change", Size: "small", Agent: "standard", Repository: w.repository, Criteria: 2},
				Outcome: w.outcome, Rounds: 1, Started: ended - w.took, Ended: ended, ElapsedMs: w.took, DeliveryMs: w.took, LaneMs: w.took,
				ByKind: map[delivery.Kind]int64{}, PersonWaits: []int64{}, Usage: delivery.Usage{CostUSD: w.cost},
			}
			timeline := delivery.Timeline{WorkOrder: w.id, Version: delivery.Version, Start: ended - w.took, End: ended, Terminal: w.outcome, Segments: []delivery.Segment{}}
			if err := tx.Delivery().Save(timeline, &record); err != nil {
				return fmt.Errorf("%s: %w", w.id, err)
			}
			if err := tx.Delivery().SaveOutcome(delivery.WorkOutcome("bl-"+w.id, band(w), record)); err != nil {
				return fmt.Errorf("%s: %w", w.id, err)
			}
		}
		for i := range 3 {
			id := fmt.Sprintf("wo-unbaselined-%d", i)
			record := delivery.Record{WorkOrder: id, Version: delivery.Version, Keys: delivery.Keys{Kind: "change", Repository: "harbor-web"}, Outcome: workorders.Done, Ended: now - int64(i+1)*day}
			if err := tx.Delivery().Save(delivery.Timeline{WorkOrder: id, Version: delivery.Version, End: record.Ended, Terminal: workorders.Done, Segments: []delivery.Segment{}}, &record); err != nil {
				return err
			}
		}
		if err := tx.EstimateSets().AddBaseline(delivery.Baseline{ID: "bl-plan-1", Plan: "plan-1", PlanVersion: 1, Forecasts: forecasts, CreatedAt: now - 60*day}); err != nil {
			return err
		}
		for _, m := range []struct {
			forecast delivery.Forecast
			endedAgo int64
		}{{forecasts[0], 5 * day}, {forecasts[1], 12 * day}} {
			ends := []delivery.Record{{WorkOrder: "wo-" + m.forecast.Milestone, Outcome: workorders.Done, Ended: now - m.endedAgo}}
			if err := tx.Delivery().SaveOutcome(delivery.MilestoneOutcome("bl-plan-1", m.forecast, ends)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestGoldenEstimateCalibration writes calibration as the Estimates tab reads it, from a ledger with outcomes and
// from one with none.
func TestGoldenEstimateCalibration(t *testing.T) {
	t.Parallel()
	d := aWorldWithCalibration(t)
	report, err := d.Client.EstimateCalibration(t.Context(), "30d")
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "calibration", steady(pretty(t, report)))
	empty, err := apitest.StartWith(t, apitest.Options{Clock: apitest.NewClock().Dialect}).Client.EstimateCalibration(t.Context(), api.DefaultCalibrationWindow)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "calibration-empty", steady(pretty(t, empty)))
}

// TestCalibrationRefusesAWindowItCannotRead says a window that is no duration is a bad request.
func TestCalibrationRefusesAWindowItCannotRead(t *testing.T) {
	t.Parallel()
	d := apitest.StartWith(t, apitest.Options{Clock: apitest.NewClock().Dialect})
	_, err := d.Client.EstimateCalibration(t.Context(), "a fortnight")
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Fatalf("a window of a fortnight: %v", err)
	}
}
