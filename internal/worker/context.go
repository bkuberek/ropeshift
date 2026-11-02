package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
)

// The inputs the context policy sends, one of each per compaction.
const (
	compactLabel   = "compact/"
	compactedLabel = "compacted/"
)

// contextInstruction names what a running turn is told, in the line a hook reads.
const contextInstruction = "context"

// compaction is how this generation's last compaction stands. Pending holds from the request to the
// verdict, and seen says its boundary came, which alone makes a usage report evidence.
type compaction struct {
	asked   int64
	pending bool
	seen    bool
	failed  bool
}

// contextAct reads the policy against where the lane stands.
func (s *laneState) contextAct() lanes.ContextAct {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.policy.Decide(lanes.ContextFacts{
		State: s.tracker.State(), Tokens: s.context, Baseline: s.baseline, CanCompact: s.canCompact,
		CompactPending: s.compact.pending, CompactFailed: s.compact.failed,
	})
}

// noteCapabilities keeps what the policy reads of a driver, for the version this session runs.
func (s *laneState) noteCapabilities(capabilities ports.Capabilities) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canCompact = capabilities.Compact == ports.CompactNative
}

func (s *laneState) askCompaction() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compact.asked++
	s.compact.pending, s.compact.seen = true, false
	return s.compact.asked
}

// compactionAsked reads a compaction still waiting for its verdict.
func (s *laneState) compactionAsked() (n int64, pending, seen bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.compact.asked, s.compact.pending, s.compact.seen
}

// endCompaction closes a compaction with no call to judge it. Failed says the lane must not try
// another, and a compaction that could not start now says nothing about one that can later.
func (s *laneState) endCompaction(failed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compact.pending, s.compact.seen, s.compact.failed = false, false, failed
}

// noteBoundary keeps the one evidence that a compaction ran. One nobody asked for is not this
// policy's to judge, because the thresholds read its next call all the same.
func (s *laneState) noteBoundary() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.compact.pending {
		s.compact.seen = true
	}
}

// evaluateContext holds one usage report, or a lane at rest, to its role's thresholds, and says
// whether it acted. It runs on the goroutine that reads the session. A lane on its way out or under
// a handoff reads as neither `idle` nor `working`, so only an outcome is left to look for: a lane
// that reported goes on calling the model until its turn ends.
func (r *laneRun) evaluateContext(ctx context.Context) bool {
	act := r.state.contextAct()
	if act == lanes.ContextWait {
		return false
	}
	if _, reported := r.reported(ctx); reported {
		return false
	}
	if act == lanes.ContextCompact {
		return r.requestCompact(ctx)
	}
	return r.contextHandoff(ctx)
}

// contextHandoff starts what the policy reads as most expensive. Which path the instruction takes
// follows from where the lane stands, and a lane that moved on stays for the next report.
func (r *laneRun) contextHandoff(ctx context.Context) bool {
	err := r.w.HandOff(ctx, r.lease.Lane, lanes.HandoffContextThreshold)
	if err != nil && !errors.Is(err, ErrNoHandoff) {
		r.w.config.Report(fmt.Errorf("lane %s: a context handoff: %w", r.lease.Lane, err))
	}
	return err == nil
}

// requestCompact asks for a compaction, which runs as a turn of its own. A kill before the turn that
// led here is saved reads that turn again, which finds the request's effect and hands off instead,
// because nothing can say whether a compaction ran that no boundary has shown yet.
func (r *laneRun) requestCompact(ctx context.Context) bool {
	n := r.state.askCompaction()
	handle := r.sessionHandle()
	effect := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.currentGeneration(), "", compactionKey(compactLabel, n))
	ran, err := r.w.bracket(ctx, effect, func() error { return r.rt.Driver.Compact(ctx, handle) })
	crashpoint.Hit("worker.context", "compacting")
	if ran && err == nil {
		r.signal(ctx, lanes.OnInputHanded)
		return true
	}
	if errors.Is(err, ports.ErrAwaitingAnswer) {
		r.state.endCompaction(false)
		return false
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: a compaction: %w", r.lease.Lane, err))
	}
	r.state.endCompaction(true)
	return r.evaluateContext(ctx)
}

// contextAtRest reads a lane whose turn ended with nothing else to do, and says whether a new turn
// is on its way.
func (r *laneRun) contextAtRest(ctx context.Context) bool {
	if _, pending, _ := r.state.compactionAsked(); pending {
		return r.afterCompaction(ctx)
	}
	return r.evaluateContext(ctx)
}

// anotherCompaction says a turn held a compaction this lane did not ask for, or one it has already
// gone on from. A person runs one at a pane, in a lane at rest or with input on its way, and that
// ended no work: reading it as a turn's end would nudge the lane, or end it with no outcome.
func (r *laneRun) anotherCompaction(ctx context.Context, event ports.DriverEvent) bool {
	if event.Kind != ports.EventTurnEnded || !event.Turn.Compaction {
		return false
	}
	n, pending, _ := r.state.compactionAsked()
	if !pending {
		return true
	}
	told := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.currentGeneration(), "", compactionKey(compactedLabel, n))
	_, err := r.w.journal.Effect(ctx, told.Key)
	return err == nil
}

// afterCompaction settles a turn that followed a request. A turn with no boundary did not compact.
// One with a boundary proves nothing yet, and a compaction starts no turn, so the session is told to
// go on, and the first call of that turn is the verdict. A session already told, which is one whose
// figure comes after its turn ended, stays pending: it rests like any lane with no outcome, and the
// next figure judges it. A session that cannot be told gives no next call, so that failed.
func (r *laneRun) afterCompaction(ctx context.Context) bool {
	n, _, seen := r.state.compactionAsked()
	if !seen {
		r.state.endCompaction(true)
		return r.evaluateContext(ctx)
	}
	effect := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.currentGeneration(), "", compactionKey(compactedLabel, n))
	ran, err := r.w.bracket(ctx, effect, func() error {
		_, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: lanes.AfterCompaction})
		return err
	})
	crashpoint.Hit("worker.context", "continued")
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: after a compaction: %w", r.lease.Lane, err))
		r.state.endCompaction(true)
		return r.evaluateContext(ctx)
	}
	if ran {
		r.signal(ctx, lanes.OnInputHanded)
	}
	return ran
}

// instructionID names what this generation's running turn is told.
func (r *laneRun) instructionID() string {
	return messages.DerivedID(contextInstruction, r.lease.Lane, strconv.FormatInt(r.currentGeneration(), 10))
}

// sendInstruction takes the path that reaches a running turn. A session with none is told by its
// hold, and stdin keeps the instruction for the end of the turn, for a turn that calls no tool.
func (r *laneRun) sendInstruction(ctx context.Context, midTurn bool) error {
	handle := r.sessionHandle()
	if midTurn {
		id := r.instructionID()
		_, err := r.rt.Driver.Send(ctx, handle, ports.Input{ID: messages.InputID(id), Text: lanes.MidTurnInstruction(id), MidTurn: true})
		if !errors.Is(err, ports.ErrNoChannel) {
			return err
		}
	}
	_, err := r.rt.Driver.Send(ctx, handle, ports.Input{Text: lanes.HandoffInstruction()})
	return err
}

// holdTools arms the denial before the instruction leaves, so no tool call can pass between them.
func (r *laneRun) holdTools(ctx context.Context) {
	if holdsNoTools() {
		return
	}
	s := r.state
	s.mu.Lock()
	s.held = true
	s.mu.Unlock()
	if err := r.rt.Driver.HoldTools(ctx, r.sessionHandle(), lanes.ReasonHandOff); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: holding its tools: %w", r.lease.Lane, err))
	}
	crashpoint.Hit("worker.context", "held")
}

// liftHold ends the denial once the session took what it was told. Both a stream and a hook can say
// so, and the first is enough.
func (r *laneRun) liftHold(ctx context.Context) {
	s := r.state
	s.mu.Lock()
	held := s.held
	s.held = false
	s.mu.Unlock()
	if !held {
		return
	}
	if err := r.rt.Driver.HoldTools(ctx, r.sessionHandle(), ""); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: lifting its hold: %w", r.lease.Lane, err))
	}
}
