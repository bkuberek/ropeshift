package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const workerColumns = `id, name, epoch, protocol, capabilities, proof_digest, last_handshake_at, last_beat_at, report, reported_at`

const capacityColumns = `worker_id, cap, cap_value, epoch, started_at, seen_at, ended_at`

var (
	stmtFindWorker = define("worker.find",
		`SELECT `+workerColumns+` FROM workers WHERE workspace_id = :workspace_id AND id = :id`)

	stmtListWorkers = define("worker.list",
		`SELECT `+workerColumns+` FROM workers WHERE workspace_id = :workspace_id ORDER BY id`)

	stmtInsertWorker = define("worker.insert",
		`INSERT INTO workers (workspace_id, id, name, epoch, protocol, capabilities, proof_digest)
VALUES (:workspace_id, :id, :name, 0, 0, '{}', :proof_digest)`)

	stmtReplaceProof = define("worker.replace_proof",
		`UPDATE workers SET proof_digest = :proof_digest WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOpenEpoch = define("worker.open_epoch",
		`UPDATE workers
   SET epoch = epoch + 1,
       protocol = :protocol,
       capabilities = :capabilities,
       last_handshake_at = :now
 WHERE workspace_id = :workspace_id AND id = :id
RETURNING epoch`)

	stmtSaveReport = define(OpSaveReport,
		`UPDATE workers SET report = :report, reported_at = :reported_at
 WHERE workspace_id = :workspace_id AND id = :id AND epoch = :epoch`)

	stmtBeat = define(OpBeat,
		`UPDATE workers SET last_beat_at = :now
 WHERE workspace_id = :workspace_id AND id = :id AND epoch = :epoch`)

	stmtStandingCapacity = define("capacity.standing",
		`SELECT `+capacityColumns+` FROM capacity_intervals
 WHERE workspace_id = :workspace_id AND worker_id = :worker AND ended_at = 0
 ORDER BY started_at, cap`)

	stmtSeeCapacity = define("capacity.see",
		`UPDATE capacity_intervals SET seen_at = :seen_at
 WHERE workspace_id = :workspace_id AND worker_id = :worker AND cap = :cap AND started_at = :started_at`)

	stmtEndCapacity = define("capacity.end",
		`UPDATE capacity_intervals SET ended_at = :ended_at
 WHERE workspace_id = :workspace_id AND worker_id = :worker AND cap = :cap AND started_at = :started_at`)

	// stmtBeginCapacity takes the place of an interval that ended where this one begins, which held no time.
	stmtBeginCapacity = define("capacity.begin",
		`INSERT INTO capacity_intervals (workspace_id, worker_id, cap, cap_value, epoch, started_at, seen_at, ended_at)
VALUES (:workspace_id, :worker, :cap, :cap_value, :epoch, :started_at, :seen_at, 0)
ON CONFLICT (workspace_id, worker_id, cap, started_at) DO UPDATE
   SET cap_value = :cap_value, epoch = :epoch, seen_at = :seen_at, ended_at = 0`)

	stmtCapacitySince = define("capacity.since",
		`SELECT `+capacityColumns+` FROM capacity_intervals
 WHERE workspace_id = :workspace_id AND CASE WHEN ended_at = 0 THEN seen_at ELSE ended_at END >= CAST(:since AS BIGINT)
 ORDER BY started_at, worker_id, cap`)

	stmtInsertJoinGrant = define("join_grant.insert",
		`INSERT INTO join_grants (workspace_id, id, digest, created_by)
VALUES (:workspace_id, :id, :digest, :created_by)`)

	stmtReadJoinGrant = define("join_grant.read",
		`SELECT id, digest, created_by, used_by FROM join_grants
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtSpendJoinGrant = define("join_grant.spend",
		`UPDATE join_grants SET used_by = :worker
 WHERE workspace_id = :workspace_id AND id = :id AND used_by IS NULL`)

	stmtFindOrchestrator = define("orchestrator.of_worker",
		`SELECT id, worker_id, desired_state, run, resets, actual_state, generation, conditions, reported_at, settings FROM orchestrators
 WHERE workspace_id = :workspace_id AND worker_id = :worker`)

	stmtSaveOrchestrator = define("orchestrator.save",
		`INSERT INTO orchestrators (workspace_id, id, worker_id, desired_state, run, resets, actual_state, generation, conditions, reported_at, settings)
VALUES (:workspace_id, :id, :worker, :desired_state, :run, :resets, :actual_state, :generation, :conditions, :reported_at, :settings)
ON CONFLICT (workspace_id, id) DO UPDATE
   SET desired_state = :desired_state, run = :run, resets = :resets, actual_state = :actual_state, generation = :generation,
       conditions = :conditions, reported_at = :reported_at, settings = :settings`)

	stmtFindAutonomy = define("autonomy.of_worker",
		`SELECT worker_id, level, acknowledged_by, acknowledged_kind, acknowledged_digest, acknowledged_at FROM worker_autonomy
 WHERE workspace_id = :workspace_id AND worker_id = :worker`)

	stmtSaveAutonomy = define("autonomy.save",
		`INSERT INTO worker_autonomy (workspace_id, worker_id, level, acknowledged_by, acknowledged_kind, acknowledged_digest, acknowledged_at)
VALUES (:workspace_id, :worker, :level, :acknowledged_by, :acknowledged_kind, :acknowledged_digest, :acknowledged_at)
ON CONFLICT (workspace_id, worker_id) DO UPDATE
   SET level = :level, acknowledged_by = :acknowledged_by, acknowledged_kind = :acknowledged_kind,
       acknowledged_digest = :acknowledged_digest, acknowledged_at = :acknowledged_at`)

	stmtInsertWindow = define("run_window.insert",
		`INSERT INTO run_windows (workspace_id, id, worker_id, opened_by, opened_kind, opened_at, closes_at, closed_by, closed_at)
VALUES (:workspace_id, :id, :worker, :opened_by, :opened_kind, :opened_at, :closes_at, '', 0)`)

	stmtReadWindow = define("run_window.read",
		`SELECT id, worker_id, opened_by, opened_kind, opened_at, closes_at, closed_by, closed_at FROM run_windows
 WHERE workspace_id = :workspace_id AND id = :id`)

	stmtOpenWindow = define("run_window.open",
		`SELECT id, worker_id, opened_by, opened_kind, opened_at, closes_at, closed_by, closed_at FROM run_windows
 WHERE workspace_id = :workspace_id AND worker_id = :worker AND closed_at = 0
   AND opened_at <= CAST(:now AS BIGINT) AND closes_at > CAST(:now AS BIGINT)
 ORDER BY closes_at DESC, id LIMIT 1`)

	stmtCloseWindow = define("run_window.close",
		`UPDATE run_windows SET closed_by = :closed_by, closed_at = :closed_at
 WHERE workspace_id = :workspace_id AND id = :id AND closed_at = 0`)
)

// Autonomy returns what the ledger holds of a worker beyond the policy.
func (t workerTx) Autonomy(worker string) (authority.WorkerAutonomy, error) {
	found, err := t.row(stmtFindAutonomy, t.scoped(params{"worker": worker}))
	if err != nil {
		return authority.WorkerAutonomy{}, err
	}
	row := authority.WorkerAutonomy{WorkerID: worker}
	var level string
	err = found.Scan(&row.WorkerID, &level, &row.AcknowledgedBy, &row.AcknowledgedKind, &row.AcknowledgedDigest, &row.AcknowledgedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.WorkerAutonomy{WorkerID: worker}, nil
	}
	row.Level = authority.Level(level)
	return row, err
}

// SaveAutonomy writes what the ledger holds of a worker.
func (t workerTx) SaveAutonomy(row authority.WorkerAutonomy) error {
	_, err := t.exec(stmtSaveAutonomy, t.scoped(params{
		"worker": row.WorkerID, "level": string(row.Level), "acknowledged_by": row.AcknowledgedBy,
		"acknowledged_kind": row.AcknowledgedKind, "acknowledged_digest": row.AcknowledgedDigest, "acknowledged_at": row.AcknowledgedAt,
	}))
	return err
}

func scanWindow(row scanner, name string) (authority.RunWindow, error) {
	var out authority.RunWindow
	err := row.Scan(&out.ID, &out.WorkerID, &out.OpenedBy, &out.OpenedKind, &out.OpenedAt, &out.ClosesAt, &out.ClosedBy, &out.ClosedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return authority.RunWindow{}, fmt.Errorf("run window %s: %w", name, ports.ErrNotFound)
	}
	return out, err
}

// Window returns one run window, or ErrNotFound.
func (t workerTx) Window(id string) (authority.RunWindow, error) {
	found, err := t.row(stmtReadWindow, t.scoped(params{"id": id}))
	if err != nil {
		return authority.RunWindow{}, err
	}
	return scanWindow(found, id)
}

// OpenWindow returns the run window that stands for a worker at a time, or ErrNotFound.
func (t workerTx) OpenWindow(worker string, now int64) (authority.RunWindow, error) {
	found, err := t.row(stmtOpenWindow, t.scoped(params{"worker": worker, "now": now}))
	if err != nil {
		return authority.RunWindow{}, err
	}
	return scanWindow(found, "of "+worker)
}

// InsertWindow adds a run window.
func (t workerTx) InsertWindow(row authority.RunWindow) error {
	_, err := t.exec(stmtInsertWindow, t.scoped(params{
		"id": row.ID, "worker": row.WorkerID, "opened_by": row.OpenedBy, "opened_kind": row.OpenedKind,
		"opened_at": row.OpenedAt, "closes_at": row.ClosesAt,
	}))
	return err
}

// CloseWindow is the compare-and-set that ends a window.
func (t workerTx) CloseWindow(id, by string, at int64) (bool, error) {
	changed, err := t.exec(stmtCloseWindow, t.scoped(params{"id": id, "closed_by": by, "closed_at": at}))
	return changed == 1, err
}

// Orchestrator returns one worker's orchestrator, or ErrNotFound.
func (t workerTx) Orchestrator(worker string) (ledger.Orchestrator, error) {
	found, err := t.row(stmtFindOrchestrator, t.scoped(params{"worker": worker}))
	if err != nil {
		return ledger.Orchestrator{}, err
	}
	var row ledger.Orchestrator
	var conditions, settings string
	err = found.Scan(&row.ID, &row.WorkerID, &row.Desired, &row.Run, &row.Resets, &row.Actual, &row.Generation, &conditions, &row.ReportedAt, &settings)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Orchestrator{}, fmt.Errorf("orchestrator of %s: %w", worker, ports.ErrNotFound)
	}
	if conditions != "" {
		row.Conditions = strings.Split(conditions, conditionSeparator)
	}
	if settings != "" {
		row.Settings = json.RawMessage(settings)
	}
	return row, err
}

// SaveOrchestrator writes the projection.
func (t workerTx) SaveOrchestrator(row ledger.Orchestrator) error {
	_, err := t.exec(stmtSaveOrchestrator, t.scoped(params{
		"id": row.ID, "worker": row.WorkerID, "desired_state": row.Desired, "run": row.Run, "resets": row.Resets,
		"actual_state": row.Actual, "generation": row.Generation,
		"conditions": strings.Join(row.Conditions, conditionSeparator), "reported_at": row.ReportedAt, "settings": string(row.Settings),
	}))
	return err
}

// workerTx is the worker area of one transaction.
type workerTx struct {
	*session
	scope ledger.WorkspaceID
	now   int64
}

func (t workerTx) scoped(p params) params {
	p["workspace_id"] = string(t.scope)
	return p
}

func scanWorker(row scanner) (ledger.WorkerRecord, error) {
	var w ledger.WorkerRecord
	var manifest, report string
	var handshake sql.NullInt64
	err := row.Scan(&w.ID, &w.Name, &w.Epoch, &w.Protocol, &manifest, &w.ProofDigest, &handshake, &w.LastBeatAt, &report, &w.ReportedAt)
	if err != nil {
		return ledger.WorkerRecord{}, err
	}
	w.LastHandshakeAt = handshake.Int64
	if err := json.Unmarshal([]byte(manifest), &w.Manifest); err != nil {
		return ledger.WorkerRecord{}, fmt.Errorf("worker %s: %w", w.ID, err)
	}
	if report != "" {
		w.Report = &ledger.ResourceReport{}
		if err := json.Unmarshal([]byte(report), w.Report); err != nil {
			return ledger.WorkerRecord{}, fmt.Errorf("worker %s: report: %w", w.ID, err)
		}
	}
	return w, nil
}

// saveReport is the write of a worker's report: one short transaction on the worker's rows, with no
// revision and no event, because a heartbeat carries one every few seconds and must not wait for the lock
// every change takes. It dates the report by database time less the age the worker sent, and moves the
// worker's capacity intervals by it (FR-OBS-14). A stale epoch matches no row and moves no interval.
func (s *Store) saveReport(ctx context.Context, scope ledger.WorkspaceID, worker string, epoch int64, report ledger.ResourceReport, freshMs int64) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	age := max(report.AgeMs, 0)
	report.AgeMs = 0
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := workerTx{session: s.session(ctx, tx, ModeWrite, OpSaveReport), scope: scope}
	now, err := t.readClock()
	if err != nil {
		return err
	}
	at := now - age
	saved, err := t.exec(stmtSaveReport, t.scoped(params{"id": worker, "epoch": epoch, "report": string(raw), "reported_at": at}))
	if err != nil {
		return err
	}
	if saved > 0 {
		if err := t.moveCapacity(worker, epoch, report, at, freshMs); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", OpSaveReport, err)
	}
	return nil
}

// beat records that a worker's session beat, dated by database time: one short transaction on the worker's
// row, with no revision and no event, because every heartbeat writes it. A stale epoch matches no row.
func (s *Store) beat(ctx context.Context, scope ledger.WorkspaceID, worker string, epoch int64) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := workerTx{session: s.session(ctx, tx, ModeWrite, OpBeat), scope: scope}
	now, err := t.readClock()
	if err != nil {
		return err
	}
	if _, err := t.exec(stmtBeat, t.scoped(params{"id": worker, "epoch": epoch, "now": now})); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", OpBeat, err)
	}
	return nil
}

// moveCapacity ends, extends, and begins a worker's capacity intervals as one report says.
func (t workerTx) moveCapacity(worker string, epoch int64, report ledger.ResourceReport, at, freshMs int64) error {
	standing, err := t.capacity(stmtStandingCapacity, params{"worker": worker})
	if err != nil {
		return err
	}
	step := lanes.StepCapacity(standing, worker, epoch, report, at, freshMs)
	key := func(c ledger.CapacityInterval) params {
		return t.scoped(params{"worker": c.Worker, "cap": c.Cap, "started_at": c.StartedAt})
	}
	for _, c := range step.Ended {
		p := key(c)
		p["ended_at"] = c.EndedAt
		if _, err := t.exec(stmtEndCapacity, p); err != nil {
			return err
		}
	}
	for _, c := range step.Seen {
		p := key(c)
		p["seen_at"] = c.SeenAt
		if _, err := t.exec(stmtSeeCapacity, p); err != nil {
			return err
		}
	}
	for _, c := range step.Begun {
		p := key(c)
		p["cap_value"], p["epoch"], p["seen_at"] = c.Value, c.Epoch, c.SeenAt
		if _, err := t.exec(stmtBeginCapacity, p); err != nil {
			return err
		}
	}
	return nil
}

// CapacityIntervals returns every worker's capacity interval that ends at or after a time.
func (t workerTx) CapacityIntervals(since int64) ([]ledger.CapacityInterval, error) {
	return t.capacity(stmtCapacitySince, params{"since": since})
}

func (t workerTx) capacity(st *statement, p params) ([]ledger.CapacityInterval, error) {
	rows, err := t.rows(st, t.scoped(p))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledger.CapacityInterval{}
	for rows.Next() {
		var c ledger.CapacityInterval
		if err := rows.Scan(&c.Worker, &c.Cap, &c.Value, &c.Epoch, &c.StartedAt, &c.SeenAt, &c.EndedAt); err != nil {
			return nil, fmt.Errorf("%s: %w", st.name, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (t workerTx) Find(id string) (ledger.WorkerRecord, error) {
	row, err := t.row(stmtFindWorker, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.WorkerRecord{}, err
	}
	w, err := scanWorker(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.WorkerRecord{}, fmt.Errorf("worker %s: %w", id, ports.ErrNotFound)
	}
	return w, err
}

func (t workerTx) List() ([]ledger.Worker, error) {
	rows, err := t.rows(stmtListWorkers, t.scoped(params{}))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.Worker
	for rows.Next() {
		w, err := scanWorker(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w.Worker)
	}
	return out, rows.Err()
}

func (t workerTx) Insert(w ledger.WorkerRecord) error {
	_, err := t.exec(stmtInsertWorker, t.scoped(params{"id": w.ID, "name": w.Name, "proof_digest": w.ProofDigest}))
	return err
}

func (t workerTx) ReplaceProof(id, digest string) error {
	_, err := t.exec(stmtReplaceProof, t.scoped(params{"id": id, "proof_digest": digest}))
	return err
}

func (t workerTx) OpenEpoch(w ledger.Worker) (int64, error) {
	raw, err := json.Marshal(w.Manifest)
	if err != nil {
		return 0, err
	}
	row, err := t.row(stmtOpenEpoch, t.scoped(params{
		"id": w.ID, "protocol": int64(w.Protocol), "capabilities": string(raw), "now": t.now,
	}))
	if err != nil {
		return 0, err
	}
	var epoch int64
	if err := row.Scan(&epoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("worker %s: %w", w.ID, ports.ErrNotFound)
		}
		return 0, err
	}
	t.changed = true
	return epoch, nil
}

func (t workerTx) InsertJoinGrant(g ledger.JoinGrant) error {
	_, err := t.exec(stmtInsertJoinGrant, t.scoped(params{"id": g.ID, "digest": g.Digest, "created_by": g.CreatedBy}))
	return err
}

func (t workerTx) JoinGrant(id string) (ledger.JoinGrant, error) {
	row, err := t.row(stmtReadJoinGrant, t.scoped(params{"id": id}))
	if err != nil {
		return ledger.JoinGrant{}, err
	}
	var g ledger.JoinGrant
	var used sql.NullString
	err = row.Scan(&g.ID, &g.Digest, &g.CreatedBy, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.JoinGrant{}, fmt.Errorf("join grant %s: %w", id, ports.ErrNotFound)
	}
	g.UsedBy = used.String
	return g, err
}

func (t workerTx) SpendJoinGrant(id, worker string) error {
	_, err := t.exec(stmtSpendJoinGrant, t.scoped(params{"id": id, "worker": worker}))
	return err
}
