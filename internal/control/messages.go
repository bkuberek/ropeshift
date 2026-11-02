package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"ropeshift.dev/ropeshift/internal/authority"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// The operations of a message.
const (
	opMessageSend   = "message.send"
	opMessageExpire = "message.expire"
)

// Messages holds the use cases of messages to lanes. Orchestrators do not chat and ropeshift has
// no general peer bus, so this is a narrow path: a message tells a lane to look, and a message
// never counts as a permission decision or as consent (FR-MSG-6).
type Messages struct {
	store   ports.Store
	windows messages.Windows
}

// NewMessages returns the use cases over a Store. A kind the windows leave out takes the default.
func NewMessages(store ports.Store, windows messages.Windows) *Messages {
	return &Messages{store: store, windows: windows}
}

// Send records a message for a lane. The client generates its ID, so a retry reads what the
// first attempt wrote. A zero generation is the lane's current one.
func (m *Messages) Send(ctx context.Context, scope ledger.WorkspaceID, draft messages.Draft, actor ledger.Actor) (messages.Message, error) {
	if !authority.Allowed(actor, authority.ActionSendMessage) {
		return messages.Message{}, workorders.ErrForbidden
	}
	if _, err := messages.New(draft, messages.Sender{}, 0, messages.DefaultWindow); err != nil {
		return messages.Message{}, err
	}
	var record messages.Message
	err := m.store.Write(ctx, scope, opMessageSend, func(tx ports.WriteTx) error {
		existing, err := tx.Messages().Message(draft.ID)
		if err == nil {
			record = existing
			return nil
		}
		if !errors.Is(err, ports.ErrNotFound) {
			return err
		}
		record, err = sendIn(tx, draft, m.windows, actor)
		return err
	})
	if err != nil {
		return messages.Message{}, err
	}
	return record, nil
}

// sendIn records a message in the transaction that caused it. A message for a generation that
// ended meets the rollover table here, so it is never offered to anyone (FR-MSG-5).
func sendIn(tx ports.WriteTx, draft messages.Draft, windows messages.Windows, actor ledger.Actor) (messages.Message, error) {
	row, err := tx.Lanes().Row(draft.To.Lane)
	if err != nil {
		return messages.Message{}, err
	}
	if draft.To.Generation == 0 {
		draft.To.Generation = row.Generation
	}
	seq, err := tx.Messages().NextSeq(string(actor.Kind), actor.ID)
	if err != nil {
		return messages.Message{}, err
	}
	from := messages.Sender{Kind: string(actor.Kind), ID: actor.ID, Seq: seq}
	record, err := messages.New(draft, from, tx.Now(), windows.For(draft.Kind))
	if err != nil {
		return messages.Message{}, err
	}
	if err := tx.Messages().Insert(record); err != nil {
		return messages.Message{}, err
	}
	crashpoint.Hit("message.send", "inserted")
	if err := appendMessage(tx, record, actor, noStamp); err != nil {
		return messages.Message{}, err
	}
	open, err := generationOpen(tx, draft.To)
	if err != nil || open {
		return record, err
	}
	rolled := messages.Rollover(record.State, record.Kind, row.Status.ActualState != ledger.StateEnded)
	return rollOne(tx, record, rolled, actor)
}

// generationOpen says a session of this generation may still read a message. The ledger's record
// of the generation decides, because a lane's reported state can lag behind it.
func generationOpen(tx ports.WriteTx, to messages.Target) (bool, error) {
	generations, err := tx.Lanes().Generations(to.Lane)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(generations, func(g ledger.Generation) bool { return g.Number == to.Generation && g.EndedAt == 0 }), nil
}

func appendMessage(tx ports.WriteTx, record messages.Message, actor ledger.Actor, stamp func(*ledger.EventDraft)) error {
	event, err := messages.RecordEvent(record, actor)
	if err != nil {
		return err
	}
	stamp(&event)
	_, err = tx.Append(event)
	return err
}

// moveMessage takes one edge of the delivery table, under the state the caller read.
func moveMessage(tx ports.WriteTx, record messages.Message, to messages.State, reason string, actor ledger.Actor, stamp func(*ledger.EventDraft)) (messages.Message, error) {
	from := record.State
	record.State, record.Reason = to, reason
	won, err := tx.Messages().Save(record, from)
	if err != nil {
		return messages.Message{}, err
	}
	if !won {
		return messages.Message{}, fmt.Errorf("message %s left %s in this transaction", record.ID, from)
	}
	return record, appendMessage(tx, record, actor, stamp)
}

func rollOne(tx ports.WriteTx, record messages.Message, rolled messages.Rolled, actor ledger.Actor) (messages.Message, error) {
	switch rolled {
	case messages.RollFact:
		return moveMessage(tx, record, messages.Expired, messages.ReasonLedgerFact, actor, noStamp)
	case messages.RollExpire:
		return moveMessage(tx, record, messages.Expired, messages.ReasonGenerationEnded, actor, noStamp)
	}
	return record, nil
}

// rollMessages applies the rollover table to what a lane's generations left, in the transaction
// that ends one. A message that has not entered a turn dies with its process, with no receipt, so
// this is how its sender learns. Next says a generation can follow, and nothing is retargeted.
func rollMessages(tx ports.WriteTx, lane string, generation int64, next bool, actor ledger.Actor) error {
	if leavesMessages() {
		return nil
	}
	rows, err := tx.Messages().Unsettled(lane, generation)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := rollOne(tx, row, messages.Rollover(row.State, row.Kind, next), actor); err != nil {
			return err
		}
		crashpoint.Hit("message.rollover", "moved")
	}
	return nil
}

// rollWorkOrder is the end of every lane of a work order that will have no next generation.
func rollWorkOrder(tx ports.WriteTx, workOrder string, actor ledger.Actor) error {
	rows, err := tx.Lanes().Rows()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.WorkOrder != workOrder {
			continue
		}
		if err := rollMessages(tx, row.ID, row.Generation, false, actor); err != nil {
			return err
		}
	}
	return nil
}

// ReadMessage reads one message, which is how a sender learns what became of it.
func (m *Messages) ReadMessage(ctx context.Context, scope ledger.WorkspaceID, id string) (messages.Message, error) {
	var record messages.Message
	err := m.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Messages().Message(id)
		return err
	})
	return record, err
}

// LaneMessages reads what was sent to a lane, oldest first.
func (m *Messages) LaneMessages(ctx context.Context, scope ledger.WorkspaceID, lane string) ([]messages.Message, error) {
	var rows []messages.Message
	err := m.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		rows, err = tx.Messages().ToLane(lane)
		return err
	})
	return rows, err
}

// ExpireMessages ends what outlived its window. It reads with no lock, and each message is read
// again in a short transaction of its own, because a signal can arrive after the list.
func (m *Messages) ExpireMessages(ctx context.Context, scope ledger.WorkspaceID, actor ledger.Actor) ([]string, error) {
	var due []string
	err := m.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		now, err := tx.Now()
		if err != nil {
			return err
		}
		due, err = tx.Messages().Due(now)
		return err
	})
	if err != nil {
		return nil, err
	}
	crashpoint.Hit("message.expire", "listed")
	var expired []string
	for _, id := range due {
		moved := false
		err := m.store.Write(ctx, scope, opMessageExpire, func(tx ports.WriteTx) error {
			row, err := tx.Messages().Message(id)
			if err != nil {
				return err
			}
			if row.State.Settled() && !expiresSettled() {
				return nil
			}
			_, err = moveMessage(tx, row, messages.Expired, messages.ReasonTimedOut, actor, noStamp)
			moved = err == nil
			return err
		})
		if err != nil {
			return expired, err
		}
		if moved {
			expired = append(expired, id)
		}
	}
	return expired, nil
}

// workerReports are what a worker can say of a message. Queued and expired are the ledger's.
var workerReports = []messages.State{messages.AtWorker, messages.HandedOff, messages.Admitted, messages.Refused, messages.Replied}

// messageUpload stores a worker's word on one message. A worker that ran this claim speaks for
// its lane, and only for messages to that lane, so a word from a lane that moved on still counts:
// what happened to a message stays true. A word the table refuses changes nothing, because a
// signal can arrive late or twice, and that is not an error.
func messageUpload(tx ports.WriteTx, worker, id string, e ledger.UploadEvent) error {
	var report messages.Report
	if err := json.Unmarshal(e.Payload, &report); err != nil {
		return fmt.Errorf("%w: %v", workorders.ErrInvalid, err)
	}
	if !slices.Contains(workerReports, report.State) {
		return fmt.Errorf("%w: a worker cannot report %q", workorders.ErrInvalid, report.State)
	}
	if err := ranLane(tx, worker, e); err != nil {
		return err
	}
	row, err := tx.Messages().Message(report.ID)
	if err != nil {
		return err
	}
	if row.To.Lane != e.Lane && !readsAnyLane() {
		return fmt.Errorf("%w: %v: %s of %s", workorders.ErrInvalid, messages.ErrForeign, report.ID, e.Lane)
	}
	if !messages.CanMove(row.State, report.State) {
		return nil
	}
	row.Reply = report.Reply
	stamp := func(draft *ledger.EventDraft) { draft.ID, draft.OccurredAt = id, e.OccurredAt }
	_, err = moveMessage(tx, row, report.State, "", ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, stamp)
	return err
}

// carryMessages settles what a generation's first prompt stated, in the transaction that shows
// the prompt reached a turn. A message of any other lane is no fact of this one.
func carryMessages(tx ports.WriteTx, worker, lane string, generation int64, ids []string) error {
	for _, id := range ids {
		row, err := tx.Messages().Message(id)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if row.To.Lane != lane || row.CarriedIn != 0 {
			continue
		}
		row.CarriedIn = generation
		won, err := tx.Messages().Save(row, row.State)
		if err != nil {
			return err
		}
		if !won {
			return fmt.Errorf("message %s moved in this transaction", id)
		}
		if err := appendMessage(tx, row, ledger.Actor{Kind: ledger.ActorWorker, ID: worker}, noStamp); err != nil {
			return err
		}
	}
	return nil
}

// desiredMessages reads what no worker has acknowledged for a lane.
func desiredMessages(tx ports.ReadTx, lane string, now int64) ([]ledger.DesiredMessage, error) {
	rows, err := tx.Messages().Queued(lane)
	if err != nil {
		return nil, err
	}
	var out []ledger.DesiredMessage
	for _, row := range rows {
		out = append(out, ledger.DesiredMessage{
			ID: row.ID, Lane: row.To.Lane, Generation: row.To.Generation, FromKind: row.From.Kind, ReplyTo: row.ReplyTo, Kind: string(row.Kind),
			Body: row.Body, Traceparent: row.Traceparent, AgeMs: now - row.CreatedAt, StaleAfterMs: row.StaleAt - row.CreatedAt, ExpireAfterMs: row.ExpiresAt - row.CreatedAt,
		})
	}
	return out, nil
}

// messageFacts reads what a generation's first prompt owes.
func messageFacts(reader ports.MessageReader, lane string, ended int64) ([]ledger.MessageFact, error) {
	rows, err := reader.Facts(lane, ended)
	if err != nil {
		return nil, err
	}
	var facts []ledger.MessageFact
	for _, row := range rows {
		facts = append(facts, ledger.MessageFact{
			ID: row.ID, Kind: string(row.Kind), ReplyTo: row.ReplyTo, Body: row.Body, Unanswered: row.State == messages.Admitted,
		})
	}
	return facts, nil
}
