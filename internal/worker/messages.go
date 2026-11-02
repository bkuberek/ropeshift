package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
)

// messageLabel names a message's one write, which is the key that keeps it to one.
const messageLabel = "message/"

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
	return lanes.Record{ID: lease.Lane, WorkOrder: lease.WorkOrder, ClaimID: lease.ClaimID, Fence: lease.Fence, Generation: generation}
}

func (w *Worker) messageEvent(record lanes.Record, report messages.Report) (ledger.UploadEvent, error) {
	payload, err := json.Marshal(report)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return w.event(ledger.UploadMessage, record, payload), nil
}

// deliver writes one message, at most once, for as long as it can be written, and says whether it
// wrote. A message for a generation this run left is never injected, and neither is one past its
// window, because a stale instruction is worse than none. Either way the ledger tells the sender.
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
	begun, err := r.w.journal.BeginEffect(ctx, effect)
	if err != nil || !begun {
		return false, err
	}
	crashpoint.Hit("worker.message", "begun")
	_, sent := r.rt.Driver.Send(ctx, handle, ports.Input{ID: held.InputID, Text: messages.Render(held.Message, age, window), Peer: true})
	crashpoint.Hit("worker.message", "written")
	if sent != nil {
		if err := r.w.journal.FinishEffect(context.WithoutCancel(ctx), effect.Key, lanes.EffectFailed, sent.Error()); err != nil {
			return false, errors.Join(sent, err)
		}
		if errors.Is(sent, ports.ErrNoChannel) || errors.Is(sent, ports.ErrNoSession) {
			return false, nil
		}
		return false, sent
	}
	return true, r.recordHandedOff(ctx, held, effect.Key)
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
		go w.uploadSoon(ctx)
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

// uploadSoon sends what a hook left, apart from the hook, which has milliseconds to answer.
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
	go w.uploadSoon(ctx)
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
// is away leaves what a resume was told at its claim.
func (r *laneRun) readFacts(ctx context.Context) {
	r.setFacts(nil)
	if r.currentGeneration() == firstGeneration {
		return
	}
	bounded, cancel := context.WithTimeout(ctx, laneFactsDeadline)
	defer cancel()
	held, err := r.w.link.LaneFacts(bounded, ledger.LaneFactsRequest{
		Auth: r.w.Auth(), Epoch: r.w.epoch(), WorkOrder: r.lease.WorkOrder, Fence: r.lease.Fence, Lane: r.lease.Lane,
	})
	switch {
	case err == nil:
		r.setFacts(held.Facts)
	case r.lease.Resume != nil:
		r.w.config.Report(fmt.Errorf("lane %s: what its prompt owes: %w", r.lease.Lane, err))
		r.setFacts(r.lease.Resume.Facts)
	default:
		r.w.config.Report(fmt.Errorf("lane %s: what its prompt owes: %w", r.lease.Lane, err))
	}
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
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.facts
}

func (r *laneRun) carriedIDs() []string {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.carried
}
