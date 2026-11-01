package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

// reachWindow is how long a piece of reachability evidence stays fresh.
const reachWindow = time.Minute

// roleLane names a lane's thresholds in a profile.
const roleLane = "lane"

// The hooks that say something about a lane's state.
const (
	hookPreCompact  = "PreCompact"
	hookPostCompact = "PostCompact"
	hookPreToolUse  = "PreToolUse"
	hookPostToolUse = "PostToolUse"
	hookStopFailure = "StopFailure"
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
	tools    map[string]bool
	context  int64
	since    time.Time
	model    reachSeen
	ledger   reachSeen

	unconfirmed bool
	retried     bool
	degraded    bool
	unpushed    bool
	warnTokens  int64
	stallAfter  time.Duration
	briefGrace  time.Duration
	waitUntil   time.Time
	limits      lanes.Breaker
	// handoffLimit is how long an agent has to write its part of a handoff, and
	// handoffDegraded says the last handoff has only the worker's part. That stays until a
	// later handoff has both, because it is what this generation was told.
	handoffLimit    time.Duration
	handoffDegraded bool
	// askLimit is how long a lane rests on a blocking question, and drainLimit how long a
	// draining fleet lets a turn run.
	askLimit   time.Duration
	drainLimit time.Duration
}

func newLaneState(start lanes.ActualState, now time.Time) *laneState {
	return &laneState{tracker: lanes.NewTracker(start), tools: map[string]bool{}, progress: now, since: now}
}

// thresholds reads what a profile sets. A duration that does not parse leaves its check
// off, because a bad profile value must not stop a running lane.
func (s *laneState) thresholds(profile lanes.Profile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stallAfter, _ = time.ParseDuration(profile.Lifecycle.StallAfter)
	s.briefGrace, _ = time.ParseDuration(profile.Lifecycle.BriefGrace)
	s.warnTokens = profile.Context.Roles[roleLane].WarnTokens
	window, _ := time.ParseDuration(profile.Lifecycle.RestartWindow)
	backoff, _ := time.ParseDuration(profile.Lifecycle.RestartBackoff)
	s.limits = lanes.Breaker{
		MaxRestarts: profile.Lifecycle.MaxRestarts, Window: window, MaxPerDay: profile.Lifecycle.MaxGenerationsPerDay, Backoff: backoff,
	}
	s.handoffLimit, _ = time.ParseDuration(profile.Lifecycle.HandoffTimeout)
	s.askLimit, _ = time.ParseDuration(profile.Lifecycle.AskWait)
	s.drainLimit, _ = time.ParseDuration(profile.Lifecycle.DrainDeadline)
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

// newGeneration clears what one session left, and keeps what belongs to the lane.
func (s *laneState) newGeneration(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools, s.progress, s.since, s.context = map[string]bool{}, now, now, 0
	s.unconfirmed, s.retried = false, false
}

// signal moves the lane and keeps where it stands, so an adopted run goes on from there.
func (r *laneRun) signal(ctx context.Context, signals ...lanes.Signal) {
	s := r.state
	s.mu.Lock()
	moved := false
	for _, signal := range signals {
		if s.tracker.Apply(signal) {
			moved = true
			s.since = r.w.clock.Now()
		}
	}
	state := s.tracker.State()
	s.mu.Unlock()
	if !moved {
		return
	}
	if err := r.w.journal.SaveActual(context.WithoutCancel(ctx), r.lease.Lane, state); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
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
	case ports.EventSessionBound, ports.EventInputAdmitted, ports.EventCompaction:
		r.progressed()
		r.signal(ctx, lanes.OnActivity)
	case ports.EventUsage:
		r.progressed()
		s.mu.Lock()
		s.context = event.ContextTokens
		s.mu.Unlock()
		r.noteModel(lanes.Reachable)
		r.signal(ctx, lanes.OnActivity)
	case ports.EventTurnEnded:
		r.progressed()
		if event.Turn.Status == ports.TurnFailed {
			r.noteModel(lanes.Impaired)
		} else {
			r.noteModel(lanes.Reachable)
		}
		r.signal(ctx, lanes.OnActivity, lanes.OnTurnFinished)
	case ports.EventRetry:
		r.noteModel(lanes.Impaired)
	case ports.EventLimit:
		if event.Detail != "" && event.Detail != "allowed" {
			r.noteModel(lanes.Impaired)
		}
	}
}

// observeHook turns one hook event into what it says about the lane. A tool call that has
// started and not finished is a wait, not a stall.
func (r *laneRun) observeHook(ctx context.Context, event lanes.HookEvent) {
	s := r.state
	switch event.Event {
	case hookPreCompact:
		r.signal(ctx, lanes.OnCompactionStarted)
	case hookPostCompact:
		r.signal(ctx, lanes.OnCompactionFinished)
	case hookPreToolUse:
		r.progressed()
		s.mu.Lock()
		s.tools[event.Subject] = true
		s.mu.Unlock()
		r.signal(ctx, lanes.OnActivity)
	case hookPostToolUse:
		r.progressed()
		s.mu.Lock()
		delete(s.tools, event.Subject)
		s.mu.Unlock()
	case hookStopFailure:
		r.noteModel(lanes.ReachOfFailure(event.Detail))
	}
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
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	now := r.w.clock.Now()
	state := s.tracker.State()
	facts := lanes.Facts{
		State: state, SinceProgress: now.Sub(s.progress), StallAfter: s.stallAfter, ToolInFlight: len(s.tools) != 0,
		WaitDeclared: now.Before(s.waitUntil), PastDeadline: r.lease.Deadline != 0 && now.UnixMilli() > r.lease.Deadline,
		ContextTokens: s.context, WarnTokens: s.warnTokens, BriefUnconfirmed: s.unconfirmed, Degraded: s.degraded,
		UnpushedWork: s.unpushed, HandoffDegraded: s.handoffDegraded,
	}
	out := ledger.LaneStatus{
		ActualState: string(state), Liveness: string(liveness),
		ModelReach:  string(lanes.ReadReach(state, evidenceOf(s.model, now), reachWindow)),
		LedgerReach: string(lanes.ReadReach(state, evidenceOf(s.ledger, now), reachWindow)),
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
		a.LedgerReach == b.LedgerReach && slices.Equal(a.Conditions, b.Conditions)
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
		if record.State != lanes.StateRunning {
			continue
		}
		status := ledger.LaneStatus{
			ActualState: string(record.Actual), Liveness: string(lanes.LivenessUnknown),
			ModelReach: string(lanes.ReachUnknown), LedgerReach: string(lanes.ReachUnknown),
		}
		w.mu.Lock()
		run, held := w.running[record.ID]
		w.mu.Unlock()
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
	if sent && sameStatus(last, status) {
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
	w.published[record.ID] = status
	return nil
}
