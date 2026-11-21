package sqlite

import (
	"context"
	"encoding/json"

	"ropeshift.com/ropeshift/internal/ledger"
)

// BeginConsultation keeps a consultation a worker begins, until its end is journaled. One the journal holds is kept as it
// is, so a call sent again changes nothing.
func (j *Journal) BeginConsultation(ctx context.Context, run ledger.ConsultationRun) error {
	raw, err := json.Marshal(run)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `INSERT INTO consultations (id, run) VALUES (?1, ?2) ON CONFLICT (id) DO NOTHING`, run.Grant.ID, string(raw))
	return err
}

// Consultations lists the consultations whose end the journal does not hold, oldest first.
func (j *Journal) Consultations(ctx context.Context) ([]ledger.ConsultationRun, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT run FROM consultations ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.ConsultationRun
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var run ledger.ConsultationRun
		if err := json.Unmarshal([]byte(raw), &run); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// EndConsultation forgets a consultation's start and stores the event that carries its end, in one transaction. A
// consultation the journal does not hold ended already, so nothing is stored.
func (j *Journal) EndConsultation(ctx context.Context, id string, event ledger.UploadEvent) (ledger.UploadEvent, bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `DELETE FROM consultations WHERE id = ?1`, id)
	if err != nil {
		return ledger.UploadEvent{}, false, err
	}
	if deleted, err := result.RowsAffected(); err != nil || deleted == 0 {
		return ledger.UploadEvent{}, false, err
	}
	if event, err = insertEvent(ctx, tx, event); err != nil {
		return ledger.UploadEvent{}, false, err
	}
	return event, true, tx.Commit()
}
