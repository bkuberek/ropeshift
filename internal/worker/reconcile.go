package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// effectSteps names the three steps of each kind of effect, so the crash matrix can
// kill a worker inside every bracket.
var effectSteps = map[lanes.EffectKind]struct{ started, performed, finished func() }{
	lanes.EffectWorktree: {
		started:   func() { crashpoint.Hit("effect.worktree", "started") },
		performed: func() { crashpoint.Hit("effect.worktree", "performed") },
		finished:  func() { crashpoint.Hit("effect.worktree", "finished") },
	},
	lanes.EffectSession: {
		started:   func() { crashpoint.Hit("effect.session", "started") },
		performed: func() { crashpoint.Hit("effect.session", "performed") },
		finished:  func() { crashpoint.Hit("effect.session", "finished") },
	},
	lanes.EffectInput: {
		started:   func() { crashpoint.Hit("effect.input", "started") },
		performed: func() { crashpoint.Hit("effect.input", "performed") },
		finished:  func() { crashpoint.Hit("effect.input", "finished") },
	},
	lanes.EffectStop: {
		started:   func() { crashpoint.Hit("effect.stop", "started") },
		performed: func() { crashpoint.Hit("effect.stop", "performed") },
		finished:  func() { crashpoint.Hit("effect.stop", "finished") },
	},
	lanes.EffectGuard: {
		started:   func() { crashpoint.Hit("effect.guard", "started") },
		performed: func() { crashpoint.Hit("effect.guard", "performed") },
		finished:  func() { crashpoint.Hit("effect.guard", "finished") },
	},
	lanes.EffectRemoval: {
		started:   func() { crashpoint.Hit("effect.removal", "started") },
		performed: func() { crashpoint.Hit("effect.removal", "performed") },
		finished:  func() { crashpoint.Hit("effect.removal", "finished") },
	},
	lanes.EffectHook: {
		started:   func() { crashpoint.Hit("effect.hook", "started") },
		performed: func() { crashpoint.Hit("effect.hook", "performed") },
		finished:  func() { crashpoint.Hit("effect.hook", "finished") },
	},
}

// bracket performs one external effect between two journal writes, so a crash at any
// point leaves a record of it. It reports whether perform ran. An effect already on
// record runs only if a retry cleared it to started, or if its kind repeats from where it
// stands, because a stop that failed or a guard that reads again is safe.
func (w *Worker) bracket(ctx context.Context, effect lanes.Effect, perform func() error) (bool, error) {
	begun, err := w.journal.BeginEffect(ctx, effect)
	if err != nil {
		return false, err
	}
	if !begun {
		current, err := w.journal.Effect(ctx, effect.Key)
		if err != nil {
			return false, err
		}
		again := effect.Kind.Repeats(current.State)
		if current.State != lanes.EffectStarted && !again {
			return false, nil
		}
		if again {
			if _, err := w.journal.RetryEffect(ctx, effect.Key); err != nil {
				return false, err
			}
		}
	}
	steps := effectSteps[effect.Kind]
	steps.started()
	failure := perform()
	steps.performed()
	state, detail := lanes.EffectCompleted, ""
	if failure != nil {
		state, detail = lanes.EffectFailed, failure.Error()
	}
	if err := w.journal.FinishEffect(context.WithoutCancel(ctx), effect.Key, state, detail); err != nil {
		return true, errors.Join(failure, err)
	}
	steps.finished()
	return true, failure
}

// markUnknown runs once in a process, before it begins any effect: whatever an earlier
// process left started may or may not have happened.
func (w *Worker) markUnknown(ctx context.Context) error {
	var err error
	w.marked.Do(func() {
		_, err = w.journal.MarkUnknown(ctx)
		crashpoint.Hit("worker.reconcile", "marked")
	})
	return err
}

// observed is what one pass read, beside the world it hands to Plan.
type observed struct {
	leases   map[string]ledger.DesiredLease
	records  map[string]lanes.Record
	located  map[string]ports.Located
	archives map[string]ledger.DesiredArchive
	woken    map[string]ledger.DesiredWoken
}

func (w *Worker) held(lane string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.running[lane]
	return ok
}

// desiredLeases reads the leases this worker should act on. No desired state yet means
// no lease, and a worker still reconciles what it holds.
func (w *Worker) desiredLeases(ctx context.Context) ([]ledger.DesiredLease, error) {
	state, err := w.desired(ctx)
	return state.Leases, err
}

func (w *Worker) desired(ctx context.Context) (ledger.DesiredState, error) {
	state, err := w.journal.Desired(ctx)
	if errors.Is(err, ports.ErrNotFound) {
		return ledger.DesiredState{}, nil
	}
	if err == nil && state.Orchestrator != nil {
		state.Leases = append(state.Leases, orchestratorLease(*state.Orchestrator))
	}
	return state, err
}

// sessionSpec is what a driver needs to look for a session it did not start.
func (w *Worker) sessionSpec(record lanes.Record) ports.SessionSpec {
	rt := w.config.Lanes
	return ports.SessionSpec{
		Lane: record.ID, WorkOrder: record.WorkOrder, Generation: record.ProcessGeneration(), Dir: record.Worktree,
		ConfigDir: rt.configDir(record.ID), TempDir: rt.tempDir(record.ID), Binary: rt.Binary, Override: rt.Override,
	}
}

// observe reads the world without changing it. A read that fails counts as unknown,
// which starts nothing, and is reported.
func (w *Worker) observe(ctx context.Context) (lanes.World, observed, error) {
	seen := observed{
		leases: map[string]ledger.DesiredLease{}, records: map[string]lanes.Record{}, located: map[string]ports.Located{},
		archives: map[string]ledger.DesiredArchive{}, woken: map[string]ledger.DesiredWoken{},
	}
	desired, err := w.desired(ctx)
	if err != nil {
		return lanes.World{}, seen, err
	}
	// Allocations come before the lanes. A lane journals its start before it takes anything,
	// so whatever this finds has its record in the next read, and a lane that starts between
	// the two reads cannot look like resources nobody holds.
	allocations, err := w.journal.Allocations(ctx)
	if err != nil {
		return lanes.World{}, seen, err
	}
	records, err := w.journal.Lanes(ctx)
	if err != nil {
		return lanes.World{}, seen, err
	}
	effects, err := w.journal.Effects(ctx)
	if err != nil {
		return lanes.World{}, seen, err
	}
	rejected, err := w.journal.Rejected(ctx)
	if err != nil {
		return lanes.World{}, seen, err
	}
	refused := map[int64]bool{}
	for _, result := range rejected {
		refused[result.Seq] = true
	}
	byLane := map[string][]lanes.Effect{}
	for _, effect := range effects {
		byLane[effect.Lane] = append(byLane[effect.Lane], effect)
	}
	var world lanes.World
	unknownRead := map[string]bool{}
	for _, record := range records {
		seen.records[record.ID] = record
		actual := lanes.Actual{Record: record, Held: w.held(record.ID), Effects: byLane[record.ID]}
		actual.ParkRefused = record.State == lanes.StateParked && record.Parking != nil && refused[record.Parking.Seq]
		if !actual.Held && (record.State == lanes.StateRunning || hasUnknown(byLane[record.ID])) {
			located, err := w.config.Lanes.Driver.Find(ctx, w.sessionSpec(record))
			if err != nil {
				w.config.Report(fmt.Errorf("lane %s: looking for its session: %w", record.ID, err))
				unknownRead[record.ID] = true
				located = ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessUnknown}}
			}
			seen.located[record.ID] = located
			actual.Session = located.Session
		}
		world.Lanes = append(world.Lanes, actual)
	}
	for _, effect := range effects {
		if effect.State != lanes.EffectUnknown {
			continue
		}
		finding := w.find(ctx, effect, seen.located[effect.Lane], unknownRead[effect.Lane])
		world.Unknown = append(world.Unknown, lanes.ObservedEffect{Effect: effect, Finding: finding})
	}
	for _, lease := range desired.Leases {
		seen.leases[lease.Lane] = lease
		record, recorded := seen.records[lease.Lane]
		switch {
		case lease.Lane == "" || lease.State != string(workorders.Claimed) || w.held(lease.Lane):
		case lease.Resume != nil:
			if !recorded || record.ClaimID != lease.ClaimID {
				world.Resuming = append(world.Resuming, lease.Lane)
			}
		case !recorded:
			world.Wanted = append(world.Wanted, lease.Lane)
		}
	}
	for _, woken := range desired.Woken {
		seen.woken[woken.Lane] = woken
		world.Woken = append(world.Woken, woken.Lane)
	}
	for _, allocation := range allocations {
		world.Allocations = append(world.Allocations, allocation.Lane)
	}
	for _, archive := range desired.Archives {
		seen.archives[archive.Lane] = archive
		world.Archives = append(world.Archives, archive.Lane)
	}
	return world, seen, nil
}

func hasUnknown(effects []lanes.Effect) bool {
	for _, effect := range effects {
		if effect.State == lanes.EffectUnknown {
			return true
		}
	}
	return false
}

// find looks for one effect a crash left unknown. A session that exists shows a spawn
// happened, and a session that is gone shows a stop did.
func (w *Worker) find(ctx context.Context, effect lanes.Effect, session ports.Located, incomplete bool) lanes.Finding {
	switch effect.Kind {
	case lanes.EffectWorktree:
		_, err := w.config.Lanes.Provisioner.Worktrees.Inspect(ctx, effect.Target)
		switch {
		case err == nil:
			return lanes.Found
		case errors.Is(err, ports.ErrNotFound):
			return lanes.FoundNothing
		}
		w.config.Report(fmt.Errorf("effect %s: %w", effect.Key, err))
		return lanes.FoundUnknown
	case lanes.EffectSession:
		switch {
		case incomplete:
			return lanes.FoundUnknown
		case session.Found:
			return lanes.Found
		}
		return lanes.FoundNothing
	case lanes.EffectStop:
		switch {
		case incomplete || session.Found && session.Liveness == lanes.LivenessUnknown:
			return lanes.FoundUnknown
		case !session.Found || session.Liveness == lanes.LivenessDead:
			return lanes.Found
		}
		return lanes.FoundNothing
	case lanes.EffectRemoval:
		_, err := w.config.Lanes.Provisioner.Worktrees.Inspect(ctx, effect.Target)
		switch {
		case errors.Is(err, ports.ErrNotFound):
			return lanes.Found
		case err == nil:
			return lanes.FoundNothing
		}
		w.config.Report(fmt.Errorf("effect %s: %w", effect.Key, err))
		return lanes.FoundUnknown
	}
	return lanes.FoundNothing
}

// Reconcile makes one pass and returns what it did. Lanes it starts run on, so a second
// pass with nothing changed finds them held and does nothing (NFR-REL-3).
func (w *Worker) Reconcile(ctx context.Context) ([]lanes.Action, error) {
	if w.config.Lanes == nil {
		return nil, nil
	}
	world, seen, err := w.observe(ctx)
	if err != nil {
		return nil, err
	}
	crashpoint.Hit("worker.reconcile", "observed")
	actions := lanes.Plan(world)
	crashpoint.Hit("worker.reconcile", "planned")
	for _, action := range actions {
		if err := w.perform(ctx, action, seen); err != nil {
			w.config.Report(fmt.Errorf("lane %s: %s: %w", action.Lane, action.Kind, err))
		}
	}
	w.raiseUnknown(ctx, world.Unknown, seen.records)
	w.settleDecided(ctx)
	w.drainLanes(ctx)
	w.briefCleared(ctx)
	w.respawnLanes(ctx)
	w.tendSignIn(ctx)
	w.deliverMessages(ctx)
	w.parkOnPolicy(ctx)
	w.tendOrchestrator(ctx)
	return actions, nil
}

func (w *Worker) perform(ctx context.Context, action lanes.Action, seen observed) error {
	switch action.Kind {
	case lanes.ActionStart:
		lease := seen.leases[action.Lane]
		if reason := w.admission(ctx, lease, false); lease.WorkOrder != "" && reason != "" {
			return w.hold(ctx, lease, reason)
		}
		w.startRun(ctx, newRun(w, lease), (*laneRun).begin)
	case lanes.ActionClaim:
		return w.claimWoken(ctx, seen.woken[action.Lane])
	case lanes.ActionResume:
		lease := seen.leases[action.Lane]
		if reason := w.admission(ctx, lease, true); reason != "" {
			return w.hold(ctx, lease, reason)
		}
		w.startRun(ctx, w.wokenRun(lease, seen.records[action.Lane]), (*laneRun).resume)
	case lanes.ActionConfirm:
		return w.journal.FinishEffect(ctx, action.Effect, lanes.EffectCompleted, "found after a crash")
	case lanes.ActionAbandon:
		return w.journal.FinishEffect(ctx, action.Effect, lanes.EffectFailed, "not found, and its lane ended")
	case lanes.ActionRetry:
		_, err := w.journal.RetryEffect(ctx, action.Effect)
		return err
	case lanes.ActionContinue:
		lease, ok := seen.leases[action.Lane]
		if !ok || !w.admitsRestart(ctx) {
			return w.endUnattended(ctx, seen.records[action.Lane])
		}
		w.startRun(ctx, w.resumedRun(lease, seen.records[action.Lane]), (*laneRun).proceed)
	case lanes.ActionAdopt:
		run, err := w.adoptedRun(ctx, seen.records[action.Lane], seen.located[action.Lane], seen.leases[action.Lane])
		if err != nil {
			return err
		}
		w.startRun(ctx, run, (*laneRun).adopt)
	case lanes.ActionHandoff:
		lease, ok := seen.leases[action.Lane]
		if !ok || forgetsHandoff() {
			return w.endUnattended(ctx, seen.records[action.Lane])
		}
		w.startRun(ctx, w.resumedRun(lease, seen.records[action.Lane]), (*laneRun).finishHandoff)
	case lanes.ActionPark:
		lease, ok := seen.leases[action.Lane]
		if !ok || forgetsWake() {
			return w.endUnattended(ctx, seen.records[action.Lane])
		}
		w.startRun(ctx, w.resumedRun(lease, seen.records[action.Lane]), (*laneRun).finishParking)
	case lanes.ActionEnd:
		return w.endUnattended(ctx, seen.records[action.Lane])
	case lanes.ActionRelease:
		return w.release(ctx, action.Lane)
	case lanes.ActionArchive:
		return w.archive(ctx, seen.records[action.Lane], seen.archives[action.Lane])
	}
	return nil
}

// endUnattended accounts for a lane with no session left to read. A report it made
// before the crash stands. A lane whose lease went stops through the same guard.
func (w *Worker) endUnattended(ctx context.Context, record lanes.Record) error {
	result := workorders.LaneResult{
		Report:  workorders.Report{Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonWorkerRestarted},
		CostUSD: record.Progress.PriorUSD + record.Progress.Cost.CostUSD, SessionTotalUSD: record.Progress.Cost.SessionTotalUSD,
		UnknownCostTurns: record.Progress.PriorUnknown + record.Progress.Cost.UnknownTurns,
		InputTokens:      record.Progress.Usage.Context(), OutputTokens: record.Progress.Usage.OutputTokens,
	}
	if report, ok := decodeReport(record.Report); ok {
		result.Report = report
	}
	return w.endLane(ctx, record, result, w.leaseGone(ctx, record))
}

func (rt *LaneRuntime) configDir(lane string) string {
	return filepath.Join(rt.StateDir, "lanes", lane)
}

func (rt *LaneRuntime) tempDir(lane string) string {
	if rt.RunDir == "" {
		return ""
	}
	return filepath.Join(rt.RunDir, "t", lane)
}
