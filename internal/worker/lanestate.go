package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// reachWindow is how long a piece of reachability evidence stays fresh.
const reachWindow = time.Minute

// The hooks that say something about a lane's state.
const (
	hookPreCompact   = "PreCompact"
	hookPostCompact  = "PostCompact"
	hookPreToolUse   = "PreToolUse"
	hookPostToolUse  = "PostToolUse"
	hookStopFailure  = "StopFailure"
	hookSessionStart = "SessionStart"

	hookPermissionRequest = "PermissionRequest"
)

// reachSeen is the last thing seen about one target.
type reachSeen struct {
	reach lanes.Reachability
	at    time.Time
}

// laneState is what a run derives about its lane, from driver events, hooks, and its own
// acts. A hook arrives on another goroutine, so everything here is behind one lock.
type laneState struct {
	mu       sync.Mutex
	tracker  *lanes.Tracker
	progress time.Time
	// tools holds the calls at work, by the ID of each, with the digest of what it called.
	tools    map[string]string
	context  int64
	baseline int64
	since    time.Time
	model    reachSeen
	ledger   reachSeen

	unconfirmed bool
	retried     bool
	degraded    bool
	unpushed    bool
	// policy is what a role's lane is held to, and baselined says this generation's baseline went
	// on its way to the ledger.
	policy     lanes.ContextPolicy
	baselined  bool
	compact    compaction
	canCompact bool
	// clearedMs is when the clear that began this generation fired, by the clock of the hook that said
	// so. A hook stamped before it describes the session the clear ended.
	clearedMs int64
	// held says this run has a session's tools held, and has not lifted that.
	held       bool
	stallAfter time.Duration
	briefGrace time.Duration
	waitUntil  time.Time
	limits     lanes.Breaker
	// handoffLimit is how long an agent has to write its part of a handoff, and
	// handoffDegraded says the last handoff has only the worker's part. That stays until a
	// later handoff has both, because it is what this generation was told.
	handoffLimit    time.Duration
	handoffDegraded bool
	// dayCapped says the breaker's last verdict found the lane's day full.
	dayCapped bool
	// forPerson says a planning lane waits at rest for the person it talks to.
	forPerson bool
	// forWake says an orchestrator's generation waits for something to judge, before it prepares a session.
	forWake bool
	// background is how many background tasks the session's newest Stop said still run, and backgroundSince when
	// the worker heard that Stop. Each task wakes the session with a turn of its own when it ends, and the prompt
	// that starts that turn ends the count.
	background      int
	backgroundSince time.Time
	// stopLeftWork says the newest Stop left background work and no turn's end has been decided since. A prompt
	// leaves it: that work can start the next turn before the worker has decided the end of the one that stopped.
	stopLeftWork bool
	// stoppedMs and promptedMs are when the newest Stop and prompt fired, by their hooks' clock, because a spooled
	// hook arrives late or twice.
	stoppedMs  int64
	promptedMs int64
	// refusedUntil is when the wait on a nudge a turn's end could not send runs out, and zero with no such wait.
	refusedUntil time.Time
	// limitUnread is the usage limit an adoption found a turn failed on, until that turn's end is read.
	limitUnread unreadLimit
	// askLimit is how long a lane rests on a blocking question, and drainLimit how long a
	// draining fleet lets a turn run.
	askLimit time.Duration
	// usageBase is how long the first wait on a limit with no known reset lasts.
	usageBase  time.Duration
	drainLimit time.Duration
	// approvalLimit is how long a call waits on a decision, and approvals how many it has open.
	approvalLimit time.Duration
	approvals     int
	// wakeDebounce and wakeFloor batch an orchestrator's wakes.
	wakeDebounce time.Duration
	wakeFloor    time.Duration
}

func newLaneState(start lanes.ActualState, now time.Time) *laneState {
	return &laneState{tracker: lanes.NewTracker(start), tools: map[string]string{}, progress: now, since: now}
}

// laneStateOf goes on from what a journal holds.
func laneStateOf(record lanes.Record, now time.Time) *laneState {
	state := newLaneState(record.Actual, now)
	state.tracker = lanes.ResumeTracker(record.Actual, record.CompactFrom)
	return state
}

// atRest says no turn is running: the lane is idle, or in a compaction it began there.
func (s *laneState) atRest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tracker.State() == lanes.Idle || s.tracker.Left() == lanes.Idle
}

// compactionHolds says a lane stands in a compaction that does not read `stalled`, which is one that
// may still end. `lanes.Conditions` says what stalled means, here as everywhere.
func (s *laneState) compactionHolds(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tracker.State() != lanes.Compacting {
		return false
	}
	return !slices.Contains(lanes.Conditions(lanes.Facts{
		State: lanes.Compacting, SinceProgress: now.Sub(s.progress), StallAfter: s.stallAfter, WaitDeclared: now.Before(s.waitUntil),
	}), lanes.Stalled)
}

// thresholds reads what a profile sets. A duration that does not parse leaves its check
// off, because a bad profile value must not stop a running lane.
func (s *laneState) thresholds(profile lanes.Profile, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallAfter, _ = time.ParseDuration(profile.Lifecycle.StallAfter)
	s.briefGrace, _ = time.ParseDuration(profile.Lifecycle.BriefGrace)
	s.policy = lanes.PolicyFor(profile.Context, role)
	s.wakeDebounce, s.wakeFloor = lanes.WakeBounds(profile.Lifecycle)
	window, _ := time.ParseDuration(profile.Lifecycle.RestartWindow)
	backoff, _ := time.ParseDuration(profile.Lifecycle.RestartBackoff)
	s.limits = lanes.Breaker{
		MaxRestarts: profile.Lifecycle.MaxRestarts, Window: window, MaxPerDay: profile.Lifecycle.MaxGenerationsPerDay, Backoff: backoff,
	}
	s.handoffLimit, _ = time.ParseDuration(profile.Lifecycle.HandoffTimeout)
	s.askLimit, _ = time.ParseDuration(profile.Lifecycle.AskWait)
	s.usageBase, _ = time.ParseDuration(profile.Lifecycle.UsageBackoff)
	s.drainLimit, _ = time.ParseDuration(profile.Lifecycle.DrainDeadline)
	s.approvalLimit, _ = time.ParseDuration(profile.Lifecycle.ApprovalWait)
}

// defaultWakeBounds batches the wakes of an orchestrator whose profile could not be read by the defaults.
// Bounds a profile named stay.
func (s *laneState) defaultWakeBounds() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wakeFloor == 0 {
		s.wakeDebounce, s.wakeFloor = lanes.WakeBounds(lanes.LifecycleSettings{})
	}
}

// wakeBounds reads the two settings that batch an orchestrator's wakes, once a profile named them or their
// defaults stand in for one that could not be read.
func (s *laneState) wakeBounds() (debounce, floor time.Duration, known bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wakeDebounce, s.wakeFloor, s.wakeFloor != 0
}

// usageBackoff reads how long a limit with no known reset waits first. Zero takes the default.
func (s *laneState) usageBackoff() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageBase
}

// waitForPerson marks the start and the end of a planning lane's wait for its person.
func (s *laneState) waitForPerson(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forPerson = on
}

// waitForWake marks the start and the end of an orchestrator's wait for something to judge, and says whether
// that changed what the lane says.
func (s *laneState) waitForWake(on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.forWake != on
	s.forWake = on
	return changed
}

// askWait reads a profile's limit. A value it cannot read takes the default, because a lane must
// not hold a process on a person's time.
func (s *laneState) askWait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.askLimit <= 0 {
		return defaultAskWait
	}
	return s.askLimit
}

// drainDeadline reads a profile's limit, the same way.
func (s *laneState) drainDeadline() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.drainLimit <= 0 {
		return defaultDrainDeadline
	}
	return s.drainLimit
}

// handoffTimeout reads a profile's limit. A value it cannot read takes the default, because a
// wait with no end would block a lane.
func (s *laneState) handoffTimeout() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handoffLimit <= 0 {
		return defaultHandoffTimeout
	}
	return s.handoffLimit
}

// noteHandoff keeps how the last handoff stands.
func (s *laneState) noteHandoff(degraded bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handoffDegraded = degraded
}

// current reads where the lane stands.
func (s *laneState) current() lanes.ActualState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tracker.State()
}

// newGeneration clears what one session left, and keeps what belongs to the lane. A session that
// goes on keeps the baseline it started with, because its next call holds the whole conversation.
func (s *laneState) newGeneration(now time.Time, baseline int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools, s.progress, s.since, s.context, s.baseline = map[string]string{}, now, now, 0, baseline
	s.unconfirmed, s.retried, s.baselined, s.stopLeftWork = false, false, false, false
	s.compact, s.refusedUntil, s.limitUnread = compaction{}, time.Time{}, unreadLimit{}
}

// noteCleared keeps when the clear that began this generation fired.
func (s *laneState) noteCleared(atMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearedMs = max(s.clearedMs, atMs)
}

// beforeClear says a hook fired before the clear that began this generation. It can reach the worker
// late and twice, from a spool that a clear overtook, and it says nothing about the session since. A
// hook with no time cannot be placed.
func (s *laneState) beforeClear(event lanes.HookEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return event.AtMs > 0 && event.AtMs < s.clearedMs
}

// noteContext keeps the context of one API call. The first of a session is what every path
// carries. It says what to record of a generation that has recorded nothing, and zero otherwise.
// The first call after a compaction's boundary is its verdict, because nothing else shows it worked.
func (s *laneState) noteContext(tokens int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.context = tokens
	if s.compact.pending && s.compact.seen {
		s.compact.pending, s.compact.seen, s.compact.failed = false, false, !s.policy.Compacted(tokens)
	}
	if s.baseline == 0 {
		s.baseline = tokens
	}
	if s.baselined {
		return 0
	}
	s.baselined = true
	return s.baseline
}

// contextRead reads what the session held at its last call, and at its first.
func (s *laneState) contextRead() (tokens, baseline int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.context, s.baseline
}

// keepContext goes on from what a journal held.
func (s *laneState) keepContext(progress lanes.Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.context, s.baseline = progress.ContextTokens, progress.BaselineTokens
	s.compact = compaction{asked: progress.Compactions, pending: progress.CompactPending, seen: progress.CompactSeen}
}

// journalContext writes what keepContext reads.
func (s *laneState) journalContext(progress *lanes.Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	progress.ContextTokens, progress.BaselineTokens = s.context, s.baseline
	progress.Compactions, progress.CompactPending, progress.CompactSeen = s.compact.asked, s.compact.pending, s.compact.seen
}

// callStarted keeps a call at work. A call a hook refused never ran, and nothing will say it ended.
func (s *laneState) callStarted(event lanes.HookEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Detail != lanes.DetailDenied {
		s.tools[event.Subject] = event.Digest
	}
}

func (s *laneState) callEnded(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tools, subject)
}

// callsEnded closes what a turn left open. A cancelled call and a deferred one report no end, and a
// turn's end is the end of its calls.
func (s *laneState) callsEnded() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.tools)
}

// refuseCalls ends the calls with one digest, and names them. A `PermissionRequest` names no call, so
// a digest is all that ties a refusal to the calls it ends.
func (s *laneState) refuseCalls(digest string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for subject, called := range s.tools {
		if called == digest {
			out = append(out, subject)
			delete(s.tools, subject)
		}
	}
	return out
}

// callRefused ends a call a worker refused. A refusal reaches no hook, so the journal keeps one a
// restart can read.
func (r *laneRun) callRefused(ctx context.Context, lane lanes.Record, digest string) {
	for _, subject := range r.state.refuseCalls(digest) {
		event := lanes.HookEvent{
			Lane: lane.ID, Generation: lane.Generation, Event: lanes.EventCallDenied, Subject: subject, AtMs: r.w.clock.Now().UnixMilli(),
		}
		if err := r.w.journal.MergeHook(context.WithoutCancel(ctx), event); err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		}
	}
}

// signal moves the lane, keeps where it stands, so an adopted run goes on from there, and wakes the worker to
// publish the move.
func (r *laneRun) signal(ctx context.Context, signals ...lanes.Signal) {
	r.signalAt(ctx, 0, signals...)
}

// signalAt is signal for a hook's signals, which carry the time the hook fired.
func (r *laneRun) signalAt(ctx context.Context, atMs int64, signals ...lanes.Signal) {
	s := r.state
	s.mu.Lock()
	moved := false
	for _, signal := range signals {
		if s.tracker.ApplyAt(signal, atMs) {
			moved = true
			s.since = r.w.clock.Now()
		}
	}
	state, from := s.tracker.State(), s.tracker.Left()
	s.mu.Unlock()
	if !moved {
		return
	}
	if err := r.w.journal.SaveActual(context.WithoutCancel(ctx), r.lease.Lane, state, from); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	r.w.laneMoved()
}

func (r *laneRun) progressed() {
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = r.w.clock.Now()
}

func (r *laneRun) noteModel(reach lanes.Reachability) {
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = reachSeen{reach: reach, at: r.w.clock.Now()}
}

func (r *laneRun) noteLedger(reach lanes.Reachability) {
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ledger = reachSeen{reach: reach, at: r.w.clock.Now()}
}

// observeEvent turns one driver event into what it says about the lane. Any evidence of
// activity moves a lane out of `briefing`, because some paths have no acknowledgement.
func (r *laneRun) observeEvent(ctx context.Context, event ports.DriverEvent) {
	s := r.state
	switch event.Kind {
	case ports.EventInputAdmitted:
		r.progressed()
		r.signal(ctx, lanes.OnActivity)
		r.noteInput(ctx, event.InputID)
	case ports.EventInputRefused:
		r.noteRefused(ctx, event.InputID)
	case ports.EventCompaction:
		s.noteBoundary()
		r.progressed()
		r.signal(ctx, lanes.OnActivity)
	case ports.EventSessionBound:
		r.progressed()
		r.signal(ctx, lanes.OnActivity)
	case ports.EventAgentUsage:
		r.measureAgent(event.Agent, event.Tokens)
	case ports.EventCost:
		// A usage report that shows what the session has spent so far, in the middle of a turn: the limit is held here,
		// so no tool call starts once the report shows it passed (NFR-COST-2).
		r.progressed()
		r.noteRunning(event.RunningUSD)
		r.evaluateSpend(ctx)
	case ports.EventUsage:
		r.progressed()
		_, waiting, _ := s.compactionAsked()
		baseline := s.noteContext(event.ContextTokens)
		r.measureContext(event.ContextTokens, baseline)
		r.noteModel(lanes.Reachable)
		r.signal(ctx, lanes.OnActivity)
		r.recordBaseline(ctx, baseline)
		// A figure can come after the turn it belongs to, and no save follows that.
		if _, pending, _ := s.compactionAsked(); waiting && !pending {
			r.saveProgress(ctx, r.cursor)
		}
		// Each model call reports its usage, and a call whose cost cannot be priced reports no running total, so no cost
		// event shows the lane its limit passed. The lane's limits are read here as they are at a cost report, and a lane
		// that began to stop at one has no use for a handoff of its context.
		if r.evaluateSpend(ctx) {
			return
		}
		r.evaluateContext(ctx)
	case ports.EventTurnEnded:
		r.progressed()
		if event.Turn.Status == ports.TurnFailed {
			r.noteModel(lanes.Impaired)
		} else {
			r.noteModel(lanes.Reachable)
		}
		r.turnEndedWithRequests(ctx, event.Turn)
		s.callsEnded()
		r.signal(ctx, lanes.OnActivity, lanes.OnCompactionFailed, lanes.OnTurnFinished)
		if event.Turn.Status == ports.TurnCompleted {
			r.limitPassed(ctx)
		}
	case ports.EventRetry:
		r.noteModel(lanes.Impaired)
		r.noteRetry(ctx, event)
	case ports.EventLimit:
		if event.Detail != "" && event.Detail != "allowed" {
			r.noteModel(lanes.Impaired)
		}
		if event.Usage != nil {
			r.noteUsage(ctx, *event.Usage)
		}
	}
}

// observeHook turns one hook event into what it says about the lane. A tool call that has
// started and not finished is a wait, not a stall.
func (r *laneRun) observeHook(ctx context.Context, event lanes.HookEvent) {
	r.observe(ctx, event, false)
}

// observeReplayedHook reads a hook a journal kept while no worker listened. It happened before this worker
// started, so it says nothing of the sign-in or the model's reach now: a failure that was a usage limit then
// would otherwise start a new limit at every restart. One no run has heard is noted as it arrives
// (noteUnheard).
func (r *laneRun) observeReplayedHook(ctx context.Context, event lanes.HookEvent) {
	r.observe(ctx, event, true)
}

// observe is what both read: a replayed hook changes no state a worker keeps for the lanes together.
func (r *laneRun) observe(ctx context.Context, event lanes.HookEvent, replayed bool) {
	s := r.state
	if s.beforeClear(event) {
		return
	}
	switch event.Event {
	case hookPreCompact:
		r.progressed()
		r.signalAt(ctx, event.AtMs, lanes.OnCompactionStarted)
	case hookPostCompact:
		r.progressed()
		r.signalAt(ctx, event.AtMs, lanes.OnCompactionFinished)
	case hookSessionStart:
		if event.Detail == lanes.SourceClear {
			r.cleared(ctx, event.SessionID, event.AtMs)
		}
	case hookPreToolUse:
		r.progressed()
		r.askToCheckSpend()
		s.callStarted(event)
		r.signal(ctx, lanes.OnActivity, lanes.OnCompactionFailed)
	case hookPostToolUse, lanes.EventToolFailed:
		r.progressed()
		s.callEnded(event.Subject)
		s.waitAnswered(event.AtMs)
		if event.Digest != "" {
			r.terminalAnswer(ctx, event.Digest, noteToolRan)
		}
	case lanes.EventToolDenied:
		r.progressed()
		s.callEnded(event.Subject)
		s.waitAnswered(event.AtMs)
	case lanes.EventCallDenied:
		s.callEnded(event.Subject)
	case lanes.EventStop:
		s.noteStop(event.Background, r.w.clock.Now(), event.AtMs)
	case lanes.EventUserPromptSubmit:
		s.notePrompt(event.AtMs)
	case hookStopFailure:
		s.noteStopFailure()
		if replayed {
			return
		}
		r.noteModel(lanes.ReachOfFailure(event.Detail))
		r.noteFailure(ctx, event.Detail)
	}
}

// noteStop keeps what the turn that just stopped left running in the background, and when the worker heard it.
// A Stop no newer than the last says nothing, and one older than the newest prompt left a wait that is over.
func (s *laneState) noteStop(tasks int, at time.Time, atMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atMs > 0 && atMs <= s.stoppedMs {
		return
	}
	s.stoppedMs, s.stopLeftWork = max(s.stoppedMs, atMs), tasks > 0
	if atMs > 0 && atMs < s.promptedMs {
		tasks = 0
	}
	s.background, s.backgroundSince = tasks, at
}

// notePrompt ends the wait on background work. A prompt older than the newest Stop began the turn that stopped,
// and ends none.
func (s *laneState) notePrompt(atMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.promptedMs = max(s.promptedMs, atMs)
	if atMs > 0 && atMs < s.stoppedMs {
		return
	}
	s.background, s.backgroundSince, s.refusedUntil = 0, time.Time{}, time.Time{}
	if atMs > s.limitUnread.atMs {
		s.limitUnread = unreadLimit{}
	}
}

// keepUnreadLimit keeps what an adoption found, and a zero one ends it.
func (s *laneState) keepUnreadLimit(limit unreadLimit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limitUnread = limit
}

// adoptedLimit reads it.
func (s *laneState) adoptedLimit() unreadLimit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limitUnread
}

// noteRefusal keeps until when a lane waits on the nudge a wait refused, and zero ends that wait.
func (s *laneState) noteRefusal(until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refusedUntil = until
}

// refusal reads that wait.
func (s *laneState) refusal() (until time.Time, waiting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refusedUntil, !s.refusedUntil.IsZero()
}

// waitAnswered ends that wait at a call's end. The lane still reads idle in the turn an answer begins, so this
// and a prompt are what say it works again. A call that ended before the newest Stop is of the turn that stopped.
func (s *laneState) waitAnswered(atMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if atMs > 0 && atMs < s.stoppedMs {
		return
	}
	s.refusedUntil = time.Time{}
}

// noteStopFailure says a failed turn left nothing to wait on.
func (s *laneState) noteStopFailure() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.background, s.backgroundSince, s.stopLeftWork = 0, time.Time{}, false
}

// takeStopLeftWork says the turn whose end is being decided left background work, and is true for one end.
func (s *laneState) takeStopLeftWork() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	left := s.stopLeftWork
	s.stopLeftWork = false
	return left
}

// backgroundRest says the session's last turn stopped with its own background work still running, since when,
// and how many tasks, so the lane's work is not over: the session goes on in the turn that work's end starts.
func (s *laneState) backgroundRest() (since time.Time, tasks int, resting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backgroundSince, s.background, s.background > 0
}

// settleBrief moves a lane whose brief has shown no evidence past its grace.
func (r *laneRun) settleBrief(ctx context.Context) {
	s := r.state
	s.mu.Lock()
	late := s.tracker.State() == lanes.Briefing && s.briefGrace > 0 && r.w.clock.Now().Sub(s.since) >= s.briefGrace
	if late {
		s.unconfirmed = true
	}
	s.mu.Unlock()
	if late {
		r.signal(ctx, lanes.OnBriefGrace)
	}
}

// status reads the lane. Only the driver's answer speaks to liveness, and a failed read
// is unknown, never dead.
func (r *laneRun) status(ctx context.Context) ledger.LaneStatus {
	r.settleBrief(ctx)
	r.retryBrief(ctx)
	liveness := lanes.LivenessUnknown
	if handle := r.sessionHandle(); handle.ID != "" {
		observation, err := r.rt.Driver.Observe(ctx, handle)
		if err == nil {
			liveness = observation.Liveness
		}
	}
	// The process uses the surfaces it started with, so the condition it launched with stands, and a
	// probe's result for its version can add to it and never clears it. While the probe runs, the lane
	// names it.
	version, surfaces := r.runVersion()
	check, held, holds := r.w.qualificationOf(version, surfaces)
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	degraded := s.degraded || (holds && held.Degraded)
	now := r.w.clock.Now()
	state := s.tracker.State()
	facts := lanes.Facts{
		State: state, SinceProgress: now.Sub(s.progress), StallAfter: s.stallAfter, ToolInFlight: len(s.tools) != 0,
		WaitDeclared: now.Before(s.waitUntil), PastDeadline: r.lease.Deadline != 0 && now.UnixMilli() > r.lease.Deadline,
		ContextTokens: s.context, WarnTokens: s.policy.WarnTokens, BriefUnconfirmed: s.unconfirmed, Degraded: degraded,
		UnpushedWork: s.unpushed, HandoffDegraded: s.handoffDegraded, PackOversized: s.policy.Oversized(s.baseline),
		DayCapped: s.dayCapped, WaitingForPerson: s.forPerson && state == lanes.Idle, AwaitingWake: s.forWake,
		BackgroundInFlight: s.background > 0 && state == lanes.Idle,
	}
	out := ledger.LaneStatus{
		ActualState: string(state), Liveness: string(liveness),
		ModelReach:  string(lanes.ReadReach(state, evidenceOf(s.model, now), reachWindow)),
		LedgerReach: string(lanes.ReadReach(state, evidenceOf(s.ledger, now), reachWindow)),
		Checking:    check,
		Surfaces:    surfaces,
	}
	for _, condition := range lanes.Conditions(facts) {
		out.Conditions = append(out.Conditions, string(condition))
	}
	return out
}

func evidenceOf(seen reachSeen, now time.Time) lanes.ReachEvidence {
	return lanes.ReachEvidence{Reach: seen.reach, Age: now.Sub(seen.at), Seen: !seen.at.IsZero()}
}

func sameStatus(a, b ledger.LaneStatus) bool {
	return a.ActualState == b.ActualState && a.Liveness == b.Liveness && a.ModelReach == b.ModelReach &&
		a.LedgerReach == b.LedgerReach && slices.Equal(a.Conditions, b.Conditions) && ledger.SameCheck(a.Checking, b.Checking) && a.Surfaces == b.Surfaces
}

// Publish journals what changed about each lane, for the next upload. A status travels on
// change, because the one write a heartbeat makes is the lease, and that lease is what
// keeps a reported value observed.
func (w *Worker) Publish(ctx context.Context) error {
	if w.config.Lanes == nil {
		return nil
	}
	records, err := w.journal.Lanes(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		w.mu.Lock()
		run, held := w.running[record.ID]
		w.mu.Unlock()
		// A woken lane the day stopped stands parked in the journal, under the claim that parked it,
		// until a person lets it go on, and that person needs to read why.
		stopped := held && record.State == lanes.StateParked && run.state.current() == lanes.Tripped
		if stopped {
			record = run.record()
		}
		if record.State != lanes.StateRunning {
			continue
		}
		status := ledger.LaneStatus{
			ActualState: string(record.Actual), Liveness: string(lanes.LivenessUnknown),
			ModelReach: string(lanes.ReachUnknown), LedgerReach: string(lanes.ReachUnknown),
		}
		if held {
			status = run.status(ctx)
			// The stamp is read after the status, and the record above may be older than
			// both. A status under a generation the ledger has moved past is refused, and
			// a refused status is never sent again, because it reads here as sent.
			record.Generation = run.currentGeneration()
		}
		if err := w.publishOne(ctx, record, status); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) publishOne(ctx context.Context, record lanes.Record, status ledger.LaneStatus) error {
	w.mu.Lock()
	last, sent := w.published[record.ID]
	w.mu.Unlock()
	if sent && last.generation == record.Generation && sameStatus(last.status, status) {
		return nil
	}
	payload, err := json.Marshal(status)
	if err != nil {
		return err
	}
	if _, err := w.Record(ctx, w.event(ledger.UploadLaneStatus, record, payload)); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.published[record.ID] = publishedStatus{generation: record.Generation, status: status}
	return nil
}

// publishedStatus is the last thing a worker said of a lane. A new generation has said nothing, and
// the ledger reads it as starting until it does, so a status counts as sent for one generation.
type publishedStatus struct {
	generation int64
	status     ledger.LaneStatus
}
