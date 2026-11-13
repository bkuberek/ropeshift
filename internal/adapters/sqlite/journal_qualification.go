package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// Qualification reads what this worker's last probe observed, which is nothing until it first saves.
func (j *Journal) Qualification(ctx context.Context) (lanes.Qualification, error) {
	var raw string
	err := j.db.QueryRowContext(ctx, `SELECT state FROM qualification WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return lanes.Qualification{}, nil
	}
	if err != nil {
		return lanes.Qualification{}, err
	}
	var q lanes.Qualification
	return q, json.Unmarshal([]byte(raw), &q)
}

// SaveQualification keeps a probe's result and the event that records it in one write, so a worker
// never acts on a result the ledger will not hear of.
func (j *Journal) SaveQualification(ctx context.Context, q lanes.Qualification, events ...ledger.UploadEvent) error {
	raw, err := json.Marshal(q)
	if err != nil {
		return err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO qualification (id, state) VALUES (1, ?1)
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

// LanePeaks reads the peaks of the last lanes this worker ran, which is none until it first saves.
func (j *Journal) LanePeaks(ctx context.Context) ([]lanes.LanePeak, error) {
	var raw string
	err := j.db.QueryRowContext(ctx, `SELECT peaks FROM lane_peaks WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var peaks []lanes.LanePeak
	return peaks, json.Unmarshal([]byte(raw), &peaks)
}

// SaveLanePeaks keeps the peaks in place of what the journal held.
func (j *Journal) SaveLanePeaks(ctx context.Context, peaks []lanes.LanePeak) error {
	if peaks == nil {
		peaks = []lanes.LanePeak{}
	}
	raw, err := json.Marshal(peaks)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `INSERT INTO lane_peaks (id, peaks) VALUES (1, ?1)
ON CONFLICT (id) DO UPDATE SET peaks = excluded.peaks`, string(raw))
	return err
}
