package worker

import (
	"context"
	"fmt"
	"strconv"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
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
func (r *laneRun) respawn(ctx context.Context, asked int64) {
	if r.currentFlight() != nil || r.fenced.Load() {
		return
	}
	r.respawnAsked.Store(asked)
	r.signal(ctx, lanes.OnStopRequested, lanes.OnLaneEnding)
	r.stop(ctx, ports.StopInterrupt)
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
	now := r.w.clock.Now().UnixMilli()
	charge := lanes.Restart{Key: "g" + strconv.FormatInt(next, 10), AtMs: now, NotBeforeMs: now, Planned: true}
	if _, err := r.w.journal.ChargeRestart(ctx, r.lease.Lane, charge); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return false
	}
	r.signal(ctx, lanes.OnNextGeneration)
	moved := r.nextGeneration(ctx, next, ledger.StartUserRespawn)
	r.respawnAsked.Store(0)
	return moved
}
