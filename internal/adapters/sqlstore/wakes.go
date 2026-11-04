package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/parking"
	"ropeshift.dev/ropeshift/internal/ports"
)

// wakeTx is the wake area of one transaction.
type wakeTx struct {
	*session
	scope ledger.WorkspaceID
	now   int64
}

const wakeColumns = `id, lane_id, expr, next_eval_at, fired_at, fired_by, stored_rev`

var (
	// stmtInsertWake takes a client's ID, so a retry stores one wake.
	stmtInsertWake = define("wake.insert",
		`INSERT INTO wake_conditions (workspace_id, id, lane_id, expr, next_eval_at, stored_rev)
VALUES (:workspace_id, :id, :lane_id, :expr, CAST(:next_eval_at AS BIGINT), :stored_rev)
ON CONFLICT (workspace_id, id) DO NOTHING`)

	stmtReadWake = define("wake.read",
		`SELECT `+wakeColumns+` FROM wake_conditions WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOpenWakes = define("wake.open",
		`SELECT `+wakeColumns+` FROM wake_conditions
 WHERE workspace_id = :workspace_id AND fired_at IS NULL
 ORDER BY id`)

	stmtAllWakes = define("wake.all",
		`SELECT `+wakeColumns+` FROM wake_conditions WHERE workspace_id = :workspace_id ORDER BY id`)

	// stmtDueWakes names no window, so a wake a long downtime passed still comes due.
	stmtDueWakes = define("wake.due",
		`SELECT id FROM wake_conditions
 WHERE workspace_id = :workspace_id
   AND next_eval_at <= :now
   AND fired_at IS NULL
 ORDER BY next_eval_at, id`)

	// stmtFireWake is the guard that stops a stale ticker: one that read its due list and
	// stalled while the wake fired, the lane resumed, and it parked again.
	stmtFireWake = define("wake.fire",
		`UPDATE wake_conditions
   SET fired_at = :now, fired_by = :cause
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fired_at IS NULL`)

	// stmtMoveToReady is the guard that stops concurrent firers. A row count of 0 is a defect,
	// because the state was read after the revision.
	stmtMoveToReady = define("wake.move",
		`UPDATE work_orders
   SET state = 'ready', resume_lane_id = :lane_id
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND state = 'parked'`)

	// stmtRetireWakes closes what a lane of this work order left open. It needs no revision of
	// its own, because it runs in the transaction that ends the lane.
	stmtRetireWakes = define("wake.retire",
		`UPDATE wake_conditions
   SET fired_at = :now, fired_by = :cause
 WHERE workspace_id = :workspace_id
   AND fired_at IS NULL
   AND lane_id IN (SELECT id FROM lanes WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id)`)

	stmtInsertSignal = define("wake.signal",
		`INSERT INTO wake_signals (workspace_id, kind, signal_key, rev)
VALUES (:workspace_id, :kind, :signal_key, :rev)`)

	stmtHasSignal = define("wake.signaled",
		`SELECT count(*) FROM wake_signals
 WHERE workspace_id = :workspace_id AND kind = :kind AND signal_key = :signal_key AND rev > :after`)

	// stmtNextEval runs alone, after the transaction that evaluated. It takes no revision and
	// appends no event, because it only schedules the next evaluation.
	stmtNextEval = define(OpWakeNextEval,
		`UPDATE wake_conditions
   SET next_eval_at = CAST(:next_eval_at AS BIGINT)
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fired_at IS NULL`)
)

func (t wakeTx) params(id string) params {
	return params{"workspace_id": string(t.scope), "id": id}
}

func scanWake(row scanner) (parking.Wake, error) {
	var w parking.Wake
	var next, fired sql.NullInt64
	var by sql.NullString
	if err := row.Scan(&w.ID, &w.Lane, &w.Expr, &next, &fired, &by, &w.StoredRev); err != nil {
		return parking.Wake{}, err
	}
	w.NextEvalAt, w.FiredAt, w.FiredBy = next.Int64, fired.Int64, by.String
	return w, nil
}

func (t wakeTx) Get(id string) (parking.Wake, error) {
	row, err := t.row(stmtReadWake, t.params(id))
	if err != nil {
		return parking.Wake{}, err
	}
	w, err := scanWake(row)
	if errors.Is(err, sql.ErrNoRows) {
		return parking.Wake{}, fmt.Errorf("wake %s: %w", id, ports.ErrNotFound)
	}
	return w, err
}

func (t wakeTx) list(st *statement) ([]parking.Wake, error) {
	rows, err := t.rows(st, params{"workspace_id": string(t.scope)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []parking.Wake
	for rows.Next() {
		w, err := scanWake(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (t wakeTx) Open() ([]parking.Wake, error) { return t.list(stmtOpenWakes) }

func (t wakeTx) All() ([]parking.Wake, error) { return t.list(stmtAllWakes) }

func (t wakeTx) Due(now int64) ([]string, error) {
	rows, err := t.rows(stmtDueWakes, params{"workspace_id": string(t.scope), "now": now})
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

func (t wakeTx) Signaled(kind parking.Kind, key string, after int64) (bool, error) {
	row, err := t.row(stmtHasSignal, params{
		"workspace_id": string(t.scope), "kind": string(kind), "signal_key": key, "after": after,
	})
	if err != nil {
		return false, err
	}
	var count int64
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (t wakeTx) Insert(w parking.Wake) (bool, error) {
	p := t.params(w.ID)
	p["lane_id"], p["expr"], p["next_eval_at"], p["stored_rev"] = w.Lane, w.Expr, nullInt(w.NextEvalAt), w.StoredRev
	n, err := t.exec(stmtInsertWake, p)
	return n > 0, err
}

func (t wakeTx) Fire(id, cause string) (bool, error) {
	p := t.params(id)
	p["now"], p["cause"] = t.now, cause
	n, err := t.exec(stmtFireWake, p)
	return n > 0, err
}

func (t wakeTx) MoveToReady(workOrder, lane string) (bool, error) {
	p := t.params(workOrder)
	p["lane_id"] = lane
	n, err := t.exec(stmtMoveToReady, p)
	return n > 0, err
}

func (t wakeTx) Retire(workOrder, cause string) error {
	_, err := t.exec(stmtRetireWakes, params{
		"workspace_id": string(t.scope), "work_order_id": workOrder, "now": t.now, "cause": cause,
	})
	return err
}

func (t wakeTx) Signal(signal parking.Signal, rev int64) error {
	_, err := t.exec(stmtInsertSignal, params{
		"workspace_id": string(t.scope), "kind": string(signal.Kind), "signal_key": signal.Key, "rev": rev,
	})
	return err
}

// ScheduleWake is the one write besides a renewal that takes no revision and appends no event:
// one guarded statement on one row, which only says when to look again.
func (s *Store) ScheduleWake(ctx context.Context, scope ledger.WorkspaceID, id string, nextEvalAt int64) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := wakeTx{session: s.session(ctx, tx, ModeWrite, OpWakeNextEval), scope: scope}
	p := t.params(id)
	p["next_eval_at"] = nullInt(nextEvalAt)
	if _, err := t.exec(stmtNextEval, p); err != nil {
		return err
	}
	crashpoint.Hit(OpWakeNextEval, "before-commit")
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", OpWakeNextEval, err)
	}
	return nil
}
