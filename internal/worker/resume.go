package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// wokenRun is a run of a lane a claim brings back, on what the journal holds of it, or on
// nothing, for a worker that never ran it.
func (w *Worker) wokenRun(lease ledger.DesiredLease, record lanes.Record) *laneRun {
	run := w.resumedRun(lease, record)
	run.state = newLaneState(lanes.Parked, w.clock.Now())
	return run
}

// claimWoken takes a woken lane, because a wake must bring it back with nobody there to dispatch
// it. Admission comes first, so a machine that cannot start the lane leaves it to one that can,
// and a claim that loses a race is no failure.
func (w *Worker) claimWoken(ctx context.Context, woken ledger.DesiredWoken) error {
	if reason := w.admission(ctx, ledger.DesiredLease{Lane: woken.Lane, Repository: woken.Repository}, true); reason != "" {
		return nil
	}
	request := workorders.ClaimRequest{
		WorkOrder: woken.WorkOrder, TTLMs: w.Session().LeaseTTLMs,
		ClaimID: fmt.Sprintf("%s/%s/f%d", w.Auth().WorkerID, woken.Lane, woken.Fence),
	}
	_, err := w.link.Claim(ctx, w.Auth(), request)
	switch {
	case errors.Is(err, workorders.ErrLost), errors.Is(err, workorders.ErrIneligible):
		return nil
	case err != nil:
		return err
	}
	crashpoint.Hit("worker.resume", "claimed")
	return w.Sync(ctx)
}

// sessionOf names the session a record's lane last ran. A generation that parked before its
// first turn bound none, and continues the one it took over.
func sessionOf(record lanes.Record) string {
	if record.SessionID != "" {
		return record.SessionID
	}
	return record.ResumeSession
}

// resumeFacts gathers what the table reads. Time is the ledger's, as a duration, so the worker's
// clock decides nothing, and a capability that cannot be read leaves the path that needs no
// session of this machine's.
func (r *laneRun) resumeFacts(ctx context.Context, record lanes.Record, local bool) lanes.ResumeFacts {
	from := r.lease.Resume
	capabilities, err := r.rt.Driver.Capabilities(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	costs := capabilities.Resume
	facts := lanes.ResumeFacts{
		NativeResume: capabilities.NativeResume, Local: local, Degraded: from.Degraded,
		ResponseKnown: from.Responded, SinceResponse: time.Duration(from.SinceResponseMs) * time.Millisecond,
		CacheLifetime:      r.rt.Override.CacheLifetime(costs.Lifetime(r.w.signInKind())),
		ConversationTokens: lanes.ConversationTokens(record.Progress.ContextTokens, record.Progress.BaselineTokens),
		HandoffTokens:      lanes.EstimateTokens(len(from.Handoff)),
	}
	doc, err := lanes.ReadHandoff(from.Handoff)
	if err != nil {
		facts.Degraded = true
		return facts
	}
	if doc.Narrative != nil {
		facts.Carries = doc.Narrative.CarriesNextStep
	}
	facts.Ratio = costs.Ratio(doc.Deterministic.ModelResolved)
	return facts
}

// resume brings a woken lane back as a new generation, by the first matching row of the table,
// under the claim that took it. The move is one journal write with its event, so a kill leaves
// the lane parked or resumed, and a resumed lane with no session continues as any lane does that
// a crash stopped before its session.
func (r *laneRun) resume(ctx context.Context) error {
	from := r.lease.Resume
	record, err := r.w.journal.Lane(ctx, r.lease.Lane)
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	session := sessionOf(record)
	local := from.Worker == r.w.Auth().WorkerID && session != ""
	choice := lanes.ChooseResume(r.resumeFacts(ctx, record, local))
	next := from.Generation + 1
	now := r.w.clock.Now().UnixMilli()
	charge := lanes.Restart{Key: "g" + strconv.FormatInt(next, 10), AtMs: now, NotBeforeMs: now, Planned: true}
	if _, err := r.w.journal.ChargeRestart(ctx, r.lease.Lane, charge); err != nil {
		return err
	}
	crashpoint.Hit("worker.resume", "charged")
	stored := r.resumeRecord(record, next, choice, session)
	if !local {
		stored.Parking.Fetch = fetchRefs(from)
	}
	reason := ledger.StartHandoff
	if choice.Path == lanes.ResumeNative {
		reason = ledger.StartResume
	}
	payload, err := json.Marshal(ledger.GenerationStart{Reason: reason})
	if err != nil {
		return err
	}
	if err := r.keepResume(ctx, stored, payload); err != nil {
		return err
	}
	crashpoint.Hit("worker.resume", "begun")
	r.uploadBounded(ctx)
	crashpoint.Hit("worker.resume", "uploaded")
	r.enterResumed(stored)
	r.signal(ctx, lanes.OnWakeFired)
	return r.conclude(ctx, r.session(ctx))
}

// keepResume is one journal write, because a lane on a generation the ledger never hears of
// would have every later write refused.
func (r *laneRun) keepResume(ctx context.Context, stored lanes.Record, payload []byte) error {
	event := r.w.event(ledger.UploadLaneResumed, stored, payload)
	if !splitsResume() {
		_, err := r.w.journal.BeginResume(ctx, stored, event)
		return err
	}
	if _, err := r.w.journal.BeginResume(ctx, stored, r.w.event(ledger.UploadLaneStatus, stored, nil)); err != nil {
		return err
	}
	crashpoint.Hit("worker.resume", "split")
	_, err := r.w.journal.Append(ctx, event)
	return err
}

// fetchRefs names what a worker with no copy of the work must bring from a remote.
func fetchRefs(from *ledger.DesiredResume) []string {
	var refs []string
	if from.Branch != "" {
		refs = append(refs, lanes.HeadsRef(from.Branch))
	}
	if from.SnapshotRef != "" {
		refs = append(refs, from.SnapshotRef)
	}
	return refs
}

// resumeRecord is a lane as it stands in its next generation. A native resume continues the
// session and its running total, and a fresh start owes the document everything. The checkpoint's
// pending instruction goes along on both paths.
func (r *laneRun) resumeRecord(record lanes.Record, next int64, choice lanes.ResumeChoice, session string) lanes.Record {
	from := r.lease.Resume
	stored := lanes.Record{
		ID: r.lease.Lane, WorkOrder: r.lease.WorkOrder, ClaimID: r.lease.ClaimID, Fence: r.lease.Fence, Generation: next,
		State: lanes.StateRunning, Actual: lanes.Provisioning, Worktree: record.Worktree, Branch: record.Branch,
		Handoff: &lanes.HandoffProgress{Reason: string(choice.Rule), Document: from.Handoff, Begun: true},
		Parking: &lanes.ParkProgress{Generation: from.Generation, Pending: from.Pending, Begun: true},
	}
	if leavesParked() {
		stored.Actual = lanes.Parked
	}
	r.priorUSD, r.priorUnknown = r.priorUSD+r.cost.CostUSD, r.priorUnknown+r.cost.UnknownTurns
	stored.Progress = lanes.Progress{
		Usage: r.usage, PriorUSD: r.priorUSD, PriorUnknown: r.priorUnknown, Instruction: from.Pending,
	}
	if choice.Path == lanes.ResumeNative {
		stored.ResumeSession = session
		stored.Progress.Cost = lanes.CostMeter{SessionTotalUSD: r.cost.SessionTotalUSD}
		stored.Progress.BaselineTokens = record.Progress.BaselineTokens
	}
	return stored
}

// enterResumed moves this run to a record's generation. It holds no question over, because the
// wake is what those questions were waiting on.
func (r *laneRun) enterResumed(stored lanes.Record) {
	r.enterGeneration(stored.Generation, stored.ResumeSession, stored.Progress)
	r.flightLock.Lock()
	r.parking = stored.Parking
	r.flightLock.Unlock()
	r.inherited = nil
	r.inheritedFrom(stored.Handoff)
	r.handleLock.Lock()
	r.blocking, r.instruction = nil, stored.Progress.Instruction
	r.handleLock.Unlock()
}

// woken says this generation is the one a parked generation came back as.
func (r *laneRun) woken() *lanes.ParkProgress {
	parking := r.parkingOf()
	if parking == nil || !parking.Begun || parking.Generation+1 != r.currentGeneration() {
		return nil
	}
	return parking
}

// pendingInstruction is what a generation's first prompt must state again.
func (r *laneRun) pendingInstruction() string {
	if parking := r.woken(); parking != nil {
		return parking.Pending
	}
	return ""
}

// resumePrompt is what a session is told on the native path, because a resume alone starts no
// turn.
func (r *laneRun) resumePrompt() string {
	if parking := r.woken(); parking != nil {
		return lanes.ResumePrompt(parking.Pending, r.factsOwed()...)
	}
	return lanes.RestartPrompt(r.factsOwed()...)
}

// fetchNames is what this generation must bring from a remote before its worktree exists.
func (r *laneRun) fetchNames() []string {
	if parking := r.woken(); parking != nil {
		return parking.Fetch
	}
	return nil
}
