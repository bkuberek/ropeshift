package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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

	stmtInRepository = define("lease.in_repository",
		`SELECT count(*) FROM work_orders
 WHERE workspace_id = :workspace_id AND repository_id = :repository AND state IN ('claimed', 'running', 'lapsed')`)

	stmtHeldByWorker = define("lease.held_by_worker",
		`SELECT count(*) FROM work_orders
 WHERE workspace_id = :workspace_id AND lease_worker = :worker AND state IN ('claimed', 'running', 'lapsed')`)

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

	// stmtStartWorkOrder clears the lane to resume here and not at the claim, because a claim
	// given back before its lane starts leaves that lane still to resume.
	stmtStartWorkOrder = define("lease.start",
		`UPDATE work_orders
   SET state = 'running', resume_lane_id = NULL
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

func (t leaseTx) count(st *statement, p params) (int64, error) {
	row, err := t.row(st, p)
	if err != nil {
		return 0, err
	}
	var count int64
	err = row.Scan(&count)
	return count, err
}

func (t leaseTx) InRepository(repository string) (int64, error) {
	return t.count(stmtInRepository, params{"workspace_id": string(t.scope), "repository": repository})
}

func (t leaseTx) HeldByWorker(worker string) (int64, error) {
	return t.count(stmtHeldByWorker, params{"workspace_id": string(t.scope), "worker": worker})
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

	// stmtEndLane closes a lease by its lane's outcome. The fence moves here too, so
	// the old holder's token is stale at once, whichever state the work order takes.
	stmtEndLane = define("lease.end",
		`UPDATE work_orders
   SET state = :to_state,
       fence = fence + 1,
       claim_id = NULL,
       lease_worker = NULL,
       lease_until = NULL,
       attempts = :attempts
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence
   AND claim_id = :claim_id
   AND state = 'running'`)
)

// stmtParkLane releases a lease with its process gone. The fence moves here too, so the old
// holder's token is stale at once.
var stmtParkLane = define("lease.park",
	`UPDATE work_orders
   SET state = 'parked',
       fence = fence + 1,
       claim_id = NULL,
       lease_worker = NULL,
       lease_until = NULL
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND fence = :fence
   AND claim_id = :claim_id
   AND state = 'running'`)

func (t leaseTx) Park(lease workorders.Lease) (bool, error) {
	return t.matched(stmtParkLane, t.leaseParams(lease))
}

// stmtCancel repeats the state and fence the use case read, and moves the fence in the statement that
// ends any ownership, so a holder's token is stale at once.
var stmtCancel = define("lease.cancel",
	`UPDATE work_orders
   SET state = 'canceled',
       fence = fence + CAST(:moved AS BIGINT),
       claim_id = NULL,
       lease_worker = NULL,
       lease_until = NULL,
       resume_lane_id = NULL
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND state = :from_state
   AND fence = :fence`)

func (t leaseTx) Cancel(workOrder string, from workorders.State, fence int64, held bool) (bool, error) {
	p := t.params(workOrder)
	p["from_state"], p["fence"], p["moved"] = string(from), fence, int64(0)
	if held {
		p["moved"] = int64(1)
	}
	return t.matched(stmtCancel, p)
}

func (t leaseTx) End(lease workorders.Lease, to workorders.State, attempts int64) (bool, error) {
	p := t.leaseParams(lease)
	p["to_state"], p["attempts"] = string(to), attempts
	return t.matched(stmtEndLane, p)
}

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
		`SELECT id, work_order_id, worker_id, fence, granted_at, ended_at, end_reason, lane_id, lane_overrides, pack_id
  FROM claims
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListClaims = define("claim.list",
		`SELECT id, work_order_id, worker_id, fence, granted_at, ended_at, end_reason, lane_id, lane_overrides, pack_id
  FROM claims
 WHERE workspace_id = :workspace_id
 ORDER BY work_order_id, fence`)

	stmtRecordClaim = define("claim.record",
		`INSERT INTO claims (workspace_id, id, work_order_id, worker_id, fence, granted_at, lane_id, lane_overrides, pack_id)
VALUES (:workspace_id, :id, :work_order_id, :worker_id, :fence, :granted_at, :lane, :overrides, :pack_id)`)

	stmtClaimOfLane = define("claim.of_lane",
		`SELECT id, work_order_id, worker_id, fence, granted_at, ended_at, end_reason, lane_id, lane_overrides, pack_id
  FROM claims
 WHERE workspace_id = :workspace_id AND lane_id = :lane
 ORDER BY fence DESC
 LIMIT 1`)

	stmtEndClaim = define("claim.end",
		`UPDATE claims SET ended_at = :now, end_reason = :reason
 WHERE workspace_id = :workspace_id AND id = :id AND ended_at IS NULL`)

	stmtDispatchClaim = define("claim.dispatch",
		`UPDATE claims SET lane_id = :lane, lane_overrides = :overrides
 WHERE workspace_id = :workspace_id AND id = :id AND ended_at IS NULL`)
)

func scanClaim(row scanner) (workorders.Claim, error) {
	var c workorders.Claim
	var endedAt sql.NullInt64
	var reason, lane, pack sql.NullString
	if err := row.Scan(&c.ID, &c.WorkOrder, &c.Worker, &c.Fence, &c.GrantedAt, &endedAt, &reason, &lane, &c.Overrides, &pack); err != nil {
		return workorders.Claim{}, err
	}
	c.EndedAt, c.EndReason, c.Lane, c.PackID = endedAt.Int64, reason.String, lane.String, pack.String
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
		"lane": nullText(c.Lane), "overrides": c.Overrides, "pack_id": nullText(c.PackID),
	})
	return err
}

func (t claimTx) OfLane(lane string) (workorders.Claim, error) {
	row, err := t.row(stmtClaimOfLane, params{"workspace_id": string(t.scope), "lane": lane})
	if err != nil {
		return workorders.Claim{}, err
	}
	claim, err := scanClaim(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workorders.Claim{}, fmt.Errorf("claim of lane %s: %w", lane, ports.ErrNotFound)
	}
	return claim, err
}

func (t claimTx) Dispatch(id, lane, overrides string) error {
	_, err := t.exec(stmtDispatchClaim, params{"workspace_id": string(t.scope), "id": id, "lane": lane, "overrides": overrides})
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

	stmtLaneReport = define("lane.report",
		`UPDATE lanes
   SET worker_id = :worker_id, actual_state = :actual_state, conditions = :conditions, liveness = :liveness,
       reach_model = :reach_model, reach_ledger = :reach_ledger, reported_at = :now, reported_by_epoch = :epoch
 WHERE workspace_id = :workspace_id
   AND id = :id
   AND actual_state <> 'ended'
   AND reported_by_epoch <= :epoch`)

	stmtLaneMarkEnded = define("lane.mark_ended",
		`UPDATE lanes
   SET actual_state = 'ended', liveness = 'dead', reach_model = 'unknown', reach_ledger = 'unknown', reported_at = :now
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneParking = define("lane.mark_parking",
		`UPDATE lanes SET actual_state = 'parking', reported_at = :now
 WHERE workspace_id = :workspace_id AND id = :id AND actual_state <> 'ended'`)

	stmtLaneParked = define("lane.mark_parked",
		`UPDATE lanes
   SET actual_state = 'parked', liveness = 'dead', reach_model = 'unknown', reach_ledger = 'unknown', reported_at = :now,
       resume_affinity = :affinity, resume_worker = :worker
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneWakened = define("lane.wakened",
		`UPDATE lanes SET resume_after = CAST(:after AS BIGINT) WHERE workspace_id = :workspace_id AND id = :id`)

	// stmtLaneEase repeats its predicates, because a tick's list can be out of date by now.
	stmtLaneEase = define("lane.ease",
		`UPDATE lanes SET resume_affinity = ''
 WHERE workspace_id = :workspace_id AND id = :id AND resume_affinity = 'preferred' AND resume_after <= :now`)

	stmtLaneEasing = define("lane.easing",
		`SELECT id FROM lanes
 WHERE workspace_id = :workspace_id AND resume_affinity = 'preferred' AND resume_after <= :now
 ORDER BY id`)

	stmtLaneResume = define("lane.resume",
		`UPDATE lanes
   SET started_fence = :fence, resume_affinity = '', resume_after = NULL, actual_state = 'provisioning',
       liveness = 'unknown', reported_at = :now
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneGuarded = define("lane.guarded",
		`UPDATE lanes
   SET unpushed_work = CAST(:unpushed AS BIGINT),
       archive_request = CASE WHEN CAST(:unpushed AS BIGINT) = 1 AND archive_request = 'plain' THEN '' ELSE archive_request END
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtInsertArtifact = define("artifact.insert",
		`INSERT INTO artifacts (workspace_id, id, lane_id, kind, uri, digest, fence, created_at)
VALUES (:workspace_id, :id, :lane_id, :kind, :uri, :digest, :fence, :now)`)

	stmtListArtifacts = define("artifact.list",
		`SELECT id, lane_id, kind, uri, digest, fence, created_at FROM artifacts
 WHERE workspace_id = :workspace_id AND lane_id = :lane_id ORDER BY created_at, kind, uri, id`)

	stmtRequestArchive = define("lane.request_archive",
		`UPDATE lanes SET archive_request = :request WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneArchived = define("lane.archived",
		`UPDATE lanes SET archived = 1, archive_request = '', unpushed_work = 0
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLanesArchiving = define("lane.archiving",
		`SELECT l.id, l.archive_request, w.repository_id
  FROM lanes l
  JOIN claims c ON c.workspace_id = l.workspace_id AND c.lane_id = l.id
  JOIN work_orders w ON w.workspace_id = l.workspace_id AND w.id = l.work_order_id
 WHERE l.workspace_id = :workspace_id AND l.archive_request <> '' AND l.archived = 0 AND c.worker_id = :worker
 ORDER BY l.id`)

	stmtLaneRow = define("lane.row",
		`SELECT `+laneRowColumns+` FROM lanes WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneRows = define("lane.rows",
		`SELECT `+laneRowColumns+` FROM lanes WHERE workspace_id = :workspace_id ORDER BY id`)

	stmtLaneWantPark = define("lane.want_park",
		`UPDATE lanes SET park_wanted = :reason WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneResets = define("lane.resets",
		`SELECT resets FROM lanes WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneRespawns = define("lane.respawns",
		`SELECT respawns FROM lanes WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneRespawn = define("lane.respawn",
		`UPDATE lanes SET respawns = respawns + 1 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtLaneReset = define("lane.reset",
		`UPDATE lanes SET resets = resets + 1
 WHERE workspace_id = :workspace_id AND id = :id
RETURNING resets`)

	stmtNextGeneration = define("lane.next_generation",
		`UPDATE lanes SET current_generation = current_generation + 1
 WHERE workspace_id = :workspace_id AND id = :id
RETURNING current_generation`)
)

const laneRowColumns = `id, work_order_id, worker_id, current_generation, actual_state, conditions, liveness,
       reach_model, reach_ledger, reported_at, reported_by_epoch, started_fence, unpushed_work, archived,
       resume_affinity, resume_worker, park_wanted`

// The values of `archive_request`. Empty means nobody asked.
const (
	archivePlain  = "plain"
	archiveForced = "forced"
)

// conditionSeparator joins a lane's conditions, none of which holds it.
const conditionSeparator = ","

func scanLaneRow(row interface{ Scan(...any) error }) (ledger.LaneRow, error) {
	var out ledger.LaneRow
	var conditions string
	var unpushed, archived int64
	err := row.Scan(&out.ID, &out.WorkOrder, &out.WorkerID, &out.Generation, &out.Status.ActualState, &conditions,
		&out.Status.Liveness, &out.Status.ModelReach, &out.Status.LedgerReach, &out.ReportedAt, &out.ReportedByEpoch,
		&out.StartedFence, &unpushed, &archived, &out.Hold.Affinity, &out.Hold.Worker, &out.ParkWanted)
	if conditions != "" {
		out.Status.Conditions = strings.Split(conditions, conditionSeparator)
	}
	out.UnpushedWork, out.Archived = unpushed != 0, archived != 0
	return out, err
}

func (t laneTx) Row(lane string) (ledger.LaneRow, error) {
	row, err := t.row(stmtLaneRow, params{"workspace_id": string(t.scope), "id": lane})
	if err != nil {
		return ledger.LaneRow{}, err
	}
	out, err := scanLaneRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.LaneRow{}, fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
	}
	return out, err
}

func (t laneTx) Rows() ([]ledger.LaneRow, error) {
	rows, err := t.rows(stmtLaneRows, params{"workspace_id": string(t.scope)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.LaneRow
	for rows.Next() {
		row, err := scanLaneRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (t laneTx) Report(row ledger.LaneRow) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtLaneReport, params{
		"workspace_id": string(t.scope), "id": row.ID, "worker_id": row.WorkerID, "actual_state": row.Status.ActualState,
		"conditions": strings.Join(row.Status.Conditions, conditionSeparator), "liveness": row.Status.Liveness,
		"reach_model": row.Status.ModelReach, "reach_ledger": row.Status.LedgerReach, "now": now, "epoch": row.ReportedByEpoch,
	})
	return err
}

func (t laneTx) WantPark(lane, reason string) error {
	_, err := t.exec(stmtLaneWantPark, params{"workspace_id": string(t.scope), "id": lane, "reason": reason})
	return err
}

func (t laneTx) Guarded(lane string, unpushed bool) error {
	flag := int64(0)
	if unpushed {
		flag = 1
	}
	_, err := t.exec(stmtLaneGuarded, params{"workspace_id": string(t.scope), "id": lane, "unpushed": flag})
	return err
}

func (t laneTx) AddArtifact(artifact ledger.Artifact) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtInsertArtifact, params{
		"workspace_id": string(t.scope), "id": artifact.ID, "lane_id": artifact.Lane, "kind": artifact.Kind,
		"uri": artifact.URI, "digest": artifact.Digest, "fence": artifact.Fence, "now": now,
	})
	return err
}

func (t laneTx) Artifacts(lane string) ([]ledger.Artifact, error) {
	rows, err := t.rows(stmtListArtifacts, params{"workspace_id": string(t.scope), "lane_id": lane})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func scanArtifacts(rows *sql.Rows) ([]ledger.Artifact, error) {
	var out []ledger.Artifact
	for rows.Next() {
		var artifact ledger.Artifact
		if err := rows.Scan(&artifact.ID, &artifact.Lane, &artifact.Kind, &artifact.URI, &artifact.Digest, &artifact.Fence, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}

func (t laneTx) RequestArchive(lane string, forced bool) error {
	request := archivePlain
	if forced {
		request = archiveForced
	}
	_, err := t.exec(stmtRequestArchive, params{"workspace_id": string(t.scope), "id": lane, "request": request})
	return err
}

func (t laneTx) Archived(lane string) error {
	_, err := t.exec(stmtLaneArchived, params{"workspace_id": string(t.scope), "id": lane})
	return err
}

func (t laneTx) Archiving(worker string) ([]ledger.DesiredArchive, error) {
	rows, err := t.rows(stmtLanesArchiving, params{"workspace_id": string(t.scope), "worker": worker})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.DesiredArchive
	for rows.Next() {
		var archive ledger.DesiredArchive
		var request string
		var repository sql.NullString
		if err := rows.Scan(&archive.Lane, &request, &repository); err != nil {
			return nil, err
		}
		archive.Forced, archive.Repository = request == archiveForced, repository.String
		out = append(out, archive)
	}
	return out, rows.Err()
}

func (t laneTx) MarkEnded(lane string) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtLaneMarkEnded, params{"workspace_id": string(t.scope), "id": lane, "now": now})
	return err
}

func (t laneTx) MarkParking(lane string) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtLaneParking, params{"workspace_id": string(t.scope), "id": lane, "now": now})
	return err
}

func (t laneTx) MarkParked(lane string, hold ledger.ResumeHold) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtLaneParked, params{
		"workspace_id": string(t.scope), "id": lane, "now": now, "affinity": hold.Affinity, "worker": hold.Worker,
	})
	return err
}

func (t laneTx) Wakened(lane string, after int64) error {
	_, err := t.exec(stmtLaneWakened, params{"workspace_id": string(t.scope), "id": lane, "after": after})
	return err
}

func (t laneTx) Ease(lane string, now int64) (bool, error) {
	n, err := t.exec(stmtLaneEase, params{"workspace_id": string(t.scope), "id": lane, "now": now})
	return n > 0, err
}

func (t laneTx) EasingDue(now int64) ([]string, error) {
	rows, err := t.rows(stmtLaneEasing, params{"workspace_id": string(t.scope), "now": now})
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

func (t laneTx) Resume(lane string, fence int64) error {
	now, err := t.readClock()
	if err != nil {
		return err
	}
	_, err = t.exec(stmtLaneResume, params{"workspace_id": string(t.scope), "id": lane, "fence": fence, "now": now})
	return err
}

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

func (t laneTx) Resets(lane string) (int64, error) {
	row, err := t.row(stmtLaneResets, params{"workspace_id": string(t.scope), "id": lane})
	if err != nil {
		return 0, err
	}
	var resets int64
	if err := row.Scan(&resets); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
		}
		return 0, err
	}
	return resets, nil
}

func (t laneTx) Respawns(lane string) (int64, error) {
	row, err := t.row(stmtLaneRespawns, params{"workspace_id": string(t.scope), "id": lane})
	if err != nil {
		return 0, err
	}
	var respawns int64
	if err := row.Scan(&respawns); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
		}
		return 0, err
	}
	return respawns, nil
}

func (t laneTx) Respawn(lane string) error {
	_, err := t.exec(stmtLaneRespawn, params{"workspace_id": string(t.scope), "id": lane})
	return err
}

func (t laneTx) Reset(lane string) (int64, error) {
	row, err := t.row(stmtLaneReset, params{"workspace_id": string(t.scope), "id": lane})
	if err != nil {
		return 0, err
	}
	var resets int64
	if err := row.Scan(&resets); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("lane %s: %w", lane, ports.ErrNotFound)
		}
		return 0, err
	}
	t.changed = true
	return resets, nil
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
