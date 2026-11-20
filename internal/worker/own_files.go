package worker

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
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
func (r *laneRun) readStart(ctx context.Context, event lanes.HookEvent) {
	if event.Event != lanes.EventUserPromptSubmit || r.currentGeneration() != firstGeneration || !r.readable(ctx) {
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

// laneFiles reads what the journal holds of a lane's own files, from the digest of each of its generations, and the
// paths the orchestrator decided its repository keeps. A generation with no digest started no session, and one the
// journal cannot read leaves the writes unknown.
func (w *Worker) laneFiles(ctx context.Context, record lanes.Record, decided *ledger.DesiredFiles) lanes.LaneFiles {
	files := w.digestFiles(ctx, record)
	if decided != nil {
		files.Kept = decided.Keep
	}
	return files
}

func (w *Worker) digestFiles(ctx context.Context, record lanes.Record) lanes.LaneFiles {
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

// leaseFiles is what the orchestrator decided of paths in a lane's repository, as the lease its claim holds says, and
// nil once no lease names the claim.
func (w *Worker) leaseFiles(ctx context.Context, record lanes.Record) *ledger.DesiredFiles {
	state, err := w.desired(ctx)
	if err != nil || leaseless(record.Owner) {
		return nil
	}
	for _, lease := range state.Leases {
		if lease.ClaimID == record.ClaimID {
			return lease.Files
		}
	}
	return nil
}

// foreignCommits lists what the lane's commits beyond base hold that is neither its own nor a path its repository
// keeps (FR-LANE-20). The worker reads them before every push it makes or lets a lane ask for. With no base, a store
// that lists no commits, or a worktree it cannot read, it cannot tell, and a guard that refused a push before still
// stands.
func (w *Worker) foreignCommits(ctx context.Context, record lanes.Record, base string, files lanes.LaneFiles) []string {
	trees, listed := w.config.Lanes.Provisioner.Worktrees.(ports.FileCommits)
	if !listed || base == "" || record.Worktree == "" {
		return record.Guard.Refused
	}
	commits, err := trees.Commits(ctx, record.Worktree, base)
	if err != nil {
		w.config.Report(fmt.Errorf("lane %s: reading its commits before a push: %w", record.ID, err))
		return record.Guard.Refused
	}
	var paths []string
	for _, commit := range commits {
		paths = append(paths, commit.Files...)
	}
	_, foreign := files.Split(paths)
	if len(foreign) > maxLeftOut {
		foreign = foreign[:maxLeftOut]
	}
	return foreign
}

// refusedPush is the reason the worker refuses a push of a lane's branch, and empty when its commits hold only what
// is the lane's own.
func (w *Worker) refusedPush(ctx context.Context, record lanes.Record) string {
	base := w.baseOf(ctx, record)
	foreign := w.foreignCommits(ctx, record, base, w.laneFiles(ctx, record, w.leaseFiles(ctx, record)))
	if len(foreign) == 0 {
		return ""
	}
	return lanes.ForeignCommits(base, foreign)
}

// ignoreDecided adds each path the orchestrator decided to ignore in this lane's repository to its .gitignore,
// before the lane's first session starts, in a commit of that file alone (FR-LANE-20). A line the file holds
// already is not added again, so a start a crash repeats commits nothing twice. A lane that writes no branch of its
// own, and a later generation, whose worktree holds the session's work, add nothing.
func (r *laneRun) ignoreDecided(ctx context.Context, worktree string) {
	decided := r.lease.Files
	if decided == nil || len(decided.Ignore) == 0 || r.reviewing() || r.orchestrating() || r.assisting() ||
		r.currentGeneration() != firstGeneration || r.resumeFrom != "" {
		return
	}
	trees, offered := r.rt.Provisioner.Worktrees.(ports.FileCommits)
	if !offered {
		return
	}
	lines := make([]string, 0, len(decided.Ignore))
	for _, path := range decided.Ignore {
		lines = append(lines, lanes.IgnoreLine(path))
	}
	file := ports.TreeFile{Path: planning.GitignorePath, Lines: lines}
	if _, err := trees.CommitFiles(ctx, worktree, []ports.TreeFile{file}, lanes.IgnoreCommitSubject); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: ignoring the paths the orchestrator decided: %w", r.lease.Lane, err))
	}
}

// refusedPublish is the reason the worker refuses to push a branch for its pull request, read from the lane that
// made it, and empty when the branch is not that lane's or holds only its own files. A lane this worker's journal
// does not hold is not read.
func (w *Worker) refusedPublish(ctx context.Context, request ledger.DesiredForge) string {
	if request.Lane == "" {
		return ""
	}
	record, err := w.journal.Lane(ctx, request.Lane)
	if err != nil || record.Branch != request.Branch {
		return ""
	}
	foreign := w.foreignCommits(ctx, record, request.BaseCommit, w.laneFiles(ctx, record, request.Files))
	if len(foreign) == 0 {
		return ""
	}
	return lanes.ForeignCommits(request.BaseCommit, foreign)
}
