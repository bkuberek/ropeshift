package sqlite

import (
	"context"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// ReportEffect keeps one attempt's report and its event together, so a crash leaves a person told or
// a report still owed, and never one with no event behind it.
func (j *Journal) ReportEffect(ctx context.Context, key string, attempt int64, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO effect_reports (key, attempt) VALUES (?1, ?2) ON CONFLICT DO NOTHING`, key, attempt)
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

// SettleEffect applies a person's word and keeps its event together, once per word. It moves only the
// attempt the person read, so a word about an attempt that moved on says nothing of the one that followed.
func (j *Journal) SettleEffect(ctx context.Context, verdict lanes.EffectVerdict, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO effect_settlements (id) VALUES (?1) ON CONFLICT DO NOTHING`, verdict.ID)
	if err != nil {
		return false, err
	}
	first, err := result.RowsAffected()
	if err != nil || first == 0 {
		return false, err
	}
	if verdict.Retry {
		_, err = tx.ExecContext(ctx, `UPDATE effects SET state = ?1, attempts = attempts + 1 WHERE key = ?2 AND attempts = ?3 AND state = ?4`,
			lanes.EffectStarted, verdict.Key, verdict.Attempt, lanes.EffectUnknown)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE effects SET state = ?1, detail = ?2 WHERE key = ?3 AND attempts = ?4 AND state = ?5`,
			verdict.State, verdict.Detail, verdict.Key, verdict.Attempt, lanes.EffectUnknown)
	}
	if err != nil {
		return false, err
	}
	if _, err := insertEvent(ctx, tx, event); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
