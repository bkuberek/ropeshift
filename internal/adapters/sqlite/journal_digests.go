package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// SaveDigest keeps what a generation's digest has so far. A save of a digest that is over changes nothing,
// so the state a generation ended with is the state it keeps, whoever saves late.
func (j *Journal) SaveDigest(ctx context.Context, lane string, generation int64, state []byte, over bool) error {
	_, err := j.db.ExecContext(ctx, `INSERT INTO generation_digests (lane, generation, state, over) VALUES (?1, ?2, ?3, ?4)
ON CONFLICT (lane, generation) DO UPDATE SET state = ?3, over = ?4 WHERE over = 0`, lane, generation, state, over)
	return err
}

// Digest returns what SaveDigest kept of a generation.
func (j *Journal) Digest(ctx context.Context, lane string, generation int64) (lanes.DigestRecord, error) {
	row := j.db.QueryRowContext(ctx, `SELECT lane, generation, state, over, recorded FROM generation_digests
 WHERE lane = ?1 AND generation = ?2`, lane, generation)
	record, err := scanDigest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return lanes.DigestRecord{}, fmt.Errorf("digest of %s generation %d: %w", lane, generation, ports.ErrNotFound)
	}
	return record, err
}

// UnrecordedDigests lists the generations that are over and whose digest no event carries yet.
func (j *Journal) UnrecordedDigests(ctx context.Context) ([]lanes.DigestRecord, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT lane, generation, state, over, recorded FROM generation_digests
 WHERE over = 1 AND recorded = 0 ORDER BY lane, generation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.DigestRecord
	for rows.Next() {
		record, err := scanDigest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

// RecordDigest marks a finished digest recorded and stores the event that carries it, in one write, so a
// crash leaves the digest unrecorded or its event in the journal. It reports false, and stores nothing, for a
// digest that is recorded already, that is not over, or that was never saved.
func (j *Journal) RecordDigest(ctx context.Context, lane string, generation int64, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE generation_digests SET recorded = 1
 WHERE lane = ?1 AND generation = ?2 AND over = 1 AND recorded = 0`, lane, generation)
	if err != nil {
		return false, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed == 0 {
		return false, err
	}
	if _, err := insertEvent(ctx, tx, event); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func scanDigest(row interface{ Scan(...any) error }) (lanes.DigestRecord, error) {
	var record lanes.DigestRecord
	var over, recorded int64
	err := row.Scan(&record.Lane, &record.Generation, &record.State, &over, &recorded)
	record.Over, record.Recorded = over != 0, recorded != 0
	return record, err
}
