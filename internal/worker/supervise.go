package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// superviseEvery is how often a lane in `backoff` or `tripped` looks at its clock and its
// desired state. Each such lane has one goroutine, and it does nothing else.
const superviseEvery = 20 * time.Millisecond

// briefRetryKey names a generation's one retried brief on the breaker.
const briefRetryKey = "brief"

// breaker reads what a profile sets. A lane with no limit it can read does not restart.
func (s *laneState) breaker() lanes.Breaker {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limits
}

// loadProfile gives an adopted lane the thresholds its profile sets, because a restart
// leaves nothing of them. A lane whose profile cannot be read runs without its checks.
func (r *laneRun) loadProfile(ctx context.Context) {
	repository, err := r.repository(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	profile, err := r.rt.Provisioner.Profiles.Load(ctx, repository, r.lease.Overrides)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	r.state.thresholds(profile, r.role())
	r.handleLock.Lock()
	if r.settings.Driver == "" {
		r.settings = laneSettings(profile, r.role(), r.startedMode)
	}
	r.handleLock.Unlock()
}

// mayRestart says a generation ended in a way a new one could mend: it crashed, nothing
// else has settled the lane, the lease is still this worker's, and the profile allows a
// restart at all. A profile of `max_restarts = 0` turns restarts off, so a crash ends the
// lane as `failed`, and there is nothing for the breaker to trip on.
func (r *laneRun) mayRestart(ctx context.Context) bool {
	if !r.crashed || r.decided != nil || r.fenced.Load() || ctx.Err() != nil || r.state.breaker().MaxRestarts <= 0 {
		return false
	}
	_, reported := r.reported(ctx)
	return !reported
}

// supervised runs generations until one gives the lane an outcome. It reports false when
// the worker is going away, because a lane in `backoff` or `tripped` is not over, and the
// next worker goes on from what the journal holds.
func (r *laneRun) supervised(ctx context.Context, report workorders.Report) (workorders.Report, bool) {
	for {
		if r.parks(ctx) {
			r.commitParked(ctx)
			return report, false
		}
		moved := false
		switch {
		case r.handedOff(ctx):
			moved = r.handoffGeneration(ctx)
		case r.respawning(ctx):
			moved = r.respawnGeneration(ctx)
		case r.mayRestart(ctx):
			moved = r.backOff(ctx)
		default:
			if ctx.Err() != nil && (r.crashed || r.currentFlight() != nil || r.orchestrating()) {
				return report, false
			}
			return report, true
		}
		if !moved {
			if ctx.Err() != nil {
				return report, false
			}
			return r.settled(ctx, report), true
		}
		report = r.session(ctx)
	}
}

// settled is the outcome of a lane that lost its way to a next generation.
func (r *laneRun) settled(ctx context.Context, report workorders.Report) workorders.Report {
	if r.fenced.Load() {
		return failed(workorders.ReasonFenced)
	}
	if reported, ok := r.reported(ctx); ok {
		return reported
	}
	return report
}

// backOff charges the next generation once, whichever process gets here, and waits out
// its delay. The charge comes first, so a worker that dies during the wait leaves a delay
// the next one finishes, and the crash still costs one restart. It reports false when
// the lane has no next generation.
func (r *laneRun) backOff(ctx context.Context) bool {
	if !r.orchestrating() && !r.w.admitsRestart(ctx) {
		return false
	}
	next := r.currentGeneration() + 1
	key := "g" + strconv.FormatInt(next, 10)
	charge, ok := r.chargeFor(ctx, key)
	if !ok {
		return false
	}
	crashpoint.Hit("worker.restart", "charged")
	if !r.waitUntil(ctx, time.UnixMilli(charge.NotBeforeMs)) {
		return false
	}
	r.signal(ctx, lanes.OnDelayElapsed)
	reason := ledger.StartCrashRestart
	if charge.Reset {
		reason = ledger.StartReset
	}
	return r.nextGeneration(ctx, next, reason)
}

// chargeFor finds a charge a crash left, or judges a new one, and a delay counts from the
// crash, however late this runs. A verdict of tripped waits for a person, and a reset
// clears what the breaker counted and starts the lane at once, because a person is waiting
// to see it run.
func (r *laneRun) chargeFor(ctx context.Context, key string) (lanes.Restart, bool) {
	return r.charged(ctx, key, r.crashedAt, func(breaker lanes.Breaker, charges []lanes.Restart, from time.Time) lanes.BreakerVerdict {
		return breaker.Judge(charges, from)
	})
}

// chargePlanned pays for a generation nothing crashed to cause: a handoff, a resume, or a respawn.
// The window has no word on one, and the day limits it as it limits any generation (architecture,
// "Per-lane resources and admission"), so a lane past its day waits for a person with what it needs
// to go on kept. It reports false when the lane has no next generation.
func (r *laneRun) chargePlanned(ctx context.Context, next int64) bool {
	key := "g" + strconv.FormatInt(next, 10)
	_, ok := r.charged(ctx, key, r.w.clock.Now(), func(breaker lanes.Breaker, charges []lanes.Restart, from time.Time) lanes.BreakerVerdict {
		return lanes.BreakerVerdict{Tripped: breaker.OverDay(charges, from), Planned: true}
	})
	return ok
}

// judging reads what is on record for one generation.
type judging func(breaker lanes.Breaker, charges []lanes.Restart, from time.Time) lanes.BreakerVerdict

func (r *laneRun) charged(ctx context.Context, key string, from time.Time, judge judging) (lanes.Restart, bool) {
	reset := false
	for {
		charges, err := r.w.journal.Restarts(ctx, r.lease.Lane)
		if err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
			return lanes.Restart{}, false
		}
		for _, charge := range charges {
			if charge.Key == key {
				return charge, true
			}
		}
		verdict := judge(r.state.breaker().Bounded(r.w.respawnBound(ctx)), charges, from)
		if reset {
			verdict.Delay, verdict.Tripped = 0, false
		}
		if !verdict.Tripped {
			charge := lanes.Restart{
				Key: chargeKey(key), AtMs: from.UnixMilli(), NotBeforeMs: from.Add(verdict.Delay).UnixMilli(),
				Planned: verdict.Planned, Reset: reset && !verdict.Planned,
			}
			if _, err := r.w.journal.ChargeRestart(ctx, r.lease.Lane, charge); err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
				return lanes.Restart{}, false
			}
			return charge, true
		}
		r.signal(ctx, lanes.OnRestartLimit)
		if !r.awaitReset(ctx) {
			return lanes.Restart{}, false
		}
		from, reset = r.w.clock.Now(), true
	}
}

// respawnBound reads what this worker's autonomy holds a lane's generations to. A journal that cannot
// answer leaves the profile's cap, which still stands.
func (w *Worker) respawnBound(ctx context.Context) int64 {
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading this worker's bound on respawns: %w", err))
		return 0
	}
	return state.MaxRespawnsPerDay
}

// waitUntil waits on the worker's clock, and a lost lease or a stopping worker ends it.
func (r *laneRun) waitUntil(ctx context.Context, moment time.Time) bool {
	for r.w.clock.Now().Before(moment) {
		if r.fenced.Load() {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(superviseEvery):
		}
	}
	return !r.fenced.Load()
}

// awaitReset holds a tripped lane until a member resets it. A reset the worker has seen is
// counted in the journal, so a restart neither loses one nor takes one twice.
func (r *laneRun) awaitReset(ctx context.Context) bool {
	for {
		if r.fenced.Load() {
			return false
		}
		resets, seen, err := r.resetCounts(ctx)
		if err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		} else if resets > seen {
			if err := r.w.journal.ClearRestarts(ctx, r.lease.Lane, resets); err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
				return false
			}
			r.signal(ctx, lanes.OnHumanReset)
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(superviseEvery):
		}
	}
}

func (r *laneRun) resetCounts(ctx context.Context) (resets, seen int64, err error) {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		return 0, 0, err
	}
	leases, err := r.w.desiredLeases(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, lease := range leases {
		if lease.Lane == r.lease.Lane {
			return lease.Resets, record.ResetsSeen, nil
		}
	}
	return 0, record.ResetsSeen, nil
}

// nextGeneration moves the lane on, in the journal and in this run together, so a kill
// here leaves one generation on record and a report of it on its way to the ledger. What
// the last generation cost carries over, because each generation has a meter of its own. A
// restart continues the session it replaces, and a handoff starts a new one, so its running
// total starts over too.
func (r *laneRun) nextGeneration(ctx context.Context, next int64, reason ledger.StartReason) bool {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return false
	}
	resume := record.SessionID
	if resume == "" {
		resume = record.ResumeSession
	}
	meter := lanes.CostMeter{SessionTotalUSD: r.cost.SessionTotalUSD}
	switch reason {
	case ledger.StartHandoff:
		resume, meter = "", lanes.CostMeter{}
		record.Handoff.Begun = true
	case ledger.StartReset:
		resume, meter = "", lanes.CostMeter{}
		record.Handoff, r.inherited = nil, nil
	}
	if asked := r.respawnAsked.Load(); asked != 0 {
		record.RespawnsSeen = asked
	}
	payload, err := json.Marshal(ledger.GenerationStart{Reason: reason})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return false
	}
	r.priorUSD, r.priorUnknown = r.priorUSD+r.cost.CostUSD, r.priorUnknown+r.cost.UnknownTurns
	record.Generation, record.ResumeSession = next, resume
	record.Progress = lanes.Progress{Cost: meter, Usage: r.usage, PriorUSD: r.priorUSD, PriorUnknown: r.priorUnknown}
	if resume != "" {
		_, record.Progress.BaselineTokens = r.state.contextRead()
	}
	event := r.w.event(ledger.UploadGenerationStarted, record, payload)
	if _, err := r.w.journal.BeginGeneration(ctx, record, event); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return false
	}
	crashpoint.Hit("worker.restart", "next")
	if err := r.w.Upload(ctx); err != nil {
		r.w.config.Report(err)
	}
	r.enterGeneration(next, resume, record.Progress)
	r.inheritedFrom(record.Handoff)
	return true
}

// enterGeneration resets what belongs to one generation.
func (r *laneRun) enterGeneration(next int64, resume string, progress lanes.Progress) {
	r.handleLock.Lock()
	r.generation, r.process = next, 0
	r.handle = ports.SessionHandle{}
	r.stopOnce = newOnce()
	r.resolved, r.fellBack = "", false
	r.handleLock.Unlock()
	r.flightLock.Lock()
	r.flight, r.left = nil, nil
	r.flightLock.Unlock()
	// Unreached first, so a stop that reads the last session over never takes the next as reached.
	r.reached.Store(false)
	r.sessionOver.Store(false)
	r.resumeFrom = resume
	r.bound, r.crashed, r.cursor = false, false, 0
	r.nudged.Store(false)
	r.owesBrief.Store(false)
	r.cost = progress.Cost
	r.state.newGeneration(r.w.clock.Now(), progress.BaselineTokens)
}

// retryBrief sends a brief that showed no evidence once more, as a new message, after
// observation shows the session is idle, and that retry counts toward the breaker.
func (r *laneRun) retryBrief(ctx context.Context) {
	s := r.state
	s.mu.Lock()
	due := s.unconfirmed && !s.retried && s.tracker.State() == lanes.Idle
	s.mu.Unlock()
	if !due {
		return
	}
	handle := r.sessionHandle()
	observation, err := r.rt.Driver.Observe(ctx, handle)
	if err != nil || observation.Liveness != lanes.LivenessAlive {
		return
	}
	s.mu.Lock()
	s.retried = true
	s.mu.Unlock()
	generation := r.currentGeneration()
	label := briefRetryKey + "/g" + strconv.FormatInt(generation, 10)
	now := r.w.clock.Now().UnixMilli()
	if _, err := r.w.journal.ChargeRestart(ctx, r.lease.Lane, lanes.Restart{Key: chargeKey(label), AtMs: now, NotBeforeMs: now}); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	effect := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, generation, "", briefRetryKey)
	ran, err := r.w.bracket(ctx, effect, func() error {
		_, err := r.rt.Driver.Send(ctx, handle, ports.Input{Text: r.briefText()})
		return err
	})
	if err != nil && !errors.Is(err, ports.ErrNoSession) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	if ran && err == nil {
		r.signal(ctx, lanes.OnInputHanded)
	}
}

func (r *laneRun) briefText() string {
	if r.orchestrating() {
		return r.briefOrchestrator()
	}
	return lanes.FirstCall(r.callsOwed(), lanes.Brief(lanes.BriefSpec{
		WorkOrder: r.lease.WorkOrder, Title: r.lease.Title, Statement: r.lease.Statement, Criteria: r.lease.Criteria,
		Pack: r.readPack() != nil, Handoff: r.inherited, Pending: r.pendingInstruction(), Facts: r.factsOwed(),
		Kind: workorders.Kind(r.lease.Kind), Review: r.lease.Review,
	}))
}

// declareWait keeps a lane's own word that it waits on something outside, for a time it
// names. A quiet lane past that is a stalled one again.
func (r *laneRun) declareWait(wait lanes.WaitDeclaration) {
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitUntil = r.w.clock.Now().Add(time.Duration(wait.Minutes) * time.Minute)
}
