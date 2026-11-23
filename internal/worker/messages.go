package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

// messageLabel names a message's one write, which is the key that keeps it to one.
const messageLabel = "message/"

// detailNoChannel begins the detail of a write that failed because the session had no channel for a peer's message, and
// the driver's error follows it. The journal keeps an error as text, which names no sentinel, so a restart reads this
// prefix, and never the words of an error, to tell a write nothing took from one that failed another way.
const detailNoChannel = "no channel for the message"

// takesMessages are the states a session reads a message in. A lane on its way out reads nothing
// more, so what waits for it meets the rollover table instead.
var takesMessages = []lanes.ActualState{lanes.Working, lanes.Idle, lanes.NeedsDecision}

// deliverMessages acknowledges what desired state offers and writes what can be written. It runs
// on every pass, so a message waits at most one beat for a session that can take it. One lane's
// failure stops no other.
func (w *Worker) deliverMessages(ctx context.Context) {
	moved, err := w.takeMessages(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("taking messages: %w", err))
	}
	waiting, err := w.journal.WaitingMessages(ctx)
	if err != nil {
		w.config.Report(fmt.Errorf("reading messages: %w", err))
		return
	}
	for _, held := range waiting {
		run := w.runOf(held.To.Lane)
		if run == nil {
			continue
		}
		wrote, err := run.deliver(ctx, held)
		if err != nil {
			w.config.Report(fmt.Errorf("lane %s: message %s: %w", held.To.Lane, held.ID, err))
		}
		moved = moved || wrote
	}
	if moved {
		w.uploadMoved(ctx)
	}
}

// uploadMoved tells the ledger what moved now, and not at the next heartbeat, because a sender
// waits on it. A control plane that hangs must not hold a pass, and the heartbeat sends it later.
func (w *Worker) uploadMoved(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, laneFactsDeadline)
	defer cancel()
	if err := w.Upload(bounded); err != nil && ctx.Err() == nil {
		w.config.Report(err)
	}
}

// takeMessages keeps each offer once, with the word that stops the control plane offering it, and
// says whether anything was new. Transport acknowledgement stops here (FR-MSG-3), and the
// journal's key deduplicates by ID.
func (w *Worker) takeMessages(ctx context.Context) (bool, error) {
	state, err := w.desired(ctx)
	if err != nil {
		return false, err
	}
	now := w.clock.Now().UnixMilli()
	took := false
	for _, lease := range state.Leases {
		for _, offered := range lease.Messages {
			held := messages.Held{
				Message: messages.Message{
					ID: offered.ID, To: messages.Target{Lane: offered.Lane, Generation: offered.Generation}, From: messages.Sender{Kind: offered.FromKind},
					ReplyTo: offered.ReplyTo, Kind: messages.Kind(offered.Kind), Body: offered.Body, Traceparent: offered.Traceparent, State: messages.AtWorker,
				},
				InputID: messages.InputID(offered.ID), AgeMs: offered.AgeMs, TakenMs: now, StaleAfterMs: offered.StaleAfterMs, ExpireAfterMs: offered.ExpireAfterMs,
			}
			event, err := w.messageEvent(leaseRecord(lease, offered.Generation), messages.Report{ID: held.ID, State: messages.AtWorker})
			if err != nil {
				return took, err
			}
			first, err := w.journal.TakeMessage(ctx, held, event)
			if err != nil {
				return took, err
			}
			crashpoint.Hit("worker.message", "taken")
			took = took || first
		}
	}
	return took, nil
}

func leaseRecord(lease ledger.DesiredLease, generation int64) lanes.Record {
	return lanes.Record{ID: lease.Lane, Project: lease.Project, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence, Generation: generation}
}

func (w *Worker) messageEvent(record lanes.Record, report messages.Report) (ledger.UploadEvent, error) {
	payload, err := json.Marshal(report)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return w.event(ledger.UploadMessage, record, payload), nil
}

// deliver writes one message, at most once, for as long as it can be written, and says whether it
// wrote or reported. A message for a generation this run left is never injected, and neither is one
// past its window, because a stale instruction is worse than none. Either way the ledger tells the
// sender. A session with no channel for a message takes none (undeliverable).
func (r *laneRun) deliver(ctx context.Context, held messages.Held) (bool, error) {
	generation := r.currentGeneration()
	now := r.w.clock.Now().UnixMilli()
	age, window := held.Age(now), held.Window()
	switch {
	case held.To.Generation > generation:
		return false, nil
	case held.To.Generation < generation || window.Tier(age) == messages.TierExpired:
		return false, r.w.journal.CloseMessage(ctx, held.ID)
	case !slices.Contains(takesMessages, r.state.current()) || r.sessionOver.Load():
		return false, nil
	}
	handle := r.sessionHandle()
	if handle.ID == "" {
		return false, nil
	}
	effect := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, generation, "", messageKey(held.ID))
	effect.Project = r.lease.Project
	begun, err := r.w.journal.BeginEffect(ctx, effect)
	if err != nil {
		return false, err
	}
	if !begun {
		current, err := r.w.journal.Effect(ctx, effect.Key)
		if err != nil {
			return false, err
		}
		if leftWithNoChannel(current) {
			return r.undeliverable(ctx, held)
		}
		again, err := r.afterAPersonsWord(ctx, held, current)
		if err != nil || !again {
			return false, err
		}
	}
	crashpoint.Hit("worker.message", "begun")
	_, sent := r.rt.Driver.Send(ctx, handle, ports.Input{ID: held.InputID, Text: messages.Render(held.Message, age, window), Peer: true})
	crashpoint.Hit("worker.message", "written")
	if sent != nil {
		if err := r.w.journal.FinishEffect(context.WithoutCancel(ctx), effect.Key, lanes.EffectFailed, writeFailure(sent)); err != nil {
			return false, errors.Join(sent, err)
		}
		switch {
		case errors.Is(sent, ports.ErrNoChannel):
			return r.undeliverable(ctx, held)
		case errors.Is(sent, ports.ErrNoSession):
			return false, nil
		}
		return false, sent
	}
	return true, r.recordHandedOff(ctx, held, effect.Key)
}

// writeFailure is what the journal keeps of a write that failed. A write that no channel took says so first, so a
// worker that dies before it reports the message undeliverable settles it on its restart (leftWithNoChannel).
func writeFailure(sent error) string {
	if errors.Is(sent, ports.ErrNoChannel) {
		return detailNoChannel + ": " + sent.Error()
	}
	return sent.Error()
}

// leftWithNoChannel says a write failed for want of a channel. Nothing was written into the session, and no later
// attempt will write it, so what is left is to report the message undeliverable and move it, which a worker that died
// between the failed write and that report has yet to do.
func leftWithNoChannel(effect lanes.Effect) bool {
	return effect.Kind == lanes.EffectInput && effect.State == lanes.EffectFailed && strings.HasPrefix(effect.Detail, detailNoChannel)
}

// undeliverable settles a message the session has no channel for, which nothing writes into it, not even
// as the worker's own input, because that would give it the standing of the worker's word. The worker
// reports it undeliverable and stops holding it, so its sender reads why it was not delivered, and a
// message that bears a decision reaches the next generation as a ledger fact. An answer the lane waits
// on parks the lane at once, on every question it waits on, as `ask_wait` parks it. A lane that waits on
// only that question parks on a wake the answer has already fired, so it resumes and its next generation's
// first prompt carries the answer (FR-PARK-2, FR-MSG-5). A lane that waits on another question too stays
// parked until that one is answered as well, because a resume clears what the lane waited on and would
// leave that question with nothing waiting on it. Only a lane at rest parks: one in a turn is not cut off, and
// parks when its turn ends (awaitAnswer reads the answer as undelivered, answerLeftUndelivered). A lane that
// cannot park now is on its way out already, or parks on its questions at the end of `ask_wait`, and its next
// generation hears of the answer either way.
func (r *laneRun) undeliverable(ctx context.Context, held messages.Held) (bool, error) {
	event, err := r.w.messageEvent(r.recordAt(held.To.Generation), messages.Report{ID: held.ID, State: messages.Expired, Reason: messages.ReasonUndeliverable})
	if err != nil {
		return false, err
	}
	moved, err := r.w.journal.MoveMessage(context.WithoutCancel(ctx), held.ID, messages.AtWorker, messages.Expired, event)
	if err != nil || !moved {
		return false, err
	}
	waiting := r.blockingQuestions()
	if held.Kind == messages.KindAnswer && slices.Contains(waiting, held.ReplyTo) && r.state.atRest() {
		r.parkReported(ctx, lanes.ParkRequest{
			Trigger: lanes.ParkBlockingQuestion, Expr: lanes.QuestionWake(waiting), PastCompaction: true,
		})
	}
	return true, nil
}

// answerLeftUndelivered says an answer to a question the lane still waits on was settled as undeliverable in this
// generation: its message reads expired, and its write failed for want of a channel. The journal keeps both across a
// restart, so a lane whose worker died between that move and the lane's park is found by them.
func (r *laneRun) answerLeftUndelivered(ctx context.Context) bool {
	waiting := r.blockingQuestions()
	if len(waiting) == 0 {
		return false
	}
	effects, err := r.w.journal.Effects(ctx)
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: reading its effects: %w", r.lease.Lane, err))
		return false
	}
	generation := r.currentGeneration()
	prefix := lanes.NewEffect(lanes.EffectInput, r.lease.Lane, generation, "", messageLabel).Key
	for _, effect := range effects {
		id, ours := strings.CutPrefix(effect.Key, prefix)
		if !ours || effect.Lane != r.lease.Lane || effect.Generation != generation || !leftWithNoChannel(effect) {
			continue
		}
		held, err := r.w.journal.Message(ctx, id)
		if err != nil {
			continue
		}
		if held.State == messages.Expired && held.Kind == messages.KindAnswer && slices.Contains(waiting, held.ReplyTo) {
			return true
		}
	}
	return false
}

// afterAPersonsWord reads a write that already has a record, and says whether to write again. Only a
// person sends one back, since a restart turns whatever it finds started to unknown, and only a
// person's word that it arrived stands for a hand-off no write of ours saw.
func (r *laneRun) afterAPersonsWord(ctx context.Context, held messages.Held, current lanes.Effect) (bool, error) {
	switch {
	case current.State == lanes.EffectStarted && current.Attempts > 1:
		return true, nil
	case current.State == lanes.EffectCompleted && current.Detail == lanes.DetailSaidHappened:
		return false, r.recordHandedOff(ctx, held, current.Key)
	}
	return false, nil
}

// recordHandedOff keeps the write and what it proves in one journal write. A state that followed the
// record of its effect would leave a window where a message reads unwritten and cannot be written.
func (r *laneRun) recordHandedOff(ctx context.Context, held messages.Held, effect string) error {
	event, err := r.w.messageEvent(r.recordAt(held.To.Generation), messages.Report{ID: held.ID, State: messages.HandedOff})
	if err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if splitsMessage() {
		if err := r.w.journal.FinishEffect(ctx, effect, lanes.EffectCompleted, ""); err != nil {
			return err
		}
		crashpoint.Hit("worker.message", "split")
	}
	if _, err := r.w.journal.HandMessage(ctx, effect, held.ID, event); err != nil {
		return err
	}
	crashpoint.Hit("worker.message", "recorded")
	return nil
}

func (r *laneRun) recordAt(generation int64) lanes.Record {
	record := r.record()
	record.Generation = generation
	return record
}

// takeAdmission reads the one thing a hook takes from a prompt, with or without a run, because a
// worker that was away reads this from the spool. A prompt can open with any line, so only a
// message to this lane counts.
func (w *Worker) takeAdmission(ctx context.Context, event lanes.HookEvent) {
	if event.Message == "" {
		return
	}
	if run := w.runOf(event.Lane); run != nil && event.Message == run.instructionID() {
		run.liftHold(ctx)
		return
	}
	held, err := w.journal.Message(ctx, event.Message)
	if errors.Is(err, ports.ErrNotFound) {
		return
	}
	if err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", event.Lane, err))
		return
	}
	if held.To.Lane == event.Lane {
		w.admit(ctx, held)
	}
}

// admit records that a session took a message into a turn. It does not show that the model acted
// on it. An answer ends the wait on its question, because the lane has it now.
func (w *Worker) admit(ctx context.Context, held messages.Held) {
	if !messages.CanMove(held.State, messages.Admitted) {
		return
	}
	if w.moveMessage(ctx, held, messages.Admitted) {
		w.goTracked(trackedUpload, func() { w.uploadSoon(ctx) })
	}
	run := w.runOf(held.To.Lane)
	if run == nil || run.currentGeneration() != held.To.Generation {
		return
	}
	run.signal(ctx, lanes.OnInputHanded)
	if held.Kind == messages.KindAnswer && held.ReplyTo != "" {
		run.noteAsked(ctx, questions.AskResult{Status: questions.AskAnswered, QuestionID: held.ReplyTo})
	}
}

// moveMessage takes one edge of the delivery table with the word that tells the ledger, in one
// journal write. The lane's own record names the claim, because a lane can outlive the run that
// took a message.
func (w *Worker) moveMessage(ctx context.Context, held messages.Held, to messages.State) bool {
	record, err := w.journal.Lane(ctx, held.To.Lane)
	if err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", held.To.Lane, err))
		return false
	}
	record.Generation = held.To.Generation
	event, err := w.messageEvent(record, messages.Report{ID: held.ID, State: to})
	if err != nil {
		w.config.Report(err)
		return false
	}
	moved, err := w.journal.MoveMessage(ctx, held.ID, held.State, to, event)
	if err != nil {
		w.config.Report(fmt.Errorf("lane %s: %w", held.To.Lane, err))
	}
	return moved
}

// uploadSoon sends what the journal holds now rather than at the next heartbeat: what a hook left, apart
// from the hook, which has milliseconds to answer, or a forge report.
func (w *Worker) uploadSoon(ctx context.Context) {
	if err := w.Upload(ctx); err != nil && ctx.Err() == nil {
		w.config.Report(err)
	}
}

// noteInput reads the stream's signal, which names what a session calls an input. Anything but a
// message of this worker's, such as a brief, moves no message.
func (r *laneRun) noteInput(ctx context.Context, input string) {
	if input == "" {
		return
	}
	if input == messages.InputID(r.instructionID()) {
		r.liftHold(ctx)
		return
	}
	held, err := r.w.journal.MessageByInput(ctx, input)
	if errors.Is(err, ports.ErrNotFound) {
		return
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if held.To.Lane == r.lease.Lane {
		r.w.admit(ctx, held)
	}
}

// noteRefused reads a receipt. A held message stays `handed_off`, and the driver says nothing of
// one, so this is the one receipt that moves anything.
func (r *laneRun) noteRefused(ctx context.Context, input string) {
	held, err := r.w.journal.MessageByInput(ctx, input)
	if errors.Is(err, ports.ErrNotFound) {
		return
	}
	if err != nil {
		r.w.config.Report(fmt.Errorf("lane %s: %w", r.lease.Lane, err))
		return
	}
	if held.To.Lane == r.lease.Lane && messages.CanMove(held.State, messages.Refused) {
		r.w.moveMessage(ctx, held, messages.Refused)
	}
}

// takeReply keeps a lane's answer to a message, which a journal write makes durable before the
// lane hears so. A lane answers only what was sent to it (FR-AUTH-1), and what a brief listed is
// the one message it may answer that this worker never held.
func (w *Worker) takeReply(ctx context.Context, lane lanes.Record, request lanes.ReplyRequest) lanes.Reply {
	if lane.Owner == lanes.OwnerOrchestrator {
		return w.orchestratorReply(ctx, request)
	}
	if strings.TrimSpace(request.Body) == "" {
		return refused(lanes.CodeInvalid, "a reply needs a body")
	}
	known, err := w.repliesTo(ctx, lane, request.ReplyTo)
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	if !known {
		return refused(lanes.CodeInvalid, "this lane has no message "+request.ReplyTo)
	}
	event, err := w.messageEvent(lane, messages.Report{ID: request.ReplyTo, State: messages.Replied, Reply: request.Body})
	if err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	if _, err := w.Record(ctx, event); err != nil {
		return refused(lanes.CodeUnavailable, err.Error())
	}
	w.goTracked(trackedUpload, func() { w.uploadSoon(ctx) })
	return lanes.Reply{OK: true}
}

func (w *Worker) repliesTo(ctx context.Context, lane lanes.Record, id string) (bool, error) {
	held, err := w.journal.Message(ctx, id)
	switch {
	case err == nil:
		return held.To.Lane == lane.ID, nil
	case !errors.Is(err, ports.ErrNotFound):
		return false, err
	}
	run := w.runOf(lane.ID)
	return run != nil && slices.Contains(run.carriedIDs(), id), nil
}

// readFacts asks what this generation's first prompt owes, after the ledger moved to it, so a fact
// that lands later reaches this generation as a message and not by this read. A control plane that
// is away leaves what a resume was told at its claim. The ledger keeps those facts under a work order,
// so a run with none, an orchestrator's or an assistant's, owes its prompt none: a member's messages
// reach an orchestrator in its wakes and an assistant in its conversation.
func (r *laneRun) readFacts(ctx context.Context) {
	r.setFacts(nil)
	if r.lease.WorkOrder == "" {
		return
	}
	first := r.currentGeneration() == firstGeneration
	if first && !r.holdsToLimit() {
		return
	}
	r.handleLock.Lock()
	r.allowanceTriedAt = r.w.clock.Now()
	r.handleLock.Unlock()
	held, err := r.laneFacts(ctx)
	if r.holdsToLimit() {
		// A lane that cannot say what its work order allows is held after a span and not run unheld for good.
		if err == nil {
			r.setAllowance(held.Spend)
		} else {
			r.noteAllowanceFailed()
		}
	}
	switch {
	case err == nil:
		if !first {
			r.setFacts(held.Facts)
			r.setPack(held.Pack)
		}
	case first:
		r.w.config.Report(fmt.Errorf("lane %s: what its work order allows it to spend: %w", r.lease.Lane, err))
	case r.lease.Resume != nil:
		r.w.config.Report(fmt.Errorf("lane %s: what its prompt owes: %w", r.lease.Lane, err))
		r.setFacts(r.lease.Resume.Facts)
	default:
		r.w.config.Report(fmt.Errorf("lane %s: what its prompt owes: %w", r.lease.Lane, err))
	}
}

// laneFacts reads what only the ledger knows of this lane, in the time a handoff can spare.
func (r *laneRun) laneFacts(ctx context.Context) (ledger.LaneFacts, error) {
	bounded, cancel := context.WithTimeout(ctx, laneFactsDeadline)
	defer cancel()
	return r.w.link.LaneFacts(bounded, ledger.LaneFactsRequest{
		Auth: r.w.Auth(), Epoch: r.w.epoch(), Project: r.lease.Project, WorkOrder: r.lease.WorkOrder, Fence: r.lease.Fence, Lane: r.lease.Lane,
		Generation: r.currentGeneration(),
	})
}

func (r *laneRun) setFacts(facts []ledger.MessageFact) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.facts, r.carried = facts, nil
	for _, fact := range facts {
		r.carried = append(r.carried, fact.ID)
	}
}

func (r *laneRun) factsOwed() []ledger.MessageFact {
	if dropsFacts() {
		return nil
	}
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.facts
}

func (r *laneRun) carriedIDs() []string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.carried
}
