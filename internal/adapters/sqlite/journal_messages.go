package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
)

const messageColumns = `id, input_id, lane, generation, from_kind, reply_to, kind, body, traceparent, age_ms, taken_ms, stale_after_ms, expire_after_ms, state`

func scanHeld(row interface{ Scan(...any) error }) (messages.Held, error) {
	var h messages.Held
	err := row.Scan(&h.ID, &h.InputID, &h.To.Lane, &h.To.Generation, &h.From.Kind, &h.ReplyTo, &h.Kind, &h.Body, &h.Traceparent,
		&h.AgeMs, &h.TakenMs, &h.StaleAfterMs, &h.ExpireAfterMs, &h.State)
	return h, err
}

// TakeMessage keeps a message once, with its event. The primary key is what makes a second offer
// of one message impossible to miss.
func (j *Journal) TakeMessage(ctx context.Context, h messages.Held, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO messages (`+messageColumns+`)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14) ON CONFLICT (id) DO NOTHING`,
		h.ID, h.InputID, h.To.Lane, h.To.Generation, h.From.Kind, h.ReplyTo, h.Kind, h.Body, h.Traceparent,
		h.AgeMs, h.TakenMs, h.StaleAfterMs, h.ExpireAfterMs, h.State)
	if err != nil {
		return false, err
	}
	first, err := result.RowsAffected()
	if err != nil || first == 0 {
		return false, err
	}
	if _, err := insertEvent(ctx, tx, event); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (j *Journal) messageWhere(ctx context.Context, column, value string) (messages.Held, error) {
	row := j.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE `+column+` = ?1`, value)
	h, err := scanHeld(row)
	if errors.Is(err, sql.ErrNoRows) {
		return messages.Held{}, fmt.Errorf("message: %w", ports.ErrNotFound)
	}
	return h, err
}

// Message returns one held message.
func (j *Journal) Message(ctx context.Context, id string) (messages.Held, error) {
	return j.messageWhere(ctx, "id", id)
}

// MessageByInput finds a held message by what its session calls it.
func (j *Journal) MessageByInput(ctx context.Context, input string) (messages.Held, error) {
	return j.messageWhere(ctx, "input_id", input)
}

// WaitingMessages lists what is held and not written, in the order it arrived.
func (j *Journal) WaitingMessages(ctx context.Context) ([]messages.Held, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT `+messageColumns+` FROM messages WHERE state = ?1 ORDER BY rowid`, messages.AtWorker)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []messages.Held
	for rows.Next() {
		h, err := scanHeld(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HandMessage settles an effect, moves its message, and stores the event, in one write.
func (j *Journal) HandMessage(ctx context.Context, effect, id string, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE effects SET state = ?1 WHERE key = ?2`, lanes.EffectCompleted, effect); err != nil {
		return ledger.UploadEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET state = ?1 WHERE id = ?2`, messages.HandedOff, id); err != nil {
		return ledger.UploadEvent{}, err
	}
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

// MoveMessage moves a message from the state its caller read, with its event.
func (j *Journal) MoveMessage(ctx context.Context, id string, from, to messages.State, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE messages SET state = ?1 WHERE id = ?2 AND state = ?3`, to, id, from)
	if err != nil {
		return false, err
	}
	moved, err := result.RowsAffected()
	if err != nil || moved == 0 {
		return false, err
	}
	if _, err := insertEvent(ctx, tx, event); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// CloseMessage stops holding a message that was waiting.
func (j *Journal) CloseMessage(ctx context.Context, id string) error {
	_, err := j.db.ExecContext(ctx, `UPDATE messages SET state = ?1 WHERE id = ?2 AND state = ?3`, messages.Expired, id, messages.AtWorker)
	return err
}
