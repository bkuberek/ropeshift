package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	w      *Worker
	rt     *LaneRuntime
	lease  ledger.DesiredLease
	handle ports.SessionHandle
	model  string

	profileDigest string
	version       string
	degraded      bool

	bound    bool
	nudged   bool
	decided  *workorders.Report
	cost     lanes.CostMeter
	usage    lanes.Usage
	fenced   atomic.Bool
	stopping sync.Once
}

// reconcile starts the lanes desired state names and this worker has not started. A
// lane in the journal was started here, so a restart never starts it twice, because
// spawning is not idempotent.
func (w *Worker) reconcile(ctx context.Context) {
	if w.config.Lanes == nil {
		return
	}
	state, err := w.journal.Desired(ctx)
	if errors.Is(err, ports.ErrNotFound) {
		return
	}
	if err != nil {
		w.config.Report(err)
		return
	}
	for _, lease := range state.Leases {
		if lease.Lane == "" || lease.State != string(workorders.Claimed) {
			continue
		}
		_, err := w.journal.Lane(ctx, lease.Lane)
		if err == nil {
			continue
		}
		if !errors.Is(err, ports.ErrNotFound) {
			w.config.Report(err)
			continue
		}
		w.startRun(ctx, lease)
	}
}

func (w *Worker) startRun(ctx context.Context, lease ledger.DesiredLease) {
	run := &laneRun{w: w, rt: w.config.Lanes, lease: lease}
	w.mu.Lock()
	if _, started := w.running[lease.Lane]; started {
		w.mu.Unlock()
		return
	}
	w.running[lease.Lane] = run
	w.mu.Unlock()
	w.laneGroup.Go(func() {
		defer func() {
			w.mu.Lock()
			delete(w.running, lease.Lane)
			w.mu.Unlock()
		}()
		if err := run.run(ctx); err != nil && ctx.Err() == nil {
			w.config.Report(fmt.Errorf("lane %s: %w", lease.Lane, err))
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

// recoverLanes accounts for lanes a crash left behind. A headless session dies with its
// worker until the shim holds its pipes, so a lane still marked running ended without
// an outcome of its own. A report it made before the crash stands.
func (w *Worker) recoverLanes(ctx context.Context) error {
	records, err := w.journal.Lanes(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.State != lanes.StateRunning {
			continue
		}
		w.mu.Lock()
		_, live := w.running[record.ID]
		w.mu.Unlock()
		if live {
			continue
		}
		result := workorders.LaneResult{Report: workorders.Report{
			Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonWorkerRestarted,
		}}
		if report, ok := decodeReport(record.Report); ok {
			result.Report = report
		}
		if err := w.endLane(ctx, record, result); err != nil {
			return err
		}
	}
	return nil
}

func decodeReport(raw []byte) (workorders.Report, bool) {
	var report workorders.Report
	if len(raw) == 0 || json.Unmarshal(raw, &report) != nil {
		return workorders.Report{}, false
	}
	return report, true
}

// Converge makes one pass with no loop: it accounts for what a crash left, reads
// desired state, runs what it names to the end, and uploads.
func (w *Worker) Converge(ctx context.Context) error {
	if err := w.recoverLanes(ctx); err != nil {
		return err
	}
	if err := w.Sync(ctx); err != nil {
		return err
	}
	w.reconcile(ctx)
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
		Generation: firstGeneration, State: lanes.StateRunning,
	}
}

// run takes one lane from its start to its outcome. The start is journaled first, so
// whatever happens next, the lane has an end on record.
func (r *laneRun) run(ctx context.Context) error {
	record := r.record()
	_, err := r.w.journal.BeginLane(ctx, record, r.w.event(ledger.UploadLaneStarted, record, nil))
	if err != nil {
		return err
	}
	crashpoint.Hit("worker.lane", "begun")
	if err := r.w.Upload(ctx); err != nil {
		r.w.config.Report(err)
	}
	report := r.session(ctx)
	if dir := r.tempDir(); dir != "" {
		_ = os.RemoveAll(dir)
	}
	return r.w.endLane(context.WithoutCancel(ctx), record, workorders.LaneResult{
		Report: report, CostUSD: r.cost.CostUSD, SessionTotalUSD: r.cost.SessionTotalUSD,
		UnknownCostTurns: r.cost.UnknownTurns, InputTokens: r.usage.Context(), OutputTokens: r.usage.OutputTokens,
	})
}

func failed(reason string) workorders.Report {
	return workorders.Report{Outcome: workorders.OutcomeFailed, Reason: reason}
}

func (r *laneRun) failStart(reason string, err error) workorders.Report {
	r.w.config.Report(fmt.Errorf("lane %s: %s: %w", r.lease.Lane, reason, err))
	return failed(reason)
}

func (r *laneRun) tempDir() string {
	if r.rt.RunDir == "" {
		return ""
	}
	return filepath.Join(r.rt.RunDir, "t", r.lease.Lane)
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
			lanes.EnvLaneID: r.lease.Lane, lanes.EnvGeneration: strconv.Itoa(firstGeneration),
			lanes.EnvWorkOrder: r.lease.WorkOrder, lanes.EnvLaneToken: token,
			lanes.EnvWorkerSocket: r.rt.Socket, lanes.EnvSpool: r.rt.Spool,
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
	r.handle, err = r.rt.Driver.Start(ctx, spec)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
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
		Lane: r.lease.Lane, WorkOrder: r.lease.WorkOrder, Generation: firstGeneration, Dir: prepared.Worktree.Path,
		Env: prepared.Environment, Model: r.model, PermissionMode: prepared.Profile.Agent.PermissionMode,
		Brief: lanes.Brief(lanes.BriefSpec{
			WorkOrder: r.lease.WorkOrder, Title: r.lease.Title, Statement: r.lease.Statement, Criteria: r.lease.Criteria,
		}),
		ConfigDir: filepath.Join(r.rt.StateDir, "lanes", r.lease.Lane), Binary: r.rt.Binary, Tools: lanes.Tools,
		Credential: credential, TempDir: r.tempDir(), Override: r.rt.Override,
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
		switch event.Kind {
		case ports.EventSessionBound:
			r.bind(ctx, event)
		case ports.EventTurnEnded:
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.afterTurn(ctx, event.Turn)
		case ports.EventSessionEnded:
			return r.verdict(ctx, event.End)
		}
	}
	return failed(workorders.ReasonCrashed)
}

// bind records the generation once, with the resolved model. A session's identity is
// read on every turn, and a generation's binding is one fact.
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
	if _, err := r.w.Record(ctx, bound); err != nil {
		r.w.config.Report(err)
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
		if _, err := r.rt.Driver.Send(ctx, r.handle, ports.Input{Text: lanes.Nudge}); err == nil {
			return
		}
	}
	r.decided = &workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonNoOutcome}
	r.stop(ctx, ports.StopAtRest)
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
		go func() {
			if err := r.rt.Driver.Stop(context.WithoutCancel(ctx), r.handle, how); err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: stop: %w", r.lease.Lane, err))
			}
		}()
	})
}
