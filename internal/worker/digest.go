package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// A generation's digest is built from events the worker already receives (FR-SLOG-1): the stream's tool calls
// and results for a headless session, and the hooks the worker already takes for an interactive one. No hook
// does more work to build it. The journal keeps the digest while the generation runs, and the generation's end
// records it as one `generation_digest` event, which a restart that finds a digest ended and unrecorded sends
// once (FR-SLOG-2).

// setStartedMode keeps the run mode of the session this lane started, which says where its digest comes from.
func (r *laneRun) setStartedMode(mode string) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.startedMode = mode
}

func (r *laneRun) startedRunMode() string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.startedMode
}

// digestBegins starts a digest for the generation this run follows from now, at a time. The digest of the one
// before ended with it.
func (r *laneRun) digestBegins(now int64) {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	r.digest, r.digestGeneration, r.digestStartMs = nil, 0, now
}

// digestSource is where this run's digest comes from, and empty before a session started.
func (r *laneRun) digestSource() lanes.DigestSource {
	switch r.startedRunMode() {
	case "":
		return ""
	case ports.RunModeInteractive:
		return lanes.DigestHooks
	}
	return lanes.DigestStream
}

// openDigest is the builder of this generation's digest, which digestLock guards the use of. A run that has
// begun none reads the journal first, because a worker that took a session over goes on from what the one
// before it kept. It is nil for a generation that has no session or whose digest is over already.
func (r *laneRun) openDigest(ctx context.Context) *lanes.DigestBuilder {
	generation := r.currentGeneration()
	if r.digest != nil && r.digestGeneration == generation {
		return r.digest
	}
	source := r.digestSource()
	if source == "" {
		return nil
	}
	r.digest, r.digestGeneration = nil, generation
	kept, err := r.w.journal.Digest(ctx, r.lease.Lane, generation)
	switch {
	case err == nil && kept.Over:
		return nil
	case err == nil:
		builder, err := lanes.RestoreDigest(kept.State)
		if err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
			return nil
		}
		// A restart may have missed hooks no stream keeps for it.
		if builder.Source() == lanes.DigestHooks {
			builder.Missed()
		}
		r.digest = builder
	case errors.Is(err, ports.ErrNotFound):
		r.digest = lanes.NewDigest(source, r.worktreePath(), r.digestStartMs)
		// A session this run took over, with nothing kept of it, has events this digest never saw.
		if r.adopted.Load() && (source == lanes.DigestHooks || r.cursor > 0) {
			r.digest.Missed()
		}
	default:
		r.w.config.Report(fmt.Errorf("lane %s: reading its digest: %w", r.lease.Lane, err))
	}
	return r.digest
}

// withDigest folds into this generation's digest, when the digest comes from the source the events do.
func (r *laneRun) withDigest(ctx context.Context, source lanes.DigestSource, fold func(*lanes.DigestBuilder)) {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if builder := r.openDigest(ctx); builder != nil && builder.Source() == source {
		fold(builder)
	}
}

// foldEvent folds one event of a headless session's stream: the calls its message made, a turn's end, and a
// line the driver could not read.
func (r *laneRun) foldEvent(ctx context.Context, event ports.DriverEvent) {
	if len(event.Tools) == 0 && event.Kind != ports.EventTurnEnded && event.Kind != ports.EventMalformed {
		return
	}
	r.withDigest(ctx, lanes.DigestStream, func(builder *lanes.DigestBuilder) {
		if len(event.Tools) != 0 {
			builder.Calls(event.Seq, event.Tools)
		}
		switch event.Kind {
		case ports.EventTurnEnded:
			builder.Turn(event.Seq, lanes.TurnFacts{DurationMs: event.Turn.DurationMs, APIMs: event.Turn.APIMs, Compaction: event.Turn.Compaction})
		case ports.EventMalformed:
			builder.Missed()
		}
	})
}

// foldHook folds one hook an interactive session ran, as the worker takes it. A replay of the hooks a journal
// kept is no hook the session ran now, so it is never folded.
func (r *laneRun) foldHook(ctx context.Context, event lanes.HookEvent) {
	switch event.Event {
	case lanes.EventUserPromptSubmit, lanes.EventPreToolUse, lanes.EventStop:
	default:
		return
	}
	r.withDigest(ctx, lanes.DigestHooks, func(builder *lanes.DigestBuilder) {
		builder.Hook(event)
		if event.Event == lanes.EventStop {
			r.keepDigest(ctx, builder, false)
		}
	})
}

// saveDigest keeps the digest so far in the journal, before the progress that names the position it holds to.
func (r *laneRun) saveDigest(ctx context.Context) {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if r.digest != nil && r.digestGeneration == r.currentGeneration() {
		r.keepDigest(ctx, r.digest, false)
	}
}

func (r *laneRun) keepDigest(ctx context.Context, builder *lanes.DigestBuilder, over bool) {
	state, err := builder.Marshal()
	if err == nil {
		err = r.w.journal.SaveDigest(context.WithoutCancel(ctx), r.lease.Lane, r.digestGeneration, state, over)
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: keeping its digest: %w", r.lease.Lane, err))
	}
}

// endDigest finishes this generation's digest and records it, once the session that made it is over or a clear
// has begun the next generation. The digest is kept as final first, so a crash before the record leaves it for
// the next start.
func (r *laneRun) endDigest(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	builder := r.openDigest(ctx)
	if builder == nil {
		return
	}
	generation := r.digestGeneration
	digest := builder.Finish(r.w.clock.Now().UnixMilli())
	r.keepDigest(ctx, builder, true)
	r.digest = nil
	crashpoint.Hit("worker.digest", "ended")
	record := r.record()
	record.Generation = generation
	if err := r.w.recordDigest(ctx, record, digest); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: recording its digest: %w", r.lease.Lane, err))
	}
}

// recordDigest journals the event that carries a finished digest, once. A digest that was recorded already
// records nothing new.
func (w *Worker) recordDigest(ctx context.Context, record lanes.Record, digest lanes.Digest) error {
	payload, err := json.Marshal(digest)
	if err != nil {
		return err
	}
	_, err = w.journal.RecordDigest(ctx, record.ID, record.Generation, w.event(ledger.UploadGenerationDigest, record, payload))
	return err
}

// recordLeftDigests records the digests a crash left between a generation's end and its record. It runs
// once in a process, before any run begins.
func (w *Worker) recordLeftDigests(ctx context.Context) error {
	left, err := w.journal.UnrecordedDigests(ctx)
	if err != nil {
		return err
	}
	for _, kept := range left {
		digest, err := kept.Digest()
		if err != nil {
			w.config.Report(fmt.Errorf("lane %s: a digest left by a crash: %w", kept.Lane, err))
			continue
		}
		record, err := w.journal.Lane(ctx, kept.Lane)
		if err != nil {
			w.config.Report(fmt.Errorf("lane %s: a digest left by a crash: %w", kept.Lane, err))
			continue
		}
		record.Generation = kept.Generation
		if err := w.recordDigest(ctx, record, digest); err != nil {
			return err
		}
	}
	return nil
}
