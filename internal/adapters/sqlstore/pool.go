package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// The delivery pool lives in the installation's database alone: a store with no installation holds one project,
// which has no other to share with.
var (
	stmtPoolRecords = defineInstallation("pool.records",
		`SELECT kind, size, capability_class, outcome, ended_at, elapsed_ms, delivery_ms, lane_ms, person_ms, cost_usd, cost_lower_bound
  FROM delivery_pool WHERE workspace_id = :workspace_id AND project_id <> :project_id
 ORDER BY ended_at, project_id, seq`)

	stmtPoolShared = defineInstallation("pool.shared",
		`SELECT digest FROM delivery_pool_shares WHERE workspace_id = :workspace_id AND project_id = :project_id`)

	stmtPoolDrop = defineInstallation("pool.drop",
		`DELETE FROM delivery_pool WHERE workspace_id = :workspace_id AND project_id = :project_id`)

	stmtPoolDropShare = defineInstallation("pool.drop_share",
		`DELETE FROM delivery_pool_shares WHERE workspace_id = :workspace_id AND project_id = :project_id`)

	stmtPoolAdd = defineInstallation("pool.add",
		`INSERT INTO delivery_pool
  (workspace_id, project_id, seq, kind, size, capability_class, outcome, ended_at, elapsed_ms, delivery_ms, lane_ms, person_ms, cost_usd, cost_lower_bound)
VALUES (:workspace_id, :project_id, :seq, :kind, :size, :capability_class, :outcome, :ended_at, :elapsed_ms, :delivery_ms, :lane_ms, :person_ms, :cost_usd, :cost_lower_bound)`)

	stmtPoolSaveShare = defineInstallation("pool.save_share",
		`INSERT INTO delivery_pool_shares (workspace_id, project_id, digest, shared_at) VALUES (:workspace_id, :project_id, :digest, :shared_at)
ON CONFLICT (workspace_id, project_id) DO UPDATE SET digest = :digest, shared_at = :shared_at`)
)

// Pool reads the delivery pool within a read of a project.
func (t *readTx) Pool() ports.PoolReader { return poolOf(t.side, t.session, t.scope) }

// Pool reads the delivery pool within a write of a project.
func (t *writeTx) Pool() ports.PoolReader { return poolOf(t.side, t.session, t.scope) }

// poolOf reads the pool on the installation's side of a project's transaction, as that project.
func poolOf(side *shared, own *session, project ledger.WorkspaceID) ports.PoolReader {
	if side == nil {
		return poolTx{}
	}
	se, scope := side.session(own, project)
	return poolTx{session: se, scope: scope, project: project}
}

// poolTx reads the pool as one project: what the others share, and the digest of its own share. With no
// installation its session is nil, and it reads nothing.
type poolTx struct {
	*session
	scope   ledger.WorkspaceID
	project ledger.WorkspaceID
}

func (t poolTx) scoped(p params) params {
	p["workspace_id"], p["project_id"] = string(t.scope), string(t.project)
	return p
}

func (t poolTx) Records() ([]delivery.Record, error) {
	out := []delivery.Record{}
	if t.session == nil {
		return out, nil
	}
	rows, err := t.rows(stmtPoolRecords, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r delivery.Record
		var outcome string
		var lowerBound int64
		err := rows.Scan(&r.Kind, &r.Size, &r.CapabilityClass, &outcome, &r.Ended, &r.ElapsedMs, &r.DeliveryMs, &r.LaneMs, &r.PersonMs, &r.Usage.CostUSD, &lowerBound)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", stmtPoolRecords.name, err)
		}
		r.Outcome, r.Usage.CostLowerBound = workorders.State(outcome), lowerBound != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t poolTx) Shared() (string, error) {
	if t.session == nil {
		return "", nil
	}
	row, err := t.row(stmtPoolShared, t.scoped(params{}))
	if err != nil {
		return "", err
	}
	var digest string
	if err := row.Scan(&digest); errors.Is(err, sql.ErrNoRows) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("%s: %w", stmtPoolShared.name, err)
	}
	return digest, nil
}

// sharePool replaces a project's share of the pool in one short transaction, with no revision and no event,
// because the share is derived from the project's delivery records. An empty digest takes the share out.
func (s *Store) sharePool(ctx context.Context, scope, project ledger.WorkspaceID, digest string, records []delivery.Record) error {
	if err := project.Validate(); err != nil {
		return err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := poolTx{session: s.session(ctx, tx, ModeWrite, OpSharePool), scope: scope, project: project}
	now, err := t.readClock()
	if err != nil {
		return err
	}
	for _, st := range []*statement{stmtPoolDrop, stmtPoolDropShare} {
		if _, err := t.exec(st, t.scoped(params{})); err != nil {
			return err
		}
	}
	if digest != "" {
		for i, r := range delivery.Pooled(records) {
			lowerBound := int64(0)
			if r.Usage.CostLowerBound {
				lowerBound = 1
			}
			_, err := t.exec(stmtPoolAdd, t.scoped(params{
				"seq": int64(i), "kind": r.Kind, "size": r.Size, "capability_class": r.CapabilityClass, "outcome": string(r.Outcome),
				"ended_at": r.Ended, "elapsed_ms": r.ElapsedMs, "delivery_ms": r.DeliveryMs, "lane_ms": r.LaneMs, "person_ms": r.PersonMs,
				"cost_usd": r.Usage.CostUSD, "cost_lower_bound": lowerBound,
			}))
			if err != nil {
				return err
			}
		}
		if _, err := t.exec(stmtPoolSaveShare, t.scoped(params{"digest": digest, "shared_at": now})); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", OpSharePool, err)
	}
	return nil
}

var _ ports.PoolReader = poolTx{}
