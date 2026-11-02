package sqlite

import (
	"context"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
)

// BeginApproval keeps a lane's request and the event that carries it to the ledger, together. A call
// that already stands keeps its row and adds no event.
func (j *Journal) BeginApproval(ctx context.Context, open lanes.OpenApproval, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO open_approvals (id, lane, generation, tool, digest, deadline_ms)
VALUES (?1, ?2, ?3, ?4, ?5, ?6) ON CONFLICT (lane, digest) DO NOTHING`,
		open.ID, open.Lane, open.Generation, open.Tool, open.Digest, open.DeadlineMs)
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

// RenewApproval gives a request a generation that went on from the one that raised it, and a wait of
// its own.
func (j *Journal) RenewApproval(ctx context.Context, id string, generation, deadlineMs int64) error {
	_, err := j.db.ExecContext(ctx, `UPDATE open_approvals SET generation = ?2, deadline_ms = ?3 WHERE id = ?1`, id, generation, deadlineMs)
	return err
}

// OpenApprovals lists what a lane has asked and not heard back on, oldest deadline first.
func (j *Journal) OpenApprovals(ctx context.Context, lane string) ([]lanes.OpenApproval, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT id, lane, generation, tool, digest, deadline_ms FROM open_approvals
 WHERE lane = ?1 ORDER BY deadline_ms, id`, lane)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.OpenApproval
	for rows.Next() {
		var open lanes.OpenApproval
		if err := rows.Scan(&open.ID, &open.Lane, &open.Generation, &open.Tool, &open.Digest, &open.DeadlineMs); err != nil {
			return nil, err
		}
		out = append(out, open)
	}
	return out, rows.Err()
}

// CloseApproval stops following a request and stores what the ledger must hear of that, together.
func (j *Journal) CloseApproval(ctx context.Context, id string, events ...ledger.UploadEvent) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM open_approvals WHERE id = ?1`, id); err != nil {
		return err
	}
	for _, event := range events {
		if _, err := insertEvent(ctx, tx, event); err != nil {
			return err
		}
	}
	return tx.Commit()
}
