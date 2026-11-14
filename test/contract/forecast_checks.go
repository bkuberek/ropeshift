package contract

import (
	"fmt"
	"reflect"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ports"
)

// ForecastChecks hold kept forecasts (ADR-0026, FR-EST-8): the latest of each plan version and milestone, and the
// last of each day, kept with no revision.
var ForecastChecks = []Check{
	{"FC1", "a plan version keeps the latest forecast of the plan and of each milestone, and the last of each day, with no revision", checkForecastsKept},
}

// dayOne is a UTC midnight, in epoch milliseconds, that the kept forecasts here date from.
const dayOne = int64(1_700_006_400_000)

// keepForecasts keeps one run's forecasts in a transaction of the forecast tick.
func (r *Rig) keepForecasts(s ports.Store, forecasts []delivery.Forecast) error {
	return s.Forecast(r.Ctx, r.Scope, func(tx ports.FoldTx) error { return tx.Forecast().Keep(forecasts) })
}

// forecastsOf reads a plan version's latest forecasts, and the history of the plan's own and of one milestone.
func (r *Rig) forecastsOf(s ports.Store, plan string, version int64, milestone string) (latest, own, of []delivery.Forecast, err error) {
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		if latest, err = tx.Forecasts().Latest(plan, version); err != nil {
			return err
		}
		if own, err = tx.Forecasts().History(plan, version, ""); err != nil {
			return err
		}
		of, err = tx.Forecasts().History(plan, version, milestone)
		return err
	})
	return latest, own, of, err
}

func checkForecastsKept(r *Rig) error {
	s := r.Store()
	plan, set := r.ID("plan"), r.ID("set")
	run := func(at, rev, end int64) []delivery.Forecast {
		forecast := func(milestone string) delivery.Forecast {
			return delivery.Forecast{
				ID: r.ID("fc"), SetID: set, Plan: plan, PlanVersion: 2, Milestone: milestone, Status: delivery.Forecasted, Rev: rev,
				Seed: delivery.ForecastSeed("sha256:plan", rev), Trials: 2000, Cap: 3, AsOf: at - 1000, PersonMedianMs: 60_000, PolicyVersion: 1,
				EndP50: end, EndP85: end + 3_600_000, CostP50USD: 2.5, CostP85USD: 4, EndP85CapPlusOne: end + 1_800_000, EndP85PersonMedian: end + 600_000,
				Confidence: delivery.Low, LowConfidenceWorkOrders: []string{"wo-1"}, LeftOut: []string{}, Outside: []string{}, LowerBound: delivery.LowerP85,
				Explanation: fmt.Sprintf("Low confidence, from 2000 trials, at revision %d.", rev), Inputs: "sha256:inputs", Day: delivery.DayOf(at), CreatedAt: at,
			}
		}
		return []delivery.Forecast{forecast(""), forecast("m1")}
	}
	morning, evening, next := run(dayOne+3_600_000, 10, dayOne+86_400_000), run(dayOne+72_000_000, 12, dayOne+90_000_000), run(dayOne+90_000_000, 15, dayOne+95_000_000)
	before, err := r.workspaceRev(s)
	if err != nil {
		return err
	}
	for _, kept := range [][]delivery.Forecast{morning, evening, next} {
		if err := r.keepForecasts(s, kept); err != nil {
			return err
		}
	}
	if after, err := r.workspaceRev(s); err != nil || after != before {
		return Violatedf("keeping forecasts moved the revision from %d to %d: %v", before, after, err)
	}
	if _, err := CheckRevisionFirst(r.trace.Entries()); err != nil {
		return err
	}
	latest, own, of, err := r.forecastsOf(s, plan, 2, "m1")
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(latest, next) {
		return Violatedf("the latest forecasts read %+v, kept %+v", latest, next)
	}
	if !reflect.DeepEqual(own, []delivery.Forecast{evening[0], next[0]}) || !reflect.DeepEqual(of, []delivery.Forecast{evening[1], next[1]}) {
		return Violatedf("the plan's days read %+v, and the milestone's %+v", own, of)
	}
	for _, other := range []struct {
		plan    string
		version int64
	}{{plan, 1}, {r.ID("plan"), 2}} {
		if latest, own, _, err := r.forecastsOf(s, other.plan, other.version, "m1"); err != nil || len(latest) != 0 || len(own) != 0 {
			return Violatedf("plan %s version %d, which kept none, reads %+v and %+v: %v", other.plan, other.version, latest, own, err)
		}
	}
	return nil
}
