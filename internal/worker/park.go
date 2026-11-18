package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

const (
	// defaultAskWait is what a profile that sets nothing it can read gets, because a lane must
	// not hold a process on a person's time.
	defaultAskWait = 5 * time.Minute
	// defaultDrainDeadline bounds a turn a draining fleet lets finish.
	defaultDrainDeadline = 10 * time.Minute
	// goneAttempts is how many reads a process gets to be gone before it takes SIGKILL.
	goneAttempts = 100
	// attachedRecheck is how long a planning lane waits to read its pane again after a read said nobody is
	// attached. A driver reads a pane's clients about once a second, and this is as long as a hook trusts
	// what the last read said, so one read that failed is followed by one that did not.
	attachedRecheck = lanes.AttachedStale
	// attachedWaits is how many `ask_wait`s in a row a terminal attached to a planning lane's pane keeps the
	// lane while its person sends nothing. A terminal left attached says nothing of whether anyone is at it,
	// and no process waits on a person without end.
	attachedWaits = 12
)

// ErrNoParking means a lane is not at a point where it can park.
var ErrNoParking = errors.New("this lane cannot park now")

// parkSignals are the edges into `parking`, one from each state a lane can park from.
var parkSignals = []lanes.Signal{lanes.OnCannotProceed, lanes.OnParkRequested, lanes.OnWaitBound, lanes.OnUnknownScreen}

// parkPath is the way there, which starts at a compaction's failure edge for a lane that parks from
// one, so both a lane that begins and a worker that goes on from a kill reach `parking` the same way.
var parkPath = append([]lanes.Signal{lanes.OnCompactionFailed}, parkSignals...)

// Park starts the sequence on a lane this worker runs (FR-PARK-1). It returns once the wake is
// kept and the lane is `parking`, and the lane's run sees it through.
func (w *Worker) Park(ctx context.Context, lane string, request lanes.ParkRequest) error {
	run := w.runOf(lane)
	if run == nil {
		return fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
	}
	return run.beginParking(ctx, request)
}

func parkable(state lanes.ActualState) bool {
	return slices.ContainsFunc(parkSignals, func(signal lanes.Signal) bool {
		_, moves := lanes.Next(state, signal)
		return moves
	})
}

// parksFrom says a lane may park from where it stands. A compaction has no edge into `parking`, so
// only a hold with a bound of its own may take a lane out of one.
func parksFrom(state lanes.ActualState, request lanes.ParkRequest) bool {
	return parkable(state) || (request.PastCompaction && state == lanes.Compacting)
}

// beginParking keeps the wake before anything else happens, so a kill at any later step leaves
// a worker that finishes parking. The wake and the event that carries it are one journal write.
func (r *laneRun) beginParking(ctx context.Context, request lanes.ParkRequest) error {
	r.flightLock.Lock()
	defer r.flightLock.Unlock()
	state := r.state.current()
	if r.orchestrating() || r.flight != nil || r.fenced.Load() || !parksFrom(state, request) {
		return fmt.Errorf("%w: lane %s is %s, lost its lease, or has a handoff under way", ErrNoParking, r.lease.Lane, state)
	}
	record := r.record()
	parking := lanes.ParkProgress{
		Wake: lanes.WakeID(record.ID, record.Generation), Expr: request.Expr, Trigger: request.Trigger,
		SkipNarrative: request.SkipNarrative || state == lanes.Compacting, Generation: record.Generation,
	}
	handoff := lanes.HandoffProgress{
		Reason: string(request.Trigger), DeadlineMs: r.w.clock.Now().Add(r.state.handoffTimeout()).UnixMilli(),
		MidTurn: request.MidTurn && state == lanes.Working,
	}
	// What a lane had spent when its limit stopped it is kept with the wake, so a worker that takes the park over
	// reports at least that, and the parked commit never reads as a limit that was raised.
	if request.Trigger == lanes.ParkSpendLimit {
		usage := r.generationUsage()
		parking.Usage = &usage
	}
	payload, err := json.Marshal(ledger.ParkingStart{Wake: parking.Wake, Expr: parking.Expr})
	if err != nil {
		return err
	}
	if _, err := r.w.journal.BeginParking(ctx, record.ID, parking, handoff, r.w.event(ledger.UploadLaneParking, record, payload)); err != nil {
		return err
	}
	crashpoint.Hit("worker.park", "begun")
	r.parking = &parking
	r.signal(ctx, parkPath...)
	r.startParking(handoff)
	return nil
}

// startParking follows the sequence on a goroutine of its own, because the run's own goroutine
// stays on the session's stream. The caller holds flightLock.
func (r *laneRun) startParking(progress lanes.HandoffProgress) {
	r.flight = &handoffFlight{progress: progress}
	r.sequence.Go(func() { r.runParking(r.life) })
}

// parkingOf is the parking this run follows, or nil.
func (r *laneRun) parkingOf() *lanes.ParkProgress {
	r.flightLock.Lock()
	defer r.flightLock.Unlock()
	return r.parking
}

// resumeParking goes on with parking an earlier process left, from what the journal holds.
func (r *laneRun) resumeParking(ctx context.Context) {
	if !r.parkingOf().Open() {
		return
	}
	r.flightLock.Lock()
	defer r.flightLock.Unlock()
	if r.flight != nil {
		return
	}
	if staysCompacting() {
		r.signal(ctx, parkSignals...)
	} else {
		r.signal(ctx, parkPath...)
	}
	progress := lanes.HandoffProgress{Reason: string(r.parking.Trigger)}
	if r.left != nil {
		progress = *r.left
	}
	r.startParking(progress)
}

// finishParking settles parking whose session went with the worker that followed it. There is
// nothing to read and nothing to stop, so what is left is the failure edge of the handoff.
func (r *laneRun) finishParking(ctx context.Context) error {
	r.loadProfile(ctx)
	r.sessionOver.Store(true)
	// A long `lane_wait` the journal kept and no park began for parks now, from what the journal holds, because
	// the session that asked is gone and nothing else would wake the lane.
	if !r.parkingOf().Open() {
		r.parkForWait(ctx)
	}
	r.resumeParking(ctx)
	return r.conclude(ctx, r.unattended(ctx))
}

// runParking walks the sequence's steps. The wake was stored before this began. Each step
// repeats safely from what the journal holds, and every failure leaves a document with no
// narrative, so parking can degrade but it cannot block.
func (r *laneRun) runParking(ctx context.Context) {
	r.uploadBounded(ctx)
	crashpoint.Hit("worker.park", "stored")
	flight := r.currentFlight()
	if len(flight.read().Document) == 0 {
		if !r.parkingOf().SkipNarrative {
			r.requestNarrative(ctx, flight)
			crashpoint.Hit("worker.park", "requested")
			r.awaitNarrative(ctx, flight)
		}
		if r.abandons(ctx) {
			return
		}
		crashpoint.Hit("worker.park", "awaited")
		r.storeHandoff(ctx, flight)
	}
	if r.abandons(ctx) || r.sessionOver.Load() {
		return
	}
	if parksFirst() {
		r.commitParked(ctx)
	}
	r.stop(ctx, ports.StopInterrupt)
}

// parks waits for the sequence to reach its end, and says the lane goes on to its commit. A
// lane that reported, lost its lease, or lost its worker does not.
func (r *laneRun) parks(ctx context.Context) bool {
	if !r.parkingOf().Open() {
		return false
	}
	r.sequence.Wait()
	r.stopped.Wait()
	return !r.abandons(ctx) && r.awaitGone(ctx)
}

// awaitGone confirms what the stream said, because the process stops before the lease does.
// Dead needs positive evidence, so an incomplete read is read again. A process that outlives
// its stop takes SIGKILL, and the lease stays renewed until it has gone.
func (r *laneRun) awaitGone(ctx context.Context) bool {
	handle := r.sessionHandle()
	if handle.ID == "" {
		return true
	}
	for attempt := 1; ; attempt++ {
		observation, err := r.rt.Driver.Observe(ctx, handle)
		if errors.Is(err, ports.ErrNoSession) || (err == nil && observation.Liveness == ports.LivenessDead) {
			return true
		}
		if attempt%goneAttempts == 0 {
			if err := r.rt.Driver.Stop(ctx, handle, ports.StopNow); err != nil {
				r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(superviseEvery):
		}
	}
}

// commitParked keeps the commit in the journal, which is durable whether or not a control plane
// is there to hear it. A last guard reads what the session wrote on its way out.
func (r *laneRun) commitParked(ctx context.Context) {
	crashpoint.Hit("worker.park", "gone")
	lane := r.lease.Lane
	report := func(err error) { r.w.config.Report(fmt.Errorf("lane %s: %w", lane, err)) }
	record, err := r.w.journal.Lane(ctx, lane)
	if err != nil {
		report(err)
		return
	}
	guard, err := r.w.guardPass(ctx, record, false, guardAtEnd)
	if err != nil {
		report(err)
	}
	if err := r.w.reportGuard(ctx, record, guard); err != nil {
		report(err)
	}
	crashpoint.Hit("worker.park", "saved")
	branch, head := r.w.finalBranch(ctx, record)
	progress := *record.Parking
	progress.Pending = r.instructionInProgress()
	payload, err := json.Marshal(ledger.ParkedCommit{Wake: progress.Wake, Checkpoint: ledger.Checkpoint{
		Lane: lane, Generation: record.Generation, Branch: branch, Head: head, SnapshotRef: guard.Ref,
		Pushed: !guard.Unpushed, PendingInstruction: progress.Pending,
	}})
	if err != nil {
		report(err)
		return
	}
	if err := r.keepParked(ctx, record, progress, payload); err != nil {
		report(err)
		return
	}
	crashpoint.Hit("worker.park", "committed")
	r.signal(ctx, lanes.OnProcessGone)
	if err := r.w.release(ctx, lane); err != nil {
		report(err)
	}
	r.uploadBounded(ctx)
}

// uploadBounded hands what the journal holds to the ledger, and goes on either way. A control
// plane that hangs must not hold a lane's sequence, and the heartbeat's upload sends it later.
func (r *laneRun) uploadBounded(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, laneFactsDeadline)
	defer cancel()
	if err := r.w.Upload(bounded); err != nil && ctx.Err() == nil {
		r.w.config.Report(err)
	}
}

// keepParked is one journal write, because a commit with no upload on its way would leave a
// lane no wake can reach, and an upload with no commit would report a lane still held here.
func (r *laneRun) keepParked(ctx context.Context, record lanes.Record, progress lanes.ParkProgress, payload []byte) error {
	r.recordUsage(ctx)
	event := r.w.event(ledger.UploadLaneParked, record, payload)
	if !splitsPark() {
		_, err := r.w.journal.CommitParked(ctx, record.ID, progress, event)
		return err
	}
	if _, err := r.w.journal.CommitParked(ctx, record.ID, progress, r.w.event(ledger.UploadLaneStatus, record, nil)); err != nil {
		return err
	}
	crashpoint.Hit("worker.park", "split")
	_, err := r.w.journal.Append(ctx, event)
	return err
}

// instructionInProgress reads what the session was told and did not finish.
func (r *laneRun) instructionInProgress() string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.instruction
}

func (r *laneRun) setInstruction(text string) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.instruction = text
}

// finishTurn says the session finished what it was told. A turn that was stopped did not, and
// a stopped turn is not continued, so its instruction stays to be stated again.
func (r *laneRun) finishTurn(turn *ports.TurnResult) {
	if turn.Status == ports.TurnCompleted {
		r.setInstruction("")
	}
}

// noteResponse tells the ledger a model answered, which starts the resume table's cache clock. A
// failed turn may have answered nothing, and a late report can only choose a costlier path.
func (r *laneRun) noteResponse(ctx context.Context, turn *ports.TurnResult) {
	if turn.Status == ports.TurnFailed {
		return
	}
	if _, err := r.w.Record(ctx, r.w.event(ledger.UploadGenerationResponded, r.record(), nil)); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

// noteAsked keeps a blocking question that has no answer yet, and lets go of one the lane read
// the answer to.
func (r *laneRun) noteAsked(ctx context.Context, result questions.AskResult) {
	r.handleLock.Lock()
	known := slices.Contains(r.blocking, result.QuestionID)
	answered := result.Status == questions.AskAnswered
	switch {
	case answered:
		r.blocking = slices.DeleteFunc(r.blocking, func(id string) bool { return id == result.QuestionID })
	case !known:
		r.blocking = append(r.blocking, result.QuestionID)
	}
	blocking := slices.Clone(r.blocking)
	r.handleLock.Unlock()
	if known == !answered {
		return
	}
	if err := r.w.journal.SaveBlocking(ctx, r.lease.Lane, blocking); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

func (r *laneRun) blockingQuestions() []string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return slices.Clone(r.blocking)
}

// awaitAnswer holds a lane at rest for `ask_wait`, then parks it, because no process waits on a
// person. A new turn ends the wait, and the next rest starts it again. A compaction at rest sits this
// out while it may still end, and once it reads `stalled` nothing will end it, so the lane parks from
// where it stands.
func (r *laneRun) awaitAnswer(ctx context.Context) {
	// The deadline is read before the flag is taken, so a lane that reads as waiting has its deadline.
	deadline := r.w.clock.Now().Add(r.state.askWait())
	if !r.waiting.CompareAndSwap(false, true) {
		return
	}
	r.sequence.Go(func() {
		defer r.waiting.Store(false)
		if !r.restUntil(ctx, deadline) {
			return
		}
		request := lanes.ParkRequest{
			Trigger: lanes.ParkBlockingQuestion, Expr: lanes.QuestionWake(r.blockingQuestions()), PastCompaction: true,
		}
		r.parkReported(ctx, request)
	})
}

// awaitPerson holds a planning lane at rest for the person it talks to, because a turn that ends in the
// conversation asks for the person's next line (ADR-0018). A person at its pane when `ask_wait` runs out
// keeps the session for another wait: a terminal attached to it, or input a member typed into it through
// the web pane during that wait. With nobody there it parks on a wake only a person ends, because no
// process waits on a person. Nobody is there only when two reads `attachedRecheck` apart both say so,
// because one read that failed says the same. An attached terminal alone keeps the lane for
// `attachedWaits` waits in a row, and then it parks as with nobody there, because a terminal left attached
// says nothing of whether anyone is at it. A line the person types starts a turn, which ends the wait, and
// the next rest starts it again, and input in the web pane starts the count again.
//
// The first wait's start is read before the lane reads as waiting, as awaitAnswer reads its deadline, so a
// lane that reads as waiting has its start: a clock moved on seeing it cannot move that wait's deadline, and
// input typed into the web pane from then on counts as typed during the wait.
func (r *laneRun) awaitPerson(ctx context.Context) {
	from := r.w.clock.Now()
	if !r.waiting.CompareAndSwap(false, true) {
		return
	}
	r.state.waitForPerson(true)
	r.sequence.Go(func() {
		defer r.waiting.Store(false)
		defer r.state.waitForPerson(false)
		quiet := 0
		for {
			if !r.restUntil(ctx, from.Add(r.state.askWait())) {
				return
			}
			if !r.attended(ctx, from) {
				if !r.restUntil(ctx, r.w.clock.Now().Add(attachedRecheck)) {
					return
				}
				if !r.attended(ctx, from) {
					break
				}
			}
			quiet++
			if r.typedInPane(from) {
				quiet = 0
			}
			if quiet >= attachedWaits {
				break
			}
			from = r.w.clock.Now()
		}
		r.parkReported(ctx, lanes.ParkRequest{Trigger: lanes.ParkWaitingForPerson, Expr: lanes.HumanWake(), PastCompaction: true})
	})
}

// attended says a person is at the lane's pane: a member typed into it through the web pane since a time,
// or a terminal is attached to it.
func (r *laneRun) attended(ctx context.Context, since time.Time) bool {
	return r.typedInPane(since) || r.personAttached(ctx)
}

// typedInPane says a member typed into the pane of the lane's current generation through the web pane
// since a time. The web pane attaches no client, so no read of the pane's clients sees them.
func (r *laneRun) typedInPane(since time.Time) bool {
	return r.rt.PaneInputs.TypedSince(r.lease.Lane, r.currentGeneration(), since)
}

// personAttached reads whether a person's terminal is attached to the lane's pane. A read that fails says
// nobody is, as the hook reads a marker it cannot (ADR-0018).
func (r *laneRun) personAttached(ctx context.Context) bool {
	observation, err := r.rt.Driver.Observe(ctx, r.sessionHandle())
	return err == nil && observation.Attached
}

// restUntil holds while the lane rests, until the deadline and past it while a compaction at rest may
// still end, and says whether the lane still rests then.
func (r *laneRun) restUntil(ctx context.Context, deadline time.Time) bool {
	for r.w.clock.Now().Before(deadline) || r.state.compactionHolds(r.w.clock.Now()) {
		if !r.restsOn(ctx) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(superviseEvery):
		}
	}
	return r.restsOn(ctx)
}

// restsOn says the wait goes on. A compaction at rest does not end it, because an automatic one ends
// no turn, and nothing would start the wait again. It parks only at `idle`.
func (r *laneRun) restsOn(ctx context.Context) bool {
	return ctx.Err() == nil && !r.sessionOver.Load() && !r.fenced.Load() && r.state.atRest()
}

// parkReported starts parking for a trigger nobody waits on. A lane that moved on has nothing to
// answer for.
func (r *laneRun) parkReported(ctx context.Context, request lanes.ParkRequest) {
	if err := r.beginParking(ctx, request); err != nil && !errors.Is(err, ErrNoParking) {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
}

// parkOnScreen parks a lane no check could start. A person has to look, and no agent exists to
// write a narrative. A lane that cannot park ends blocked, because it must not start again.
func (r *laneRun) parkOnScreen(ctx context.Context) workorders.Report {
	r.sessionOver.Store(true)
	request := lanes.ParkRequest{Trigger: lanes.ParkUnknownScreen, Expr: lanes.HumanWake(), SkipNarrative: true}
	if err := r.beginParking(ctx, request); err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
	}
	return workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonUnknownScreen}
}

// draining says the fleet of this lane's project stops new work, as desired state last said.
func (r *laneRun) draining(ctx context.Context) bool {
	return r.w.fleetMode(ctx, r.lease.Project) == lanes.FleetDrain
}

// parkDrained parks a lane a draining fleet stops. Finishing the turn comes first, then the
// lane completes or parks, and a turn past the deadline parks where it stands.
func (r *laneRun) parkDrained(ctx context.Context) {
	r.flightLock.Lock()
	parking := r.flight != nil
	r.flightLock.Unlock()
	if parking {
		return
	}
	state := r.state.current()
	r.handleLock.Lock()
	if r.drainingSince.IsZero() {
		r.drainingSince = r.w.clock.Now()
	}
	since := r.drainingSince
	r.handleLock.Unlock()
	past := r.w.clock.Now().Sub(since) >= r.state.drainDeadline()
	if state == lanes.Idle || (past && (state == lanes.Working || state == lanes.NeedsDecision || state == lanes.Compacting)) {
		r.parkReported(ctx, lanes.ParkRequest{Trigger: lanes.ParkFleetDrain, Expr: lanes.HumanWake(), PastCompaction: true})
	}
}

// drainLanes applies a draining fleet to the lanes this worker runs, each by its own project's mode. Each
// lane decides alone, so one stuck lane stops no other.
func (w *Worker) drainLanes(ctx context.Context) {
	for _, run := range w.runs() {
		if run.draining(ctx) {
			run.parkDrained(ctx)
		}
	}
}

// runs lists what this worker runs.
func (w *Worker) runs() []*laneRun {
	w.mu.Lock()
	defer w.mu.Unlock()
	runs := make([]*laneRun, 0, len(w.running))
	for _, run := range w.running {
		runs = append(runs, run)
	}
	return runs
}
