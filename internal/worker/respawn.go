package worker

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// respawnLanes acts on the requests for a lane's next generation. A count the worker compares with
// what it has acted on means a lost hint costs a pass, and one request acts once.
func (w *Worker) respawnLanes(ctx context.Context) {
	leases, err := w.desiredLeases(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading what is asked of lanes: %w", err))
		return
	}
	for _, lease := range leases {
		run := w.runOf(lease.Lane)
		if run == nil || lease.Respawns == 0 {
			continue
		}
		record, err := w.journal.Lane(ctx, lease.Lane)
		if err != nil {
			w.config.Report(fmt.Errorf("lane %s: %w", lease.Lane, err))
			continue
		}
		if lease.Respawns > record.RespawnsSeen {
			run.respawn(ctx, lease.Respawns)
		}
	}
}

// respawn stops this generation's session, and supervision begins the next.
//
// A pass reads the journal and then acts, so it can hand a run a request the run has acted on, and ask
// refuses it. A stop asked of a session the driver does not hold yet fails and spends the generation's
// one stop, so a request that comes before reach waits for it, and reach stops the session then. ask sets
// what reach reads before stopReached reads what reach sets, so one of them stops it.
func (r *laneRun) respawn(ctx context.Context, asked int64) {
	if r.currentFlight() != nil || r.fenced.Load() || !r.ask(asked) {
		return
	}
	r.signal(ctx, lanes.OnStopRequested, lanes.OnLaneEnding)
	r.stopReached(ctx)
}

// ask takes a request, and reports whether this run had not taken it. The ledger counts requests, so one
// no newer than what the run took is one it acted on, or one a pass read before the run's generation began.
func (r *laneRun) ask(asked int64) bool {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if asked <= r.handled {
		return false
	}
	r.handled = asked
	r.respawnAsked.Store(asked)
	return true
}

// answered says a generation began with the requests up to asked recorded as seen. A request that came
// after that read still waits.
func (r *laneRun) answered(asked int64) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if r.respawnAsked.Load() == asked {
		r.respawnAsked.Store(0)
	}
}

// respawning says a request stopped this generation, and a lane that reported has nothing to go on
// with.
func (r *laneRun) respawning(ctx context.Context) bool {
	if r.respawnAsked.Load() == 0 {
		return false
	}
	r.stopped.Wait()
	return !r.abandons(ctx)
}

// respawnGeneration begins what a request asked for. It goes on from the session it replaces, which
// a reset does not, and it counts toward the day and not toward the breaker, because nothing crashed.
func (r *laneRun) respawnGeneration(ctx context.Context) bool {
	next := r.currentGeneration() + 1
	if !r.chargePlanned(ctx, next) {
		return false
	}
	r.signal(ctx, lanes.OnNextGeneration)
	return r.nextGeneration(ctx, next, ledger.StartUserRespawn)
}
