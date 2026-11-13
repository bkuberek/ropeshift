package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

// messageTx is the message area of one transaction. No statement here has a limit, because a read
// that stopped at a page would call what lies past it missing.
type messageTx struct {
	*session
	scope ledger.WorkspaceID
}

const messageColumns = `id, envelope, to_lane, to_generation, from_kind, from_id, sender_seq, kind, reply_to, body, state, reason,
       reply, carried_in, created_at, stale_at, expires_at, traceparent, to_kind`

var (
	// stmtInsertMessage takes a client's ID, so a retry sends one message.
	stmtInsertMessage = define("message.insert",
		`INSERT INTO messages (workspace_id, id, envelope, to_kind, to_lane, to_generation, from_kind, from_id, sender_seq, kind, reply_to, body,
  state, reason, created_at, stale_at, expires_at, traceparent)
VALUES (:workspace_id, :id, :envelope, :to_kind, :to_lane, :to_generation, :from_kind, :from_id, :sender_seq, :kind, :reply_to, :body,
  :state, :reason, :created_at, :stale_at, :expires_at, :traceparent)`)

	stmtReadMessage = define("message.read",
		`SELECT `+messageColumns+` FROM messages WHERE workspace_id = :workspace_id AND id = :id`)

	stmtAllMessages = define("message.all",
		`SELECT `+messageColumns+` FROM messages WHERE workspace_id = :workspace_id ORDER BY created_at, id`)

	stmtMessagesToLane = define("message.to_lane",
		`SELECT `+messageColumns+` FROM messages
 WHERE workspace_id = :workspace_id AND to_kind = 'lane' AND to_lane = :lane
 ORDER BY created_at, id`)

	stmtQueuedMessages = define("message.queued",
		`SELECT `+messageColumns+` FROM messages
 WHERE workspace_id = :workspace_id AND to_kind = 'lane' AND to_lane = :lane AND state = 'queued'
 ORDER BY created_at, id`)

	stmtMessagesToTarget = define("message.to_target",
		`SELECT `+messageColumns+` FROM messages
 WHERE workspace_id = :workspace_id AND to_kind = :to_kind AND to_lane = :target
 ORDER BY created_at, id`)

	stmtQueuedToTarget = define("message.queued_to",
		`SELECT `+messageColumns+` FROM messages
 WHERE workspace_id = :workspace_id AND to_kind = :to_kind AND to_lane = :target AND state = 'queued'
 ORDER BY created_at, id`)

	stmtDropToTarget = define("message.drop_to",
		`DELETE FROM messages WHERE workspace_id = :workspace_id AND to_kind = :to_kind AND to_lane = :target`)

	// stmtUnsettledMessages reads what one generation's end decides, and what an earlier end left.
	stmtUnsettledMessages = define("message.unsettled",
		`SELECT `+messageColumns+` FROM messages
 WHERE workspace_id = :workspace_id AND to_kind = 'lane' AND to_lane = :lane AND to_generation <= :through
   AND state IN ('queued', 'at_worker', 'handed_off')
 ORDER BY created_at, id`)

	// stmtMessageFacts reads what a generation's first prompt owes: a fact no session took in, or
	// a message an ended generation took in and did not answer.
	stmtMessageFacts = define("message.facts",
		`SELECT `+messageColumns+` FROM messages
 WHERE workspace_id = :workspace_id AND to_kind = 'lane' AND to_lane = :lane AND carried_in IS NULL
   AND ((state = 'expired' AND reason = 'ledger_fact') OR (state = 'admitted' AND to_generation <= :ended))
 ORDER BY created_at, id`)

	// stmtDueMessages names no window, so what a long downtime passed still comes due.
	stmtDueMessages = define("message.due",
		`SELECT id FROM messages
 WHERE workspace_id = :workspace_id AND expires_at < :now
   AND state IN ('queued', 'at_worker', 'handed_off')
 ORDER BY expires_at, id`)

	stmtNextMessageSeq = define("message.next_seq",
		`SELECT coalesce(max(sender_seq), 0) + 1 FROM messages
 WHERE workspace_id = :workspace_id AND from_kind = :from_kind AND from_id = :from_id`)

	// stmtSaveMessage is the compare-and-set of the delivery table: it moves a message only from
	// the state its caller read.
	stmtSaveMessage = define("message.save",
		`UPDATE messages
   SET state = :state, reason = :reason, reply = :reply, carried_in = CAST(:carried_in AS BIGINT)
 WHERE workspace_id = :workspace_id AND id = :id AND state = :from_state`)
)

// scanMessage reads a row. to_lane holds the target's ID for every kind, and the target names it in the
// field of its kind.
func scanMessage(row scanner) (messages.Message, error) {
	var m messages.Message
	var replyTo, reason, reply, traceparent sql.NullString
	var carried sql.NullInt64
	var target, kind string
	err := row.Scan(&m.ID, &m.Envelope, &target, &m.To.Generation, &m.From.Kind, &m.From.ID, &m.From.Seq, &m.Kind, &replyTo, &m.Body,
		&m.State, &reason, &reply, &carried, &m.CreatedAt, &m.StaleAt, &m.ExpiresAt, &traceparent, &kind)
	if err != nil {
		return messages.Message{}, err
	}
	m.To = messages.To(messages.TargetKind(kind), target, m.To.Generation)
	m.ReplyTo, m.Reason, m.Reply, m.Traceparent = replyTo.String, reason.String, reply.String, traceparent.String
	m.CarriedIn = carried.Int64
	return m, nil
}

func (t messageTx) params(extra params) params {
	extra["workspace_id"] = string(t.scope)
	return extra
}

func (t messageTx) list(st *statement, p params) ([]messages.Message, error) {
	rows, err := t.rows(st, t.params(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []messages.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (t messageTx) Message(id string) (messages.Message, error) {
	row, err := t.row(stmtReadMessage, t.params(params{"id": id}))
	if err != nil {
		return messages.Message{}, err
	}
	m, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return messages.Message{}, fmt.Errorf("message %s: %w", id, ports.ErrNotFound)
	}
	return m, err
}

func (t messageTx) All() ([]messages.Message, error) { return t.list(stmtAllMessages, params{}) }

func (t messageTx) ToLane(lane string) ([]messages.Message, error) {
	return t.list(stmtMessagesToLane, params{"lane": lane})
}

func (t messageTx) Queued(lane string) ([]messages.Message, error) {
	return t.list(stmtQueuedMessages, params{"lane": lane})
}

func (t messageTx) Unsettled(lane string, through int64) ([]messages.Message, error) {
	return t.list(stmtUnsettledMessages, params{"lane": lane, "through": through})
}

func (t messageTx) Facts(lane string, ended int64) ([]messages.Message, error) {
	return t.list(stmtMessageFacts, params{"lane": lane, "ended": ended})
}

func (t messageTx) Due(now int64) ([]string, error) {
	rows, err := t.rows(stmtDueMessages, t.params(params{"now": now}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (t messageTx) ToTarget(kind messages.TargetKind, id string) ([]messages.Message, error) {
	return t.list(stmtMessagesToTarget, params{"to_kind": string(kind), "target": id})
}

func (t messageTx) QueuedTo(kind messages.TargetKind, id string) ([]messages.Message, error) {
	return t.list(stmtQueuedToTarget, params{"to_kind": string(kind), "target": id})
}

func (t messageTx) DropTo(kind messages.TargetKind, id string) (int64, error) {
	return t.exec(stmtDropToTarget, t.params(params{"to_kind": string(kind), "target": id}))
}

func (t messageTx) NextSeq(kind, id string) (int64, error) {
	row, err := t.row(stmtNextMessageSeq, t.params(params{"from_kind": kind, "from_id": id}))
	if err != nil {
		return 0, err
	}
	var seq int64
	err = row.Scan(&seq)
	return seq, err
}

func (t messageTx) Insert(m messages.Message) error {
	_, err := t.exec(stmtInsertMessage, t.params(params{
		"id": m.ID, "envelope": int64(m.Envelope), "to_kind": string(m.To.Of()), "to_lane": m.To.ID(), "to_generation": m.To.Generation, "from_kind": m.From.Kind, "from_id": m.From.ID,
		"sender_seq": m.From.Seq, "kind": string(m.Kind), "reply_to": nullText(m.ReplyTo), "body": m.Body, "state": string(m.State),
		"reason": nullText(m.Reason), "created_at": m.CreatedAt, "stale_at": m.StaleAt, "expires_at": m.ExpiresAt,
		"traceparent": nullText(m.Traceparent),
	}))
	return err
}

func (t messageTx) Save(m messages.Message, from messages.State) (bool, error) {
	n, err := t.exec(stmtSaveMessage, t.params(params{
		"id": m.ID, "state": string(m.State), "reason": nullText(m.Reason), "reply": nullText(m.Reply),
		"carried_in": nullInt(m.CarriedIn), "from_state": string(from),
	}))
	return n > 0, err
}

func (t *readTx) Messages() ports.MessageReader { return messageTx{session: t.session, scope: t.scope} }

func (t *writeTx) Messages() ports.MessageWriter {
	return messageTx{session: t.session, scope: t.scope}
}
