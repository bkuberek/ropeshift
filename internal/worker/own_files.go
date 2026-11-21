package worker

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// maxLeftOut is how many left-out paths a guard names. A folder git tracks nothing in is one path, and the guard
// counts the rest, so a worktree with more than this is one a person reads for themselves.
const maxLeftOut = 100

// readStart keeps what the worktree holds when the lane's first session begins its first turn, which is when its
// first prompt is submitted: the session's own start has run by then, and its tools have not (FR-LANE-20). Only a
// digest that has seen no turn and no call takes it, and only from a prompt that reaches the worker live. A reading
// that fails, or one too late to take, leaves the lane unread for good, so it owns every change and nothing it made
// can read as there before.
//
// A first turn whose hooks missed their acks waits in the spool, and reads as no turn until the spool is taken, so
// the spool is taken before a prompt that could take the reading is read.
func (r *laneRun) readStart(ctx context.Context, event lanes.HookEvent) {
	if event.Event != lanes.EventUserPromptSubmit || r.currentGeneration() != firstGeneration || r.startSettled(ctx) {
		return
	}
	r.w.drainBeforeActing(ctx)
	if !r.readable(ctx) {
		return
	}
	worktree := r.worktreePath()
	if worktree == "" || r.w.config.Lanes == nil {
		r.leaveUnread(ctx)
		return
	}
	found, err := r.w.config.Lanes.Provisioner.Worktrees.Survey(ctx, worktree)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its worktree at its first turn: %w", r.lease.Lane, err))
		r.leaveUnread(ctx)
		return
	}
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if builder := r.openDigest(ctx); builder != nil && !builder.Settled() {
		builder.Begin(found.DirtyPaths)
		r.keepDigest(ctx, builder, false)
	}
}

// startSettled says the reading was kept or given up on, or that this run has no digest to keep one in.
func (r *laneRun) startSettled(ctx context.Context) bool {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	builder := r.openDigest(ctx)
	return builder == nil || builder.Settled()
}

// readable says this is the moment for the reading: none was kept or given up on, and the digest has seen nothing
// of the session's work. A digest past that moment is left unread here.
func (r *laneRun) readable(ctx context.Context) bool {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	builder := r.openDigest(ctx)
	if builder == nil || builder.Settled() {
		return false
	}
	if !builder.Fresh() {
		builder.LeaveUnread()
		r.keepDigest(ctx, builder, false)
		return false
	}
	return true
}

// leaveUnread gives up on the reading for the first generation, as a prompt a spool held does: by the time it
// arrives, the session may have done work the reading would take for what was there.
func (r *laneRun) leaveUnread(ctx context.Context) {
	if r.currentGeneration() != firstGeneration {
		return
	}
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if builder := r.openDigest(ctx); builder != nil && !builder.Settled() {
		builder.LeaveUnread()
		r.keepDigest(ctx, builder, false)
	}
}

// laneFiles reads what the journal holds of a lane's own files, from the digest of each of its generations. A
// generation with no digest started no session, and one the journal cannot read leaves the writes unknown.
func (w *Worker) laneFiles(ctx context.Context, record lanes.Record) lanes.LaneFiles {
	var records []lanes.DigestRecord
	for generation := int64(firstGeneration); generation <= record.Generation; generation++ {
		kept, err := w.journal.Digest(ctx, record.ID, generation)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			w.config.Report(fmt.Errorf("lane %s: reading its digest: %w", record.ID, err))
			return lanes.LaneFiles{}
		}
		records = append(records, kept)
	}
	return lanes.LaneFilesOf(records)
}

// ownChanges is what of a survey's changes a guard saves, and what it leaves out. A store that cannot snapshot
// named paths saves the whole worktree, so it leaves nothing out.
func (w *Worker) ownChanges(files lanes.LaneFiles, found lanes.WorktreeFindings) (own, left []string) {
	if _, scoped := w.config.Lanes.Provisioner.Worktrees.(ports.PathSnapshots); !scoped {
		return found.DirtyPaths, nil
	}
	return files.Split(found.DirtyPaths)
}

// leftOut is what a guard reports of the files it left out: a folder git tracks nothing in by the folder, at most
// maxLeftOut paths, and how many more there were.
func leftOut(own, left []string, found lanes.WorktreeFindings) ([]string, int64) {
	report := lanes.CollapseLeft(left, own, found.Folders)
	if len(report) > maxLeftOut {
		return report[:maxLeftOut], int64(len(report) - maxLeftOut)
	}
	return report, 0
}

// owned is a survey as a guard reads it: dirty only for a change of the lane's own.
func owned(found lanes.WorktreeFindings, own, left []string) lanes.WorktreeFindings {
	if len(left) != 0 && len(own) == 0 {
		found.Dirty = false
	}
	found.DirtyPaths = own
	return found
}
