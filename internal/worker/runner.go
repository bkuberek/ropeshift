package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// firstGeneration is a lane's start. Later generations arrive with restarts.
const firstGeneration = 1

// tokenBytes sizes a lane token.
const tokenBytes = 32

// LaneRuntime is what a worker needs to run lanes.
type LaneRuntime struct {
	Driver      ports.AgentDriver
	Provisioner Provisioner
	Secrets     ports.SecretResolver
	// Repository finds a repository's checkout on this machine.
	Repository func(ctx context.Context, id string) (string, error)
	// Socket and Spool are what a lane's hooks and MCP server reach.
	Socket string
	Spool  string
	// Binary is the stable path generated settings name (D18).
	Binary string
	// StateDir holds generated settings, per lane.
	StateDir string
	// RunDir holds each session's short private directory.
	RunDir string
	// Override carries a real-session suite's guard flags.
	Override lanes.SessionOverride
}

// laneRun is one lane this process is running.
type laneRun struct {
	w          *Worker
	rt         *LaneRuntime
	lease      ledger.DesiredLease
	generation int64
	handle     ports.SessionHandle
	model      string

	profileDigest string
	version       string
	degraded      bool

	bound    bool
	nudged   bool
	cursor   int64
	decided  *workorders.Report
	cost     lanes.CostMeter
	usage    lanes.Usage
	fenced   atomic.Bool
	stopping sync.Once
	stopped  sync.WaitGroup
}

// newRun is a lane's run at its first generation.
func newRun(w *Worker, lease ledger.DesiredLease) *laneRun {
	return &laneRun{w: w, rt: w.config.Lanes, lease: lease, generation: firstGeneration}
}

// startRun runs one lane apart, so one stuck lane stops no other.
func (w *Worker) startRun(ctx context.Context, run *laneRun, body func(*laneRun, context.Context) error) {
	lane := run.lease.Lane
	w.mu.Lock()
	if _, started := w.running[lane]; started {
		w.mu.Unlock()
		return
	}
	w.running[lane] = run
	w.mu.Unlock()
	w.laneGroup.Go(func() {
		defer func() {
			w.mu.Lock()
			delete(w.running, lane)
			w.mu.Unlock()
		}()
		if err := body(run, ctx); err != nil && ctx.Err() == nil {
			w.config.Report(fmt.Errorf("lane %s: %w", lane, err))
		}
	})
}

// stopFenced stops the lane of a lease this worker lost. Its outcome is rejected, and
// the rejection is kept in the journal.
func (w *Worker) stopFenced(ctx context.Context, workOrder string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, run := range w.running {
		if run.lease.WorkOrder == workOrder {
			run.fenced.Store(true)
			run.stop(ctx, ports.StopInterrupt)
		}
	}
}

func decodeReport(raw []byte) (workorders.Report, bool) {
	var report workorders.Report
	if len(raw) == 0 || json.Unmarshal(raw, &report) != nil {
		return workorders.Report{}, false
	}
	return report, true
}

// Converge makes one pass with no loop: it marks what a crash left, reads desired
// state, reconciles, runs what that started to the end, and uploads.
func (w *Worker) Converge(ctx context.Context) error {
	if err := w.markUnknown(ctx); err != nil {
		return err
	}
	if err := w.Sync(ctx); err != nil {
		return err
	}
	if _, err := w.Reconcile(ctx); err != nil {
		return err
	}
	w.laneGroup.Wait()
	return w.Upload(ctx)
}

func (w *Worker) event(eventType string, record lanes.Record, payload []byte) ledger.UploadEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return ledger.UploadEvent{
		Epoch: w.identity.Epoch, Type: eventType, WorkOrder: record.WorkOrder, ClaimID: record.ClaimID, Fence: record.Fence,
		Lane: record.ID, Generation: record.Generation, Payload: payload, OccurredAt: w.clock.Now().UnixMilli(),
	}
}

// endLane journals a lane's end and its outcome together, then uploads. A crash after
// the journal write uploads on the next start.
func (w *Worker) endLane(ctx context.Context, record lanes.Record, result workorders.LaneResult) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := w.journal.EndLane(ctx, record.ID, w.event(ledger.UploadLaneEnded, record, payload)); err != nil {
		return err
	}
	crashpoint.Hit("worker.lane", "ended")
	return w.Upload(ctx)
}

func (r *laneRun) record() lanes.Record {
	return lanes.Record{
		ID: r.lease.Lane, WorkOrder: r.lease.WorkOrder, ClaimID: r.lease.ClaimID, Fence: r.lease.Fence,
		Generation: r.generation, State: lanes.StateRunning,
	}
}

// begin takes one lane from its start to its outcome. The start is journaled first, so
// whatever happens next, the lane has an end on record.
func (r *laneRun) begin(ctx context.Context) error {
	record := r.record()
	_, err := r.w.journal.BeginLane(ctx, record, r.w.event(ledger.UploadLaneStarted, record, nil))
	if err != nil {
		return err
	}
	crashpoint.Hit("worker.lane", "begun")
	if err := r.w.Upload(ctx); err != nil {
		r.w.config.Report(err)
	}
	return r.proceed(ctx)
}

// proceed runs a lane's session to the end. A restart comes here for a lane a crash
// stopped before its session existed, and the effects on record say how far it got.
func (r *laneRun) proceed(ctx context.Context) error {
	return r.finish(ctx, r.session(ctx))
}

// finish records the end only after a stop in flight has its own end on record, so a
// clean exit leaves no effect open.
func (r *laneRun) finish(ctx context.Context, report workorders.Report) error {
	r.stopped.Wait()
	if dir := r.rt.tempDir(r.lease.Lane); dir != "" {
		_ = os.RemoveAll(dir)
	}
	return r.w.endLane(context.WithoutCancel(ctx), r.record(), workorders.LaneResult{
		Report: report, CostUSD: r.cost.CostUSD, SessionTotalUSD: r.cost.SessionTotalUSD,
		UnknownCostTurns: r.cost.UnknownTurns, InputTokens: r.usage.Context(), OutputTokens: r.usage.OutputTokens,
	})
}

// adoptedRun rebuilds a lane's run from the journal, because nothing else survived.
func (w *Worker) adoptedRun(ctx context.Context, record lanes.Record, located ports.Located) (*laneRun, error) {
	run := newRun(w, ledger.DesiredLease{
		WorkOrder: record.WorkOrder, ClaimID: record.ClaimID, Fence: record.Fence, Lane: record.ID,
	})
	run.generation, run.handle = record.Generation, located.Handle
	run.bound, run.cursor = record.SessionID != "", record.Progress.Cursor
	run.cost, run.usage = record.Progress.Cost, record.Progress.Usage
	_, err := w.journal.Effect(ctx, run.nudge().Key)
	switch {
	case err == nil:
		run.nudged = true
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	return run, nil
}

// adopt reads a session this process did not start, from where the journal says the
// last one stopped. A lane that already reported waits only for its session to end.
func (r *laneRun) adopt(ctx context.Context) error {
	err := r.rt.Driver.Adopt(ctx, r.handle, ports.AdoptFrom{Cursor: r.cursor, Cost: r.cost})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: adopting its session: %w", r.lease.Lane, err))
		return r.finish(ctx, r.unattended(ctx))
	}
	crashpoint.Hit("worker.lane", "adopted")
	events, err := r.rt.Driver.Events(ctx, r.handle)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its session: %w", r.lease.Lane, err))
		return r.finish(ctx, r.unattended(ctx))
	}
	if _, ok := r.reported(ctx); ok {
		r.stop(ctx, ports.StopAtRest)
	}
	return r.finish(ctx, r.watch(ctx, events))
}

// unattended is the outcome of a lane whose session cannot be read.
func (r *laneRun) unattended(ctx context.Context) workorders.Report {
	if report, ok := r.reported(ctx); ok {
		return report
	}
	return failed(workorders.ReasonWorkerRestarted)
}

func failed(reason string) workorders.Report {
	return workorders.Report{Outcome: workorders.OutcomeFailed, Reason: reason}
}

func (r *laneRun) failStart(reason string, err error) workorders.Report {
	r.w.config.Report(fmt.Errorf("lane %s: %s: %w", r.lease.Lane, reason, err))
	return failed(reason)
}

// session provisions and runs the session, and returns the lane's outcome. The token
// exists only in the session's environment, and the journal keeps its digest.
func (r *laneRun) session(ctx context.Context) workorders.Report {
	repository, err := r.rt.Repository(ctx, r.lease.Repository)
	if err != nil {
		return r.failStart(workorders.ReasonNoRepository, err)
	}
	token, err := newToken(tokenBytes)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	prepared, err := r.rt.Provisioner.Prepare(ctx, LaneRequest{
		Lane: r.lease.Lane, Repository: repository, Branch: r.lease.Branch,
		Tags: map[string]string{
			lanes.EnvLaneID: r.lease.Lane, lanes.EnvGeneration: strconv.FormatInt(r.generation, 10),
			lanes.EnvWorkOrder: r.lease.WorkOrder, lanes.EnvLaneToken: token,
			lanes.EnvWorkerSocket: r.rt.Socket, lanes.EnvSpool: r.rt.Spool,
		},
		Bracket: func(target string, perform func() error) (bool, error) {
			effect := lanes.NewEffect(lanes.EffectWorktree, r.lease.Lane, r.generation, target, "")
			return r.w.bracket(ctx, effect, perform)
		},
	})
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	spec, err := r.spec(ctx, prepared)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	record := r.record()
	record.TokenDigest, record.Worktree, record.Branch = lanes.TokenDigest(token), prepared.Worktree.Path, prepared.Worktree.Branch
	if err := r.w.journal.SaveLane(ctx, record); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	spawn := lanes.NewEffect(lanes.EffectSession, r.lease.Lane, r.generation, prepared.Worktree.Path, "")
	ran, err := r.w.bracket(ctx, spawn, func() error {
		var err error
		r.handle, err = r.rt.Driver.Start(ctx, spec)
		return err
	})
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if !ran {
		return failed(workorders.ReasonWorkerRestarted)
	}
	crashpoint.Hit("worker.lane", "spawned")
	events, err := r.rt.Driver.Events(ctx, r.handle)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	return r.watch(ctx, events)
}

// spec builds a session from what the worker prepared, and refuses what the driver
// cannot run before a process starts.
func (r *laneRun) spec(ctx context.Context, prepared PreparedLane) (ports.SessionSpec, error) {
	capabilities, err := r.rt.Driver.Capabilities(ctx)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	mode := prepared.Profile.RunMode
	if mode != "" && !slices.Contains(capabilities.RunModes, mode) {
		return ports.SessionSpec{}, fmt.Errorf("the driver has no run mode %q", mode)
	}
	credential, err := r.rt.Secrets.Resolve(ctx, prepared.Profile.Agent.Credentials.SecretRef)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	r.model, r.profileDigest = prepared.Profile.Agent.Model, prepared.ProfileDigest
	r.version, r.degraded = capabilities.Version, capabilities.Degraded
	spec := ports.SessionSpec{
		Lane: r.lease.Lane, WorkOrder: r.lease.WorkOrder, Generation: r.generation, Dir: prepared.Worktree.Path,
		Env: prepared.Environment, Model: r.model, PermissionMode: prepared.Profile.Agent.PermissionMode,
		Brief: lanes.Brief(lanes.BriefSpec{
			WorkOrder: r.lease.WorkOrder, Title: r.lease.Title, Statement: r.lease.Statement, Criteria: r.lease.Criteria,
		}),
		ConfigDir: r.rt.configDir(r.lease.Lane), Binary: r.rt.Binary, Tools: lanes.Tools,
		Credential: credential, TempDir: r.rt.tempDir(r.lease.Lane), Override: r.rt.Override,
	}
	if spec.TempDir != "" {
		if err := os.MkdirAll(spec.TempDir, 0o700); err != nil {
			return ports.SessionSpec{}, err
		}
	}
	report, err := r.rt.Driver.Preflight(ctx, spec)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	if len(report.Problems) != 0 {
		return ports.SessionSpec{}, fmt.Errorf("preflight: %v", report.Problems)
	}
	return spec, nil
}

// watch follows the session to its end. Lane state derives from driver events, and
// no person marks anything (FR-LANE-5).
func (r *laneRun) watch(ctx context.Context, events <-chan ports.DriverEvent) workorders.Report {
	for event := range events {
		if event.Seq != 0 && event.Seq <= r.cursor {
			continue
		}
		switch event.Kind {
		case ports.EventSessionBound:
			r.bind(ctx, event)
		case ports.EventTurnEnded:
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.afterTurn(ctx, event.Turn)
			r.saveProgress(ctx, event.Seq)
		case ports.EventSessionEnded:
			return r.verdict(ctx, event.End)
		}
	}
	return failed(workorders.ReasonCrashed)
}

// saveProgress keeps how far this run read, so a kill costs at most one redelivered
// turn, and a redelivered turn is dropped by its position.
func (r *laneRun) saveProgress(ctx context.Context, cursor int64) {
	r.cursor = cursor
	progress := lanes.Progress{Cursor: cursor, Cost: r.cost, Usage: r.usage}
	if err := r.w.journal.SaveProgress(ctx, r.lease.Lane, progress); err != nil {
		r.w.config.Report(err)
	}
}

// bind records the generation once, with the resolved model. A session's identity is
// read on every turn, and a generation's binding is one fact, even across a restart.
func (r *laneRun) bind(ctx context.Context, event ports.DriverEvent) {
	if r.bound {
		return
	}
	r.bound = true
	payload, err := json.Marshal(workorders.GenerationBinding{
		SessionID: event.SessionID, ModelRequested: r.model, ModelResolved: event.ModelResolved,
		ProfileDigest: r.profileDigest, DriverVersion: r.version, DegradedMode: r.degraded,
	})
	if err != nil {
		r.w.config.Report(err)
		return
	}
	bound := r.w.event(ledger.UploadGenerationBound, r.record(), payload)
	first, err := r.w.journal.BindGeneration(ctx, r.lease.Lane, event.SessionID, bound)
	if err != nil {
		r.w.config.Report(err)
		return
	}
	if !first {
		return
	}
	if err := r.w.Upload(ctx); err != nil {
		r.w.config.Report(err)
	}
}

// afterTurn decides what an idle lane does: stop if it reported, and nudge it once if
// it did not (FR-LANE-2). A turn that failed has nothing to nudge.
func (r *laneRun) afterTurn(ctx context.Context, turn *ports.TurnResult) {
	if _, ok := r.reported(ctx); ok {
		r.stop(ctx, ports.StopAtRest)
		return
	}
	if turn.Status != ports.TurnCompleted {
		report := failed(turn.Reason)
		r.decided = &report
		r.stop(ctx, ports.StopAtRest)
		return
	}
	if !r.nudged {
		r.nudged = true
		ran, err := r.w.bracket(ctx, r.nudge(), func() error {
			_, err := r.rt.Driver.Send(ctx, r.handle, ports.Input{Text: lanes.Nudge})
			return err
		})
		if ran && err == nil {
			return
		}
	}
	r.decided = &workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonNoOutcome}
	r.stop(ctx, ports.StopAtRest)
}

// nudge is the one input a worker sends a lane on its own.
func (r *laneRun) nudge() lanes.Effect {
	return lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.generation, "", "nudge")
}

func (r *laneRun) reported(ctx context.Context) (workorders.Report, bool) {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(err)
		return workorders.Report{}, false
	}
	return decodeReport(record.Report)
}

// verdict settles the outcome when the session is gone. A session that died with no
// result for its input crashed, even if it had reported nothing before it.
func (r *laneRun) verdict(ctx context.Context, end *ports.SessionEnd) workorders.Report {
	if r.fenced.Load() {
		return failed(workorders.ReasonFenced)
	}
	if report, ok := r.reported(ctx); ok {
		return report
	}
	if r.decided != nil {
		return *r.decided
	}
	if end != nil && end.Crashed {
		return failed(workorders.ReasonCrashed)
	}
	return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonNoOutcome}
}

// stop runs apart from the event loop, because a stop waits for the session to end and
// the loop must keep reading its stream.
func (r *laneRun) stop(ctx context.Context, how ports.StopMode) {
	r.stopping.Do(func() {
		r.stopped.Go(func() {
			ctx := context.WithoutCancel(ctx)
			effect := lanes.NewEffect(lanes.EffectStop, r.lease.Lane, r.generation, "", "")
			_, err := r.w.bracket(ctx, effect, func() error { return r.rt.Driver.Stop(ctx, r.handle, how) })
			if err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: stop: %w", r.lease.Lane, err))
			}
		})
	})
}
