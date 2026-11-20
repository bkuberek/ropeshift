package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
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
	// AssistantCommands are the commands of Binary an assistant's shell may run, and Routes the first
	// words of Binary that name no command, which its rules deny by name (ADR-0024).
	AssistantCommands []string
	Routes            []string
	// ControlEnv is what an assistant's ropeshift reads to reach the control plane where it listens, such
	// as the run directory of its socket. It comes from the daemon's own paths, and replaces whatever else
	// the session's environment names for it.
	ControlEnv []string
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
	// Layouts reads how the clones on this machine are laid out, for a lane whose lease carries a restructuring: the
	// worker reads the layout again as its plan did, and restructures only when it reads the same one
	// (FR-ADOPT-14). Nil restructures nothing, and such a lane does not start.
	Layouts ports.Layouts
	// Forge runs what the control plane asks of a forge: pushes, pull requests, comments, and reads of the
	// sign-in's health (ADR-0020). Nil runs none, and a repository that reviews through pull requests waits.
	Forge ports.Forge
	// ForgeEvery is how often the forge's sign-in is read for each repository, and how long a failed forge
	// request waits before its next attempt. Zero takes a minute.
	ForgeEvery time.Duration
	// ManifestTemplate and IntakeDirectory are what a planning lane's first brief shows of the manifest of a
	// project's code repositories and of where intake reads plan files (FR-TPL-3). The entry package renders
	// the manifest and passes the daemon's --dir, because this package never imports the templates.
	ManifestTemplate string
	IntakeDirectory  string
	// Render writes the files a restructuring adds from the project templates, and the starting instructions an
	// adopt brief shows. The entry package passes it, because this package never imports the templates. Nil
	// writes no such file, and a lane whose restructuring lists one does not start.
	Render projectfiles.Render
	// PaneInputs is where the web pane records what a member types into a lane's pane, which a planning
	// lane waiting for its person reads as a person at the pane. Nil records none.
	PaneInputs *PaneInputs
}

// laneRun is one lane this process is running.
type laneRun struct {
	w          *Worker
	rt         *LaneRuntime
	lease      ledger.DesiredLease
	generation int64
	model      string

	// handleLock guards the generation and the handle, because a heartbeat and a hook read
	// them while a start writes them. It guards handled too.
	handleLock sync.Mutex
	handle     ports.SessionHandle
	// handled is the newest request for a new generation this run has taken, as the ledger counts them.
	// It only rises, and a generation beginning does not reset it, so a request is acted on once.
	handled int64

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
	// gates are the permission rules this lane's profile gates, which its requests are read against.
	gates []string

	bound   bool
	nudged  atomic.Bool
	cursor  int64
	decided *workorders.Report
	cost    lanes.CostMeter
	usage   lanes.Usage
	fenced  atomic.Bool
	// stopAsked says this run heard its lease is gone.
	stopAsked atomic.Bool
	// keptReported says this run told the operator its lane kept a worktree the root in force would not have
	// chosen, so the report comes once however many generations follow.
	keptReported atomic.Bool
	// unignoredReported says this run told the operator why its lane's root stayed outside its workspace, once.
	unignoredReported atomic.Bool
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
	// departed says the worker stopped reading a session that goes on, because the worker is on its way
	// out. The lane is not over: the worker after this one adopts the session.
	departed atomic.Bool
	// resumeFrom is the session a restarted generation continues. Empty starts fresh.
	resumeFrom string
	// startedMode is the run mode of the session this lane last started.
	startedMode string
	// priorUSD and priorUnknown are what earlier generations cost.
	priorUSD     float64
	priorUnknown int64
	// allowance is what the ledger said this lane's work order allows it to spend, when this generation began, and
	// running is the session's last running cost total. The handle's lock guards both. spendStop says the lane began
	// its stop at the hard limit, which it does once.
	allowance *ledger.SpendAllowance
	running   float64
	// stoppedUSD holds what each turn that stopped had been estimated at when it did, until a later turn reports a total, and
	// the handle's lock guards it with running.
	stoppedUSD []float64
	spendStop  atomic.Bool
	// allowanceKnown says the ledger answered what the allowance is, nil for none included, and allowanceFailedAt is
	// when a read of it first failed with nothing known, and allowanceTriedAt when one last ran. A lane whose allowance
	// stays unknown is held (FR-SPEND-5). The handle's lock guards all three.
	allowanceKnown    bool
	allowanceFailedAt time.Time
	allowanceTriedAt  time.Time
	// allowanceReadAt is when a read of the allowance last answered, which an allowance that says its project has a limit
	// is held to: one the ledger has not answered for longer than the grace is unknown again (FR-SPEND-6). The handle's lock
	// guards it.
	allowanceReadAt time.Time
	// spendCheck carries a hook's word that a tool call is starting to the goroutine that reads this run's stream, which
	// then holds the lane to its limits as a usage report does. It holds one word, so a burst of calls is one check.
	spendCheck chan struct{}
	// backgroundDue carries the end of a rest on background work to the same goroutine, which decides what the lane
	// does next.
	backgroundDue chan struct{}
	// projectReportedAt and projectReportedUSD are when this lane last told the ledger what it spent for its project's
	// limits, and what it was charged then, which the handle's lock guards (FR-SPEND-6).
	projectReportedAt  time.Time
	projectReportedUSD float64
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
	// waitParkAt is the end, in milliseconds, of the longest `lane_wait` past the live bound this lane declared
	// that no pass has parked it for yet, and zero when none waits. The handle's lock guards it.
	waitParkAt int64
	// process is the generation whose process runs now, which a clear does not move. The handle's
	// lock guards it.
	process int64
	// respawnAsked is the request this run has taken and no generation has begun on, as the ledger counts
	// them, and zero when none waits. handleLock guards its writes.
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
	// endTold says an earlier worker read the end of this generation's session, and told the telemetry of a turn
	// it ended in then, so a run that reads that end again counts the turn in its meter alone. endKept says the
	// journal's meter counts that turn too, so the run counts it nowhere.
	endTold bool
	endKept bool
	// restructured is what the worker did to this lane's repository before the lane started, which its brief
	// tells it, and the handle's lock guards it (FR-ADOPT-14).
	restructured *lanes.Restructured
	// instructions is the starting AGENTS.md an adopt brief shows when the repository holds no instruction file, and
	// the handle's lock guards it.
	instructions string
	// assisted is what an assistant's run holds beside the lane code's state, and the handle's lock guards
	// its making.
	assisted *assistantState
	// digest is the builder of this generation's digest, for the generation digestGeneration, and
	// digestStartMs is when that generation began. The digest lock guards all three, and is taken before the
	// handle's.
	digestLock       sync.Mutex
	digest           *lanes.DigestBuilder
	digestGeneration int64
	digestStartMs    int64
}

func newOnce() *sync.Once { return &sync.Once{} }

// newRun is a lane's run at its first generation.
func newRun(w *Worker, lease ledger.DesiredLease) *laneRun {
	return &laneRun{
		w: w, rt: w.config.Lanes, lease: lease, generation: firstGeneration, stopOnce: newOnce(), spendCheck: make(chan struct{}, 1),
		backgroundDue: make(chan struct{}, 1),
		state:         newLaneState(lanes.Pending, w.clock.Now()), pack: lease.Pack, digestStartMs: w.clock.Now().UnixMilli(),
	}
}

// resumedRun is a run that goes on from a record, for a lane a crash stopped before its
// session. A later generation keeps what the earlier ones left.
func (w *Worker) resumedRun(lease ledger.DesiredLease, record lanes.Record) *laneRun {
	run := newRun(w, lease)
	run.generation, run.resumeFrom, run.startedMode = record.Generation, record.ResumeSession, record.RunMode
	run.cost, run.usage, run.endKept = record.Progress.Cost, record.Progress.Usage, record.Progress.EndCounted
	run.priorUSD, run.priorUnknown = record.Progress.PriorUSD, record.Progress.PriorUnknown
	if record.Actual != "" {
		run.state = laneStateOf(record, w.clock.Now())
	}
	run.left, run.parking = record.Handoff, record.Parking
	run.worktree = record.Worktree
	run.instruction, run.blocking, run.carried = record.Progress.Instruction, record.Blocking, record.Progress.Carried
	run.packRead, run.process = record.Progress.Pack, record.Process
	run.handled = record.RespawnsSeen
	run.waitParkAt = record.WaitParkAt
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
	r.stopReached(ctx)
}

// stopReached stops the session the driver holds for the generation running now, unless that session ended.
// It is for a caller that runs beside the lane's own goroutine, which can begin the next generation while the
// caller is between two reads.
//
// A stop asked of a session the driver does not hold yet fails and spends the generation's one stop, so a
// decision about one generation must not spend the next one's. This takes the generation's stop before it
// reads anything, so what it reads is never older than the stop it spends. It reads sessionOver before
// reached, because enterGeneration clears reached first: a read of the last session as over, or of the next
// as not reached, stops nothing.
func (r *laneRun) stopReached(ctx context.Context) {
	once := r.generationStop()
	if !r.sessionOver.Load() && r.reached.Load() {
		r.stopWith(ctx, once, ports.StopInterrupt)
	}
}

// reach says the driver holds this generation's session. A stop asked of a session it does not hold
// yet fails, and would spend the generation's one stop, so a lane that lost its lease before this, or
// was asked for a new generation, is stopped here. lost and respawn set what this reads before they read
// what this sets, so one of them stops it.
func (r *laneRun) reach(ctx context.Context) {
	r.reached.Store(true)
	if (r.stopAsked.Load() || r.respawnAsked.Load() != 0) && !r.sessionOver.Load() {
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
// the journal write uploads on the next start. Base is the commit the lane's claim began its
// branch at, which the change it made is measured from.
func (w *Worker) endLane(ctx context.Context, record lanes.Record, base string, result workorders.LaneResult, fenced bool) error {
	if err := w.finalGuard(ctx, record, fenced); err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", record.ID, err))
	}
	worktree := w.finalWorktree(ctx, record)
	result.Branch, result.Head, result.Change = worktree.Branch, worktree.Head, w.changeSize(ctx, worktree, base)
	payload, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if _, err := w.journal.EndLane(ctx, record.ID, w.event(ledger.UploadLaneEnded, record, payload)); err != nil {
		return err
	}
	crashpoint.Hit("worker.lane", "ended")
	if w.config.Lanes != nil {
		w.config.Lanes.PaneInputs.Forget(record.ID)
	}
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
	if r.assisting() {
		record.Owner = ownerAssistant
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
	if r.orchestrating() {
		r.loadProfile(ctx)
	}
	return r.proceed(ctx)
}

// proceed runs a lane's session to the end. A restart comes here for a lane a crash
// stopped before its session existed, and the effects on record say how far it got.
//
// The claim is granted by the time a lane is here, and `claim_granted` is the only way out of
// `pending`. Proceeding sends it, and not only the path that begins a lane, because a kill
// between that journaled start and this signal leaves a lane in `pending`, and a lane a restart
// continues from there would hear every later signal from a state with no edge for it. The
// signal moves nothing from any other state.
func (r *laneRun) proceed(ctx context.Context) error {
	r.signal(ctx, lanes.OnClaimGranted)
	return r.conclude(ctx, r.session(ctx))
}

// conclude supervises what a session left, and ends the lane once it has an outcome. A lane that is
// not over stays in the journal for whoever goes on with it. What the run started beside its session,
// a sequence or a stop, writes the journal until it ends, with the worker's context or with its stop,
// so the run waits for it, and nothing of the lane outlives the worker's wait.
func (r *laneRun) conclude(ctx context.Context, report workorders.Report) error {
	report, over := r.supervised(ctx, report)
	if !over {
		r.sequence.Wait()
		r.stopped.Wait()
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
	r.recordUsage(context.WithoutCancel(ctx))
	return r.w.endLane(context.WithoutCancel(ctx), r.record(), r.lease.BaseCommit, r.endResult(report), r.fenced.Load())
}

// endResult is what the lane's end records of what only the worker saw, beside the report.
func (r *laneRun) endResult(report workorders.Report) workorders.LaneResult {
	return workorders.LaneResult{
		Report: report, CostUSD: r.priorUSD + r.cost.CostUSD, SessionTotalUSD: r.cost.SessionTotalUSD,
		UnknownCostTurns: r.priorUnknown + r.cost.UnknownTurns, InputTokens: r.usage.Context(), OutputTokens: r.usage.OutputTokens,
		Settings: r.endSettings(), MaxReviewRounds: r.reviewBound(), ImportMerge: r.importMerge(),
	}
}

// adoptedRun rebuilds a lane's run from the journal, because nothing else survived.
func (w *Worker) adoptedRun(ctx context.Context, record lanes.Record, located ports.Located, lease ledger.DesiredLease) (*laneRun, error) {
	lease.WorkOrder, lease.ClaimID, lease.Fence, lease.Lane = record.WorkOrder, record.ClaimID, record.Fence, record.ID
	run := w.resumedRun(lease, record)
	run.fenced.Store(w.leaseGone(ctx, record))
	run.setHandle(located.Handle)
	run.bound, run.cursor = record.SessionID != "", record.Progress.Cursor
	// A session's token ends only when a worker reads its end, or with its generation or its lane.
	run.endTold = record.TokenDigest == ""
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
	// The allowance is not in the journal, so a worker that takes a session over reads it again, and holds the lane
	// to its limit from here.
	r.readAllowance(ctx)
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
	_, reported := r.reported(ctx)
	if reported {
		r.stop(ctx, ports.StopAtRest)
	}
	r.resumeParking(ctx)
	r.resumeHandoff(ctx)
	switch {
	case r.state.current() != lanes.Idle:
	case len(r.blockingQuestions()) != 0:
		r.awaitAnswer(ctx)
	case r.planning() && !reported && !r.owesBrief.Load():
		// A planning lane at rest with nothing to report was waiting for its person, and goes on waiting.
		r.awaitPerson(ctx)
	}
	return r.conclude(ctx, r.watch(ctx, events))
}

// unread settles a lane whose session cannot be read. Parking and a handoff still finish,
// because those are the failures with somewhere to go. The turn the session may have ended in counts first, so the
// lane's end, its next generation, or its wake carries it.
func (r *laneRun) unread(ctx context.Context) error {
	r.unreadEnd(ctx)
	if _, _, asked := r.waitParkOf(); asked || r.parkingOf().Open() {
		return r.finishParking(ctx)
	}
	if r.left.Open() {
		return r.finishHandoff(ctx)
	}
	return r.finish(ctx, r.unattended(ctx))
}

// unreadEnd counts the turn a session this run cannot read may have ended in, as endUnattended does for a lane no run
// holds: what the journal last recorded of the lane decides, and a read that fails counts. The telemetry hears of it
// only while the lane's token stood at adoption, because a worker that read the session's end ended the token as it
// counted the turn.
func (r *laneRun) unreadEnd(ctx context.Context) {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	} else if !r.w.endedInATurn(ctx, record) {
		return
	}
	r.unreportedEnd(ctx)
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

// maxFailureDetail bounds, in bytes, the cause a lane that failed to start carries in its outcome.
const maxFailureDetail = 500

// failStart ends a lane that could not start. The outcome carries the cause as its detail, so the card of the
// failed work and the work order's lanes say more than the reason. What an error says may come from a
// repository, a terminal, or a process, so it is plain text on one line, cut on a rune boundary.
func (r *laneRun) failStart(reason string, err error) workorders.Report {
	r.w.config.Report(fmt.Errorf("lane %s: %s: %w", r.lease.Lane, reason, err))
	report := failed(reason)
	report.Detail = failureDetail(err)
	return report
}

// failureDetail writes what an error says as plain text on one line, cut at maxFailureDetail bytes. A hook's output
// or a git error can carry a credential, such as a token or a remote's URL with its password, and the ledger keeps
// the detail for every member to read, so each credential is redacted before the cut: a token cut in two no longer
// matches the shape of one.
func failureDetail(err error) string {
	text := strings.Join(strings.Fields(ledger.SanitizeCIText(err.Error())), " ")
	if len(text) <= maxFailureDetail {
		return text
	}
	cut := maxFailureDetail
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// recordedWorktree is the worktree the journal recorded for this lane's earlier generation, or nothing. A start
// that begins the lane again records none until its worktree exists.
func (r *laneRun) recordedWorktree(ctx context.Context) string {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		return ""
	}
	return record.Worktree
}

// session provisions and runs the session, and returns the lane's outcome. The token
// exists only in the session's environment, and the journal keeps its digest.
func (r *laneRun) session(ctx context.Context) workorders.Report {
	if r.orchestrating() && !r.assisting() && !r.awaitWake(ctx) {
		return failed(workorders.ReasonWorkerRestarted)
	}
	if r.assisting() {
		if _, err := r.assistantGrant(ctx); err != nil {
			return r.failStart(workorders.ReasonSpawnFailed, err)
		}
		if r.currentGeneration() == firstGeneration && r.resumeFrom == "" {
			r.resumeFrom = r.resumable(ctx)
		}
	}
	r.w.qualifyLane(ctx, r.lease)
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
		Lane: r.lease.Lane, Repository: repository, Project: r.lease.Project, PlanningRepository: r.w.planningRepository(ctx, r.lease.Project), Recorded: r.recordedWorktree(ctx), Branch: r.lease.Branch, Base: r.begins(), Overrides: r.lease.Overrides, Delegated: r.lease.Delegated, Agent: r.runsAs(), Fetch: r.fetchNames(), Private: r.orchestrating(),
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
	// The outcome says which policy refused the start and what the layer raised, so every surface that reads
	// it can say why the lane never ran.
	var refused *lanes.DelegatePolicyRefusal
	if errors.As(err, &refused) {
		report := r.failStart(workorders.ReasonDelegatePolicy, err)
		report.Detail = refused.Detail()
		return report
	}
	// The outcome names the root, the clone, and the layer that set the root, with the fix.
	var inside *WorktreeInsideClone
	if errors.As(err, &inside) {
		report := r.failStart(workorders.ReasonWorktreeInsideClone, err)
		report.Detail = inside.Detail()
		return report
	}
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	if prepared.Kept != nil && r.keptReported.CompareAndSwap(false, true) {
		r.w.config.Notice(fmt.Sprintf("lane %s: %s", r.lease.Lane, prepared.Kept.Notice()))
	}
	if prepared.Fallback.Reason != "" && r.unignoredReported.CompareAndSwap(false, true) {
		r.w.config.Notice(fmt.Sprintf("lane %s: %s", r.lease.Lane, prepared.Fallback.Notice()))
	}
	r.signal(ctx, lanes.OnResourcesReady)
	r.setWorktreePath(prepared.Worktree.Path)
	r.setReads(r.prepareReads(ctx, prepared.Worktree.Path))
	if r.assisting() {
		grant := r.assistant().grantNow()
		prepared.Environment = assistantEnvironment(prepared.Environment, grant, r.w.projectName(ctx, r.lease.Project, grant), r.rt.ControlEnv)
		r.setReads(r.assistantReads(ctx, prepared.Worktree.Path, grant.Context.Repositories))
	}
	if report, failed := r.restructure(ctx, repository, prepared.Worktree.Path); failed {
		return report
	}
	r.startingInstructions(ctx, repository)
	if err := r.writeInventory(ctx, repository, prepared.Worktree.Path); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err)
	}
	r.state.thresholds(prepared.Profile, r.role())
	r.readFacts(ctx)
	// A generation that begins with nothing left to spend parks before its first turn (FR-SPEND-5).
	if report, parked := r.parkAtLimitBeforeStart(ctx); parked {
		return report
	}
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
	if r.assisting() {
		resumed.Brief = r.briefAssistant(true)
	}
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
	// back to tmux. A session that goes on cannot change its mode, so only a fresh one moves, and an
	// assistant never does: a pane's launch file would hold its credential.
	if mode == ports.RunModeHeadless && r.resumeFrom == "" && !r.assisting() && !slices.Contains(capabilities.RunModes, mode) &&
		slices.Contains(capabilities.RunModes, ports.RunModeInteractive) {
		r.w.config.Report(fmt.Errorf("lane %s: version %s offers no headless lane on this sign-in, so it runs in tmux", r.lease.Lane, capabilities.Version))
		mode = ports.RunModeInteractive
	}
	r.setStartedMode(mode)
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
	r.gates = slices.Clone(prepared.Profile.Agent.Gates)
	r.handleLock.Unlock()
	r.noteReviewBound(prepared.Profile)
	r.state.mu.Lock()
	r.state.degraded = capabilities.Degraded
	r.state.mu.Unlock()
	r.state.noteCapabilities(capabilities)
	spec := ports.SessionSpec{
		Lane: r.lease.Lane, WorkOrder: r.lease.WorkOrder, Generation: r.currentGeneration(), Dir: prepared.Worktree.Path,
		Contract: r.lease.Contract, ContractVersion: r.lease.ContractVersion, Repository: repository, Env: prepared.Environment, Model: r.model, PermissionMode: r.permissionMode(prepared.Profile),
		Brief: r.briefText(), SystemPromptFile: packFile,
		ConfigDir: r.rt.configDir(r.lease.Lane), Binary: r.rt.Binary, Tools: r.tools(), OnlyTools: r.orchestrating() && !r.assisting(),
		Credential: credential, TempDir: r.rt.tempDir(r.lease.Lane), Override: r.rt.Override,
		RunMode: mode, Planning: r.planning(), ReadOnly: r.reviewing() || r.assisting(), Assistant: r.assisting(),
		ReadyTimeout: readyTimeout(prepared.Profile), DefaultBranch: prepared.DefaultBranch,
		Kept: r.kept(), ReadOnlyDirs: readDirs(r.readTrees()),
		Gates: prepared.Profile.Agent.Gates, ApprovalWait: r.state.approvalWait(),
		Telemetry: r.sessionTelemetry(prepared, repository),
		Agents:    r.sessionAgents(prepared.Profile, capabilities.Support()), Effort: effort, Deny: deny,
	}
	if !r.orchestrating() && !r.assisting() {
		spec.Waits = lanes.WaitRulesFrom(r.lease.Spend)
	}
	// A headless generation's client holds a cap of its own, as a backstop for a worker slow to stop it. It is what
	// remains of the work order's hard limit and a share of the limit beyond it, and a driver passes it only where its
	// qualification observed the flag (FR-SPEND-5).
	if allowance := r.allowanceNow(); mode == ports.RunModeHeadless && r.holdsToLimit() && allowance != nil {
		spec.MaxBudgetUSD = allowance.BudgetUSD(r.chargedUSD())
	}
	r.assistantShell(&spec)
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
	if r.assisting() {
		// An assistant's replies are read from its stream, which only a headless session writes.
		return ports.RunModeHeadless, nil
	}
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
//
// A worker on its way out stops reading and leaves the session running, because a stop keeps lanes and
// the stream ends only with the session. The session is not over then, and the worker after this one
// adopts it from the cursor the journal holds.
func (r *laneRun) watch(ctx context.Context, events <-chan ports.DriverEvent) workorders.Report {
	defer func() {
		if !r.departed.Load() {
			// The digest ends first: a handoff waits on the session's end to begin the next generation, and
			// the digest is of the one that ended.
			r.endDigest(ctx)
			r.sessionOver.Store(true)
		}
	}()
	for {
		var event ports.DriverEvent
		select {
		case <-ctx.Done():
			r.departed.Store(true)
			return failed(workorders.ReasonWorkerRestarted)
		case next, open := <-events:
			// A stream that closed before its session ended says nothing of how it ended, so the lane settles as
			// crashed, and the turn it may have died in counts as unreported, as a crash's does.
			if !open {
				r.unreportedEnd(ctx)
				return failed(workorders.ReasonCrashed)
			}
			event = next
		case <-r.backgroundDue:
			r.backgroundBound(ctx)
			continue
		case <-r.spendCheck:
			// A tool call is starting, and no cost report may come to show the limit passed: a call that cannot be
			// priced, or a pane whose cost does not move, reports none.
			r.evaluateSpend(ctx)
			continue
		}
		if event.Seq != 0 && event.Seq <= r.cursor {
			continue
		}
		if r.anotherCompaction(ctx, event) {
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.settleTurnCost(event.Turn)
			r.measureTurn(event.Turn)
			r.progressed()
			r.saveProgress(ctx, event.Seq)
			continue
		}
		// A turn that ends while its generation owes a brief is the clear's. A clear whose hook spooled
		// owes none until the spool is read, so it is read first. The debt is read before the lane reads
		// idle, because from then a pass may brief the session and settle what it owes.
		if event.Kind == ports.EventTurnEnded {
			r.w.drainBeforeActing(ctx)
		}
		clears := event.Kind == ports.EventTurnEnded && r.owesBrief.Load()
		r.foldEvent(ctx, event)
		r.observeEvent(ctx, event)
		switch event.Kind {
		case ports.EventSessionBound:
			r.bind(ctx, event)
		case ports.EventReply:
			if r.assisting() {
				r.postReply(ctx, event)
			}
		case ports.EventTurnEnded:
			if r.assisting() {
				r.assistant().noteTurn()
			}
			r.cost, r.usage = event.Turn.Cost, r.usage.Add(event.Turn.Usage)
			r.settleTurnCost(event.Turn)
			r.measureTurn(event.Turn)
			r.finishTurn(event.Turn)
			r.noteResponse(ctx, event.Turn)
			r.afterTurn(ctx, event.Turn, clears)
			r.saveProgress(ctx, event.Seq)
		case ports.EventSessionEnded:
			r.noteRefusedResume(ctx)
			// A session that ended with an input unanswered never reported the cost of the turn it ended in, though
			// a kill the worker asked for crashes nothing. Crashed counts too: a turn a message started through the
			// session's socket is no input a driver counts, so a crash with every counted input answered may still
			// have died in one. Known limitation: a crash at rest counts a turn too, since the stream shows no documented
			// start for a turn a socket message begins. The turn counts before the token ends, so a token that ended
			// says its telemetry has the turn.
			if event.End != nil && (event.End.Crashed || event.End.InputUnanswered) {
				r.unreportedEnd(ctx)
			}
			r.expireToken(ctx)
			if event.End != nil && (event.End.Crashed || r.orchestratorLost(ctx)) {
				r.crashed, r.crashedAt = true, r.w.clock.Now()
				r.signal(ctx, lanes.OnActivity, lanes.OnSessionGone)
			}
			return r.verdict(ctx, event.End)
		}
	}
}

// expireToken ends a token with its process, so nothing left behind can act for the lane while it
// waits on a next generation (ADR-0016). A worker on its way out reads nothing of a session's end:
// the session outlives it, and the worker after it takes that session over with its token.
func (r *laneRun) expireToken(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	// A spooled line counts only while its lane has a token, so what the session spooled is read first.
	r.w.drainBeforeActing(ctx)
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
	// The digest is kept first, so it holds every event up to the position the progress names.
	r.saveDigest(ctx)
	r.cursor = cursor
	progress := lanes.Progress{
		Cursor: cursor, Cost: r.cost, Usage: r.usage, PriorUSD: r.priorUSD, PriorUnknown: r.priorUnknown,
		Instruction: r.instructionInProgress(), Carried: r.carriedIDs(), Pack: r.readPack(), OwesBrief: r.owesBrief.Load(),
		EndCounted: r.endKept,
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
	if r.assisting() {
		r.rememberSession(ctx, event.SessionID)
	}
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
// it did not (FR-LANE-2). A turn that failed has nothing to nudge, and neither has one a clear ended, nor one
// that left the lane's own background work running.
// A planning lane is a conversation, so a turn it ends with no report waits for its person instead
// (ADR-0018).
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
	// A client that ended its turn at the cap it was given reached the limit and a little more, and a lane whose
	// charged spend a turn's end shows is at its limit: each parks as at the hard limit (FR-SPEND-5).
	if reachedBudget(turn) {
		r.stopOnBudget(ctx)
		return
	}
	if r.evaluateSpend(ctx) {
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
	// A lane that declared a wait past the live bound ended its turn as it was told, so it parks now and is not
	// nudged: a nudge is a model call for a lane that is waiting on a timer.
	if r.parkForWait(ctx) {
		return
	}
	if r.draining(ctx) {
		r.parkDrained(ctx)
		return
	}
	if r.contextAtRest(ctx) {
		return
	}
	if r.planning() {
		r.awaitPerson(ctx)
		return
	}
	// A turn that stopped with the lane's own background work still running ended none of the lane's work: the work
	// starts the session's next turn when it ends. The lane rests on it, unnudged, for as long as it may wait live.
	if since, _, resting := r.state.backgroundRest(); resting {
		r.restOnBackground(ctx, since)
		return
	}
	r.nudgeOrEnd(ctx, lanes.Nudge, workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonNoOutcome})
}

// nudgeOrEnd sends the lane its one nudge, or ends it with report once the nudge is spent or cannot be sent.
func (r *laneRun) nudgeOrEnd(ctx context.Context, text string, report workorders.Report) {
	if r.nudged.CompareAndSwap(false, true) {
		ran, err := r.w.bracket(ctx, r.nudge(), func() error {
			_, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: text})
			return err
		})
		if ran && err == nil {
			r.setInstruction(text)
			r.signal(ctx, lanes.OnInputHanded)
			return
		}
	}
	r.decided = &report
	r.signal(ctx, lanes.OnOutcomeReported)
	r.stop(ctx, ports.StopAtRest)
}

// restOnBackground holds a lane at rest on its own background work until the longest it may wait live has passed
// since its turn stopped (max_live_wait, FR-SPEND-1), then hands what comes next to the goroutine that reads the
// stream. A turn the work starts, a later stop, or the session's end ends the rest first.
func (r *laneRun) restOnBackground(ctx context.Context, since time.Time) {
	deadline := since.Add(lanes.MaxLiveWait(r.lease.Spend))
	r.sequence.Go(func() {
		for r.w.clock.Now().Before(deadline) {
			if !r.restsOnBackground(ctx, since) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(superviseEvery):
			}
		}
		if r.restsOnBackground(ctx, since) {
			select {
			case r.backgroundDue <- struct{}{}:
			default:
			}
		}
	})
}

// restsOnBackground says the rest that began with the stop the worker heard at since goes on.
func (r *laneRun) restsOnBackground(ctx context.Context, since time.Time) bool {
	at, _, resting := r.state.backgroundRest()
	return resting && at.Equal(since) && r.restsOn(ctx)
}

// backgroundBound ends a rest on background work that outlasted the live wait, where the stream is read: the lane is
// nudged with words that name the work it waits on, or, its nudge spent, ends blocked with a reason that names the
// wait and a detail that says since when, what still ran, and the bound that ended it.
func (r *laneRun) backgroundBound(ctx context.Context) {
	since, tasks, resting := r.state.backgroundRest()
	bound := lanes.MaxLiveWait(r.lease.Spend)
	if !resting || r.w.clock.Now().Before(since.Add(bound)) || !r.restsOn(ctx) || r.currentFlight() != nil {
		return
	}
	detail := fmt.Sprintf("Its turn ended at %s with %s still running, and none reported within max_live_wait, %s.",
		since.UTC().Format(time.RFC3339), backgroundTasks(tasks), bound)
	report := workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonBackgroundUnreported, Detail: detail}
	r.nudgeOrEnd(ctx, lanes.BackgroundNudge, report)
}

// backgroundTasks counts tasks in words.
func backgroundTasks(n int) string {
	if n == 1 {
		return "1 background task"
	}
	return fmt.Sprintf("%d background tasks", n)
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
	r.stopWith(ctx, r.generationStop(), how)
}

// generationStop is the one stop the generation running now has.
func (r *laneRun) generationStop() *sync.Once {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.stopOnce
}

// generationSession is the session the driver holds for the generation running now, and that generation's
// stop. enterGeneration moves both together under the handle's lock, so reading them in one piece never
// pairs a session with another generation's stop, which reading them one after the other can.
func (r *laneRun) generationSession() (ports.SessionHandle, *sync.Once) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.handle, r.stopOnce
}

// stopWith spends a generation's stop. A caller that decides from what another goroutine moves takes the
// stop before it decides, so a decision about one generation never spends the next one's.
func (r *laneRun) stopWith(ctx context.Context, once *sync.Once, how ports.StopMode) {
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
