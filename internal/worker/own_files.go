package worker

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// maxLeftOut is how many left-out paths a guard reports. Git shows an untracked folder whole, so a cache is one
// path, and a worktree with more than this is one a person reads for themselves.
const maxLeftOut = 100

// readStart keeps what the worktree holds when the lane's first session begins its first turn, which is when its
// first prompt is submitted: the session's own start has run by then, and its tools have not (FR-LANE-20). Only
// the hook that reaches the worker live reads it, because one a spool held may arrive after the session began
// work. A lane that has its reading, and a generation after the first, read nothing.
func (r *laneRun) readStart(ctx context.Context, event lanes.HookEvent) {
	if event.Event != lanes.EventUserPromptSubmit || r.currentGeneration() != firstGeneration || r.started(ctx) {
		return
	}
	worktree := r.worktreePath()
	if worktree == "" || r.w.config.Lanes == nil {
		return
	}
	found, err := r.w.config.Lanes.Provisioner.Worktrees.Survey(ctx, worktree)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its worktree at its first turn: %w", r.lease.Lane, err))
		return
	}
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if builder := r.openDigest(ctx); builder != nil && builder.Begin(found.DirtyPaths) {
		r.keepDigest(ctx, builder, false)
	}
}

// started says this lane's reading is kept, or that it has no digest to keep one in.
func (r *laneRun) started(ctx context.Context) bool {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	builder := r.openDigest(ctx)
	if builder == nil {
		return true
	}
	_, began := builder.Before()
	return began
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

// owned is a survey as a guard reads it: dirty only for a change of the lane's own.
func owned(found lanes.WorktreeFindings, own, left []string) lanes.WorktreeFindings {
	if len(left) != 0 && len(own) == 0 {
		found.Dirty = false
	}
	found.DirtyPaths = own
	return found
}
