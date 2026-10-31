package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// leaseTx is the lease area of one write transaction.
type leaseTx struct {
	*session
	scope ledger.WorkspaceID
	now   int64
}

var (
	// stmtWorkInProgress counts what a holder occupies. Its index keeps this from
	// scanning the workspace while holding the workspace lock.
	stmtWorkInProgress = define("lease.work_in_progress",
		`SELECT count(*) FROM work_orders
 WHERE workspace_id = :workspace_id AND state IN ('claimed', 'running', 'lapsed')`)

	// stmtGrant is the claim. A row count of 1 means won, and 0 means lost.
	stmtGrant = define("lease.grant",
		`UPDATE work_orders
   SET state = 'claimed',
       fence = fence + 1,
       claim_id = :claim_id,
       lease_worker = :worker_id,
       lease_until = CAST(:now AS BIGINT) + CAST(:ttl AS BIGINT)
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND state = 'ready'`)

	stmtStartWorkOrder = define("lease.start",
		`UPDATE work_orders
   SET state = 'running'
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence
   AND claim_id = :claim_id
   AND state = 'claimed'`)
)

func (t leaseTx) params(id string) params {
	return params{"workspace_id": string(t.scope), "id": id}
}

func (t leaseTx) leaseParams(lease workorders.Lease) params {
	p := t.params(lease.WorkOrder)
	p["fence"], p["claim_id"] = lease.Fence, lease.ClaimID
	return p
}

func (t leaseTx) matched(st *statement, p params) (bool, error) {
	n, err := t.exec(st, p)
	return n > 0, err
}

func (t leaseTx) WorkInProgress() (int64, error) {
	row, err := t.row(stmtWorkInProgress, params{"workspace_id": string(t.scope)})
	if err != nil {
		return 0, err
	}
	var count int64
	err = row.Scan(&count)
	return count, err
}

func (t leaseTx) Grant(request workorders.ClaimRequest) (bool, error) {
	p := t.params(request.WorkOrder)
	p["claim_id"], p["worker_id"], p["now"], p["ttl"] = request.ClaimID, request.Worker, t.now, request.TTLMs
	return t.matched(stmtGrant, p)
}

func (t leaseTx) Start(lease workorders.Lease) (bool, error) {
	return t.matched(stmtStartWorkOrder, t.leaseParams(lease))
}

var (
	// stmtGuardFence is the comparison of every fenced write. It runs inside the
	// transaction that writes, so nothing can change hands before the commit.
	stmtGuardFence = define("lease.guard",
		`UPDATE work_orders
   SET rev = rev + 1
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence`)

	// stmtLapse repeats the expiry predicate, because a sweep's list of candidates
	// can be out of date by the time this runs.
	stmtLapse = define("lease.lapse",
		`UPDATE work_orders
   SET state = 'lapsed'
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND state IN ('claimed', 'running')
   AND lease_until <= :now`)

	// stmtRenew is guarded by the same token as a write, so a stale worker cannot
	// extend a lease it lost. The deadline predicate refuses a renewal that arrives
	// after the deadline and before the sweep, so a lapse always goes on record.
	stmtRenew = define(OpRenew,
		`UPDATE work_orders
   SET lease_until = CAST(:now AS BIGINT) + CAST(:ttl AS BIGINT)
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence
   AND claim_id = :claim_id
   AND state IN ('claimed', 'running')
   AND lease_until > :now`)
)

func (t leaseTx) Guard(workOrder string, fence int64) (bool, error) {
	p := t.params(workOrder)
	p["fence"] = fence
	return t.matched(stmtGuardFence, p)
}

func (t leaseTx) Lapse(workOrder string) (bool, error) {
	p := t.params(workOrder)
	p["now"] = t.now
	return t.matched(stmtLapse, p)
}

var (
	// stmtReturn restores a lapsed lease, because renewal excludes `lapsed`.
	stmtReturn = define("lease.return",
		`UPDATE work_orders
   SET state = :restored_state,
       lease_until = CAST(:now AS BIGINT) + CAST(:ttl AS BIGINT)
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence
   AND claim_id = :claim_id
   AND state = 'lapsed'`)

	// stmtRevoke moves the fence in the statement that ends ownership, so the old
	// holder's token is stale at once and not only after the next grant.
	stmtRevoke = define("lease.revoke",
		`UPDATE work_orders
   SET state = 'ready',
       fence = fence + 1,
       claim_id = NULL,
       lease_worker = NULL,
       lease_until = NULL
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND state = 'lapsed'
   AND fence = :fence`)

	// stmtRelease moves the fence the same way. The state diagram releases only a
	// claimed work order, so a running one ends through its lane's outcome.
	stmtRelease = define("lease.release",
		`UPDATE work_orders
   SET state = 'ready',
       fence = fence + 1,
       claim_id = NULL,
       lease_worker = NULL,
       lease_until = NULL
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence
   AND claim_id = :claim_id
   AND state = 'claimed'`)
)

func (t leaseTx) Return(lease workorders.Lease, restored workorders.State, ttlMs int64) (bool, error) {
	p := t.leaseParams(lease)
	p["restored_state"], p["now"], p["ttl"] = string(restored), t.now, ttlMs
	return t.matched(stmtReturn, p)
}

func (t leaseTx) Revoke(workOrder string, fence int64) (bool, error) {
	p := t.params(workOrder)
	p["fence"] = fence
	return t.matched(stmtRevoke, p)
}

func (t leaseTx) Release(lease workorders.Lease) (bool, error) {
	return t.matched(stmtRelease, t.leaseParams(lease))
}

// Renew is the one write with no revision: a single guarded statement on one row,
// because every heartbeat would otherwise wait for the workspace lock. A row count
// of 0 reads the row in the same transaction to name the rejection.
func (s *Store) Renew(ctx context.Context, scope ledger.WorkspaceID, lease workorders.Lease, ttlMs int64) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	crashpoint.Hit(OpRenew, "begun")
	t := leaseTx{session: s.session(ctx, tx, ModeWrite, OpRenew), scope: scope}
	if t.now, err = t.readClock(); err != nil {
		return err
	}
	p := t.leaseParams(lease)
	p["now"], p["ttl"] = t.now, ttlMs
	won, err := t.matched(stmtRenew, p)
	if err != nil {
		return err
	}
	if !won {
		current, err := (workOrderTx{session: t.session, scope: scope}).Get(lease.WorkOrder)
		if err != nil {
			return err
		}
		return workorders.RejectRenewal(current, lease)
	}
	crashpoint.Hit(OpRenew, "before-commit")
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", OpRenew, err)
	}
	crashpoint.Hit(OpRenew, "committed")
	return nil
}

// claimTx is the claim area of one transaction.
type claimTx struct {
	*session
	scope ledger.WorkspaceID
	now   int64
}

var (
	stmtFindClaim = define("claim.find",
		`SELECT id, work_order_id, worker_id, fence, granted_at, ended_at, end_reason
  FROM claims
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListClaims = define("claim.list",
		`SELECT id, work_order_id, worker_id, fence, granted_at, ended_at, end_reason
  FROM claims
 WHERE workspace_id = :workspace_id
 ORDER BY work_order_id, fence`)

	stmtRecordClaim = define("claim.record",
		`INSERT INTO claims (workspace_id, id, work_order_id, worker_id, fence, granted_at)
VALUES (:workspace_id, :id, :work_order_id, :worker_id, :fence, :granted_at)`)

	stmtEndClaim = define("claim.end",
		`UPDATE claims SET ended_at = :now, end_reason = :reason
 WHERE workspace_id = :workspace_id AND id = :id AND ended_at IS NULL`)
)

func scanClaim(row scanner) (workorders.Claim, error) {
	var c workorders.Claim
	var endedAt sql.NullInt64
	var reason sql.NullString
	if err := row.Scan(&c.ID, &c.WorkOrder, &c.Worker, &c.Fence, &c.GrantedAt, &endedAt, &reason); err != nil {
		return workorders.Claim{}, err
	}
	c.EndedAt, c.EndReason = endedAt.Int64, reason.String
	return c, nil
}

func (t claimTx) Find(id string) (workorders.Claim, error) {
	row, err := t.row(stmtFindClaim, params{"workspace_id": string(t.scope), "id": id})
	if err != nil {
		return workorders.Claim{}, err
	}
	c, err := scanClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workorders.Claim{}, fmt.Errorf("claim %s: %w", id, ports.ErrNotFound)
	}
	return c, err
}

func (t claimTx) List() ([]workorders.Claim, error) {
	rows, err := t.rows(stmtListClaims, params{"workspace_id": string(t.scope)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []workorders.Claim
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (t claimTx) Record(c workorders.Claim) error {
	_, err := t.exec(stmtRecordClaim, params{
		"workspace_id": string(t.scope), "id": c.ID, "work_order_id": c.WorkOrder,
		"worker_id": c.Worker, "fence": c.Fence, "granted_at": c.GrantedAt,
	})
	return err
}

func (t claimTx) End(id, reason string) error {
	_, err := t.exec(stmtEndClaim, params{
		"workspace_id": string(t.scope), "id": id, "now": t.now, "reason": reason,
	})
	return err
}

// laneTx is the lane area of one transaction.
type laneTx struct {
	*session
	scope ledger.WorkspaceID
}

var (
	stmtLaneGeneration = define("lane.generation",
		`SELECT current_generation FROM lanes WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneStartedUnder = define("lane.started_under",
		`SELECT count(*) FROM lanes
 WHERE workspace_id = :workspace_id AND work_order_id = :work_order_id AND started_fence = :fence`)

	stmtStartLane = define("lane.start",
		`INSERT INTO lanes (workspace_id, id, work_order_id, current_generation, started_fence)
VALUES (:workspace_id, :id, :work_order_id, 1, :fence)`)

	stmtNextGeneration = define("lane.next_generation",
		`UPDATE lanes SET current_generation = current_generation + 1
 WHERE workspace_id = :workspace_id AND id = :id
RETURNING current_generation`)
)

func (t laneTx) Generation(lane string) (int64, error) {
	row, err := t.row(stmtLaneGeneration, params{"workspace_id": string(t.scope), "id": lane})
	if err != nil {
		return 0, err
	}
	var generation int64
	if err := row.Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
		}
		return 0, err
	}
	return generation, nil
}

func (t laneTx) StartedUnder(workOrder string, fence int64) (bool, error) {
	row, err := t.row(stmtLaneStartedUnder, params{
		"workspace_id": string(t.scope), "work_order_id": workOrder, "fence": fence,
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

func (t laneTx) Start(lane, workOrder string, fence int64) error {
	_, err := t.exec(stmtStartLane, params{
		"workspace_id": string(t.scope), "id": lane, "work_order_id": workOrder, "fence": fence,
	})
	return err
}

func (t laneTx) NextGeneration(lane string) (int64, error) {
	row, err := t.row(stmtNextGeneration, params{"workspace_id": string(t.scope), "id": lane})
	if err != nil {
		return 0, err
	}
	var generation int64
	if err := row.Scan(&generation); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
		}
		return 0, err
	}
	t.changed = true
	return generation, nil
}
