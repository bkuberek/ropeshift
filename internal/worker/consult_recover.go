package worker

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// recoverConsultations ends the consultations a worker that went away left. The journal keeps the start of each one
// whose end it never recorded, and a session of one may still run and spend, with nobody to read it and the ledger's
// hold on its question running out. For each such consultation that this process does not run, the next start stops what
// its worker left, records its end with what the session cost, and so lets go of the question (FR-ORCH-9). Each runs on
// a goroutine of its own, because a stop that reads no end waits for its grace.
func (w *Worker) recoverConsultations(ctx context.Context) {
	if w.config.Lanes == nil {
		return
	}
	kept, err := w.journal.Consultations(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("consultations: reading the journal: %w", err))
		return
	}
	for _, start := range kept {
		if !w.consults.begin(start.Grant.ID) {
			continue
		}
		w.goTracked(trackedConsultation, func() {
			defer w.consults.finish(start.Grant.ID)
			w.recoverConsultation(ctx, start)
		})
	}
}

// recoverConsultation ends one consultation a worker left. A driver that cannot say whether its session is there, or a
// session it cannot take over, leaves the start in the journal for the next start, because the session may be running
// yet. A session that is gone ends the consultation with no cost to count, because nothing remains to read it from.
func (w *Worker) recoverConsultation(ctx context.Context, start ledger.ConsultationRun) {
	rt := w.config.Lanes
	run := &consultation{grant: start.Grant, project: start.Project, orchestrator: start.Orchestrator, lane: consultLanePrefix + start.Grant.ID}
	dir, private, config := rt.consultDirs(run.grant.ID)
	spec := ports.SessionSpec{
		Lane: run.lane, Generation: firstGeneration, Dir: private, ConfigDir: config, TempDir: rt.tempDir(run.lane), Binary: rt.Binary, Override: rt.Override,
	}
	located, err := rt.Driver.Find(ctx, spec)
	if err != nil {
		w.config.Report(fmt.Errorf("consultation %s: looking for the session its worker left: %w", run.grant.ID, err))
		return
	}
	end := ledger.ConsultationEnd{Outcome: ledger.ConsultationUnreported, Detail: "Its worker restarted and found no session of it, and it reported nothing."}
	if located.Found {
		followed, err := w.stopLeftSession(ctx, run, spec, located.Handle)
		switch {
		case errors.Is(err, ports.ErrNoSession):
		case err != nil:
			w.config.Report(fmt.Errorf("consultation %s: stopping the session its worker left: %w", run.grant.ID, err))
			return
		default:
			end = followed
		}
	}
	w.forgetLeftConsultation(ctx, run, dir, private, config)
	w.recordConsultationEnd(ctx, run, end)
}

// stopLeftSession takes over the session a worker left, stops it, and reads it to its end, for what it cost.
func (w *Worker) stopLeftSession(ctx context.Context, run *consultation, spec ports.SessionSpec, handle ports.SessionHandle) (ledger.ConsultationEnd, error) {
	rt := w.config.Lanes
	if err := rt.Driver.Adopt(ctx, handle, ports.AdoptFrom{Spec: spec}); err != nil {
		return ledger.ConsultationEnd{}, err
	}
	events, err := rt.Driver.Events(ctx, handle)
	if err != nil {
		_ = rt.Driver.Stop(context.WithoutCancel(ctx), handle, ports.StopNow)
		return ledger.ConsultationEnd{}, err
	}
	return w.followConsultation(ctx, run, handle, events, consultStop{restarted: true}), nil
}

// forgetLeftConsultation removes what a consultation's session left on this machine, as the end of a consultation this
// process ran does: its read-only worktree, which the checkout it came from forgets, and then its directories.
func (w *Worker) forgetLeftConsultation(ctx context.Context, run *consultation, dir string, paths ...string) {
	rt := w.config.Lanes
	if repository := run.grant.Repository; repository != nil {
		checkout, err := rt.Repository(ctx, run.project, repository.ID)
		if err == nil {
			err = rt.Provisioner.Worktrees.Discard(ctx, checkout, filepath.Join(dir, consultTreeDir))
		}
		if err != nil {
			w.config.Report(fmt.Errorf("consultation %s: removing its read-only worktree: %w", run.grant.ID, err))
		}
	}
	w.forgetConsultation(dir, append(paths, rt.tempDir(run.lane))...)
}
