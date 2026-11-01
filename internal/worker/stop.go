package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// guardPushLimit bounds one guard, because a remote that hangs must not hold a stop.
const guardPushLimit = 2 * time.Minute

// The passes of a guard, each one effect for good.
const (
	guardBeforeStop = "stop"
	guardAtEnd      = "end"
	guardAtRemoval  = "removal"
)

// errHookUnknown means a crash left a hook's end unknown. Only its author knows what running
// it again would do, so a person looks, and a lane does not go on from half a setup.
var errHookUnknown = errors.New("a crash left a profile's hook unknown")

func (rt *LaneRuntime) portPool() lanes.PortPool {
	if rt.Ports == (lanes.PortPool{}) {
		return lanes.DefaultPortPool
	}
	return rt.Ports
}

// allocate gives a lane its block. The same lane gets the same block back, whichever
// process and generation asks.
func (r *laneRun) allocate(ctx context.Context, settings lanes.ResourceSettings) (lanes.Allocation, error) {
	allocation, err := r.w.journal.Allocate(ctx, lanes.AllocationRequest{
		Lane: r.lease.Lane, Ports: settings.PortsPerLane, Pool: r.rt.portPool(),
		ComposeProject: lanes.ComposeProject(settings.ComposeProjectPrefix, r.lease.Lane),
	})
	if err == nil {
		crashpoint.Hit("worker.lane", "allocated")
	}
	return allocation, err
}

// release gives a lane's block back. A lane's end does this at once, and a crash before it
// leaves an ended lane holding one, which the next pass finds.
func (w *Worker) release(ctx context.Context, lane string) error {
	if keepsResources() {
		return nil
	}
	return w.journal.Release(ctx, lane)
}

// runHook runs a lane's setup once for good, whichever generation gets here first. It has
// no second attempt: a hook that failed stays failed, and one a crash cut short waits for a
// person.
func (r *laneRun) runHook(ctx context.Context, target string, perform func() error) error {
	effect := lanes.NewEffect(lanes.EffectHook, r.lease.Lane, firstGeneration, target, lanes.HookLaneCreated)
	ran, err := r.w.bracket(ctx, effect, perform)
	if ran || err != nil {
		return err
	}
	current, err := r.w.journal.Effect(ctx, effect.Key)
	if err != nil {
		return err
	}
	if current.State != lanes.EffectCompleted {
		return fmt.Errorf("%w: %s", errHookUnknown, current.State)
	}
	return nil
}

// guardWork saves what a worktree holds, by the design's table (FR-LANE-14). It decides from
// what it reads and removes nothing, so a repeat is safe and a crash at any step costs
// nothing. A read that fails leaves the worktree under guard.
func (w *Worker) guardWork(ctx context.Context, record lanes.Record, fenced bool, ref string) lanes.GuardResult {
	worktrees := w.config.Lanes.Provisioner.Worktrees
	found, err := worktrees.Survey(ctx, record.Worktree)
	if errors.Is(err, ports.ErrNotFound) || skipsGuard() {
		return lanes.GuardResult{}
	}
	if err != nil {
		return lanes.UnreadGuard(err)
	}
	crashpoint.Hit("worker.guard", "surveyed")
	acts := lanes.GuardActs{Plan: lanes.PlanGuard(found, fenced), Fenced: fenced}
	failed := func(step string, err error) {
		w.config.Report(fmt.Errorf("lane %s: %s: %w", record.ID, step, err))
		if acts.Detail == "" {
			acts.Detail = step + ": " + err.Error()
		}
	}
	if acts.Plan.Push {
		if err := worktrees.Push(ctx, record.Worktree, found.Remote, lanes.HeadsRef(found.Branch)); err != nil {
			failed("pushing the branch", err)
		}
	}
	crashpoint.Hit("worker.guard", "pushed")
	if acts.Plan.Snapshot {
		commit, err := worktrees.Snapshot(ctx, record.Worktree, ref)
		if err != nil {
			failed("taking a snapshot", err)
		} else {
			acts.Ref, acts.Commit = ref, commit
		}
	}
	crashpoint.Hit("worker.guard", "snapshotted")
	if acts.Plan.PushSnapshot && acts.Ref != "" {
		if err := worktrees.Push(ctx, record.Worktree, found.Remote, ref); err != nil {
			failed("pushing the snapshot", err)
		} else {
			acts.SnapshotPushed = true
		}
	}
	crashpoint.Hit("worker.guard", "snapshot-pushed")
	after, err := worktrees.Survey(ctx, record.Worktree)
	if err != nil {
		failed("reading the worktree again", err)
		result := lanes.SettleGuard(found, acts)
		result.Unpushed = true
		return result
	}
	return lanes.SettleGuard(after, acts)
}

// guardPass runs one pass as an effect and keeps its result. A pass that does not run is
// the same stop reaching a settled record, and the last result stands.
func (w *Worker) guardPass(ctx context.Context, record lanes.Record, fenced bool, label string) (lanes.GuardResult, error) {
	if record.Worktree == "" || w.config.Lanes == nil {
		return lanes.GuardResult{}, nil
	}
	ref := lanes.CheckpointRef(record.ID, record.Generation)
	if fenced {
		ref = lanes.SalvageRef(record.ID, record.Fence)
	}
	bounded, cancel := context.WithTimeout(ctx, guardPushLimit)
	defer cancel()
	var result lanes.GuardResult
	effect := lanes.NewEffect(lanes.EffectGuard, record.ID, record.Generation, record.Worktree, label)
	_, err := w.bracket(ctx, effect, func() error {
		result = w.guardWork(bounded, record, fenced, ref)
		return nil
	})
	if err != nil {
		return lanes.UnreadGuard(err), err
	}
	if err := w.journal.SaveGuard(ctx, record.ID, result); err != nil {
		return result, err
	}
	crashpoint.Hit("worker.guard", "saved")
	return result, nil
}

// guardBeforeStop reads the worktree while the session still runs, as the design orders, so
// a stop that goes badly has already saved what was there.
func (r *laneRun) guardBeforeStop(ctx context.Context) {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	result, err := r.w.guardPass(ctx, record, r.fenced.Load(), guardBeforeStop)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	r.state.mu.Lock()
	r.state.unpushed = result.Unpushed
	r.state.mu.Unlock()
}

// finalGuard reads the worktree once more with the session gone, so what a lane wrote on its
// way out counts, and tells the ledger how it stands. That report passes no fence, because
// a fenced lane's work is exactly what a person must be able to find.
func (w *Worker) finalGuard(ctx context.Context, record lanes.Record, fenced bool) error {
	stored, err := w.journal.Lane(ctx, record.ID)
	if err != nil {
		return err
	}
	stored.WorkOrder, stored.ClaimID, stored.Fence, stored.Generation = record.WorkOrder, record.ClaimID, record.Fence, record.Generation
	result, err := w.guardPass(ctx, stored, fenced, guardAtEnd)
	if err != nil {
		return err
	}
	return w.reportGuard(ctx, stored, result)
}

// reportGuard journals what the ledger should know. A clean stop says nothing.
func (w *Worker) reportGuard(ctx context.Context, record lanes.Record, result lanes.GuardResult) error {
	if !result.Unpushed && result.Ref == "" {
		return nil
	}
	payload, err := json.Marshal(ledger.LaneGuard{
		UnpushedWork: result.Unpushed, Ref: result.Ref, Commit: result.Commit, Detail: result.Detail,
	})
	if err != nil {
		return err
	}
	_, err = w.journal.Append(ctx, w.event(ledger.UploadLaneGuarded, record, payload))
	return err
}

// leaseGone says no desired state names a lane's claim any more, which is how a restarted
// worker learns a lane was fenced while it was away.
func (w *Worker) leaseGone(ctx context.Context, record lanes.Record) bool {
	state, err := w.desired(ctx)
	if err != nil || state.Rev == 0 {
		return false
	}
	for _, lease := range state.Leases {
		if lease.ClaimID == record.ClaimID {
			return false
		}
	}
	return true
}

// archive takes a worktree away. It reads the worktree again first, because work can arrive
// after the stop that read it last. What it finds withdraws a request nobody forced, and a
// forced one still saves what it can to a ref, so nothing is lost by a removal this
// process could have saved.
func (w *Worker) archive(ctx context.Context, record lanes.Record, request ledger.DesiredArchive) error {
	worktrees := w.config.Lanes.Provisioner.Worktrees
	if !removesBlind() {
		result, err := w.guardPass(ctx, record, false, guardAtRemoval)
		if err != nil {
			return err
		}
		if result.Unpushed && !request.Forced {
			return w.reportGuard(ctx, record, result)
		}
	}
	repository, err := w.config.Lanes.Repository(ctx, request.Repository)
	if err != nil {
		return err
	}
	effect := lanes.NewEffect(lanes.EffectRemoval, record.ID, record.Generation, record.Worktree, "")
	removal := func() error { return worktrees.Discard(ctx, repository, record.Worktree) }
	if _, err := w.bracket(ctx, effect, removal); err != nil {
		return err
	}
	if _, err := w.journal.MarkArchived(ctx, record.ID, w.event(ledger.UploadLaneArchived, record, nil)); err != nil {
		return err
	}
	crashpoint.Hit("worker.lane", "archived")
	return nil
}
