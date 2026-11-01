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
	"time"

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
	// Ports is the pool this worker hands out. Zero takes the default.
	Ports lanes.PortPool
	// StateDir holds generated settings, per lane.
	StateDir string
	// RunDir holds each session's short private directory.
	RunDir string
	// Machine reads what this machine has free. Nil leaves the floors unchecked.
	Machine ports.Machine
	// Override carries a real-session suite's guard flags.
	Override lanes.SessionOverride
}

// laneRun is one lane this process is running.
type laneRun struct {
	w          *Worker
	rt         *LaneRuntime
	lease      ledger.DesiredLease
	generation int64
	model      string

	// handleLock guards the generation and the handle, because a heartbeat and a hook read
	// them while a start writes them.
	handleLock sync.Mutex
	handle     ports.SessionHandle

	profileDigest string
	version       string
	degraded      bool

	bound   bool
	nudged  bool
	cursor  int64
	decided *workorders.Report
	cost    lanes.CostMeter
	usage   lanes.Usage
	fenced  atomic.Bool
	// stopOnce holds to one stop per generation, and the handle's lock guards it.
	stopOnce *sync.Once
	stopped  sync.WaitGroup
	state    *laneState
	// crashed says the session that just ended died on its own.
	crashed bool
	// resumeFrom is the session a restarted generation continues. Empty starts fresh.
	resumeFrom string
	// priorUSD and priorUnknown are what earlier generations cost.
	priorUSD     float64
	priorUnknown int64
}

func newOnce() *sync.Once { return &sync.Once{} }

// newRun is a lane's run at its first generation.
func newRun(w *Worker, lease ledger.DesiredLease) *laneRun {
	return &laneRun{
		w: w, rt: w.config.Lanes, lease: lease, generation: firstGeneration, stopOnce: newOnce(),
		state: newLaneState(lanes.Pending, w.clock.Now()),
	}
}

// resumedRun is a run that goes on from a record, for a lane a crash stopped before its
// session. A later generation keeps what the earlier ones left.
func (w *Worker) resumedRun(lease ledger.DesiredLease, record lanes.Record) *laneRun {
	run := newRun(w, lease)
	run.generation, run.resumeFrom = record.Generation, record.ResumeSession
	run.cost, run.usage = record.Progress.Cost, record.Progress.Usage
	run.priorUSD, run.priorUnknown = record.Progress.PriorUSD, record.Progress.PriorUnknown
	if record.Actual != "" {
		run.state = newLaneState(record.Actual, w.clock.Now())
	}
	return run
}

// sessionHandle returns the current session, which is empty before a start.
func (r *laneRun) sessionHandle() ports.SessionHandle {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.handle
}

// currentGeneration is which generation this run follows now.
func (r *laneRun) currentGeneration() int64 {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.generation
}

func (r *laneRun) setHandle(handle ports.SessionHandle) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.handle = handle
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
			run.signal(ctx, lanes.OnStopRequested, lanes.OnLaneEnding)
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
func (w *Worker) endLane(ctx context.Context, record lanes.Record, result workorders.LaneResult, fenced bool) error {
	if err := w.finalGuard(ctx, record, fenced); err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", record.ID, err))
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := w.journal.EndLane(ctx, record.ID, w.event(ledger.UploadLaneEnded, record, payload)); err != nil {
		return err
	}
	crashpoint.Hit("worker.lane", "ended")
	if err := w.release(ctx, record.ID); err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", record.ID, err))
	}
	return w.Upload(ctx)
}

func (r *laneRun) record() lanes.Record {
	return lanes.Record{
		ID: r.lease.Lane, WorkOrder: r.lease.WorkOrder, ClaimID: r.lease.ClaimID, Fence: r.lease.Fence,
		Generation: r.currentGeneration(), State: lanes.StateRunning,
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
	r.signal(ctx, lanes.OnClaimGranted)
	return r.proceed(ctx)
}

// proceed runs a lane's session to the end. A restart comes here for a lane a crash
// stopped before its session existed, and the effects on record say how far it got.
func (r *laneRun) proceed(ctx context.Context) error {
	return r.conclude(ctx, r.session(ctx))
}

// conclude supervises what a session left, and ends the lane once it has an outcome.
func (r *laneRun) conclude(ctx context.Context, report workorders.Report) error {
	report, over := r.supervised(ctx, report)
	if !over {
		return nil
	}
	return r.finish(ctx, report)
}

// finish records the end only after a stop in flight has its own end on record, so a
// clean exit leaves no effect open.
func (r *laneRun) finish(ctx context.Context, report workorders.Report) error {
	r.stopped.Wait()
	r.signal(ctx, lanes.OnLaneEnding, lanes.OnSessionStopped)
	if dir := r.rt.tempDir(r.lease.Lane); dir != "" {
		_ = os.RemoveAll(dir)
	}
	return r.w.endLane(context.WithoutCancel(ctx), r.record(), workorders.LaneResult{
		Report: report, CostUSD: r.priorUSD + r.cost.CostUSD, SessionTotalUSD: r.cost.SessionTotalUSD,
		UnknownCostTurns: r.priorUnknown + r.cost.UnknownTurns, InputTokens: r.usage.Context(), OutputTokens: r.usage.OutputTokens,
	}, r.fenced.Load())
}

// adoptedRun rebuilds a lane's run from the journal, because nothing else survived.
func (w *Worker) adoptedRun(ctx context.Context, record lanes.Record, located ports.Located, lease ledger.DesiredLease) (*laneRun, error) {
	lease.WorkOrder, lease.ClaimID, lease.Fence, lease.Lane = record.WorkOrder, record.ClaimID, record.Fence, record.ID
	run := w.resumedRun(lease, record)
	run.setHandle(located.Handle)
	run.bound, run.cursor = record.SessionID != "", record.Progress.Cursor
	run.state = newLaneState(record.Actual, w.clock.Now())
	run.loadProfile(ctx)
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
	hooks, err := r.w.journal.HookEvents(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its hooks: %w", r.lease.Lane, err))
	}
	for _, record := range hooks {
		if record.Generation == r.currentGeneration() {
			r.observeHook(ctx, record.HookEvent)
		}
	}
	err = r.rt.Driver.Adopt(ctx, r.sessionHandle(), ports.AdoptFrom{
		Spec: r.w.sessionSpec(r.record()), Cursor: r.cursor, Cost: r.cost, Hooks: hooks,
	})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: adopting its session: %w", r.lease.Lane, err))
		return r.finish(ctx, r.unattended(ctx))
	}
	crashpoint.Hit("worker.lane", "adopted")
	r.signal(ctx, lanes.OnDriverReady)
	events, err := r.rt.Driver.Events(ctx, r.sessionHandle())
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its session: %w", r.lease.Lane, err))
		return r.finish(ctx, r.unattended(ctx))
	}
	if _, ok := r.reported(ctx); ok {
		r.stop(ctx, ports.StopAtRest)
	}
	return r.conclude(ctx, r.watch(ctx, events))
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
		Lane: r.lease.Lane, Repository: repository, Branch: r.lease.Branch, Allocate: r.allocate, RunHook: r.runHook,
		Tags: map[string]string{
			lanes.EnvLaneID: r.lease.Lane, lanes.EnvGeneration: strconv.FormatInt(r.currentGeneration(), 10),
			lanes.EnvWorkOrder: r.lease.WorkOrder, lanes.EnvLaneToken: token,
			lanes.EnvWorkerSocket: r.rt.Socket, lanes.EnvSpool: r.rt.Spool,
		},
		Bracket: func(target string, perform func() error) (bool, error) {
			effect := lanes.NewEffect(lanes.EffectWorktree, r.lease.Lane, r.currentGeneration(), target, "")
			return r.w.bracket(ctx, effect, perform)
		},
	})
	if errors.Is(err, errHookUnknown) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonHookUnknown}
	}
	if errors.Is(err, ErrHookFailed) {
		return r.failStart(workorders.ReasonHookFailed, err)
	}
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	r.signal(ctx, lanes.OnResourcesReady)
	r.state.thresholds(prepared.Profile)
	spec, err := r.spec(ctx, prepared)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	record := r.record()
	record.TokenDigest, record.Worktree, record.Branch = lanes.TokenDigest(token), prepared.Worktree.Path, prepared.Worktree.Branch
	if err := r.w.journal.SaveLane(ctx, record); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	spawn := lanes.NewEffect(lanes.EffectSession, r.lease.Lane, r.currentGeneration(), prepared.Worktree.Path, "")
	ran, err := r.w.bracket(ctx, spawn, func() error {
		handle, err := r.launch(ctx, spec)
		r.setHandle(handle)
		return err
	})
	if errors.Is(err, ports.ErrUnknownScreen) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonUnknownScreen}
	}
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if !ran {
		return failed(workorders.ReasonWorkerRestarted)
	}
	crashpoint.Hit("worker.lane", "spawned")
	r.signal(ctx, lanes.OnDriverReady)
	events, err := r.rt.Driver.Events(ctx, r.sessionHandle())
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	return r.watch(ctx, events)
}

// launch starts a generation. A restart continues the session it replaces, and one that
// has none, or a resume that never reached a ready state, starts from the brief.
func (r *laneRun) launch(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if r.resumeFrom == "" {
		return r.rt.Driver.Start(ctx, spec)
	}
	resumed := spec
	resumed.Brief = lanes.Restarted
	handle, err := r.rt.Driver.Resume(ctx, resumed, ports.ResumeRef{SessionID: r.resumeFrom, SessionTotalUSD: r.cost.SessionTotalUSD})
	if errors.Is(err, ports.ErrResumeNotReady) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return r.rt.Driver.Start(ctx, spec)
	}
	return handle, err
}

// spec builds a session from what the worker prepared, and refuses what the driver
// cannot run before a process starts.
func (r *laneRun) spec(ctx context.Context, prepared PreparedLane) (ports.SessionSpec, error) {
	capabilities, err := r.rt.Driver.Capabilities(ctx)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	mode, err := runMode(prepared.Profile.RunMode)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	if !slices.Contains(capabilities.RunModes, mode) {
		return ports.SessionSpec{}, fmt.Errorf("the driver has no run mode %q", mode)
	}
	credential, err := r.rt.Secrets.Resolve(ctx, prepared.Profile.Agent.Credentials.SecretRef)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	r.model, r.profileDigest = prepared.Profile.Agent.Model, prepared.ProfileDigest
	r.version, r.degraded = capabilities.Version, capabilities.Degraded
	r.state.mu.Lock()
	r.state.degraded = capabilities.Degraded
	r.state.mu.Unlock()
	spec := ports.SessionSpec{
		Lane: r.lease.Lane, WorkOrder: r.lease.WorkOrder, Generation: r.currentGeneration(), Dir: prepared.Worktree.Path,
		Env: prepared.Environment, Model: r.model, PermissionMode: prepared.Profile.Agent.PermissionMode,
		Brief:     r.briefText(),
		ConfigDir: r.rt.configDir(r.lease.Lane), Binary: r.rt.Binary, Tools: lanes.Tools,
		Credential: credential, TempDir: r.rt.tempDir(r.lease.Lane), Override: r.rt.Override,
		RunMode: mode, ReadyTimeout: readyTimeout(prepared.Profile), DefaultBranch: prepared.DefaultBranch,
		Gates: prepared.Profile.Agent.Gates,
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

// runMode reads a profile's value in the driver's terms.
func runMode(value string) (string, error) {
	switch value {
	case lanes.ProfileRunTmux:
		return ports.RunModeInteractive, nil
	case lanes.ProfileRunHeadless, "":
		return ports.RunModeHeadless, nil
	}
	return "", fmt.Errorf("%w: run mode %q", lanes.ErrBadProfile, value)
}

// readyTimeout reads a profile's value. One that does not parse takes the driver's own,
// because a bad profile value must not stop a lane.
func readyTimeout(profile lanes.Profile) time.Duration {
	timeout, _ := time.ParseDuration(profile.Lifecycle.ReadyTimeout)
	return timeout
}

// watch follows the session to its end. Lane state derives from driver events, and
// no person marks anything (FR-LANE-5).
func (r *laneRun) watch(ctx context.Context, events <-chan ports.DriverEvent) workorders.Report {
	for event := range events {
		if event.Seq != 0 && event.Seq <= r.cursor {
			continue
		}
		r.observeEvent(ctx, event)
		switch event.Kind {
		case ports.EventSessionBound:
			r.bind(ctx, event)
		case ports.EventTurnEnded:
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.afterTurn(ctx, event.Turn)
			r.saveProgress(ctx, event.Seq)
		case ports.EventSessionEnded:
			if event.End != nil && event.End.Crashed {
				r.crashed = true
				r.signal(ctx, lanes.OnActivity, lanes.OnSessionGone)
			}
			return r.verdict(ctx, event.End)
		}
	}
	return failed(workorders.ReasonCrashed)
}

// saveProgress keeps how far this run read, so a kill costs at most one redelivered
// turn, and a redelivered turn is dropped by its position.
func (r *laneRun) saveProgress(ctx context.Context, cursor int64) {
	r.cursor = cursor
	progress := lanes.Progress{Cursor: cursor, Cost: r.cost, Usage: r.usage, PriorUSD: r.priorUSD, PriorUnknown: r.priorUnknown}
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
		r.signal(ctx, lanes.OnOutcomeReported)
		r.stop(ctx, ports.StopAtRest)
		return
	}
	if turn.Status != ports.TurnCompleted {
		report := failed(turn.Reason)
		r.decided = &report
		r.signal(ctx, lanes.OnLaneEnding)
		r.stop(ctx, ports.StopAtRest)
		return
	}
	if !r.nudged {
		r.nudged = true
		ran, err := r.w.bracket(ctx, r.nudge(), func() error {
			_, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: lanes.Nudge})
			return err
		})
		if ran && err == nil {
			r.signal(ctx, lanes.OnInputHanded)
			return
		}
	}
	r.decided = &workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonNoOutcome}
	r.signal(ctx, lanes.OnOutcomeReported)
	r.stop(ctx, ports.StopAtRest)
}

// nudge is the one input a worker sends a lane on its own.
func (r *laneRun) nudge() lanes.Effect {
	return lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.currentGeneration(), "", "nudge")
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
	r.handleLock.Lock()
	once := r.stopOnce
	r.handleLock.Unlock()
	once.Do(func() {
		r.stopped.Go(func() {
			ctx := context.WithoutCancel(ctx)
			r.guardBeforeStop(ctx)
			effect := lanes.NewEffect(lanes.EffectStop, r.lease.Lane, r.currentGeneration(), "", "")
			_, err := r.w.bracket(ctx, effect, func() error { return r.rt.Driver.Stop(ctx, r.sessionHandle(), how) })
			if err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: stop: %w", r.lease.Lane, err))
			}
		})
	})
}
