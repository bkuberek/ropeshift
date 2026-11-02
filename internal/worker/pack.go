package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// takeContext carries one read of the Context group to the control plane. The bound is the rule
// that no call stays open: with the control plane away, a lane learns that and goes on.
func (w *Worker) takeContext(ctx context.Context, lane lanes.Record, request lanes.ContextRequest) lanes.Reply {
	bounded, cancel := context.WithTimeout(ctx, askDeadline)
	defer cancel()
	found, err := w.link.LaneContext(bounded, ledger.LaneContextRequest{
		Auth: w.Auth(), Epoch: w.epoch(), WorkOrder: lane.WorkOrder, Fence: lane.Fence, Lane: lane.ID,
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

// recordBaseline tells the ledger what this generation started with, once. Zero says nothing new.
func (r *laneRun) recordBaseline(ctx context.Context, tokens int64) {
	if tokens == 0 {
		return
	}
	payload, err := json.Marshal(ledger.GenerationBaseline{Tokens: tokens})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if _, err := r.w.Record(ctx, r.w.event(ledger.UploadGenerationBaseline, r.record(), payload)); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

// setPack takes the newest version the ledger names, for the generation about to start. A control
// plane that says nothing leaves what this run already holds.
func (r *laneRun) setPack(pack *ledger.DesiredPack) {
	if pack == nil {
		return
	}
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
