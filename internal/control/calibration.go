package control

import (
	"time"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
)

// CalibrationIn reads how often finished work landed inside the estimates it was baselined with, over the last
// length and over all time, inside a read the caller holds (FR-EST-11). It reads the project's own outcomes,
// delivery records, and baselines alone: never the installation pool, so no other project's work reaches a rate.
// Each milestone outcome is set beside the forecast its baseline holds.
func CalibrationIn(tx ports.ReadTx, length time.Duration) (delivery.Calibration, error) {
	now, err := tx.Now()
	if err != nil {
		return delivery.Calibration{}, err
	}
	outcomes, err := tx.Deliveries().Outcomes()
	if err != nil {
		return delivery.Calibration{}, err
	}
	records, err := tx.Deliveries().Records()
	if err != nil {
		return delivery.Calibration{}, err
	}
	forecasts := map[string]delivery.Forecast{}
	plans := map[string]bool{}
	for _, o := range outcomes {
		if o.SubjectKind != delivery.OutcomeOfMilestone || plans[o.Plan] {
			continue
		}
		plans[o.Plan] = true
		baselines, err := tx.Estimates().Baselines(o.Plan)
		if err != nil {
			return delivery.Calibration{}, err
		}
		for _, baseline := range baselines {
			for _, f := range baseline.Forecasts {
				forecasts[f.ID] = f
			}
		}
	}
	return delivery.CalibrationOf(outcomes, records, forecasts, now+1-length.Milliseconds(), now+1), nil
}
