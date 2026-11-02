package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// roleFile holds the orchestrator's role, which stays byte-identical across generations, so that each
// reads it from cache.
const roleFile = "orchestrator-role.md"

// digestDeadline bounds the read a worker makes on each pass, so a slow control plane costs a wake
// and holds nothing else.
const digestDeadline = 2 * time.Second

// orchestrating says this run has no work order, which only an orchestrator's has.
func (r *laneRun) orchestrating() bool { return r.lease.WorkOrder == "" }

// orchestratorLease is how an orchestrator reaches the lane code: a lease with no work order, so it
// starts, adopts, restarts, and hands off the way a lane does, and no heartbeat renews it.
func orchestratorLease(desired ledger.DesiredOrchestrator) ledger.DesiredLease {
	return ledger.DesiredLease{Lane: desired.Lane, State: string(workorders.Claimed), Resets: desired.Resets}
}

// privateDir is where an orchestrator's session runs. It has no repository and no worktree, and
// nothing it does there is work.
func (rt *LaneRuntime) privateDir(lane string) string {
	return filepath.Join(rt.configDir(lane), "session")
}

// repository says where a session starts from. An orchestrator's has no repository.
func (r *laneRun) repository(ctx context.Context) (string, error) {
	if !r.orchestrating() {
		return r.rt.Repository(ctx, r.lease.Repository)
	}
	dir := r.rt.privateDir(r.lease.Lane)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// writePrompt leaves what a session reads ahead of everything else where the driver reads it: a
// lane's pack, or an orchestrator's role, which every generation reads the same.
func (r *laneRun) writePrompt(ctx context.Context, worktree string) (string, error) {
	if !r.orchestrating() {
		return r.writePack(ctx, worktree)
	}
	path := filepath.Join(r.rt.configDir(r.lease.Lane), roleFile)
	if err := os.WriteFile(path, []byte(lanes.OrchestratorRole), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// tools is what generated settings allow this session, and what `ropeshift mcp` lists for it.
func (r *laneRun) tools() []string {
	if r.orchestrating() {
		return lanes.OrchestratorTools
	}
	return lanes.Tools
}

// role names the thresholds a session is held to.
func (r *laneRun) role() string {
	if r.orchestrating() {
		return lanes.RoleOrchestrator
	}
	return lanes.RoleWriter
}

// orchestratorLost says a session ended that nobody asked to end. A lane's clean exit can be its
// last, and an orchestrator has no last turn, so this restarts under the same breaker as a crash.
func (r *laneRun) orchestratorLost(ctx context.Context) bool {
	return r.orchestrating() && ctx.Err() == nil && !r.fenced.Load() && r.currentFlight() == nil
}

// awaitWake holds a generation until there is something to judge, because a session's first prompt
// reads a whole context, and an idle fleet spends nothing. It reports false when nobody wants this
// run any more, or this process is going away, and what it was given stays on record either way.
func (r *laneRun) awaitWake(ctx context.Context) bool {
	for {
		if r.fenced.Load() || ctx.Err() != nil {
			return false
		}
		if items, due := r.w.wakeDue(); due {
			r.setWake(items)
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(superviseEvery):
		}
	}
}

func (r *laneRun) setWake(items []ledger.WakeItem) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.wake = items
}

func (r *laneRun) pendingWake() []ledger.WakeItem {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.wake
}

// briefOrchestrator is a generation's first prompt: the wake that brought it, with what the last
// generation left.
func (r *laneRun) briefOrchestrator() string {
	var out strings.Builder
	if r.inherited != nil {
		fmt.Fprintf(&out, "You take over from generation %d.\n", r.inherited.Generation)
	}
	out.WriteString(lanes.WakeText(r.pendingWake()))
	return out.String()
}

// orchestratorRests settles a turn that ended. It has no outcome to report and nothing to be nudged
// towards, so a rest is where it belongs, and a turn that failed costs one wake.
func (r *laneRun) orchestratorRests(ctx context.Context, turn *ports.TurnResult) {
	r.w.wakeLanded(r.pendingWake())
	r.setWake(nil)
	r.letGo(ctx)
	if r.currentFlight() != nil {
		return
	}
	if turn.Status != ports.TurnCompleted {
		r.noteFailure(ctx, turn.Reason)
		return
	}
	r.contextAtRest(ctx)
}

// letGo ends what a turn took and left with no record, so the question stands free for whoever reads
// it next. A call that fails leaves the hold to run out, which is all a session that died leaves, and
// one from a run a member stopped speaks for nobody.
func (r *laneRun) letGo(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, digestDeadline)
	defer cancel()
	call := ledger.OrchestratorCall{Auth: r.w.Auth(), Epoch: r.w.epoch(), Lane: r.lease.Lane}
	if _, err := r.w.link.Orchestrate(bounded, ledger.VerbLetGo, call); err != nil && !errors.Is(err, workorders.ErrForbidden) {
		r.w.config.Report(fmt.Errorf("lane %s: letting go of what it held: %w", r.lease.Lane, err))
	}
}

// wakeDue reads the batch.
func (w *Worker) wakeDue() ([]ledger.WakeItem, bool) {
	w.wakeLock.Lock()
	defer w.wakeLock.Unlock()
	if !w.wakes.Due(w.clock.Now()) {
		return nil, false
	}
	return w.wakes.Batch(), true
}

// wakeLanded keeps that a wake reached a session, after it did.
func (w *Worker) wakeLanded(items []ledger.WakeItem) {
	if len(items) == 0 {
		return
	}
	w.wakeLock.Lock()
	defer w.wakeLock.Unlock()
	w.wakes.Sent(items, w.clock.Now())
}

// orchestratorRun finds the run of the orchestrator this worker holds, if any.
func (w *Worker) orchestratorRun() *laneRun {
	for _, run := range w.runs() {
		if run.orchestrating() {
			return run
		}
	}
	return nil
}

// tendOrchestrator stops one a member stopped, reads what there is to judge, and wakes it. A worker
// reads this on every pass, so a lost hint costs a pass. It never fails a pass: a failed orchestrator
// stops nothing that is running (FR-ORCH-4).
func (w *Worker) tendOrchestrator(ctx context.Context) {
	state, err := w.desired(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading the orchestrator asked for: %w", err))
		return
	}
	run := w.orchestratorRun()
	if run == nil {
		return
	}
	if state.Orchestrator == nil || state.Orchestrator.Lane != run.lease.Lane {
		run.stopOrchestrator(ctx)
		return
	}
	debounce, floor, known := run.state.wakeBounds()
	if !known {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, digestDeadline)
	defer cancel()
	digest, err := w.link.WakeDigest(bounded, ledger.WakeRequest{Auth: w.Auth(), Epoch: w.epoch()})
	if err != nil {
		w.config.Report(fmt.Errorf("reading what the orchestrator could judge: %w", err))
		return
	}
	now := w.clock.Now()
	w.wakeLock.Lock()
	w.wakes.Debounce, w.wakes.Floor, w.wakes.Hold = debounce, floor, run.limitHolds()
	w.wakes.Note(digest.Items, now)
	w.wakeLock.Unlock()
	run.deliverWake(ctx)
}

// limitHolds says a wake would cost a turn that fails, so an orchestrator waits where a lane would
// park. With no reset to go by, it wakes when it is the one sent to find out, and that turn's end is
// what says the limit passed.
func (r *laneRun) limitHolds() bool {
	state, now := r.w.signIn(), r.w.clock.Now().UnixMilli()
	switch {
	case state.SignedOut:
		return true
	case !state.Limited:
		return false
	case state.ResetAtMs != 0:
		return now < state.ResetAtMs
	}
	return state.Probe != r.lease.Lane || now < state.ProbeAtMs
}

// deliverWake hands a live session at rest what came due. It keeps no effect, unlike what a lane is
// told, because a wake that went twice costs one context, and one a kill lost leaves a question nobody
// reads until something else changes it. What it told lives in this process, so a restart tells again.
func (r *laneRun) deliverWake(ctx context.Context) {
	if r.sessionHandle().ID == "" || r.sessionOver.Load() || r.state.current() != lanes.Idle || r.currentFlight() != nil {
		return
	}
	items, due := r.w.wakeDue()
	if !due {
		return
	}
	if r.adopted.Load() && dropsWake() {
		r.w.wakeLanded(items)
		return
	}
	crashpoint.Hit("worker.wake", "handing")
	if _, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: lanes.WakeText(items)}); err != nil {
		if !errors.Is(err, ports.ErrAwaitingAnswer) {
			r.w.config.Report(fmt.Errorf("lane %s: a wake: %w", r.lease.Lane, err))
		}
		return
	}
	crashpoint.Hit("worker.wake", "handed")
	r.w.wakeLanded(items)
	r.signal(ctx, lanes.OnInputHanded)
}

// stopOrchestrator ends what a member stopped. A run with no session yet has nothing to stop.
func (r *laneRun) stopOrchestrator(ctx context.Context) {
	if r.fenced.Swap(true) {
		return
	}
	r.signal(ctx, lanes.OnStopRequested, lanes.OnLaneEnding)
	if r.sessionHandle().ID != "" {
		r.stop(ctx, ports.StopInterrupt)
	}
}
