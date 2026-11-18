package worker

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// holdsToLimit says this run is held to a work order's hard spend limit: a lane that works one. An orchestrator and an
// assistant have no hard limit, because stopping either stops recovery or the question ladder (ADR-0037).
func (r *laneRun) holdsToLimit() bool {
	return !r.orchestrating() && !r.assisting() && r.lease.WorkOrder != ""
}

// setAllowance keeps what the ledger said the lane's work order allows, which nil says is no limit. A read that failed
// leaves what the last one said, because a lane must not run free for a control plane that is away.
func (r *laneRun) setAllowance(allowance *ledger.SpendAllowance) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.allowance = allowance
}

func (r *laneRun) allowanceNow() *ledger.SpendAllowance {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.allowance
}

// readAllowance asks the ledger what the lane's work order allows it to spend. It is a worker's read: the figures go
// to no lane, no brief, and no pack (FR-SPEND-9).
func (r *laneRun) readAllowance(ctx context.Context) {
	if !r.holdsToLimit() {
		return
	}
	held, err := r.laneFacts(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: what its work order allows it to spend: %w", r.lease.Lane, err))
		return
	}
	r.setAllowance(held.Spend)
}

// noteRunning keeps the session's running cost total, which only grows. A session that reports none leaves it at the
// total of its last turn, because a turn's end carries it.
func (r *laneRun) noteRunning(total float64) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.running = max(r.running, total)
}

// generationUsage is what the generation now running has spent, the turn in progress included: the cost of its turns,
// and what the session's running total shows beyond the last of them. A turn stopped before it reported keeps what the
// total showed, so a lane that was stopped at its limit is charged at least the figure that stopped it.
func (r *laneRun) generationUsage() ledger.GenerationUsage {
	r.handleLock.Lock()
	running := r.running
	r.handleLock.Unlock()
	return ledger.GenerationUsage{
		CostUSD: r.cost.CostUSD + max(running-r.cost.SessionTotalUSD, 0), KnownTurns: r.cost.KnownTurns, UnknownTurns: r.cost.UnknownTurns,
	}
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
// generation's mean for each turn that reported nothing. It counts no estimate for an earlier generation's stopped
// turns, so it is never above what the ledger charges the same lane, and a lane the worker stops has always reached
// its limit in the ledger too.
func (r *laneRun) chargedUSD() float64 {
	return r.priorUSD + ledger.Charge([][]ledger.GenerationUsage{{r.generationUsage()}}).ChargedUSD
}

// evaluateSpend holds the lane to its work order's hard limit, on a usage report that shows the session's cost and at
// a turn's end (FR-SPEND-5). A lane over its limit is parked, and it says whether it was.
func (r *laneRun) evaluateSpend(ctx context.Context) bool {
	allowance := r.allowanceNow()
	if allowance == nil || r.spendStop.Load() || r.currentFlight() != nil || !allowance.Crossed(r.chargedUSD()) {
		return false
	}
	if _, reported := r.reported(ctx); reported {
		return false
	}
	return r.stopForSpend(ctx, false)
}

// stopForSpend parks a lane at its hard limit. A lane in a turn has its next tool call denied until it takes the
// instruction to hand off, and the handoff is the only model call that follows, bounded by `handoff_timeout`. A lane
// whose client already ended its turn at its own cap has no session to ask, so nothing is asked of it. It reports
// whether parking began, and a lane that cannot park now is read again at the next report.
func (r *laneRun) stopForSpend(ctx context.Context, skipNarrative bool) bool {
	if !r.spendStop.CompareAndSwap(false, true) {
		return false
	}
	request := lanes.ParkRequest{
		Trigger: lanes.ParkSpendLimit, Expr: lanes.SpendLimitWake(r.lease.WorkOrder), SkipNarrative: skipNarrative, MidTurn: true,
	}
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
	r.stopForSpend(ctx, true)
}

// reachedBudget says a turn ended because its client reached the cap it was started with.
func reachedBudget(turn *ports.TurnResult) bool { return turn.Reason == ports.ReasonMaxBudget }
