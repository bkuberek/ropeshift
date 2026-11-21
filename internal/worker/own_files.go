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

// readStart begins the reading of what the worktree holds when the lane's first session begins its first turn, which
// is when its first prompt is submitted: the session's own start has run by then, and its tools have not
// (FR-LANE-20). Only a digest that has seen no turn and no call takes it, and only from a prompt that reaches the
// worker live. A reading that fails, or one too late to take, leaves the lane unread for good, so it owns every change
// and nothing it made can read as there before.
//
// A first turn whose hooks missed their acks waits in the spool, and reads as no turn until the spool is taken, so
// the spool is taken before a prompt that could take the reading is read. The prompt's hook has milliseconds for its
// answer and a survey runs git several times, so the moment is marked here, the answer goes at once, and the survey
// runs after it (takeReading).
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
	r.w.goTracked(trackedReading, func() { r.takeReading(ctx, worktree) })
}

// takeReading surveys the worktree for the reading its first prompt began, and keeps it unless something gave it up
// first. The session's tools begin while the survey runs, and a call gives the reading up (heardCall). A call whose
// hook missed its ack is in the spool before the call runs, so once the spool is taken, every call made before the
// survey ended has been heard. A worktree gone before the survey came back is a lane that ended.
func (r *laneRun) takeReading(ctx context.Context, worktree string) {
	found, err := r.w.config.Lanes.Provisioner.Worktrees.Survey(ctx, worktree)
	if err != nil {
		if !errors.Is(err, ports.ErrNotFound) {
			r.w.config.Report(fmt.Errorf("lane %s: reading its worktree at its first turn: %w", r.lease.Lane, err))
		}
		r.leaveUnread(ctx)
		return
	}
	r.w.drainBeforeActing(ctx)
	if r.currentGeneration() != firstGeneration {
		return
	}
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if builder := r.openDigest(ctx); builder != nil && !builder.Settled() {
		builder.Begin(found.DirtyPaths)
		r.keepDigest(ctx, builder, false)
	}
}

// heardCall gives up a reading whose survey has not come back once the session's tools begin: a file a call made
// before the survey ended would read as there before. It reads the hook whatever the digest's source is, because a
// stream reaches the worker later than the hook that ran before the call.
func (r *laneRun) heardCall(ctx context.Context, event lanes.HookEvent) {
	if event.Event != lanes.EventPreToolUse || r.currentGeneration() != firstGeneration {
		return
	}
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	if builder := r.openDigest(ctx); builder != nil && builder.Awaiting() {
		builder.LeaveUnread()
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

// readable marks the moment for the reading, and says it is one: none was kept, given up on, or begun, and the digest
// has seen nothing of the session's work. The mark is kept, so a worker that restarts before the survey comes back
// takes no second reading. A digest past that moment is left unread here.
func (r *laneRun) readable(ctx context.Context) bool {
	r.digestLock.Lock()
	defer r.digestLock.Unlock()
	builder := r.openDigest(ctx)
	if builder == nil || builder.Settled() {
		return false
	}
	awaited := builder.Await()
	r.keepDigest(ctx, builder, false)
	return awaited
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
		if line, ok := r.ignoreLine(ctx, trees, worktree, path); ok {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return
	}
	file := ports.TreeFile{Path: planning.GitignorePath, Lines: lines}
	if _, err := trees.CommitFiles(ctx, worktree, []ports.TreeFile{file}, lanes.IgnoreCommitSubject); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: ignoring the paths the orchestrator decided: %w", r.lease.Lane, err))
	}
}

// ignoreLine is the .gitignore line for a path the orchestrator decided to ignore, and false for a path the worker
// keeps out of the file: one no line can name alone, a .gitignore, and one the branch tracks or holds tracked files
// under. A survey reads no ignored file, so a new file there would never read as a change, and a discard of the
// worktree would lose it. A worktree it cannot read keeps the path out too.
func (r *laneRun) ignoreLine(ctx context.Context, trees ports.FileCommits, worktree, path string) (string, bool) {
	line, ok := lanes.IgnoreLine(path)
	if !ok {
		r.w.config.Report(fmt.Errorf("lane %s: no .gitignore line ignores %q alone", r.lease.Lane, path))
		return "", false
	}
	tracked, err := trees.Tracks(ctx, worktree, path)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %q stays out of the .gitignore: reading what the branch tracks: %w", r.lease.Lane, path, err))
		return "", false
	}
	if tracked {
		r.w.config.Report(fmt.Errorf("lane %s: %q stays out of the .gitignore: the branch tracks files there", r.lease.Lane, path))
		return "", false
	}
	return line, true
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
