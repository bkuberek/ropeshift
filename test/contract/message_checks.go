package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// MessageChecks are what the ledger promises a message (FR-MSG-1 to FR-MSG-6). A worker's half is
// in the worker's tests, because this suite holds no session.
var MessageChecks = []Check{
	{"M1", "a message is recorded queued, with its envelope, and a retry sends it once", checkMessageRecorded},
	{"M2", "each sender counts its own messages", checkMessageSequence},
	{"M3", "only a member or an orchestrator sends", checkMessageSenders},
	{"M4", "a message to no lane is refused", checkMessageNoLane},
	{"M5", "a queued message stays offered until its worker acknowledges it", checkMessageOffered},
	{"M6", "a worker's reports walk the table, and a word the table refuses changes nothing", checkMessageReports},
	{"M7", "a worker that never ran the claim cannot speak for a message", checkMessageOtherWorker},
	{"M8", "a worker cannot speak for a message to another lane through its own", checkMessageOtherLane},
	{"M9", "a worker reports no state the ledger owns", checkMessageLedgerStates},
	{"M10", "a message past its window expires with a reason, and one inside it stays", checkMessageExpiry},
	{"M11", "a tick that read a stale list expires nothing a signal moved", checkMessageStaleTick},
	{"M12", "a stale read cannot move a message", checkMessageCompareAndSet},
	{"M13", "a late admission corrects an expiry, and a late hand-off does not", checkMessageLateWord},
	{"M14", "the end of a generation follows the rollover table, on every path", checkMessageRollover},
	{"M15", "a message to a generation that ended is never offered", checkMessageEndedGeneration},
	{"M16", "a fact reaches a generation's first prompt, and a binding that names it settles it", checkMessageFacts},
	{"M17", "an admitted message with no reply is listed for the next generation, once", checkMessageUnanswered},
	{"M18", "a reply correlates by reply_to, and the first one stands", checkMessageReply},
	{"M19", "a message decides nothing", checkMessageDecidesNothing},
	{"M20", "a window is configured per kind", checkMessageWindows},
	{"M21", "a retried report stores nothing twice", checkMessageReportRepeat},
}

// farAhead passes any window.
const farAhead = (10*time.Minute + time.Second) / time.Millisecond

func (s *statusRig) sendTo(lane string, kind messages.Kind, change func(*messages.Draft)) (messages.Message, error) {
	draft := messages.Draft{ID: s.r.ID("m"), To: messages.Target{Lane: lane}, Kind: kind, Body: "Use two retries."}
	if change != nil {
		change(&draft)
	}
	return s.r.NewOps(s.store).Send(s.r.Ctx, s.r.Scope, draft, s.r.Actor)
}

func (s *statusRig) sent(kind messages.Kind) (messages.Message, error) {
	return s.sendTo(s.lane, kind, nil)
}

func (s *statusRig) message(id string) (messages.Message, error) {
	return s.r.NewOps(s.store).ReadMessage(s.r.Ctx, s.r.Scope, id)
}

func (s *statusRig) reportMessage(generation int64, report messages.Report) (ledger.UploadReply, error) {
	raw, err := json.Marshal(report)
	if err != nil {
		return ledger.UploadReply{}, err
	}
	return s.uploadAt(generation, ledger.UploadMessage, raw)
}

// walked reports each state in turn, as a worker would.
func (s *statusRig) walked(id string, states ...messages.State) error {
	for _, state := range states {
		if err := accepted(s.reportMessage(1, messages.Report{ID: id, State: state})); err != nil {
			return err
		}
	}
	return nil
}

func (s *statusRig) expectMessage(id string, state messages.State, reason string) error {
	m, err := s.message(id)
	if err != nil {
		return err
	}
	if m.State != state || m.Reason != reason {
		return Violatedf("message %s reads %s (%s), want %s (%s)", id, m.State, m.Reason, state, reason)
	}
	return nil
}

// offered reads what desired state offers this worker for its lane.
func (s *statusRig) offered() ([]ledger.DesiredMessage, error) {
	state, err := s.plane.Desired(s.r.Ctx, ledger.DesiredRequest{Auth: s.auth})
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredMessage
	for _, lease := range state.Leases {
		if lease.Lane == s.lane {
			out = append(out, lease.Messages...)
		}
	}
	return out, nil
}

func messageIDs(offered []ledger.DesiredMessage) []string {
	return project(offered, func(m ledger.DesiredMessage) string { return m.ID })
}

// CheckMessages compares every message with what the log replays to, and holds each sender's
// count to one use of each number.
func (r *Rig) CheckMessages(s ports.Store) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	book, err := messages.Replay(events)
	if err != nil {
		return Violatedf("the log does not replay: %v", err)
	}
	var rows []messages.Message
	err = s.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Messages().All()
		return err
	})
	if err != nil {
		return err
	}
	if len(rows) != len(book) {
		return Violatedf("%d messages and %d in the log", len(rows), len(book))
	}
	used := map[messages.Sender]bool{}
	for _, row := range rows {
		want, ok := book[row.ID]
		if !ok {
			return Violatedf("message %s has no history", row.ID)
		}
		if !reflect.DeepEqual(row, want) {
			return Violatedf("message %s disagrees with its log:\nrow %+v\nlog %+v", row.ID, row, want)
		}
		if used[row.From] {
			return Violatedf("a sender used %+v twice", row.From)
		}
		used[row.From] = true
	}
	return nil
}

func checkMessageRecorded(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	draft := messages.Draft{
		ID: r.ID("m"), To: messages.Target{Lane: s.lane}, Kind: messages.KindInstruction, Body: "Stop at the next commit.", ReplyTo: "q-1",
		Traceparent: "00-4bf92f3577b34da6-00f067aa0ba902b7-01",
	}
	ops := r.NewOps(s.store)
	first, err := ops.Send(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if first.Envelope != messages.EnvelopeVersion || first.State != messages.Queued || first.From.Kind != string(r.Actor.Kind) || first.From.ID != r.Actor.ID ||
		first.To.Generation != 1 || first.ReplyTo != "q-1" || first.Traceparent != draft.Traceparent || first.ExpiresAt <= first.CreatedAt {
		return Violatedf("recorded %+v", first)
	}
	again, err := ops.Send(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(first, again) {
		return Violatedf("a retry read %+v, and the first read %+v", again, first)
	}
	if err := countOf(r, s.store, messages.EventChanged, first.ID, 1); err != nil {
		return err
	}
	return r.CheckMessages(s.store)
}

// countOf counts one record's events.
func countOf(r *Rig, s ports.Store, eventType, subject string, want int) error {
	events, err := r.Events(s, r.Scope)
	if err != nil {
		return err
	}
	got := 0
	for _, e := range events {
		if e.Type == eventType && e.Subject.ID == subject {
			got++
		}
	}
	if got != want {
		return Violatedf("%d %s events for %s, want %d", got, eventType, subject, want)
	}
	return nil
}

func checkMessageSequence(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(s.store)
	send := func(actor ledger.Actor) (int64, error) {
		draft := messages.Draft{ID: r.ID("m"), To: messages.Target{Lane: s.lane}, Kind: messages.KindNotice, Body: "A notice."}
		m, err := ops.Send(r.Ctx, r.Scope, draft, actor)
		return m.From.Seq, err
	}
	other := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	want := []struct {
		actor ledger.Actor
		seq   int64
	}{{r.Actor, 1}, {r.Actor, 2}, {other, 1}, {r.Actor, 3}, {other, 2}}
	for i, step := range want {
		seq, err := send(step.actor)
		if err != nil {
			return err
		}
		if seq != step.seq {
			return Violatedf("send %d numbered %d, want %d", i, seq, step.seq)
		}
	}
	return r.CheckMessages(s.store)
}

func checkMessageSenders(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	ops := r.NewOps(s.store)
	for _, kind := range []ledger.ActorKind{ledger.ActorLane, ledger.ActorWorker, ledger.ActorSystem} {
		draft := messages.Draft{ID: r.ID("m"), To: messages.Target{Lane: s.lane}, Kind: messages.KindInstruction, Body: "Stop."}
		_, err := ops.Send(r.Ctx, r.Scope, draft, ledger.Actor{Kind: kind, ID: "a"})
		if !errors.Is(err, workorders.ErrForbidden) {
			return Violatedf("a %s sent a message: %v", kind, err)
		}
	}
	draft := messages.Draft{ID: r.ID("m"), To: messages.Target{Lane: s.lane}, Kind: messages.KindInstruction, Body: "Stop."}
	if _, err := ops.Send(r.Ctx, r.Scope, draft, ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "o"}); err != nil {
		return Violatedf("an orchestrator could not send: %v", err)
	}
	return nil
}

func checkMessageNoLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if _, err := s.sendTo("no-such-lane", messages.KindNotice, nil); !errors.Is(err, ports.ErrNotFound) {
		return Violatedf("a message to no lane: %v", err)
	}
	if _, err := s.sendTo(s.lane, "chat", nil); !errors.Is(err, messages.ErrInvalid) {
		return Violatedf("a message of no kind: %v", err)
	}
	return nil
}

// checkMessageOffered is transport acknowledgement stopping at the worker: the control plane goes
// on offering a message until the worker says it holds it, and no longer.
func checkMessageOffered(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	first, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	second, err := s.sent(messages.KindNotice)
	if err != nil {
		return err
	}
	for range 2 {
		got, err := s.offered()
		if err != nil {
			return err
		}
		if !slices.Equal(messageIDs(got), []string{first.ID, second.ID}) {
			return Violatedf("offered %+v", got)
		}
		if got[0].Lane != s.lane || got[0].Generation != 1 || got[0].StaleAfterMs == 0 || got[0].ExpireAfterMs == 0 || got[0].Body != first.Body || got[0].AgeMs < 0 {
			return Violatedf("offered %+v", got[0])
		}
	}
	if err := s.walked(first.ID, messages.AtWorker); err != nil {
		return err
	}
	got, err := s.offered()
	if err != nil {
		return err
	}
	if !slices.Equal(messageIDs(got), []string{second.ID}) {
		return Violatedf("offered %+v after an acknowledgement", got)
	}
	return r.CheckMessages(s.store)
}

func checkMessageReports(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	for _, state := range []messages.State{messages.AtWorker, messages.HandedOff, messages.Admitted} {
		if err := s.walked(m.ID, state); err != nil {
			return err
		}
		if err := s.expectMessage(m.ID, state, ""); err != nil {
			return err
		}
	}
	if err := s.walked(m.ID, messages.AtWorker); err != nil {
		return err
	}
	if err := s.expectMessage(m.ID, messages.Admitted, ""); err != nil {
		return err
	}
	refused, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := s.walked(refused.ID, messages.AtWorker, messages.HandedOff, messages.Refused, messages.Admitted); err != nil {
		return err
	}
	if err := s.expectMessage(refused.ID, messages.Refused, ""); err != nil {
		return err
	}
	return r.CheckMessages(s.store)
}

func checkMessageOtherWorker(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	stranger, err := newStatusRig(r)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(messages.Report{ID: m.ID, State: messages.AtWorker})
	if err != nil {
		return err
	}
	// The stranger names the first worker's claim and lane, so only the claim's owner stops it.
	stranger.seq++
	reply, err := stranger.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: stranger.auth, Epoch: stranger.epoch, Events: []ledger.UploadEvent{{
		Epoch: stranger.epoch, Seq: stranger.seq, Type: ledger.UploadMessage, WorkOrder: s.grant.WorkOrder, ClaimID: s.grant.ClaimID,
		Fence: s.grant.Fence, Lane: s.lane, Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("a stranger spoke for a message: %+v", reply)
	}
	return s.expectMessage(m.ID, messages.Queued, "")
}

// checkMessageOtherLane has a worker that holds both lanes, so the claim is its own and only the
// message's address stops it.
func checkMessageOtherLane(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	otherOrder, err := r.Ready(s.store)
	if err != nil {
		return err
	}
	otherGrant, err := s.plane.Claim(r.Ctx, s.auth, workorders.ClaimRequest{WorkOrder: otherOrder, ClaimID: r.ID("claim"), TTLMs: TTLMs})
	if err != nil {
		return err
	}
	otherLane := r.ID("lane")
	if _, err := r.NewOps(s.store).StartLane(r.Ctx, r.Scope, otherGrant.Lease, otherLane, r.Actor); err != nil {
		return err
	}
	raw, err := json.Marshal(messages.Report{ID: m.ID, State: messages.Replied, Reply: "Done."})
	if err != nil {
		return err
	}
	s.seq++
	reply, err := s.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq, Type: ledger.UploadMessage, WorkOrder: otherOrder, ClaimID: otherGrant.ClaimID,
		Fence: otherGrant.Fence, Lane: otherLane, Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("one lane replied for another: %+v", reply)
	}
	return s.expectMessage(m.ID, messages.Queued, "")
}

func checkMessageLedgerStates(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	for _, state := range []messages.State{messages.Queued, messages.Expired, "chatting"} {
		reply, err := s.reportMessage(1, messages.Report{ID: m.ID, State: state})
		if err != nil {
			return err
		}
		if len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
			return Violatedf("a worker reported %s: %+v", state, reply)
		}
	}
	reply, err := s.reportMessage(1, messages.Report{ID: "no-such-message", State: messages.AtWorker})
	if err != nil {
		return err
	}
	if len(reply.Rejected) != 1 {
		return Violatedf("a report on no message: %+v", reply)
	}
	return s.expectMessage(m.ID, messages.Queued, "")
}

func checkMessageExpiry(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	queued, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	held, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	handed, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	admitted, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := s.walked(held.ID, messages.AtWorker); err != nil {
		return err
	}
	if err := s.walked(handed.ID, messages.AtWorker, messages.HandedOff); err != nil {
		return err
	}
	if err := s.walked(admitted.ID, messages.AtWorker, messages.HandedOff, messages.Admitted); err != nil {
		return err
	}
	ops := r.NewOps(s.store)
	r.DB.ShiftClock(int64(9 * time.Minute / time.Millisecond))
	if expired, err := ops.ExpireMessages(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	} else if len(expired) != 0 {
		return Violatedf("a tick expired %v inside the window", expired)
	}
	r.DB.ShiftClock(int64(2 * time.Minute / time.Millisecond))
	expired, err := ops.ExpireMessages(r.Ctx, r.Scope, r.Actor)
	if err != nil {
		return err
	}
	slices.Sort(expired)
	want := []string{queued.ID, held.ID, handed.ID}
	slices.Sort(want)
	if !slices.Equal(expired, want) {
		return Violatedf("a tick expired %v, want %v", expired, want)
	}
	for _, id := range want {
		if err := s.expectMessage(id, messages.Expired, messages.ReasonTimedOut); err != nil {
			return err
		}
	}
	if err := s.expectMessage(admitted.ID, messages.Admitted, ""); err != nil {
		return err
	}
	got, err := s.offered()
	if err != nil {
		return err
	}
	if len(got) != 0 {
		return Violatedf("an expired message is still offered: %+v", got)
	}
	// What is settled stays out of a tick's list, however far time goes.
	err = s.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err := tx.Messages().Due(now + int64(farAhead))
		if err != nil {
			return err
		}
		if len(due) != 0 {
			return Violatedf("a tick would read %v", due)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return r.CheckMessages(s.store)
}

// checkMessageStaleTick stops a tick between its list and its write, with a signal in between. The
// re-read in the write is the only thing in the way.
func checkMessageStaleTick(r *Rig) error {
	base, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := base.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := base.walked(m.ID, messages.AtWorker, messages.HandedOff); err != nil {
		return err
	}
	r.DB.ShiftClock(int64(farAhead))
	signaled := false
	stepped := r.StoreWith(func(op, name string) {
		if op != OpMessageExpire || name != StepBefore || signaled {
			return
		}
		signaled = true
		if err := base.walked(m.ID, messages.Admitted); err != nil {
			r.T.Error(err)
		}
	})
	if expired, err := r.NewOps(stepped).ExpireMessages(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	} else if len(expired) != 0 {
		return Violatedf("a stale tick expired %v", expired)
	}
	if !signaled {
		return Violatedf("the tick never reached its write")
	}
	return base.expectMessage(m.ID, messages.Admitted, "")
}

// checkMessageCompareAndSet calls the statement with a read that went stale, because in a
// transaction that holds the revision nothing in the product can.
func checkMessageCompareAndSet(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := s.walked(m.ID, messages.AtWorker); err != nil {
		return err
	}
	stale := m
	stale.State = messages.Expired
	var won bool
	err = s.store.Write(r.Ctx, r.Scope, "note", func(tx ports.WriteTx) error {
		won, err = tx.Messages().Save(stale, messages.Queued)
		if err != nil {
			return err
		}
		_, err := tx.Append(r.noteDraft("stale"))
		return err
	})
	if err != nil {
		return err
	}
	if won {
		return Violatedf("a read that went stale moved a message")
	}
	return s.expectMessage(m.ID, messages.AtWorker, "")
}

// checkMessageLateWord is a clock that spoke first. An admission is a fact that corrects it, and a
// hand-off proves nothing a sender can use, so an expiry stands against one.
func checkMessageLateWord(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := s.walked(m.ID, messages.AtWorker); err != nil {
		return err
	}
	r.DB.ShiftClock(int64(farAhead))
	if _, err := r.NewOps(s.store).ExpireMessages(r.Ctx, r.Scope, r.Actor); err != nil {
		return err
	}
	if err := s.walked(m.ID, messages.HandedOff); err != nil {
		return err
	}
	if err := s.expectMessage(m.ID, messages.Expired, messages.ReasonTimedOut); err != nil {
		return err
	}
	if err := s.walked(m.ID, messages.Admitted); err != nil {
		return err
	}
	if err := s.expectMessage(m.ID, messages.Admitted, ""); err != nil {
		return err
	}
	return r.CheckMessages(s.store)
}

// rolloverRow is one message as a generation ends.
type rolloverRow struct {
	kind   messages.Kind
	states []messages.State
}

// rolloverRows are the design's table, plus a message nothing touched.
var rolloverRows = []rolloverRow{
	{messages.KindAnswer, nil},
	{messages.KindDecisionNotice, []messages.State{messages.AtWorker}},
	{messages.KindAnswer, []messages.State{messages.AtWorker, messages.HandedOff}},
	{messages.KindInstruction, nil},
	{messages.KindNotice, []messages.State{messages.AtWorker}},
	{messages.KindInstruction, []messages.State{messages.AtWorker, messages.HandedOff}},
	{messages.KindInstruction, []messages.State{messages.AtWorker, messages.HandedOff, messages.Admitted}},
	{messages.KindAnswer, []messages.State{messages.AtWorker, messages.HandedOff, messages.Admitted}},
	{messages.KindInstruction, []messages.State{messages.AtWorker, messages.HandedOff, messages.Replied}},
}

// rolloverPaths are the ways a generation ends. Next says a generation can follow it.
var rolloverPaths = []struct {
	name string
	next bool
	end  func(*statusRig) error
}{
	{"a restart", true, func(s *statusRig) error { return s.nextGeneration(2, ledger.StartCrashRestart) }},
	{"a handoff", true, func(s *statusRig) error { return s.nextGeneration(2, ledger.StartHandoff) }},
	{"a parked commit", true, func(s *statusRig) error {
		const wake = "w"
		if err := accepted(s.parkingUpload(wake, `{"human_resume":true}`)); err != nil {
			return err
		}
		return accepted(s.parkedUpload(wake, true))
	}},
	{"a lane's end", false, func(s *statusRig) error {
		raw, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}})
		if err != nil {
			return err
		}
		return accepted(s.upload(ledger.UploadLaneEnded, raw))
	}},
	{"a reassignment", false, func(s *statusRig) error {
		if err := s.r.Expire(s.store); err != nil {
			return err
		}
		return s.r.NewOps(s.store).Reassign(s.r.Ctx, s.r.Scope, s.grant.WorkOrder, "gone", s.r.Actor)
	}},
}

func wantRolled(row rolloverRow, next bool) (messages.State, string) {
	last := messages.Queued
	if len(row.states) != 0 {
		last = row.states[len(row.states)-1]
	}
	switch {
	case last == messages.Admitted || last == messages.Replied:
		return last, ""
	case row.kind.DecisionBearing() && next:
		return messages.Expired, messages.ReasonLedgerFact
	}
	return messages.Expired, messages.ReasonGenerationEnded
}

func checkMessageRollover(r *Rig) error {
	for _, path := range rolloverPaths {
		s, err := newStatusRig(r)
		if err != nil {
			return err
		}
		ids := make([]string, len(rolloverRows))
		for i, row := range rolloverRows {
			m, err := s.sent(row.kind)
			if err != nil {
				return err
			}
			if err := s.walked(m.ID, row.states...); err != nil {
				return err
			}
			ids[i] = m.ID
		}
		if err := path.end(s); err != nil {
			return fmt.Errorf("%s: %w", path.name, err)
		}
		for i, row := range rolloverRows {
			state, reason := wantRolled(row, path.next)
			if err := s.expectMessage(ids[i], state, reason); err != nil {
				return fmt.Errorf("%s, row %d: %w", path.name, i, err)
			}
		}
		if err := r.CheckMessages(s.store); err != nil {
			return err
		}
		if err := r.CheckProjection(s.store); err != nil {
			return err
		}
	}
	return nil
}

// checkMessageEndedGeneration sends to a generation that ended, on each way of naming one: never
// injected, whichever way it arrives (FR-MSG-5).
func checkMessageEndedGeneration(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	if err := s.nextGeneration(2, ledger.StartHandoff); err != nil {
		return err
	}
	old := func(d *messages.Draft) { d.To.Generation = 1 }
	instruction, err := s.sendTo(s.lane, messages.KindInstruction, old)
	if err != nil {
		return err
	}
	answer, err := s.sendTo(s.lane, messages.KindAnswer, old)
	if err != nil {
		return err
	}
	if err := s.expectMessage(instruction.ID, messages.Expired, messages.ReasonGenerationEnded); err != nil {
		return err
	}
	if err := s.expectMessage(answer.ID, messages.Expired, messages.ReasonLedgerFact); err != nil {
		return err
	}
	offered, err := s.offered()
	if err != nil {
		return err
	}
	if len(offered) != 0 {
		return Violatedf("offered %+v", offered)
	}
	current, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if current.To.Generation != 2 || current.State != messages.Queued {
		return Violatedf("a message to the current generation reads %+v", current)
	}

	parked, _, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	toParked, err := parked.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := parked.expectMessage(toParked.ID, messages.Expired, messages.ReasonGenerationEnded); err != nil {
		return err
	}
	answerToParked, err := parked.sent(messages.KindAnswer)
	if err != nil {
		return err
	}
	if err := parked.expectMessage(answerToParked.ID, messages.Expired, messages.ReasonLedgerFact); err != nil {
		return err
	}

	// A lane that ended has no next generation, so an answer to it is as lost as anything else.
	ended, err := newStatusRig(r)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}})
	if err != nil {
		return err
	}
	if err := accepted(ended.upload(ledger.UploadLaneEnded, raw)); err != nil {
		return err
	}
	answerToEnded, err := ended.sent(messages.KindAnswer)
	if err != nil {
		return err
	}
	if err := ended.expectMessage(answerToEnded.ID, messages.Expired, messages.ReasonGenerationEnded); err != nil {
		return err
	}
	return r.CheckMessages(s.store)
}

func factIDs(facts []ledger.MessageFact) []string {
	return project(facts, func(f ledger.MessageFact) string { return f.ID })
}

func (s *statusRig) leaseFacts() ([]ledger.MessageFact, error) {
	state, err := s.plane.Desired(s.r.Ctx, ledger.DesiredRequest{Auth: s.auth})
	if err != nil {
		return nil, err
	}
	for _, lease := range state.Leases {
		if lease.Lane == s.lane && lease.Resume != nil {
			return lease.Resume.Facts, nil
		}
	}
	return nil, Violatedf("no lease resumes lane %s", s.lane)
}

func (s *statusRig) laneFacts(fence int64) ([]ledger.MessageFact, error) {
	facts, err := s.plane.LaneFacts(s.r.Ctx, ledger.LaneFactsRequest{
		Auth: s.auth, Epoch: s.epoch, WorkOrder: s.grant.WorkOrder, Fence: fence, Lane: s.lane,
	})
	return facts.Facts, err
}

// checkMessageFacts walks one fact from a generation's end to the prompt that states it. Desired
// state and the read at a start say the same, and a binding settles only what its prompt held.
func checkMessageFacts(r *Rig) error {
	s, _, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	stated, err := s.sendTo(s.lane, messages.KindAnswer, func(d *messages.Draft) { d.ReplyTo = "q-1" })
	if err != nil {
		return err
	}
	late, err := s.sendTo(s.lane, messages.KindAnswer, nil)
	if err != nil {
		return err
	}
	if err := s.woken(); err != nil {
		return err
	}
	grant, err := s.claim()
	if err != nil {
		return err
	}
	s.grant = grant
	want := []string{stated.ID, late.ID}
	offered, err := s.leaseFacts()
	if err != nil {
		return err
	}
	if !slices.Equal(factIDs(offered), want) {
		return Violatedf("desired state offers %+v", offered)
	}
	if offered[0].Body != stated.Body || offered[0].Kind != string(messages.KindAnswer) || offered[0].ReplyTo != "q-1" || offered[0].Unanswered {
		return Violatedf("a fact reads %+v", offered[0])
	}
	if err := accepted(s.resumeUpload(grant, 2, ledger.StartResume)); err != nil {
		return err
	}
	read, err := s.laneFacts(grant.Fence)
	if err != nil {
		return err
	}
	if !slices.Equal(factIDs(read), want) {
		return Violatedf("a start reads %+v", read)
	}
	// Another lane's fact on the same list stays that lane's.
	other, _, err := parkedOn(r, true)
	if err != nil {
		return err
	}
	foreign, err := other.sent(messages.KindAnswer)
	if err != nil {
		return err
	}
	if err := s.bind(2, "session-2", stated.ID, foreign.ID, "no-such-message"); err != nil {
		return err
	}
	if m, err := other.message(foreign.ID); err != nil {
		return err
	} else if m.CarriedIn != 0 {
		return Violatedf("a lane settled another lane's fact: %+v", m)
	}
	left, err := s.laneFacts(grant.Fence)
	if err != nil {
		return err
	}
	if !slices.Equal(factIDs(left), []string{late.ID}) {
		return Violatedf("after a binding, a start reads %+v", left)
	}
	m, err := s.message(stated.ID)
	if err != nil {
		return err
	}
	if m.CarriedIn != 2 {
		return Violatedf("a stated fact reads %+v", m)
	}
	return r.CheckMessages(s.store)
}

func (s *statusRig) bind(generation int64, session string, carried ...string) error {
	raw, err := json.Marshal(workorders.GenerationBinding{SessionID: session, ModelRequested: "m", ModelResolved: "m", ProfileDigest: "p", DriverVersion: "1", Carried: carried})
	if err != nil {
		return err
	}
	return accepted(s.uploadAt(generation, ledger.UploadGenerationBound, raw))
}

// checkMessageUnanswered lists a message once. A generation that bound told its session, and the
// one after it has nothing to check. A generation that never bound told nobody.
func checkMessageUnanswered(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	if err := s.walked(m.ID, messages.AtWorker, messages.HandedOff, messages.Admitted); err != nil {
		return err
	}
	if facts, err := s.laneFacts(s.grant.Fence); err != nil {
		return err
	} else if len(facts) != 0 {
		return Violatedf("the live generation is told of its own message: %+v", facts)
	}
	if err := s.nextGeneration(2, ledger.StartCrashRestart); err != nil {
		return err
	}
	if err := s.nextGeneration(3, ledger.StartCrashRestart); err != nil {
		return err
	}
	facts, err := s.laneFacts(s.grant.Fence)
	if err != nil {
		return err
	}
	if len(facts) != 1 || facts[0].ID != m.ID || !facts[0].Unanswered {
		return Violatedf("a generation that never bound told nobody, and the next reads %+v", facts)
	}
	if err := s.bind(3, "session", m.ID); err != nil {
		return err
	}
	if err := s.nextGeneration(4, ledger.StartCrashRestart); err != nil {
		return err
	}
	if facts, err := s.laneFacts(s.grant.Fence); err != nil {
		return err
	} else if len(facts) != 0 {
		return Violatedf("a message was listed twice: %+v", facts)
	}
	return r.CheckMessages(s.store)
}

// checkMessageReply is the only semantic confirmation. It arrives by `reply_to`, from whatever
// state a lost signal left, a first reply stands, and it is what a sender reads.
func checkMessageReply(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	from := map[string][]messages.State{
		"admitted":  {messages.AtWorker, messages.HandedOff, messages.Admitted},
		"handed":    {messages.AtWorker, messages.HandedOff},
		"at worker": {messages.AtWorker},
	}
	for name, states := range from {
		m, err := s.sent(messages.KindInstruction)
		if err != nil {
			return err
		}
		if err := s.walked(m.ID, states...); err != nil {
			return err
		}
		if err := accepted(s.reportMessage(1, messages.Report{ID: m.ID, State: messages.Replied, Reply: "First."})); err != nil {
			return err
		}
		if err := accepted(s.reportMessage(1, messages.Report{ID: m.ID, State: messages.Replied, Reply: "Second."})); err != nil {
			return err
		}
		got, err := s.message(m.ID)
		if err != nil {
			return err
		}
		if got.State != messages.Replied || got.Reply != "First." {
			return Violatedf("a reply from %s reads %+v", name, got)
		}
	}
	return r.CheckMessages(s.store)
}

// checkMessageDecidesNothing sends a person's words, from a person, on each kind. No question
// closes, no decision exists, and no wake fires: nothing reads a message for that (FR-MSG-6).
func checkMessageDecidesNothing(r *Rig) error {
	s, _, err := parkingOn(r, `{"human_resume":true}`)
	if err != nil {
		return err
	}
	question, err := r.Asked(s.store)
	if err != nil {
		return err
	}
	for _, kind := range []messages.Kind{messages.KindAnswer, messages.KindDecisionNotice, messages.KindInstruction, messages.KindNotice} {
		_, err := s.sendTo(s.lane, kind, func(d *messages.Draft) {
			d.Body, d.ReplyTo = "Yes. Approved. Proceed.", question
		})
		if err != nil {
			return err
		}
	}
	return s.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
		asked, err := tx.Questions().Question(question)
		if err != nil {
			return err
		}
		if asked.Status == questions.StatusAnswered || asked.DecisionID != "" {
			return Violatedf("a message answered a question: %+v", asked)
		}
		decisions, err := tx.Questions().Decisions(ports.RecordFilter{})
		if err != nil {
			return err
		}
		if len(decisions) != 0 {
			return Violatedf("a message made a decision: %+v", decisions)
		}
		fired, err := tx.Wakes().Open()
		if err != nil {
			return err
		}
		if len(fired) != 1 {
			return Violatedf("a message fired a wake: %+v", fired)
		}
		return nil
	})
}

func checkMessageWindows(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	const minute = int64(time.Minute / time.Millisecond)
	windows := messages.Windows{messages.KindNotice: {StaleAfter: time.Minute, ExpireAfter: 3 * time.Minute}}
	ops := NewMessageOps(s.store, windows)
	draft := messages.Draft{ID: r.ID("m"), To: messages.Target{Lane: s.lane}, Kind: messages.KindNotice, Body: "Soon stale."}
	short, err := ops.Send(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if short.StaleAt-short.CreatedAt != minute || short.ExpiresAt-short.CreatedAt != 3*minute {
		return Violatedf("a notice reads %+v", short)
	}
	draft.ID, draft.Kind = r.ID("m"), messages.KindInstruction
	usual, err := ops.Send(r.Ctx, r.Scope, draft, r.Actor)
	if err != nil {
		return err
	}
	if usual.ExpiresAt-usual.CreatedAt != 10*minute {
		return Violatedf("an instruction reads %+v", usual)
	}
	return nil
}

// checkMessageReportRepeat loses an answer: a worker that never heard back sends the same event,
// with the same sequence, and the ledger stores it once.
func checkMessageReportRepeat(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	m, err := s.sent(messages.KindInstruction)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(messages.Report{ID: m.ID, State: messages.AtWorker})
	if err != nil {
		return err
	}
	batch := ledger.UploadBatch{Auth: s.auth, Epoch: s.epoch, Events: []ledger.UploadEvent{{
		Epoch: s.epoch, Seq: s.seq + 1, Type: ledger.UploadMessage, WorkOrder: s.grant.WorkOrder, ClaimID: s.grant.ClaimID,
		Fence: s.grant.Fence, Lane: s.lane, Generation: 1, Payload: raw, OccurredAt: 1,
	}}}
	for range 2 {
		if err := accepted(s.plane.Upload(r.Ctx, batch)); err != nil {
			return err
		}
	}
	if err := countOf(r, s.store, messages.EventChanged, m.ID, 2); err != nil {
		return err
	}
	events, err := r.Events(s.store, r.Scope)
	if err != nil {
		return err
	}
	id := ledger.UploadedEventID(s.auth.WorkerID, s.epoch, s.seq+1)
	if !slices.ContainsFunc(events, func(e ledger.Event) bool { return e.ID == id && e.OccurredAt == 1 }) {
		return Violatedf("no event of the report's own identity, %s", id)
	}
	return nil
}
