package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

const (
	// guardAtHandoff is the guard pass that saves a worktree for the next generation.
	guardAtHandoff = "handoff"
	// handoffInput names the one instruction of a generation's handoff.
	handoffInput = "handoff"
	// defaultHandoffTimeout is what a profile that sets nothing it can read gets, because a
	// wait with no end would block a lane.
	defaultHandoffTimeout = 3 * time.Minute
	// laneFactsDeadline bounds one read of the ledger, which a handoff does without.
	laneFactsDeadline = 3 * time.Second
)

// ErrNoHandoff means a lane is not at a point where it can hand off.
var ErrNoHandoff = errors.New("this lane cannot hand off now")

// handoffFlight is a handoff this run is following. Its progress mirrors the journal, and the
// lock keeps a late narrative from crossing the document that goes out without it.
type handoffFlight struct {
	mu       sync.Mutex
	progress lanes.HandoffProgress
}

func (f *handoffFlight) read() lanes.HandoffProgress {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.progress
}

// HandOff starts the handoff contract on a lane this worker runs (FR-LANE-9). It returns once
// the lane is `handing_off`, and the lane's run sees it through to the next generation.
func (w *Worker) HandOff(ctx context.Context, lane, reason string) error {
	run := w.runOf(lane)
	if run == nil {
		return fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
	}
	return run.beginHandoff(ctx, reason)
}

// beginHandoff keeps the deadline before the lane reads `handing_off`, so whoever sees that
// state and looks at a clock looks past the moment the deadline counts from.
func (r *laneRun) beginHandoff(ctx context.Context, reason string) error {
	r.flightLock.Lock()
	defer r.flightLock.Unlock()
	if r.flight != nil || r.fenced.Load() || r.sessionOver.Load() {
		return fmt.Errorf("%w: lane %s has one under way, lost its lease, or has no session", ErrNoHandoff, r.lease.Lane)
	}
	if _, moves := lanes.Next(r.state.current(), lanes.OnHandoffThreshold); !moves {
		return fmt.Errorf("%w: lane %s is %s", ErrNoHandoff, r.lease.Lane, r.state.current())
	}
	progress := lanes.HandoffProgress{Reason: reason, DeadlineMs: r.w.clock.Now().Add(r.state.handoffTimeout()).UnixMilli()}
	if err := r.w.journal.SaveHandoff(ctx, r.lease.Lane, &progress); err != nil {
		return err
	}
	crashpoint.Hit("worker.handoff", "begun")
	r.signal(ctx, lanes.OnHandoffThreshold)
	if r.state.current() != lanes.HandingOff {
		if err := r.w.journal.SaveHandoff(ctx, r.lease.Lane, nil); err != nil {
			return err
		}
		return fmt.Errorf("%w: lane %s moved on", ErrNoHandoff, r.lease.Lane)
	}
	r.startFlight(progress)
	return nil
}

// startFlight follows a handoff on a goroutine of its own, because the run's own goroutine
// stays on the session's stream. The caller holds flightLock.
func (r *laneRun) startFlight(progress lanes.HandoffProgress) {
	r.flight = &handoffFlight{progress: progress}
	r.sequence.Go(func() { r.runHandoff(r.life) })
}

// currentFlight is the handoff under way, or nil.
func (r *laneRun) currentFlight() *handoffFlight {
	r.flightLock.Lock()
	defer r.flightLock.Unlock()
	return r.flight
}

// resumeHandoff goes on with a handoff an earlier process left, from what the journal holds.
func (r *laneRun) resumeHandoff(ctx context.Context) {
	if !r.left.Open() {
		return
	}
	r.flightLock.Lock()
	defer r.flightLock.Unlock()
	if r.flight != nil || r.parking.Open() {
		return
	}
	r.signal(ctx, lanes.OnHandoffThreshold)
	r.startFlight(*r.left)
}

// finishHandoff settles a handoff whose session went with the worker that followed it. There
// is nothing to read and nothing to stop, so what is left is the failure edge.
func (r *laneRun) finishHandoff(ctx context.Context) error {
	r.loadProfile(ctx)
	r.sessionOver.Store(true)
	r.resumeHandoff(ctx)
	return r.conclude(ctx, r.unattended(ctx))
}

// abandons says the lane has no next generation to hand to, or that this process is leaving,
// and the journal keeps what the next one needs.
func (r *laneRun) abandons(ctx context.Context) bool {
	if ctx.Err() != nil || r.fenced.Load() {
		return true
	}
	_, reported := r.reported(ctx)
	return reported
}

// runHandoff walks the contract's steps. Each one repeats safely from what the journal holds,
// and every failure ends in a document with no narrative, so a handoff can degrade but it
// cannot block.
func (r *laneRun) runHandoff(ctx context.Context) {
	flight := r.currentFlight()
	if len(flight.read().Document) == 0 || storesAgain() {
		r.requestNarrative(ctx, flight)
		crashpoint.Hit("worker.handoff", "requested")
		r.awaitNarrative(ctx, flight)
		if r.abandons(ctx) {
			return
		}
		crashpoint.Hit("worker.handoff", "awaited")
		r.storeHandoff(ctx, flight)
	}
	if r.abandons(ctx) {
		return
	}
	r.signal(ctx, lanes.OnHandoffWritten)
	if r.sessionOver.Load() {
		return
	}
	how := ports.StopAtRest
	if flight.read().Narrative == nil {
		how = ports.StopInterrupt
	}
	r.stop(ctx, how)
}

// requestNarrative sends the instruction, at most once for a generation. One that cannot be
// sent ends the wait, because no answer can come.
func (r *laneRun) requestNarrative(ctx context.Context, flight *handoffFlight) {
	effect := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, r.currentGeneration(), "", instructionLabel())
	_, err := r.w.bracket(ctx, effect, func() error {
		_, err := r.rt.Driver.Send(ctx, r.sessionHandle(), ports.Input{Text: lanes.HandoffInstruction()})
		return err
	})
	if err == nil {
		return
	}
	r.w.config.Report(fmt.Errorf("lane %s: the handoff's instruction: %w", r.lease.Lane, err))
	flight.mu.Lock()
	defer flight.mu.Unlock()
	flight.progress.Refused = err.Error()
}

// awaitNarrative waits for the agent's part, on the worker's clock, and ends on what makes
// that part impossible.
func (r *laneRun) awaitNarrative(ctx context.Context, flight *handoffFlight) {
	deadline := time.UnixMilli(flight.read().DeadlineMs)
	for r.w.clock.Now().Before(deadline) {
		progress := flight.read()
		if progress.Narrative != nil || progress.Refused != "" || r.sessionOver.Load() || r.fenced.Load() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(superviseEvery):
		}
	}
}

// storeHandoff is the contract's third and fourth steps: the worker writes what it can
// determine, validates, and keeps the document and its upload in one journal write.
func (r *laneRun) storeHandoff(ctx context.Context, flight *handoffFlight) {
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	guard, err := r.w.guardPass(ctx, record, false, guardAtHandoff)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	crashpoint.Hit("worker.handoff", "guarded")
	flight.mu.Lock()
	defer flight.mu.Unlock()
	doc := lanes.Handoff{
		Schema: lanes.HandoffSchema, Lane: record.ID, Generation: record.Generation, Reason: flight.progress.Reason,
		Deterministic: r.handoffFacts(ctx, record, guard), Narrative: flight.progress.Narrative,
	}
	body, digest, err := doc.Seal()
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		doc.Narrative = nil
		if body, digest, err = doc.Seal(); err != nil {
			r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
			return
		}
	}
	payload, err := json.Marshal(ledger.HandoffUpload{Body: body, Digest: digest, Degraded: doc.Degraded()})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	progress := flight.progress
	progress.Document, progress.Digest = body, digest
	if err := r.keepHandoff(ctx, record, progress, payload); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	flight.progress = progress
	r.state.noteHandoff(doc.Degraded())
	crashpoint.Hit("worker.handoff", "stored")
	if err := r.w.Upload(ctx); err != nil {
		r.w.config.Report(err)
	}
}

// keepHandoff is one journal write, because a document with no upload on its way would read
// as stored and never reach the ledger.
func (r *laneRun) keepHandoff(ctx context.Context, record lanes.Record, progress lanes.HandoffProgress, payload []byte) error {
	event := r.w.event(ledger.UploadHandoffStored, record, payload)
	if !splitsHandoff() {
		_, err := r.w.journal.StoreHandoff(ctx, record.ID, progress, event)
		return err
	}
	if err := r.w.journal.SaveHandoff(ctx, record.ID, &progress); err != nil {
		return err
	}
	crashpoint.Hit("worker.handoff", "split")
	_, err := r.w.journal.Append(ctx, event)
	return err
}

// handoffFacts is the part the worker determines. What it cannot read stays out, because a
// handoff cannot wait for a machine or a control plane to come back.
func (r *laneRun) handoffFacts(ctx context.Context, record lanes.Record, guard lanes.GuardResult) lanes.HandoffFacts {
	facts := lanes.HandoffFacts{WorkOrder: record.WorkOrder, SnapshotRef: guard.Ref, ModelResolved: r.resolvedModel()}
	if r.lease.Contract != "" {
		facts.Contract = &lanes.ContractRef{ID: r.lease.Contract, Version: r.lease.ContractVersion}
	}
	facts.ContextPack = r.readPack()
	found, err := r.rt.Provisioner.Worktrees.Survey(ctx, record.Worktree)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: the handoff's survey: %w", record.ID, err))
	}
	facts.Branch, facts.Head, facts.DirtyPaths, facts.UnpushedCommits = found.Branch, found.Head, found.DirtyPaths, found.Ahead
	bounded, cancel := context.WithTimeout(ctx, laneFactsDeadline)
	defer cancel()
	held, err := r.w.link.LaneFacts(bounded, ledger.LaneFactsRequest{
		Auth: r.w.Auth(), Epoch: r.w.epoch(), WorkOrder: record.WorkOrder, Fence: record.Fence, Lane: record.ID,
	})
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: the handoff's ledger facts: %w", record.ID, err))
		return facts
	}
	facts.OpenQuestions, facts.OpenAssumptions, facts.Checks = held.OpenQuestions, held.OpenAssumptions, []lanes.CheckFact{}
	for _, check := range held.Checks {
		facts.Checks = append(facts.Checks, lanes.CheckFact{Name: check.Name, Status: check.Status})
	}
	return facts
}

// handedOff waits for a handoff in flight to finish, and says a next generation follows it.
func (r *laneRun) handedOff(ctx context.Context) bool {
	flight := r.currentFlight()
	if flight == nil || r.parkingOf().Open() {
		return false
	}
	r.sequence.Wait()
	r.stopped.Wait()
	return !r.abandons(ctx) && len(flight.read().Document) != 0
}

// handoffGeneration begins what a handoff leads to. It counts toward the day's cap on
// generations and asks nothing of the breaker, because a handoff cannot block. A drain lets a
// lane end by doing its work, and this is that work going on.
func (r *laneRun) handoffGeneration(ctx context.Context) bool {
	next := r.currentGeneration() + 1
	now := r.w.clock.Now().UnixMilli()
	charge := lanes.Restart{Key: "g" + strconv.FormatInt(next, 10), AtMs: now, NotBeforeMs: now, Planned: true}
	if _, err := r.w.journal.ChargeRestart(ctx, r.lease.Lane, charge); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return false
	}
	r.signal(ctx, lanes.OnNextGeneration)
	return r.nextGeneration(ctx, next, ledger.StartHandoff)
}

// inheritedFrom reads the document a generation started from, for its brief. One that cannot
// be read leaves the brief with no handoff, and a lane still has its contract.
func (r *laneRun) inheritedFrom(progress *lanes.HandoffProgress) {
	if progress == nil || len(progress.Document) == 0 {
		return
	}
	doc, err := lanes.ReadHandoff(progress.Document)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	r.inherited = &doc
	r.state.noteHandoff(doc.Degraded())
}

// takeHandoff keeps what a lane wrote, or why it could not be read. What cannot be read ends
// the wait, because a retry costs a turn, and the handoff goes on with what the worker knows.
func (w *Worker) takeHandoff(ctx context.Context, lane lanes.Record, raw json.RawMessage) lanes.Reply {
	run := w.runOf(lane.ID)
	if run == nil {
		return refused(lanes.CodeUnavailable, "nothing is following this lane")
	}
	flight := run.currentFlight()
	if flight == nil {
		return refused(lanes.CodeInvalid, "this lane was not asked to hand off")
	}
	flight.mu.Lock()
	defer flight.mu.Unlock()
	if len(flight.progress.Document) != 0 {
		return refused(lanes.CodeInvalid, "this handoff is already stored")
	}
	narrative, err := lanes.ParseNarrative(raw)
	progress := flight.progress
	if err == nil {
		progress.Narrative = &narrative
	} else {
		progress.Refused = err.Error()
	}
	if err := w.journal.SaveHandoff(ctx, lane.ID, &progress); err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	flight.progress = progress
	if err != nil {
		return refused(lanes.CodeInvalid, err.Error()+", so the handoff goes on without it")
	}
	return lanes.Reply{OK: true}
}
