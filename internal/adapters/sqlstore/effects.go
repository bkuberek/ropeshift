package sqlstore

import (
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const effectColumns = `id, worker_id, effect_key, kind, lane_id, work_order_id, generation, target, attempt, raised_at,
       outcome, reason, decided_by, decided_at, settled_at`

var (
	stmtInsertEffect = define("effect.insert",
		`INSERT INTO unknown_effects
  (workspace_id, id, worker_id, effect_key, kind, lane_id, work_order_id, generation, target, attempt, raised_at)
VALUES
  (:workspace_id, :id, :worker_id, :effect_key, :kind, :lane_id, :work_order_id, :generation, :target, :attempt, :raised_at)
ON CONFLICT DO NOTHING`)

	stmtEffect = define("effect.get",
		`SELECT `+effectColumns+` FROM unknown_effects WHERE workspace_id = :workspace_id AND id = :id`)

	stmtEffects = define("effect.all",
		`SELECT `+effectColumns+` FROM unknown_effects WHERE workspace_id = :workspace_id ORDER BY raised_at, id`)

	stmtEffectsDecidedFor = define("effect.decided_for",
		`SELECT `+effectColumns+` FROM unknown_effects
 WHERE workspace_id = :workspace_id AND worker_id = :worker_id AND outcome <> '' AND settled_at = 0
 ORDER BY decided_at, id`)

	stmtDecideEffect = define("effect.decide",
		`UPDATE unknown_effects
   SET outcome = :outcome, reason = :reason, decided_by = :decided_by, decided_kind = :decided_kind, decided_at = :decided_at
 WHERE workspace_id = :workspace_id AND id = :id AND outcome = ''`)

	stmtSettleEffect = define("effect.settle",
		`UPDATE unknown_effects SET settled_at = :settled_at
 WHERE workspace_id = :workspace_id AND id = :id AND outcome <> '' AND settled_at = 0`)
)

// effectTx is the unknown effect area of one transaction.
type effectTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t effectTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanEffect(row scanner) (ledger.UnknownEffect, error) {
	var e ledger.UnknownEffect
	var outcome string
	err := row.Scan(&e.ID, &e.WorkerID, &e.Key, &e.Kind, &e.Lane, &e.WorkOrder, &e.Generation, &e.Target, &e.Attempt, &e.RaisedAt,
		&outcome, &e.Reason, &e.DecidedBy, &e.DecidedAt, &e.SettledAt)
	e.Outcome = ledger.EffectOutcome(outcome)
	return e, err
}

func (t effectTx) Effect(id string) (ledger.UnknownEffect, error) {
	row, err := t.row(stmtEffect, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.UnknownEffect{}, err
	}
	effect, err := scanEffect(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.UnknownEffect{}, fmt.Errorf("effect %s: %w", id, ports.ErrNotFound)
	}
	return effect, err
}

func (t effectTx) list(st *statement, p params) ([]ledger.UnknownEffect, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.UnknownEffect
	for rows.Next() {
		effect, err := scanEffect(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, effect)
	}
	return out, rows.Err()
}

func (t effectTx) Effects() ([]ledger.UnknownEffect, error) {
	return t.list(stmtEffects, params{})
}

func (t effectTx) DecidedFor(worker string) ([]ledger.UnknownEffect, error) {
	return t.list(stmtEffectsDecidedFor, params{"worker_id": worker})
}

func (t effectTx) Insert(e ledger.UnknownEffect) (bool, error) {
	n, err := t.exec(stmtInsertEffect, t.scoped(params{
		"id": e.ID, "worker_id": e.WorkerID, "effect_key": e.Key, "kind": e.Kind, "lane_id": e.Lane, "work_order_id": e.WorkOrder,
		"generation": e.Generation, "target": e.Target, "attempt": e.Attempt, "raised_at": e.RaisedAt,
	}))
	return n > 0, err
}

func (t effectTx) Decide(id string, decided ledger.EffectDecided, kind string) (bool, error) {
	n, err := t.exec(stmtDecideEffect, t.scoped(params{
		"id": id, "outcome": string(decided.Outcome), "reason": decided.Reason, "decided_by": decided.DecidedBy,
		"decided_kind": kind, "decided_at": decided.DecidedAt,
	}))
	return n > 0, err
}

func (t effectTx) Settle(id string, at int64) (bool, error) {
	n, err := t.exec(stmtSettleEffect, t.scoped(params{"id": id, "settled_at": at}))
	return n > 0, err
}

func (t *readTx) Effects() ports.EffectReader { return effectTx{session: t.session, scope: t.scope} }

func (t *writeTx) Effects() ports.EffectWriter { return effectTx{session: t.session, scope: t.scope} }
