package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	// allowanceRetryEvery is how often a lane whose allowance is unknown asks the ledger for it again, so a control
	// plane that is away costs the lane's stream one bounded read in each span and not one in each report.
	allowanceRetryEvery = 5 * time.Second
	// allowanceGrace is how long a lane runs with no allowance known before it is held. The lane is not held for a read
	// that failed once, and it is not run for as long as a control plane stays away: a limit nobody can read is no limit.
	allowanceGrace = 30 * time.Second
)

// holdsToLimit says this run is held to a work order's hard spend limit: a lane that works one. An orchestrator and an
// assistant have no hard limit, because stopping either stops recovery or the question ladder (ADR-0037).
func (r *laneRun) holdsToLimit() bool {
	return !r.orchestrating() && !r.assisting() && r.lease.WorkOrder != ""
}

// setAllowance keeps what the ledger said the lane's work order allows, which nil says is no limit.
func (r *laneRun) setAllowance(allowance *ledger.SpendAllowance) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.allowance, r.allowanceKnown, r.allowanceFailedAt = allowance, true, time.Time{}
}

func (r *laneRun) allowanceNow() *ledger.SpendAllowance {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.allowance
}

// noteAllowanceFailed says a read of the allowance failed. A lane that already holds one keeps it, and one that holds
// none starts the span after which it is held.
func (r *laneRun) noteAllowanceFailed() {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if !r.allowanceKnown && r.allowanceFailedAt.IsZero() {
		r.allowanceFailedAt = r.w.clock.Now()
	}
}

// readAllowance asks the ledger what the lane's work order allows it to spend. It is a worker's read: the figures go
// to no lane, no brief, and no pack (FR-SPEND-9). A read that fails leaves what was known, and starts the clock of a lane
// that knows nothing.
func (r *laneRun) readAllowance(ctx context.Context) {
	if !r.holdsToLimit() {
		return
	}
	r.handleLock.Lock()
	r.allowanceTriedAt = r.w.clock.Now()
	r.handleLock.Unlock()
	held, err := r.laneFacts(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: what its work order allows it to spend: %w", r.lease.Lane, err))
		r.noteAllowanceFailed()
		return
	}
	r.setAllowance(held.Spend)
}

// noteRunning keeps the session's running cost total, which only grows within a turn. A turn's end carries the exact
// total and replaces it (settleRunning), because a figure estimated from a turn's calls is no more than a bound.
func (r *laneRun) noteRunning(total float64) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.running = max(r.running, total)
}

// settleTurnCost takes the session's exact total at the end of a turn that reported one, which replaces whatever the
// turn's calls were estimated at, and what the turns that stopped before it were estimated at too: the total covers what
// the session spent. A turn that was stopped reports none, so what it was estimated at is kept, apart from the turn in
// progress, until a later turn reports a total: a lane stopped at its limit is charged at least the figure that stopped it.
func (r *laneRun) settleTurnCost(turn *ports.TurnResult) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if turn.Status == ports.TurnStopped {
		if estimate := r.running - r.cost.SessionTotalUSD; estimate > 0 {
			r.stoppedUSD = append(r.stoppedUSD, estimate)
		}
	} else {
		r.stoppedUSD = nil
	}
	r.running = r.cost.SessionTotalUSD
}

// generationUsage is what the generation now running has spent, the turn in progress included: the cost of its turns,
// and what the session's running total shows beyond the last of them.
//
// A turn that stopped before it reported is one turn of unknown cost, which the ledger charges at the mean of the known
// ones, and it has an estimate of what it had spent, kept in the worker. It counts once, as the more of the two: an
// estimate above the mean is the better bound and takes the turn's place among the unknown, and an estimate at or below the
// mean leaves the turn to the mean. So a lane stopped at its limit is charged at least the figure that stopped it, and the
// turn is not charged twice.
func (r *laneRun) generationUsage() ledger.GenerationUsage {
	r.handleLock.Lock()
	running := r.running
	stopped := slices.Clone(r.stoppedUSD)
	r.handleLock.Unlock()
	usage := ledger.GenerationUsage{
		CostUSD: r.cost.CostUSD + max(running-r.cost.SessionTotalUSD, 0), KnownTurns: r.cost.KnownTurns, UnknownTurns: r.cost.UnknownTurns,
	}
	var mean float64
	if r.cost.KnownTurns > 0 {
		mean = r.cost.CostUSD / float64(r.cost.KnownTurns)
	}
	for _, estimate := range stopped {
		if usage.UnknownTurns > 0 && estimate > mean {
			usage.CostUSD += estimate
			usage.UnknownTurns--
		}
	}
	return usage
}

// reportedUsage is what the generation tells the ledger it spent: what it has spent, and no less than the figure a hard
// limit stopped it at. A worker that took the park over may have no turn in progress to show it.
func (r *laneRun) reportedUsage() ledger.GenerationUsage {
	usage := r.generationUsage()
	if parked := r.parkingOf(); parked != nil && parked.Usage != nil && parked.Generation == r.currentGeneration() {
		usage.CostUSD = max(usage.CostUSD, parked.Usage.CostUSD)
		usage.KnownTurns = max(usage.KnownTurns, parked.Usage.KnownTurns)
		usage.UnknownTurns = max(usage.UnknownTurns, parked.Usage.UnknownTurns)
	}
	return usage
}

// chargedUSD is what this lane has been charged: the cost of its earlier generations, and the one running at the
// generation's mean for each turn that reported nothing. The earlier generations are what the larger of two lower
// bounds says: this worker's own journal of them, and the ledger's, which a worker that took the lane from another has
// and the journal of this one lacks. It counts no estimate for the journal's stopped turns, so it is never above what
// the ledger charges the same lane, and a lane the worker stops has always reached its limit in the ledger too.
func (r *laneRun) chargedUSD() float64 {
	earlier := r.priorUSD
	if allowance := r.allowanceNow(); allowance != nil {
		earlier = allowance.LaneBefore(earlier)
	}
	return earlier + ledger.Charge([][]ledger.GenerationUsage{{r.generationUsage()}}).ChargedUSD
}

// evaluateSpend holds the lane to its work order's hard limit, on a usage report that shows the session's cost and at
// a turn's end (FR-SPEND-5). A lane over its limit is parked, and it says whether it was. A lane whose allowance is
// unknown asks for it again, and is held when it stays unknown.
func (r *laneRun) evaluateSpend(ctx context.Context) bool {
	if !r.holdsToLimit() || r.spendStop.Load() || r.currentFlight() != nil {
		return false
	}
	allowance, known := r.allowanceState()
	if !known {
		return r.allowanceUnknown(ctx)
	}
	if allowance == nil || !allowance.Crossed(r.chargedUSD()) {
		return false
	}
	if _, reported := r.reported(ctx); reported {
		return false
	}
	return r.stopLane(ctx, lanes.ParkSpendLimit, false)
}

func (r *laneRun) allowanceState() (*ledger.SpendAllowance, bool) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.allowance, r.allowanceKnown
}

// allowanceUnknown is a report that finds the lane with no allowance: it reads again once the span since it last did
// has passed, evaluates at once if that read answered, and holds the lane when the allowance has stayed unknown for the
// grace. The lane is held and parked with a reason of its own, and wakes as soon as the ledger can say it is under its limit.
func (r *laneRun) allowanceUnknown(ctx context.Context) bool {
	r.handleLock.Lock()
	tried, failed := r.allowanceTriedAt, r.allowanceFailedAt
	r.handleLock.Unlock()
	now := r.w.clock.Now()
	if now.Sub(tried) >= allowanceRetryEvery {
		r.readAllowance(ctx)
		if _, known := r.allowanceState(); known {
			return r.evaluateSpend(ctx)
		}
		r.handleLock.Lock()
		failed = r.allowanceFailedAt
		r.handleLock.Unlock()
	}
	if failed.IsZero() || now.Sub(failed) < allowanceGrace {
		return false
	}
	if _, reported := r.reported(ctx); reported {
		return false
	}
	return r.stopLane(ctx, lanes.ParkSpendUnreadable, false)
}

// stopLane parks a lane at its hard limit, or because its limit cannot be read. A lane in a turn has its next tool call
// denied until it takes the instruction to hand off, and the handoff is the only model call that follows, bounded by
// `handoff_timeout`. A lane whose client already ended its turn at its own cap has no session to ask, so nothing is
// asked of it. It reports whether parking began, and a lane that cannot park now is read again at the next report.
func (r *laneRun) stopLane(ctx context.Context, trigger lanes.ParkTrigger, skipNarrative bool) bool {
	if !r.spendStop.CompareAndSwap(false, true) {
		return false
	}
	request := lanes.ParkRequest{Trigger: trigger, Expr: lanes.SpendLimitWake(r.lease.WorkOrder), SkipNarrative: skipNarrative, MidTurn: true}
	err := r.beginParking(ctx, request)
	if err == nil {
		return true
	}
	r.spendStop.Store(false)
	if !errors.Is(err, ErrNoParking) {
		r.w.config.Report(fmt.Errorf("lane %s: stopping at its spend limit: %w", r.lease.Lane, err))
	}
	return false
}

// stopOnBudget parks the lane of a headless generation whose client ended a turn at the budget cap the worker gave it
// (FR-SPEND-5). The cap was the hard limit and a little more, so it is the limit reached, and the lane parks as at
// one. The client's own total may be the first figure to show it, so the lane is parked on that alone.
func (r *laneRun) stopOnBudget(ctx context.Context) {
	if r.spendStop.Load() || r.currentFlight() != nil {
		return
	}
	r.stopLane(ctx, lanes.ParkSpendLimit, true)
}

// parkAtLimitBeforeStart holds a generation that begins with nothing left to spend. It is read when a generation starts,
// from the allowance the ledger just gave and what the lane has spent: a lane that crossed while a handoff was in flight,
// a resume the member raised too little for, and a lane started on work already at its limit each have no turn to run.
// The generation parks before its first turn, with no process and no model call, and it says whether it did.
func (r *laneRun) parkAtLimitBeforeStart(ctx context.Context) (workorders.Report, bool) {
	allowance, _ := r.allowanceState()
	if !r.holdsToLimit() || allowance == nil || r.spendStop.Load() || !allowance.Crossed(r.chargedUSD()) {
		return workorders.Report{}, false
	}
	if !r.spendStop.CompareAndSwap(false, true) {
		return workorders.Report{}, false
	}
	r.sessionOver.Store(true)
	request := lanes.ParkRequest{Trigger: lanes.ParkSpendLimit, Expr: lanes.SpendLimitWake(r.lease.WorkOrder), SkipNarrative: true}
	if err := r.beginParking(ctx, request); err != nil {
		r.spendStop.Store(false)
		r.sessionOver.Store(false)
		r.w.config.Report(fmt.Errorf("lane %s: stopping at its spend limit before it starts: %w", r.lease.Lane, err))
		return workorders.Report{}, false
	}
	return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonSpendLimit}, true
}

// reachedBudget says a turn ended because its client reached the cap it was started with.
func reachedBudget(turn *ports.TurnResult) bool { return turn.Reason == ports.ReasonMaxBudget }
