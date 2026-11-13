package sqlstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/conversations"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// conversationTx is the conversation area of one transaction.
type conversationTx struct {
	*session
	scope ledger.WorkspaceID
}

const conversationColumns = `id, member_id, name, worker_id, desired_state, actual_state, run_lane, current_generation, stopped_reason,
       created_at, last_active_at`

const entryColumns = `conversation_id, id, seq, kind, body, data, before_clear, recorded_at`

var (
	stmtInsertConversation = define("conversation.insert",
		`INSERT INTO assistant_conversations (workspace_id, id, member_id, name, worker_id, desired_state, actual_state, run_lane,
  current_generation, stopped_reason, created_at, last_active_at)
VALUES (:workspace_id, :id, :member_id, :name, :worker_id, :desired_state, :actual_state, :run_lane,
  :current_generation, :stopped_reason, :created_at, :last_active_at)`)

	stmtSaveConversation = define("conversation.save",
		`UPDATE assistant_conversations
   SET name = :name, worker_id = :worker_id, desired_state = :desired_state, actual_state = :actual_state, run_lane = :run_lane,
       current_generation = :current_generation, stopped_reason = :stopped_reason, last_active_at = :last_active_at
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtReadConversation = define("conversation.read",
		`SELECT `+conversationColumns+` FROM assistant_conversations WHERE workspace_id = :workspace_id AND id = :id`)

	stmtMemberConversations = define("conversation.of_member",
		`SELECT `+conversationColumns+` FROM assistant_conversations
 WHERE workspace_id = :workspace_id AND member_id = :member_id
 ORDER BY last_active_at DESC, id DESC`)

	stmtRunningConversations = define("conversation.running",
		`SELECT `+conversationColumns+` FROM assistant_conversations
 WHERE workspace_id = :workspace_id AND desired_state = 'running'
 ORDER BY last_active_at, id`)

	stmtConversationByRunLane = define("conversation.by_run_lane",
		`SELECT `+conversationColumns+` FROM assistant_conversations WHERE workspace_id = :workspace_id AND run_lane = :run_lane`)

	stmtDeleteConversation = define("conversation.delete",
		`DELETE FROM assistant_conversations WHERE workspace_id = :workspace_id AND id = :id`)

	stmtDeleteEntries = define("conversation.delete_entries",
		`DELETE FROM conversation_entries WHERE workspace_id = :workspace_id AND conversation_id = :conversation_id`)

	stmtInsertEntry = define("conversation.insert_entry",
		`INSERT INTO conversation_entries (workspace_id, conversation_id, id, seq, kind, body, data, before_clear, recorded_at)
VALUES (:workspace_id, :conversation_id, :id, :seq, :kind, :body, :data, :before_clear, :recorded_at)`)

	stmtReadEntry = define("conversation.read_entry",
		`SELECT `+entryColumns+` FROM conversation_entries
 WHERE workspace_id = :workspace_id AND conversation_id = :conversation_id AND id = :id`)

	stmtEntriesBefore = define("conversation.entries_before",
		`SELECT `+entryColumns+` FROM conversation_entries
 WHERE workspace_id = :workspace_id AND conversation_id = :conversation_id AND seq < :before
 ORDER BY seq DESC
 LIMIT :limit`)

	stmtNextEntrySeq = define("conversation.next_seq",
		`SELECT coalesce(max(seq), 0) + 1 FROM conversation_entries
 WHERE workspace_id = :workspace_id AND conversation_id = :conversation_id`)

	stmtMarkBeforeClear = define("conversation.mark_before_clear",
		`UPDATE conversation_entries SET before_clear = 1
 WHERE workspace_id = :workspace_id AND conversation_id = :conversation_id AND seq <= :through`)
)

func scanConversation(row scanner) (conversations.Conversation, error) {
	var c conversations.Conversation
	var worker, runLane, reason sql.NullString
	err := row.Scan(&c.ID, &c.Member, &c.Name, &worker, &c.Desired, &c.Actual, &runLane, &c.Generation, &reason, &c.CreatedAt, &c.LastActiveAt)
	c.Worker, c.RunLane, c.StoppedReason = worker.String, runLane.String, reason.String
	return c, err
}

func scanEntry(row scanner) (conversations.Entry, error) {
	var e conversations.Entry
	var body, data sql.NullString
	var beforeClear int64
	err := row.Scan(&e.Conversation, &e.ID, &e.Seq, &e.Kind, &body, &data, &beforeClear, &e.RecordedAt)
	e.Body, e.BeforeClear = body.String, beforeClear == 1
	if data.Valid {
		e.Data = json.RawMessage(data.String)
	}
	return e, err
}

func (t conversationTx) params(extra params) params {
	extra["workspace_id"] = string(t.scope)
	return extra
}

func (t conversationTx) one(st *statement, p params, what string) (conversations.Conversation, error) {
	row, err := t.row(st, t.params(p))
	if err != nil {
		return conversations.Conversation{}, err
	}
	c, err := scanConversation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return conversations.Conversation{}, fmt.Errorf("%s: %w", what, ports.ErrNotFound)
	}
	return c, err
}

func (t conversationTx) list(st *statement, p params) ([]conversations.Conversation, error) {
	rows, err := t.rows(st, t.params(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []conversations.Conversation
	for rows.Next() {
		c, err := scanConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (t conversationTx) Conversation(id string) (conversations.Conversation, error) {
	return t.one(stmtReadConversation, params{"id": id}, "conversation "+id)
}

func (t conversationTx) OfMember(member string) ([]conversations.Conversation, error) {
	return t.list(stmtMemberConversations, params{"member_id": member})
}

func (t conversationTx) Running() ([]conversations.Conversation, error) {
	return t.list(stmtRunningConversations, params{})
}

func (t conversationTx) ByRunLane(lane string) (conversations.Conversation, error) {
	if lane == "" {
		return conversations.Conversation{}, fmt.Errorf("a conversation with no run lane: %w", ports.ErrNotFound)
	}
	return t.one(stmtConversationByRunLane, params{"run_lane": lane}, "the conversation of run lane "+lane)
}

func (t conversationTx) Entries(conversation string, before int64, limit int) ([]conversations.Entry, error) {
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := t.rows(stmtEntriesBefore, t.params(params{"conversation_id": conversation, "before": before, "limit": int64(limit)}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []conversations.Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t conversationTx) Entry(conversation, id string) (conversations.Entry, error) {
	row, err := t.row(stmtReadEntry, t.params(params{"conversation_id": conversation, "id": id}))
	if err != nil {
		return conversations.Entry{}, err
	}
	e, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return conversations.Entry{}, fmt.Errorf("entry %s of conversation %s: %w", id, conversation, ports.ErrNotFound)
	}
	return e, err
}

func (t conversationTx) NextSeq(conversation string) (int64, error) {
	row, err := t.row(stmtNextEntrySeq, t.params(params{"conversation_id": conversation}))
	if err != nil {
		return 0, err
	}
	var seq int64
	err = row.Scan(&seq)
	return seq, err
}

func (t conversationTx) record(c conversations.Conversation) params {
	return t.params(params{
		"id": c.ID, "member_id": c.Member, "name": c.Name, "worker_id": nullText(c.Worker), "desired_state": string(c.Desired),
		"actual_state": string(c.Actual), "run_lane": nullText(c.RunLane), "current_generation": c.Generation,
		"stopped_reason": nullText(c.StoppedReason), "created_at": c.CreatedAt, "last_active_at": c.LastActiveAt,
	})
}

func (t conversationTx) Insert(c conversations.Conversation) error {
	_, err := t.exec(stmtInsertConversation, t.record(c))
	return err
}

func (t conversationTx) Save(c conversations.Conversation) error {
	n, err := t.exec(stmtSaveConversation, t.record(c))
	if err == nil && n == 0 {
		return fmt.Errorf("conversation %s: %w", c.ID, ports.ErrNotFound)
	}
	return err
}

func (t conversationTx) Delete(id string) error {
	if _, err := t.exec(stmtDeleteEntries, t.params(params{"conversation_id": id})); err != nil {
		return err
	}
	_, err := t.exec(stmtDeleteConversation, t.params(params{"id": id}))
	return err
}

func (t conversationTx) AddEntry(e conversations.Entry) error {
	var data sql.NullString
	if len(e.Data) != 0 {
		data = sql.NullString{String: string(e.Data), Valid: true}
	}
	_, err := t.exec(stmtInsertEntry, t.params(params{
		"conversation_id": e.Conversation, "id": e.ID, "seq": e.Seq, "kind": string(e.Kind), "body": nullText(e.Body), "data": data,
		"before_clear": flag(e.BeforeClear), "recorded_at": e.RecordedAt,
	}))
	return err
}

func (t conversationTx) MarkBeforeClear(conversation string, through int64) error {
	_, err := t.exec(stmtMarkBeforeClear, t.params(params{"conversation_id": conversation, "through": through}))
	return err
}

func (t *readTx) Conversations() ports.ConversationReader {
	return conversationTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Conversations() ports.ConversationWriter {
	return conversationTx{session: t.session, scope: t.scope}
}
