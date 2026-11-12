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

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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
	// Repository finds this machine's checkout of a project's repository. An empty project names the
	// plane's only project, as a plane of one project sends it.
	Repository func(ctx context.Context, project, id string) (string, error)
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
	// Telemetry is where each session's telemetry goes. Empty sends none.
	Telemetry LaneTelemetry
	// Inventory reads what each repository holds for a lane whose lease asks for an inventory before it
	// starts (ADR-0023). Nil takes none, and such a lane does not start.
	Inventory ports.Inventory
	// Forge runs what the control plane asks of a forge: pushes, pull requests, comments, and reads of the
	// sign-in's health (ADR-0020). Nil runs none, and a repository that reviews through pull requests waits.
	Forge ports.Forge
	// ForgeEvery is how often the forge's sign-in is read for each repository, and how long a failed forge
	// request waits before its next attempt. Zero takes a minute.
	ForgeEvery time.Duration
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
	// settings are what this lane runs on, which its experience record holds (FR-EXP-2).
	settings measures.Settings
	// worktree is where the lane works, and reads are the read-only worktrees of the other repositories it
	// reads beside it (ADR-0022). The handle's lock guards both.
	worktree string
	reads    []lanes.ReadTree
	// maxReviewRounds is the bound on review rounds this lane's profile set, and zero when it set none.
	maxReviewRounds int64

	bound   bool
	nudged  atomic.Bool
	cursor  int64
	decided *workorders.Report
	cost    lanes.CostMeter
	usage   lanes.Usage
	fenced  atomic.Bool
	// stopAsked says this run heard its lease is gone.
	stopAsked atomic.Bool
	// reached says the driver holds this generation's session, so a stop can find it.
	reached atomic.Bool
	// stopOnce holds to one stop per generation, and the handle's lock guards it.
	stopOnce *sync.Once
	stopped  sync.WaitGroup
	state    *laneState
	// crashed says the session that just ended died on its own, and crashedAt when. That
	// time is read before the lane says `backoff`, so anyone who reads that state and looks
	// at a clock looks past the moment the delay counts from.
	crashed   bool
	crashedAt time.Time
	// sessionOver says this generation's session has ended, which a handoff waits on from
	// beside the goroutine that reads the stream.
	sessionOver atomic.Bool
	// resumeFrom is the session a restarted generation continues. Empty starts fresh.
	resumeFrom string
	// startedMode is the run mode of the session this lane last started.
	startedMode string
	// priorUSD and priorUnknown are what earlier generations cost.
	priorUSD     float64
	priorUnknown int64
	// resolved is the model the session said it runs, and handleLock guards it.
	resolved string
	// life is the context the run lives under, which outlasts whoever asked for a handoff.
	life context.Context
	// flight is the handoff this generation is following, sequence holds its goroutine, and
	// left is what a journal held of one when this run began.
	flightLock sync.Mutex
	flight     *handoffFlight
	sequence   sync.WaitGroup
	left       *lanes.HandoffProgress
	// inherited is the document this generation follows.
	inherited *lanes.Handoff
	// parking is the sequence this generation follows, and flightLock guards it.
	parking *lanes.ParkProgress
	// instruction is what the session was told and has not finished, blocking the questions it
	// cannot go on without, and drainingSince when a draining fleet first reached it. The
	// handle's lock guards all three.
	instruction   string
	blocking      []string
	drainingSince time.Time
	// waiting says a goroutine holds this lane for `ask_wait`.
	waiting atomic.Bool
	// process is the generation whose process runs now, which a clear does not move. The handle's
	// lock guards it.
	process int64
	// respawnAsked is the request this run is acting on, as the ledger counts them.
	respawnAsked atomic.Int64
	// sending says a goroutine is handing the ledger what this lane raised.
	sending atomic.Bool
	// approving says a goroutine holds this lane for `approval_wait`.
	approving atomic.Bool
	// owesBrief says a clear left this generation's session with no idea of its work.
	owesBrief atomic.Bool
	// fellBack says this generation meant to resume and started from its handoff. The handle's lock
	// guards it, because a clear can bind a generation from a hook's goroutine.
	fellBack bool
	// facts are what this generation's first prompt owes, and carried names them, which is all a
	// restarted worker keeps. The handle's lock guards both.
	facts   []ledger.MessageFact
	carried []string
	// pack is the newest version this run has heard of, and packRead the one this generation
	// holds, which a later version does not change. The handle's lock guards both.
	pack     *ledger.DesiredPack
	packRead *lanes.PackRef
	// wake is what an orchestrator's session was started on, which counts as told once its turn ends,
	// and the handle's lock guards it.
	wake []ledger.WakeItem
	// adopted says this run took over a session an earlier process left.
	adopted atomic.Bool
}

func newOnce() *sync.Once { return &sync.Once{} }

// newRun is a lane's run at its first generation.
func newRun(w *Worker, lease ledger.DesiredLease) *laneRun {
	return &laneRun{
		w: w, rt: w.config.Lanes, lease: lease, generation: firstGeneration, stopOnce: newOnce(),
		state: newLaneState(lanes.Pending, w.clock.Now()), pack: lease.Pack,
	}
}

// resumedRun is a run that goes on from a record, for a lane a crash stopped before its
// session. A later generation keeps what the earlier ones left.
func (w *Worker) resumedRun(lease ledger.DesiredLease, record lanes.Record) *laneRun {
	run := newRun(w, lease)
	run.generation, run.resumeFrom, run.startedMode = record.Generation, record.ResumeSession, record.RunMode
	run.cost, run.usage = record.Progress.Cost, record.Progress.Usage
	run.priorUSD, run.priorUnknown = record.Progress.PriorUSD, record.Progress.PriorUnknown
	if record.Actual != "" {
		run.state = laneStateOf(record, w.clock.Now())
	}
	run.left, run.parking = record.Handoff, record.Parking
	run.worktree = record.Worktree
	run.instruction, run.blocking, run.carried = record.Progress.Instruction, record.Blocking, record.Progress.Carried
	run.packRead, run.process = record.Progress.Pack, record.Process
	run.owesBrief.Store(record.Progress.OwesBrief)
	run.state.keepContext(record.Progress)
	run.inheritedFrom(record.Handoff)
	return run
}

// replayHooks reads what a session's hooks said while no worker listened. Each compaction merges
// into one record of each hook, so only their last times say whether one is still under way. A
// call's end reads after every start, because two hooks in one millisecond sort by name.
func (r *laneRun) replayHooks(ctx context.Context, hooks []lanes.HookRecord) {
	generation := r.currentGeneration()
	var began, ended int64
	var calls []lanes.HookRecord
	for _, record := range hooks {
		if record.Generation != generation {
			continue
		}
		switch record.Event {
		case hookPreCompact:
			began = record.LastMs
		case hookPostCompact:
			ended = record.LastMs
		case hookPostToolUse, lanes.EventToolFailed, lanes.EventToolDenied, lanes.EventCallDenied:
			calls = append(calls, record)
		default:
			r.observeHook(ctx, record.HookEvent)
		}
	}
	for _, record := range calls {
		r.observeHook(ctx, record.HookEvent)
	}
	switch {
	case began > ended:
		r.signal(ctx, lanes.OnCompactionStarted)
	case began != 0 || ended != 0:
		r.signal(ctx, lanes.OnCompactionFinished)
	}
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

func (r *laneRun) resolvedModel() string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.resolved
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
	run.life = ctx
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
// the rejection is kept in the journal. A lane that came back under a later claim on the same
// work order lost nothing, so the fence has to match.
func (w *Worker) stopFenced(ctx context.Context, lost ledger.FencedLease) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, run := range w.running {
		if run.lease.Project == lost.Project && run.lease.WorkOrder == lost.WorkOrder && run.lease.Fence == lost.Fence {
			run.lost(ctx)
		}
	}
}

// lost ends a lane that no longer holds its lease. A run with no session yet starts none, and one
// that races this sees its session stopped on the next pass.
func (r *laneRun) lost(ctx context.Context) {
	r.fenced.Store(true)
	if !r.stopAsked.Swap(true) {
		r.signal(ctx, lanes.OnStopRequested, lanes.OnLaneEnding)
	}
	if r.reached.Load() && !r.sessionOver.Load() {
		r.stop(ctx, ports.StopInterrupt)
	}
}

// reach says the driver holds this generation's session. A stop asked of a session it does not hold
// yet fails, and would spend the generation's one stop, so a lane that lost its lease before this is
// stopped here. lost sets what this reads before it reads what this sets, so one of them stops it.
func (r *laneRun) reach(ctx context.Context) {
	r.reached.Store(true)
	if r.stopAsked.Load() && !r.sessionOver.Load() {
		r.stop(ctx, ports.StopInterrupt)
	}
}

// stopLost stops each lane whose claim desired state stopped naming, which is how a canceled or
// reassigned lane hears. A beat only presents the leases the last read named, so a read that follows
// a hint takes the lost lease out of the very beat that would have said so. A lane that ended or
// parked wrote that to its journal before the ledger heard, so it is no loss.
func (w *Worker) stopLost(ctx context.Context) {
	state, err := w.desired(ctx)
	if err != nil || state.Rev == 0 || keepsLostLanes() {
		return
	}
	named := map[string]bool{}
	for _, lease := range state.Leases {
		named[leaseKey(lease.Project, lease.ClaimID)] = true
	}
	w.mu.Lock()
	runs := make([]*laneRun, 0, len(w.running))
	for _, run := range w.running {
		runs = append(runs, run)
	}
	w.mu.Unlock()
	for _, run := range runs {
		if run.orchestrating() || named[leaseKey(run.lease.Project, run.lease.ClaimID)] {
			continue
		}
		record, err := w.journal.Lane(ctx, run.lease.Lane)
		if err == nil && record.State == lanes.StateRunning {
			run.lost(ctx)
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
		Epoch: w.identity.Epoch, Project: record.Project, Type: eventType, WorkOrder: record.WorkOrder, ClaimID: record.ClaimID, Fence: record.Fence,
		Lane: record.ID, Generation: record.Generation, Payload: payload, OccurredAt: w.clock.Now().UnixMilli(),
	}
}

// endLane journals a lane's end and its outcome together, then uploads. A crash after
// the journal write uploads on the next start.
func (w *Worker) endLane(ctx context.Context, record lanes.Record, result workorders.LaneResult, fenced bool) error {
	if err := w.finalGuard(ctx, record, fenced); err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", record.ID, err))
	}
	result.Branch, result.Head = w.finalBranch(ctx, record)
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
	record := lanes.Record{
		ID: r.lease.Lane, Project: r.lease.Project, WorkOrder: r.lease.WorkOrder, ClaimID: r.lease.ClaimID, Fence: r.lease.Fence,
		Generation: r.currentGeneration(), State: lanes.StateRunning, Process: r.processGeneration(),
	}
	if r.orchestrating() {
		record.Owner = lanes.OwnerOrchestrator
	}
	return record
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
	if r.orchestrating() {
		r.loadProfile(ctx)
	}
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
	r.sequence.Wait()
	r.stopped.Wait()
	r.signal(ctx, lanes.OnLaneEnding, lanes.OnSessionStopped)
	if dir := r.rt.tempDir(r.lease.Lane); dir != "" {
		_ = os.RemoveAll(dir)
	}
	r.discardReads(context.WithoutCancel(ctx))
	return r.w.endLane(context.WithoutCancel(ctx), r.record(), workorders.LaneResult{
		Report: report, CostUSD: r.priorUSD + r.cost.CostUSD, SessionTotalUSD: r.cost.SessionTotalUSD,
		UnknownCostTurns: r.priorUnknown + r.cost.UnknownTurns, InputTokens: r.usage.Context(), OutputTokens: r.usage.OutputTokens,
		Settings: r.endSettings(), MaxReviewRounds: r.reviewBound(),
	}, r.fenced.Load())
}

// adoptedRun rebuilds a lane's run from the journal, because nothing else survived.
func (w *Worker) adoptedRun(ctx context.Context, record lanes.Record, located ports.Located, lease ledger.DesiredLease) (*laneRun, error) {
	lease.WorkOrder, lease.ClaimID, lease.Fence, lease.Lane = record.WorkOrder, record.ClaimID, record.Fence, record.ID
	run := w.resumedRun(lease, record)
	run.fenced.Store(w.leaseGone(ctx, record))
	run.setHandle(located.Handle)
	run.bound, run.cursor = record.SessionID != "", record.Progress.Cursor
	run.state = laneStateOf(record, w.clock.Now())
	run.noteProcess(located.Handle)
	run.state.keepContext(record.Progress)
	run.inheritedFrom(record.Handoff)
	run.loadProfile(ctx)
	if capabilities, err := w.config.Lanes.Driver.Capabilities(ctx); err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", record.ID, err))
	} else {
		run.state.noteCapabilities(capabilities)
	}
	_, err := w.journal.Effect(ctx, run.nudge().Key)
	switch {
	case err == nil:
		run.nudged.Store(true)
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	return run, nil
}

// adopt reads a session this process did not start, from where the journal says the
// last one stopped. A lane that already reported waits only for its session to end.
func (r *laneRun) adopt(ctx context.Context) error {
	r.adopted.Store(true)
	hooks, err := r.w.journal.HookEvents(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its hooks: %w", r.lease.Lane, err))
	}
	r.replayHooks(ctx, hooks)
	r.resumeApprovals(ctx)
	err = r.rt.Driver.Adopt(ctx, r.sessionHandle(), ports.AdoptFrom{
		Spec: r.w.sessionSpec(r.record()), Cursor: r.cursor, Cost: r.cost, Hooks: hooks,
	})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: adopting its session: %w", r.lease.Lane, err))
		return r.unread(ctx)
	}
	crashpoint.Hit("worker.lane", "adopted")
	r.reach(ctx)
	r.signal(ctx, lanes.OnDriverReady)
	events, err := r.rt.Driver.Events(ctx, r.sessionHandle())
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its session: %w", r.lease.Lane, err))
		return r.unread(ctx)
	}
	if _, ok := r.reported(ctx); ok {
		r.stop(ctx, ports.StopAtRest)
	}
	r.resumeParking(ctx)
	r.resumeHandoff(ctx)
	if len(r.blockingQuestions()) != 0 && r.state.current() == lanes.Idle {
		r.awaitAnswer(ctx)
	}
	return r.conclude(ctx, r.watch(ctx, events))
}

// unread settles a lane whose session cannot be read. Parking and a handoff still finish,
// because those are the failures with somewhere to go.
func (r *laneRun) unread(ctx context.Context) error {
	if r.parkingOf().Open() {
		return r.finishParking(ctx)
	}
	if r.left.Open() {
		return r.finishHandoff(ctx)
	}
	return r.finish(ctx, r.unattended(ctx))
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
	if r.orchestrating() && !r.awaitWake(ctx) {
		return failed(workorders.ReasonWorkerRestarted)
	}
	if err := r.w.awaitQualified(ctx, r.lease); err != nil {
		return failed(workorders.ReasonWorkerRestarted)
	}
	repository, err := r.repository(ctx)
	if err != nil {
		return r.failStart(workorders.ReasonNoRepository, err)
	}
	token, err := newToken(tokenBytes)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if err := r.judgeable(); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	prepared, err := r.rt.Provisioner.Prepare(ctx, LaneRequest{
		Lane: r.lease.Lane, Repository: repository, Branch: r.lease.Branch, Base: r.begins(), Overrides: r.lease.Overrides, Fetch: r.fetchNames(), Private: r.orchestrating(),
		Detached: r.reviewing(), ForgeConfigDir: r.rt.forgeConfigDir(r.lease.Lane),
		Allocate: func(settings lanes.ResourceSettings) (lanes.Allocation, error) { return r.allocate(ctx, settings) },
		RunHook:  func(target string, perform func() error) error { return r.runHook(ctx, target, perform) },
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
	r.setWorktreePath(prepared.Worktree.Path)
	r.setReads(r.prepareReads(ctx, prepared.Worktree.Path))
	if err := r.writeInventory(ctx, repository, prepared.Worktree.Path); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	r.state.thresholds(prepared.Profile, r.role())
	r.readFacts(ctx)
	capabilities, err := r.rt.Driver.Capabilities(ctx)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	packFile, err := r.writePrompt(ctx, prepared.Worktree.Path, capabilities.Support())
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if err := r.writeReview(ctx, repository, prepared.Worktree.Path); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	spec, err := r.spec(ctx, prepared, repository, packFile, capabilities)
	if errors.Is(err, errNoInteractive) {
		// Another attempt finds the same machine, so the work waits for a person to fix it.
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonNoInteractiveMode}
	}
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	record := r.record()
	record.TokenDigest, record.Worktree, record.Branch = lanes.TokenDigest(token), prepared.Worktree.Path, prepared.Worktree.Branch
	// Setting a lane up can outlast its lease, and the worktree it left is what the guard reads.
	if r.fenced.Load() {
		if err := r.w.journal.SaveLane(ctx, record); err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		}
		return failed(workorders.ReasonFenced)
	}
	record.RunMode, record.Process = spec.RunMode, record.Generation
	r.setProcess(record.Generation)
	if err := r.w.journal.SaveLane(ctx, record); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if r.resumeFrom == "" {
		r.setInstruction(spec.Brief)
	}
	// Before the process, so a kill right after it leaves a lane able to answer what it was told,
	// and able to say which pack it read.
	if len(r.carriedIDs()) != 0 || r.readPack() != nil {
		r.saveProgress(ctx, r.cursor)
	}
	spawn := lanes.NewEffect(lanes.EffectSession, r.lease.Lane, r.currentGeneration(), prepared.Worktree.Path, "")
	ran, err := r.w.bracket(ctx, spawn, func() error {
		handle, err := r.launch(ctx, spec)
		r.setHandle(handle)
		return err
	})
	if errors.Is(err, ports.ErrUnknownScreen) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return r.parkOnScreen(ctx)
	}
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if !ran {
		return failed(workorders.ReasonWorkerRestarted)
	}
	crashpoint.Hit("worker.lane", "spawned")
	r.reach(ctx)
	r.noteProcess(r.sessionHandle())
	if r.woken() != nil {
		crashpoint.Hit("worker.resume", "launched")
	}
	r.saveProgress(ctx, r.cursor)
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
	if r.woken() != nil {
		crashpoint.Hit("worker.resume", "launching")
	}
	if r.resumeFrom == "" {
		return r.rt.Driver.Start(ctx, spec)
	}
	resumed := spec
	resumed.Brief = r.resumePrompt()
	if r.woken() != nil {
		r.setInstruction(resumed.Brief)
	}
	handle, err := r.rt.Driver.Resume(ctx, resumed, ports.ResumeRef{SessionID: r.resumeFrom, SessionTotalUSD: r.cost.SessionTotalUSD})
	if errors.Is(err, ports.ErrResumeNotReady) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		fellBack := r.woken() != nil
		r.handleLock.Lock()
		r.fellBack = fellBack
		r.handleLock.Unlock()
		r.setInstruction(spec.Brief)
		return r.rt.Driver.Start(ctx, spec)
	}
	return handle, err
}

// spec builds a session from what the worker prepared, and refuses what the driver cannot run
// before a process starts. A session that goes on stays in the mode it started in, whatever the
// profile says by now, because a session resumed in the other mode is untested.
func (r *laneRun) spec(ctx context.Context, prepared PreparedLane, repository, packFile string, capabilities ports.Capabilities) (ports.SessionSpec, error) {
	mode, err := r.runMode(prepared.Profile.RunMode, slices.Contains(capabilities.RunModes, ports.RunModeInteractive))
	if err != nil {
		return ports.SessionSpec{}, err
	}
	if r.resumeFrom != "" && r.startedMode != "" {
		mode = r.startedMode
	}
	// A version whose `-p` lost a subscription's login offers no headless lane, and ADR-0002 falls
	// back to tmux. A session that goes on cannot change its mode, so only a fresh one moves.
	if mode == ports.RunModeHeadless && r.resumeFrom == "" && !slices.Contains(capabilities.RunModes, mode) &&
		slices.Contains(capabilities.RunModes, ports.RunModeInteractive) {
		r.w.config.Report(fmt.Errorf("lane %s: version %s offers no headless lane on this sign-in, so it runs in tmux", r.lease.Lane, capabilities.Version))
		mode = ports.RunModeInteractive
	}
	r.startedMode = mode
	if !slices.Contains(capabilities.RunModes, mode) {
		return ports.SessionSpec{}, fmt.Errorf("the driver has no run mode %q", mode)
	}
	credential, err := r.rt.Secrets.Resolve(ctx, prepared.Profile.Agent.Credentials.SecretRef)
	if err != nil {
		return ports.SessionSpec{}, err
	}
	model, effort, deny := r.asAgent(prepared)
	r.handleLock.Lock()
	r.model, r.profileDigest = model, prepared.ProfileDigest
	r.version, r.degraded = capabilities.Version, capabilities.Degraded
	r.settings = laneSettings(prepared.Profile, r.role(), mode)
	r.settings.ModelRequested = model
	if effort != "" {
		r.settings.Effort = effort
	}
	r.handleLock.Unlock()
	r.noteReviewBound(prepared.Profile)
	r.state.mu.Lock()
	r.state.degraded = capabilities.Degraded
	r.state.mu.Unlock()
	r.state.noteCapabilities(capabilities)
	spec := ports.SessionSpec{
		Lane: r.lease.Lane, WorkOrder: r.lease.WorkOrder, Generation: r.currentGeneration(), Dir: prepared.Worktree.Path,
		Contract: r.lease.Contract, ContractVersion: r.lease.ContractVersion, Repository: repository, Env: prepared.Environment, Model: r.model, PermissionMode: prepared.Profile.Agent.PermissionMode,
		Brief: r.briefText(), SystemPromptFile: packFile,
		ConfigDir: r.rt.configDir(r.lease.Lane), Binary: r.rt.Binary, Tools: r.tools(), OnlyTools: r.orchestrating(),
		Credential: credential, TempDir: r.rt.tempDir(r.lease.Lane), Override: r.rt.Override,
		RunMode: mode, Planning: r.planning(), ReadOnly: r.reviewing(), ReadyTimeout: readyTimeout(prepared.Profile), DefaultBranch: prepared.DefaultBranch,
		Kept: r.kept(), ReadOnlyDirs: readDirs(r.readTrees()),
		Gates: prepared.Profile.Agent.Gates, ApprovalWait: r.state.approvalWait(),
		Telemetry: r.sessionTelemetry(prepared, repository),
		Agents:    r.sessionAgents(prepared.Profile, capabilities.Support()), Effort: effort, Deny: deny,
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

// errNoInteractive says a planning lane cannot start because the driver offers no run mode where a
// person can attach, which on the Claude Code driver is a machine with no tmux.
var errNoInteractive = errors.New("a planning lane runs where a person can attach, and this driver offers no interactive run mode (it needs tmux)")

// runMode picks how this lane runs. A planning lane runs in a pane whatever its profile and its
// sign-in say, because a person is present by construction (ADR-0018), and it never falls back to
// headless. Every other lane takes what its profile and sign-in call for.
func (r *laneRun) runMode(profile string, interactive bool) (string, error) {
	if !r.planning() {
		return runMode(profile, r.w.signInKind(), interactive)
	}
	if !interactive {
		return "", errNoInteractive
	}
	return ports.RunModeInteractive, nil
}

// runMode reads a profile's value in the driver's terms. A profile that says nothing takes
// what the sign-in calls for.
func runMode(value, signIn string, interactive bool) (string, error) {
	if value == "" {
		value = lanes.DefaultRunMode(signIn, interactive)
	}
	switch value {
	case lanes.ProfileRunTmux:
		return ports.RunModeInteractive, nil
	case lanes.ProfileRunHeadless:
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
	defer r.sessionOver.Store(true)
	for event := range events {
		if event.Seq != 0 && event.Seq <= r.cursor {
			continue
		}
		if r.anotherCompaction(ctx, event) {
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.measureTurn(event.Turn)
			r.progressed()
			r.saveProgress(ctx, event.Seq)
			continue
		}
		// A turn that ends while its generation owes a brief is the clear's. It is read before the
		// lane reads idle, because from then a pass may brief the session and settle what it owes.
		clears := event.Kind == ports.EventTurnEnded && r.owesBrief.Load()
		r.observeEvent(ctx, event)
		switch event.Kind {
		case ports.EventSessionBound:
			r.bind(ctx, event)
		case ports.EventTurnEnded:
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.measureTurn(event.Turn)
			r.finishTurn(event.Turn)
			r.noteResponse(ctx, event.Turn)
			r.afterTurn(ctx, event.Turn, clears)
			r.saveProgress(ctx, event.Seq)
		case ports.EventSessionEnded:
			r.expireToken(ctx)
			if event.End != nil && (event.End.Crashed || r.orchestratorLost(ctx)) {
				r.crashed, r.crashedAt = true, r.w.clock.Now()
				r.signal(ctx, lanes.OnActivity, lanes.OnSessionGone)
			}
			return r.verdict(ctx, event.End)
		}
	}
	return failed(workorders.ReasonCrashed)
}

// expireToken ends a token with its process, so nothing left behind can act for the lane while it
// waits on a next generation (ADR-0016). A worker on its way out reads nothing of a session's end:
// the session outlives it, and the worker after it takes that session over with its token.
func (r *laneRun) expireToken(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if err := r.w.journal.ExpireToken(ctx, r.lease.Lane); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

// saveProgress keeps how far this run read, so a kill costs at most one redelivered
// turn, and a redelivered turn is dropped by its position. The generation is read before anything
// else, so what a clear overtakes is refused: everything here that a clear resets may already be of
// the generation before it.
func (r *laneRun) saveProgress(ctx context.Context, cursor int64) {
	generation := r.currentGeneration()
	r.cursor = cursor
	progress := lanes.Progress{
		Cursor: cursor, Cost: r.cost, Usage: r.usage, PriorUSD: r.priorUSD, PriorUnknown: r.priorUnknown,
		Instruction: r.instructionInProgress(), Carried: r.carriedIDs(), Pack: r.readPack(), OwesBrief: r.owesBrief.Load(),
	}
	r.state.journalContext(&progress)
	if _, err := r.w.journal.SaveProgress(ctx, r.lease.Lane, generation, progress); err != nil {
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
	if !r.bindSession(ctx, event.SessionID, event.ModelResolved) {
		return
	}
	if err := r.w.Upload(ctx); err != nil {
		r.w.config.Report(err)
	}
}

// bindSession records the session of the generation this run follows, and reports whether this
// record was the first. The stream binds a generation, and so does a clear that arrives ahead of
// the stream, so the journal keeps the first and the other records nothing.
func (r *laneRun) bindSession(ctx context.Context, session, model string) bool {
	r.handleLock.Lock()
	r.resolved = model
	fellBack := r.fellBack
	r.handleLock.Unlock()
	binding := r.binding(session, model)
	binding.FellBack = fellBack
	payload, err := json.Marshal(binding)
	if err != nil {
		r.w.config.Report(err)
		return false
	}
	bound := r.w.event(ledger.UploadGenerationBound, r.record(), payload)
	first, err := r.w.journal.BindGeneration(ctx, r.lease.Lane, session, bound)
	if err != nil {
		r.w.config.Report(err)
		return false
	}
	return first
}

// afterTurn decides what an idle lane does: stop if it reported, and nudge it once if
// it did not (FR-LANE-2). A turn that failed has nothing to nudge, and neither has one a clear ended.
func (r *laneRun) afterTurn(ctx context.Context, turn *ports.TurnResult, clears bool) {
	if r.orchestrating() {
		r.orchestratorRests(ctx, turn)
		return
	}
	if _, ok := r.reported(ctx); ok {
		r.signal(ctx, lanes.OnOutcomeReported)
		r.stop(ctx, ports.StopAtRest)
		return
	}
	if turn.Deferred && r.parkDeferred(ctx) {
		return
	}
	if r.currentFlight() != nil {
		return
	}
	if turn.Status != ports.TurnCompleted {
		if r.noteFailure(ctx, turn.Reason) {
			return
		}
		report := failed(turn.Reason)
		r.decided = &report
		r.signal(ctx, lanes.OnLaneEnding)
		r.stop(ctx, ports.StopAtRest)
		return
	}
	if clears || r.owesBrief.Load() {
		return
	}
	if len(r.blockingQuestions()) != 0 {
		r.awaitAnswer(ctx)
		return
	}
	if r.draining(ctx) {
		r.parkDrained(ctx)
		return
	}
	if r.contextAtRest(ctx) {
		return
	}
	if r.nudged.CompareAndSwap(false, true) {
		ran, err := r.w.bracket(ctx, r.nudge(), func() error {
			_, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: lanes.Nudge})
			return err
		})
		if ran && err == nil {
			r.setInstruction(lanes.Nudge)
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
