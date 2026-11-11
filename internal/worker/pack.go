package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// takeContext carries one read of the Context group to the control plane. The bound is the rule
// that no call stays open: with the control plane away, a lane learns that and goes on.
func (w *Worker) takeContext(ctx context.Context, lane lanes.Record, request lanes.ContextRequest) lanes.Reply {
	// A lane reads only its own project. An orchestrator serves every project, and names the one it reads.
	project := lane.Project
	if lane.Owner == lanes.OwnerOrchestrator {
		if err := w.namesProject(ctx, request.Project); err != nil {
			return refused(lanes.CodeInvalid, err.Error())
		}
		project = request.Project
	}
	bounded, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()
	found, err := w.link.LaneContext(bounded, ledger.LaneContextRequest{
		Auth: w.Auth(), Epoch: w.epoch(), Project: project, WorkOrder: lane.WorkOrder, Fence: lane.Fence, Lane: lane.ID,
		Tool: request.Tool, Kind: request.Kind, Ref: request.Ref, Query: request.Query, Limit: request.Limit,
	})
	var fenced *workorders.FencedError
	switch {
	case err == nil:
		w.noteLedger(lane.ID, lanes.Reachable)
		return lanes.Reply{OK: true, Context: found.Records}
	case errors.Is(err, workorders.ErrInvalid):
		return refused(lanes.CodeInvalid, err.Error())
	case errors.As(err, &fenced):
		return refused(lanes.CodeFenced, err.Error())
	}
	w.noteLedger(lane.ID, lanes.Impaired)
	return refused(lanes.CodeUnavailable, err.Error())
}

func (w *Worker) noteLedger(lane string, reach lanes.Reachability) {
	if run := w.runOf(lane); run != nil {
		run.noteLedger(reach)
	}
}

// writePack leaves the newest version this run has heard of in the worktree, and says where. A
// lane that holds no pack starts as it would have. The text goes as the ledger holds it, so every
// generation of one version reads the same bytes, which is what lets them share a cache.
func (r *laneRun) writePack(ctx context.Context, worktree string) (string, error) {
	r.handleLock.Lock()
	pack := r.pack
	r.handleLock.Unlock()
	if pack == nil {
		r.setPackRead(nil)
		return "", nil
	}
	file, err := r.rt.Provisioner.Worktrees.WriteContext(ctx, worktree, pack.Text)
	if err != nil {
		return "", err
	}
	r.setPackRead(&lanes.PackRef{ID: pack.ID, Version: pack.Version})
	return file, nil
}

// judgeable says a review lane has what it judges: the commit under review, and a claim that named it as
// where the lane begins. A lane that began anywhere else would read the wrong work and call it the right.
func (r *laneRun) judgeable() error {
	if !r.reviewing() {
		return nil
	}
	attempt := r.lease.Attempt
	if attempt == nil || attempt.Commit == "" || r.lease.BaseCommit != attempt.Commit {
		return errors.New("a review lane needs the commit it judges, and its claim did not name it as where the lane begins")
	}
	return nil
}

// writeReview leaves what a review lane reads beside its pack: the change the reviewed lane made, as a
// patch from the commit that lane began at to the commit under review, and the paths it touches. The
// worker reads them from the repository's own history, so the reviewed lane has no hand in them. A lane
// that recorded no base has no diff, and its brief says to read the history.
func (r *laneRun) writeReview(ctx context.Context, repository, worktree string) error {
	attempt := r.lease.Attempt
	if !r.reviewing() || attempt == nil || attempt.BaseCommit == "" {
		return nil
	}
	worktrees, offered := r.rt.Provisioner.Worktrees.(ports.ReviewTrees)
	if !offered {
		return ErrNoDetached
	}
	diff, err := worktrees.Diff(ctx, repository, attempt.BaseCommit, attempt.Commit)
	if err != nil {
		return fmt.Errorf("reading the diff to review: %w", err)
	}
	if _, err := worktrees.WriteContextFile(ctx, worktree, path.Base(lanes.ReviewDiffFile), diff.Patch); err != nil {
		return err
	}
	files := strings.Join(diff.Files, "\n")
	if files != "" {
		files += "\n"
	}
	_, err = worktrees.WriteContextFile(ctx, worktree, path.Base(lanes.ReviewFilesFile), files)
	return err
}

// parkOnPolicy parks each lane a policy asked to stop, for a person to look at what changed under
// it. A lane that cannot park now comes up on the next pass, and each lane decides alone.
func (w *Worker) parkOnPolicy(ctx context.Context) {
	leases, err := w.desiredLeases(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading what policy asks: %w", err))
		return
	}
	for _, lease := range leases {
		if lease.ParkWanted == "" {
			continue
		}
		if run := w.runOf(lease.Lane); run != nil {
			run.parkReported(ctx, lanes.ParkRequest{Trigger: lanes.ParkPolicy, Expr: lanes.HumanWake()})
		}
	}
}

// recordBaseline tells the ledger what this generation started with, once. Zero says nothing new.
// The journal keeps it at once, because a call a restart read first is not what the session began
// with, and the policy would read a lane near its threshold as one with no room to hand off.
func (r *laneRun) recordBaseline(ctx context.Context, tokens int64) {
	if tokens == 0 {
		return
	}
	r.saveProgress(ctx, r.cursor)
	payload, err := json.Marshal(ledger.GenerationBaseline{Tokens: tokens})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if _, err := r.w.Record(ctx, r.w.event(ledger.UploadGenerationBaseline, r.record(), payload)); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

// setPack takes the newest version the ledger names, for the generation about to start.
func (r *laneRun) setPack(pack *ledger.DesiredPack) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.pack = pack
}

func (r *laneRun) setPackRead(read *lanes.PackRef) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.packRead = read
}

// readPack names the version this generation holds.
func (r *laneRun) readPack() *lanes.PackRef {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.packRead
}
