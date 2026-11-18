package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const scheduledStartColumns = `worker_id, starts_at, scheduled_by, scheduled_at, missed`

var (
	stmtSaveScheduledStart = define("orchestrator_start.save",
		`INSERT INTO orchestrator_starts (workspace_id, worker_id, starts_at, scheduled_by, scheduled_at, missed)
VALUES (:workspace_id, :worker_id, :starts_at, :scheduled_by, :scheduled_at, :missed)
ON CONFLICT (workspace_id, worker_id) DO UPDATE
   SET starts_at = :starts_at, scheduled_by = :scheduled_by, scheduled_at = :scheduled_at, missed = :missed`)

	stmtFindScheduledStart = define("orchestrator_start.find",
		`SELECT `+scheduledStartColumns+` FROM orchestrator_starts WHERE workspace_id = :workspace_id AND worker_id = :worker_id`)

	stmtListScheduledStarts = define("orchestrator_start.list",
		`SELECT `+scheduledStartColumns+` FROM orchestrator_starts WHERE workspace_id = :workspace_id ORDER BY worker_id`)

	stmtDeleteScheduledStart = define("orchestrator_start.delete",
		`DELETE FROM orchestrator_starts WHERE workspace_id = :workspace_id AND worker_id = :worker_id`)
)

func scanScheduledStart(row scanner) (ledger.ScheduledStart, error) {
	var s ledger.ScheduledStart
	err := row.Scan(&s.Worker, &s.StartsAt, &s.ScheduledBy, &s.ScheduledAt, &s.Missed)
	return s, err
}

func (t workerTx) ScheduledStart(worker string) (ledger.ScheduledStart, error) {
	row, err := t.row(stmtFindScheduledStart, t.scoped(params{"worker_id": worker}))
	if err != nil {
		return ledger.ScheduledStart{}, err
	}
	start, err := scanScheduledStart(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.ScheduledStart{}, fmt.Errorf("scheduled start of %s: %w", worker, ports.ErrNotFound)
	}
	return start, err
}

func (t workerTx) ScheduledStarts() ([]ledger.ScheduledStart, error) {
	rows, err := t.rows(stmtListScheduledStarts, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.ScheduledStart
	for rows.Next() {
		start, err := scanScheduledStart(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, start)
	}
	return out, rows.Err()
}

func (t workerTx) SaveScheduledStart(start ledger.ScheduledStart) error {
	_, err := t.exec(stmtSaveScheduledStart, t.scoped(params{
		"worker_id": start.Worker, "starts_at": start.StartsAt, "scheduled_by": start.ScheduledBy, "scheduled_at": start.ScheduledAt, "missed": start.Missed,
	}))
	return err
}

func (t workerTx) DeleteScheduledStart(worker string) (bool, error) {
	n, err := t.exec(stmtDeleteScheduledStart, t.scoped(params{"worker_id": worker}))
	return n > 0, err
}
