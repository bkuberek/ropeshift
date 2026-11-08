package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// SignIn reads what this worker knows of its sign-in, which is nothing until it first saves.
func (j *Journal) SignIn(ctx context.Context) (lanes.SignInState, error) {
	var raw string
	err := j.db.QueryRowContext(ctx, `SELECT state FROM sign_in WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return lanes.SignInState{}, nil
	}
	if err != nil {
		return lanes.SignInState{}, err
	}
	var state lanes.SignInState
	return state, json.Unmarshal([]byte(raw), &state)
}

// SaveSignIn keeps a state and the words that follow from it in one write, so a limit never ends
// here with no word of it on its way to the ledger.
func (j *Journal) SaveSignIn(ctx context.Context, state lanes.SignInState, events ...ledger.UploadEvent) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sign_in (id, state) VALUES (1, ?1)
ON CONFLICT (id) DO UPDATE SET state = excluded.state`, string(raw)); err != nil {
		return err
	}
	for _, event := range events {
		if _, err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}
