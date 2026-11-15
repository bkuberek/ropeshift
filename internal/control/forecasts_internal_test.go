package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestAForecastIsDueWhenWhatItRestsOnChangesOrAnHourPassed holds the forecast tick's triggers (FR-EST-8): a plan
// version with no forecast, or whose inputs changed, is due at once; one whose inputs did not change is due again
// once the ledger's time is an hour past the time its forecast ran from, and only while that forecast has dates,
// because nothing else in a forecast moves with time.
func TestAForecastIsDueWhenWhatItRestsOnChangesOrAnHourPassed(t *testing.T) {
	const hour = int64(3_600_000)
	dated := &delivery.Forecast{Status: delivery.Forecasted, AsOf: 10 * hour, Inputs: "sha256:a"}
	undated := &delivery.Forecast{Status: delivery.ForecastNoHistory, AsOf: 10 * hour, Inputs: "sha256:a"}
	for _, c := range []struct {
		name   string
		last   *delivery.Forecast
		digest string
		asOf   int64
		due    bool
	}{
		{"no forecast yet", nil, "sha256:a", 10 * hour, true},
		{"other inputs", dated, "sha256:b", 10 * hour, true},
		{"the same inputs at the same time", dated, "sha256:a", 10 * hour, false},
		{"the same inputs within the hour", dated, "sha256:a", 11*hour - 1, false},
		{"the same inputs an hour on", dated, "sha256:a", 11 * hour, true},
		{"no dates, a day on", undated, "sha256:a", 34 * hour, false},
		{"no dates, other inputs", undated, "sha256:b", 10 * hour, true},
	} {
		if got := forecastDue(c.last, c.digest, c.asOf); got != c.due {
			t.Errorf("%s: due %v", c.name, got)
		}
	}
}

// TestTheLaneCapIsTheTighterOfTheWorkInProgressCapAndTheWorkers holds the cap a forecast runs under, where zero caps
// nothing: the sum of the registered workers' lane caps, each held to the lanes per worker, uncapped when any
// worker is, and held to the work-in-progress cap; with no worker, nothing can run.
func TestTheLaneCapIsTheTighterOfTheWorkInProgressCapAndTheWorkers(t *testing.T) {
	worker := func(lanes int64) ledger.Worker {
		return ledger.Worker{Manifest: ledger.Manifest{Capacity: ledger.Capacity{MaxLanes: lanes}}}
	}
	for _, c := range []struct {
		name      string
		workspace ledger.Workspace
		workers   []ledger.Worker
		cap       int64
		none      bool
	}{
		{"no worker", ledger.Workspace{WIPLimit: 4}, nil, 0, true},
		{"two workers", ledger.Workspace{}, []ledger.Worker{worker(2), worker(3)}, 5, false},
		{"lanes per worker", ledger.Workspace{WorkerLimit: 1}, []ledger.Worker{worker(2), worker(3)}, 2, false},
		{"a worker with no cap", ledger.Workspace{}, []ledger.Worker{worker(2), worker(0)}, 0, false},
		{"work in progress", ledger.Workspace{WIPLimit: 4}, []ledger.Worker{worker(2), worker(3)}, 4, false},
		{"work in progress and a worker with no cap", ledger.Workspace{WIPLimit: 4}, []ledger.Worker{worker(0)}, 4, false},
		{"workers under the work in progress", ledger.Workspace{WIPLimit: 9}, []ledger.Worker{worker(2), worker(3)}, 5, false},
	} {
		if lanes, none := laneCapOf(c.workspace, c.workers); lanes != c.cap || none != c.none {
			t.Errorf("%s: cap %d, no worker %v", c.name, lanes, none)
		}
	}
}
