package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// clearedInput names the one brief a cleared generation is owed.
const clearedInput = "cleared"

// processGeneration is the generation that names this lane's session.
func (r *laneRun) processGeneration() int64 {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if r.process != 0 {
		return r.process
	}
	return r.generation
}

func (r *laneRun) setProcess(generation int64) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.process = generation
}

// cleared records a clear that ran inside this lane's process (FR-LANE-10). The process stays, so
// nothing starts and the token stays (D4), and the new generation runs under the tags it started
// with. Two things can say one clear happened, and a session's ID says it is the same one. It runs
// on a hook's goroutine, so it answers before a session's next hook can arrive, and only the
// journal, which keeps the move and its events in one write, hears of it first.
func (r *laneRun) cleared(ctx context.Context, session string) {
	report := func(err error) { r.w.config.Report(fmt.Errorf("lane %s: a clear: %w", r.lease.Lane, err)) }
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		report(err)
		return
	}
	if session == "" || record.SessionID == session {
		return
	}
	if record.SessionID == "" {
		r.bindEnding(ctx, session)
	}
	stored := r.clearedRecord(record, session)
	start, err := json.Marshal(ledger.GenerationStart{Reason: ledger.StartClear})
	if err != nil {
		report(err)
		return
	}
	binding, err := json.Marshal(r.binding(session, r.resolvedModel()))
	if err != nil {
		report(err)
		return
	}
	r.owesBrief.Store(true)
	first, err := r.keepClear(ctx, stored, start, binding)
	if err != nil {
		report(err)
		return
	}
	if !first {
		return
	}
	crashpoint.Hit("worker.clear", "cleared")
	// What a generation reports resets before the generation moves, so a save that reads this one
	// reads nothing of the last.
	r.state.newGeneration(r.w.clock.Now(), 0)
	r.handleLock.Lock()
	r.generation, r.instruction = stored.Generation, ""
	r.handleLock.Unlock()
	r.nudged.Store(false)
	if life := r.life; life != nil {
		r.sequence.Go(func() { r.uploadBounded(life) })
	}
}

// bindEnding records the session of the generation a clear ends, when the worker has not read that
// session's binding from the stream yet. A clear's hooks can arrive ahead of the stream, and a
// binding read after the clear finds the lane on the next generation and records nothing. The
// driver already holds the session its stream names. A driver that has moved on to the session the
// clear began holds nothing of the one before it, so that answer binds nothing.
func (r *laneRun) bindEnding(ctx context.Context, cleared string) {
	handle := r.sessionHandle()
	if handle.ID == "" {
		return
	}
	observation, err := r.rt.Driver.Observe(ctx, handle)
	if err != nil || observation.SessionID == "" || observation.SessionID == cleared {
		return
	}
	r.bindSession(ctx, observation.SessionID, observation.ModelResolved)
}

// keepClear is one journal write, because a lane on a generation the ledger never hears of would
// have every later write refused.
func (r *laneRun) keepClear(ctx context.Context, stored lanes.Record, start, binding []byte) (bool, error) {
	started, bound := r.w.event(ledger.UploadGenerationStarted, stored, start), r.w.event(ledger.UploadGenerationBound, stored, binding)
	if !splitsClear() {
		return r.w.journal.ClearGeneration(ctx, stored, started, bound)
	}
	status := r.w.event(ledger.UploadLaneStatus, stored, nil)
	first, err := r.w.journal.ClearGeneration(ctx, stored, status, status)
	if err != nil || !first {
		return first, err
	}
	crashpoint.Hit("worker.clear", "split")
	if _, err := r.w.journal.Append(ctx, started); err != nil {
		return true, err
	}
	_, err = r.w.journal.Append(ctx, bound)
	return true, err
}

// clearedRecord is a lane as a clear leaves it: the next generation, on the session the hook named,
// and with a session that starts over from nothing.
func (r *laneRun) clearedRecord(record lanes.Record, session string) lanes.Record {
	progress := record.Progress
	progress.Instruction, progress.ContextTokens, progress.BaselineTokens = "", 0, 0
	progress.Compactions, progress.CompactPending, progress.CompactSeen = 0, false, false
	progress.OwesBrief = true
	stored := r.record()
	stored.Generation, stored.SessionID, stored.Progress = record.Generation+1, session, progress
	return stored
}

// binding is what a generation's session is, for the ledger.
func (r *laneRun) binding(session, model string) workorders.GenerationBinding {
	r.handleLock.Lock()
	binding := workorders.GenerationBinding{
		SessionID: session, ModelRequested: r.model, ModelResolved: model,
		ProfileDigest: r.profileDigest, DriverVersion: r.version, DegradedMode: r.degraded, Carried: r.carried,
	}
	settings := r.settings
	settings.ModelRequested, settings.ModelResolved, settings.ProfileDigest = r.model, model, r.profileDigest
	binding.Settings = &settings
	r.handleLock.Unlock()
	if read := r.readPack(); read != nil {
		binding.PackID, binding.PackVersion = read.ID, read.Version
	}
	return binding
}

// briefCleared tells a cleared session its work once it is at rest. No turn ends a clear in a pane,
// so this reads each lane on the worker's own pass, and the session reads the brief with what its
// hook told it. The effect holds this to once across a restart.
func (w *Worker) briefCleared(ctx context.Context) {
	for _, run := range w.runs() {
		run.briefCleared(ctx)
	}
}

func (r *laneRun) briefCleared(ctx context.Context) {
	if !r.owesBrief.Load() || r.state.current() != lanes.Idle || r.currentFlight() != nil {
		return
	}
	if _, reported := r.reported(ctx); reported {
		return
	}
	text := r.briefText()
	effect := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.currentGeneration(), "", clearedKey())
	ran, err := r.w.bracket(ctx, effect, func() error {
		_, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: text})
		return err
	})
	r.owesBrief.Store(false)
	crashpoint.Hit("worker.clear", "briefed")
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: a cleared session's brief: %w", r.lease.Lane, err))
		return
	}
	if ran {
		r.setInstruction(text)
		r.signal(ctx, lanes.OnInputHanded)
	}
}
