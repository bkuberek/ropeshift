package worker_test

import (
	"math"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A lane that a limit stopped stays stopped until a member raises it: the work order is charged what the park recorded, and
// the known turn that follows, the handoff, costs little and lowers the mean the stopped turn is charged at, which would take
// the charge under the limit with no raise and fire the wake the lane parked on. Here the one known turn cost 14 and the stopped
// turn is charged at that mean, which is 28 over a limit of 25. The handoff turn costs 0.1, and the mean falls to 7.05.
func TestACheapTurnAfterTheStopDoesNotLiftTheLimit(t *testing.T) {
	t.Parallel()
	r := spendLane(t, "headless")
	stopped := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnStopped, Reason: "aborted_streaming"}}
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	// The first turn ends with no outcome, which the worker asks about, and the session says nothing to it.
	nothing := fake.DriverStep{Do: func(ports.SessionSpec) {}}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(14), running(15), stopped},
		[]fake.DriverStep{nothing},
		[]fake.DriverStep{answer, completed(14.1)},
	))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	spend := r.spendOfWork(t)
	if !atItsHardLimit(spend) || spend.Charged.ChargedUSD < 28-1e-9 {
		t.Errorf("after the handoff the work order reads %+v, and a cheap known turn lowered the charge under the limit with no raise", spend.Charged)
	}
	if n, err := r.firings(lanes.WakeID("lane-1", 1)); err != nil || n != 0 {
		t.Errorf("the wake fired %d times with no raise: %v", n, err)
	}
	if row := r.row(); row.State != workorders.Parked {
		t.Errorf("the work order reads %s, and a lane a limit parked waits for a raise", row.State)
	}
	if wake := r.spendWake(t); !wake.Open() {
		t.Errorf("the wake read %+v", wake)
	}
}

// A turn that stopped reports no cost, so the meter counts it as a turn of unknown cost, which the ledger charges at the
// mean of the known ones. The worker also kept what the turn had been estimated at when it stopped. Counting both charges
// the one turn twice, so it counts once: as its estimate when that is more than the mean, because the estimate is then the
// better bound, and at the mean when the mean is more. A later turn that reports a total takes over from the estimate, as
// the turn that reported it covers what the session spent.
func TestAStoppedTurnIsCountedOnce(t *testing.T) {
	t.Parallel()
	stopped := fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnStopped, Reason: "aborted_streaming"}}
	for _, tt := range []struct {
		name    string
		steps   func(r *laneRig) []fake.DriverStep
		want    ledger.GenerationUsage
		charged float64
	}{
		{
			// The mean of the one known turn is 4, and the stopped turn's estimate of 7 is more: it counts as its estimate.
			"an estimate above the mean counts as itself",
			func(r *laneRig) []fake.DriverStep {
				return []fake.DriverStep{{Bound: true}, completed(4), running(11), stopped, r.reports(workorders.OutcomeCompleted)}
			},
			ledger.GenerationUsage{CostUSD: 11, KnownTurns: 1, UnknownTurns: 0}, 11,
		},
		{
			// The mean is 6, and the estimate of 2 is less: the turn is charged at the mean, and its estimate is not added.
			"an estimate below the mean counts as the mean",
			func(r *laneRig) []fake.DriverStep {
				return []fake.DriverStep{{Bound: true}, completed(6), running(8), stopped, r.reports(workorders.OutcomeCompleted)}
			},
			ledger.GenerationUsage{CostUSD: 6, KnownTurns: 1, UnknownTurns: 1}, 12,
		},
		{
			// With no known turn there is no mean, so the estimate is the only figure the turn has.
			"an estimate with no mean to compare it to counts as itself",
			func(r *laneRig) []fake.DriverStep {
				return []fake.DriverStep{{Bound: true}, running(3), stopped, r.reports(workorders.OutcomeCompleted)}
			},
			ledger.GenerationUsage{CostUSD: 3, KnownTurns: 0, UnknownTurns: 0}, 3,
		},
		{
			// The turn after it reports the session's total, 12, which covers the 8 the stopped turn's estimate stood for.
			"a later turn that reports a total takes over from the estimate",
			func(r *laneRig) []fake.DriverStep {
				return []fake.DriverStep{{Bound: true}, completed(4), running(11), stopped, r.reports(workorders.OutcomeCompleted), completed(12)}
			},
			ledger.GenerationUsage{CostUSD: 12, KnownTurns: 2, UnknownTurns: 1}, 18,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := spendLane(t, "headless")
			r.driver.Play(script(tt.steps(r)))
			r.start()
			r.awaitOrder(t, workorders.Acceptance)
			r.subject.Wait()
			generations := r.generations(t)
			if len(generations) != 1 {
				t.Fatalf("generations %+v", generations)
			}
			usage := generations[0].Usage
			usage.FloorUSD = 0
			if usage != tt.want {
				t.Errorf("the generation reads %+v, want %+v", usage, tt.want)
			}
			if spend := r.spendOfWork(t); math.Abs(spend.Charged.ChargedUSD-tt.charged) > 1e-9 {
				t.Errorf("the work order is charged %v, want %v", spend.Charged.ChargedUSD, tt.charged)
			}
		})
	}
}
