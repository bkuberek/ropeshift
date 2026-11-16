package control

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// calibrationTx is a read that holds only what calibration may read: the project's outcomes, records, and baselines.
// Any other part of the read is the embedded nil, and reading it panics.
type calibrationTx struct {
	ports.ReadTx
	now       int64
	outcomes  []delivery.Outcome
	records   []delivery.Record
	baselines map[string][]delivery.Baseline
}

func (c calibrationTx) Now() (int64, error) { return c.now, nil }
func (c calibrationTx) Deliveries() ports.DeliveryReader {
	return calibrationDeliveries{outcomes: c.outcomes, records: c.records}
}
func (c calibrationTx) Estimates() ports.EstimateReader {
	return calibrationEstimates{baselines: c.baselines}
}

type calibrationDeliveries struct {
	ports.DeliveryReader
	outcomes []delivery.Outcome
	records  []delivery.Record
}

func (r calibrationDeliveries) Outcomes() ([]delivery.Outcome, error) { return r.outcomes, nil }
func (r calibrationDeliveries) Records() ([]delivery.Record, error)   { return r.records, nil }

type calibrationEstimates struct {
	ports.EstimateReader
	baselines map[string][]delivery.Baseline
}

func (r calibrationEstimates) Baselines(plan string) ([]delivery.Baseline, error) {
	return r.baselines[plan], nil
}

// TestCalibrationReadsTheProjectsOwnOutcomesAlone holds FR-EST-11 with FR-EST-13: calibration reads the project's
// outcomes, records, and the baselines that hold the milestone forecasts, and never the installation pool, so no
// other project's work reaches a rate. Its recent window ends just after now, so work that ended now counts.
func TestCalibrationReadsTheProjectsOwnOutcomesAlone(t *testing.T) {
	const now = int64(1_790_000_000_000)
	yes := true
	forecast := delivery.Forecast{ID: "f-1", Plan: "plan-1", Milestone: "m1", Status: delivery.Forecasted, Confidence: delivery.Confident, EndP50: now - 1000, EndP85: now}
	tx := calibrationTx{
		now: now,
		outcomes: []delivery.Outcome{
			{SubjectKind: delivery.OutcomeOfWorkOrder, Subject: "wo-1", Confidence: delivery.Confident, Tier: 1, Outcome: workorders.Done, Ended: now, TimeAtOrBelowP50: &yes},
			{SubjectKind: delivery.OutcomeOfMilestone, Subject: "m1", Plan: "plan-1", Forecast: "f-1", Confidence: delivery.Confident, Outcome: workorders.Done, ActualEnd: now - 500, Ended: now - 500, TimeAtOrBelowP85: &yes},
		},
		records:   []delivery.Record{{WorkOrder: "wo-1", Outcome: workorders.Done, Ended: now}},
		baselines: map[string][]delivery.Baseline{"plan-1": {{ID: "bl-1", Plan: "plan-1", Forecasts: []delivery.Forecast{forecast}}}},
	}
	got, err := CalibrationIn(tx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got.To != now+1 || got.From != now+1-24*60*60*1000 {
		t.Errorf("the window runs %d to %d", got.From, got.To)
	}
	if recent := got.Scopes[0].Recent; recent.Confident.Total.Outcomes != 1 || recent.Share.Ended != 1 {
		t.Errorf("work that ended now is not in the window: %+v", recent)
	}
	if results := got.Milestones.Results; len(results) != 1 || results[0].ForecastP50 != forecast.EndP50 || results[0].ForecastP85 != forecast.EndP85 {
		t.Errorf("the milestone is not beside its forecast: %+v", results)
	}
}
