package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

//go:embed journal_migrations/*.sql
var journalFiles embed.FS

const cursorThrough = "events_through"

// Journal is the worker's journal on a SQLite file, or on an in-memory database
// (D6). It shares no code path with the ledger's statements, because it has one
// writer, no workspace, and no event log.
type Journal struct {
	db *sql.DB
}

// OpenJournal opens a journal file.
func OpenJournal(file string) (*Journal, error) {
	db, err := OpenDB(file)
	if err != nil {
		return nil, err
	}
	return &Journal{db: db}, nil
}

// OpenMemoryJournal opens a journal on a new in-memory database.
func OpenMemoryJournal() (*Journal, error) {
	db, err := OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	return &Journal{db: db}, nil
}

// Close closes the database.
func (j *Journal) Close() error { return j.db.Close() }

// Migrate applies each schema file once. A journal is local and short-lived enough
// that its one version table is its own.
func (j *Journal) Migrate(ctx context.Context) error {
	if _, err := j.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS journal_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return err
	}
	names, err := fs.Glob(journalFiles, "journal_migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, name := range names {
		if err := j.applyMigration(ctx, name); err != nil {
			return fmt.Errorf("journal migration %s: %w", name, err)
		}
	}
	return nil
}

func (j *Journal) applyMigration(ctx context.Context, name string) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var applied int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM journal_migrations WHERE name = ?1`, name).Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	text, err := journalFiles.ReadFile(name)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(text)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO journal_migrations (name) VALUES (?1)`, name); err != nil {
		return err
	}
	return tx.Commit()
}

// Identity returns the saved identity.
func (j *Journal) Identity(ctx context.Context) (ledger.WorkerIdentity, error) {
	var identity ledger.WorkerIdentity
	err := j.db.QueryRowContext(ctx, `SELECT worker_id, credential, epoch FROM identity WHERE id = 1`).
		Scan(&identity.WorkerID, &identity.Credential, &identity.Epoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.WorkerIdentity{}, fmt.Errorf("identity: %w", ports.ErrNotFound)
	}
	return identity, err
}

// SaveIdentity replaces the identity.
func (j *Journal) SaveIdentity(ctx context.Context, identity ledger.WorkerIdentity) error {
	_, err := j.db.ExecContext(ctx, `INSERT INTO identity (id, worker_id, credential, epoch) VALUES (1, ?1, ?2, ?3)
ON CONFLICT (id) DO UPDATE SET worker_id = ?1, credential = ?2, epoch = ?3`,
		identity.WorkerID, identity.Credential, identity.Epoch)
	return err
}

// Append stores one event. The primary key assigns the sequence number, and a
// rolled-back insert leaves no gap, because the counter rolls back with it.
func (j *Journal) Append(ctx context.Context, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	event, err := insertEvent(ctx, j.db, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	crashpoint.Hit("journal.append", "appended")
	return event, nil
}

// execer is a database or a transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertEvent(ctx context.Context, db execer, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	result, err := db.ExecContext(ctx, `INSERT INTO events
  (epoch, type, work_order, claim_id, fence, lane, generation, payload, occurred_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`,
		event.Epoch, event.Type, event.WorkOrder, event.ClaimID, event.Fence, event.Lane, event.Generation, event.Payload, event.OccurredAt)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	event.Seq, err = result.LastInsertId()
	return event, err
}

// Through returns the high-water mark.
func (j *Journal) Through(ctx context.Context) (int64, error) {
	var through int64
	err := j.db.QueryRowContext(ctx, `SELECT value FROM cursors WHERE name = ?1`, cursorThrough).Scan(&through)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return through, err
}

// Pending returns what the ledger has not answered.
func (j *Journal) Pending(ctx context.Context, limit int) ([]ledger.UploadEvent, error) {
	through, err := j.Through(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := j.db.QueryContext(ctx, `SELECT seq, epoch, type, work_order, claim_id, fence, lane, generation, payload, occurred_at
  FROM events WHERE seq > ?1 ORDER BY seq LIMIT ?2`, through, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.UploadEvent
	for rows.Next() {
		var e ledger.UploadEvent
		if err := rows.Scan(&e.Seq, &e.Epoch, &e.Type, &e.WorkOrder, &e.ClaimID, &e.Fence, &e.Lane, &e.Generation, &e.Payload, &e.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Acknowledge moves the mark and keeps each rejection in one transaction, so a
// crash cannot lose a rejection and still advance past it. The mark never moves
// back, because an answer can arrive twice.
func (j *Journal) Acknowledge(ctx context.Context, through int64, rejected []ports.JournalResult) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range rejected {
		_, err := tx.ExecContext(ctx, `INSERT INTO rejections (seq, status, current_fence, state, detail) VALUES (?1, ?2, ?3, ?4, ?5)
ON CONFLICT (seq) DO NOTHING`, r.Seq, r.Status, r.CurrentFence, r.State, r.Detail)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO cursors (name, value) VALUES (?1, ?2)
ON CONFLICT (name) DO UPDATE SET value = max(value, ?2)`, cursorThrough, through)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Rejected lists what the ledger refused.
func (j *Journal) Rejected(ctx context.Context) ([]ports.JournalResult, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT seq, status, current_fence, state, detail FROM rejections ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ports.JournalResult
	for rows.Next() {
		var r ports.JournalResult
		if err := rows.Scan(&r.Seq, &r.Status, &r.CurrentFence, &r.State, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Leases lists what this worker holds.
func (j *Journal) Leases(ctx context.Context) ([]ledger.HeldLease, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT work_order, claim, fence FROM leases ORDER BY work_order`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ledger.HeldLease
	for rows.Next() {
		var lease ledger.HeldLease
		if err := rows.Scan(&lease.WorkOrder, &lease.Claim, &lease.Fence); err != nil {
			return nil, err
		}
		out = append(out, lease)
	}
	return out, rows.Err()
}

// SaveLeases replaces what this worker holds.
func (j *Journal) SaveLeases(ctx context.Context, leases []ledger.HeldLease) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM leases`); err != nil {
		return err
	}
	for _, lease := range leases {
		_, err := tx.ExecContext(ctx, `INSERT INTO leases (work_order, claim, fence) VALUES (?1, ?2, ?3)`,
			lease.WorkOrder, lease.Claim, lease.Fence)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Desired returns the cached desired state.
func (j *Journal) Desired(ctx context.Context) (ledger.DesiredState, error) {
	var body string
	err := j.db.QueryRowContext(ctx, `SELECT body FROM desired WHERE id = 1`).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.DesiredState{}, fmt.Errorf("desired state: %w", ports.ErrNotFound)
	}
	if err != nil {
		return ledger.DesiredState{}, err
	}
	var state ledger.DesiredState
	err = json.Unmarshal([]byte(body), &state)
	return state, err
}

// SaveDesired replaces the cached desired state.
func (j *Journal) SaveDesired(ctx context.Context, state ledger.DesiredState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `INSERT INTO desired (id, body) VALUES (1, ?1)
ON CONFLICT (id) DO UPDATE SET body = ?1`, string(body))
	return err
}

const laneColumns = `id, work_order, claim, fence, generation, state, token_digest, worktree, branch, report, session_id, progress, actual, resume_session, resets_seen, guard, handoff, parking, blocking, run_mode, compact_from, process_generation, respawns_seen, owner`

func scanLane(row interface{ Scan(...any) error }) (lanes.Record, error) {
	var lane lanes.Record
	var digest sql.NullString
	var progress, guard, handoff, parking, blocking []byte
	err := row.Scan(&lane.ID, &lane.WorkOrder, &lane.ClaimID, &lane.Fence, &lane.Generation, &lane.State,
		&digest, &lane.Worktree, &lane.Branch, &lane.Report, &lane.SessionID, &progress, &lane.Actual,
		&lane.ResumeSession, &lane.ResetsSeen, &guard, &handoff, &parking, &blocking, &lane.RunMode, &lane.CompactFrom, &lane.Process, &lane.RespawnsSeen, &lane.Owner)
	if err != nil {
		return lane, err
	}
	lane.TokenDigest = digest.String
	if len(progress) != 0 {
		if err := json.Unmarshal(progress, &lane.Progress); err != nil {
			return lane, err
		}
	}
	if len(guard) != 0 {
		if err := json.Unmarshal(guard, &lane.Guard); err != nil {
			return lane, err
		}
	}
	if len(handoff) != 0 {
		lane.Handoff = &lanes.HandoffProgress{}
		if err := json.Unmarshal(handoff, lane.Handoff); err != nil {
			return lane, err
		}
	}
	if len(parking) != 0 {
		lane.Parking = &lanes.ParkProgress{}
		if err := json.Unmarshal(parking, lane.Parking); err != nil {
			return lane, err
		}
	}
	if len(blocking) != 0 {
		err = json.Unmarshal(blocking, &lane.Blocking)
	}
	return lane, err
}

func nullable(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

// SaveLane creates or replaces a lane. It leaves a report alone, because a report
// arrives on another goroutine.
func (j *Journal) SaveLane(ctx context.Context, lane lanes.Record) error {
	return saveLane(ctx, j.db, lane)
}

func saveLane(ctx context.Context, db execer, lane lanes.Record) error {
	_, err := db.ExecContext(ctx, `INSERT INTO lanes
  (id, work_order, claim, fence, generation, state, token_digest, worktree, branch, report, run_mode, process_generation, owner)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13)
ON CONFLICT (id) DO UPDATE SET work_order = ?2, claim = ?3, fence = ?4, generation = ?5, state = ?6,
  token_digest = ?7, worktree = ?8, branch = ?9, report = coalesce(lanes.report, ?10),
  run_mode = coalesce(nullif(?11, ''), lanes.run_mode),
  process_generation = CASE WHEN ?12 = 0 THEN lanes.process_generation ELSE ?12 END,
  owner = coalesce(nullif(?13, ''), lanes.owner)`,
		lane.ID, lane.WorkOrder, lane.ClaimID, lane.Fence, lane.Generation, lane.State,
		nullable(lane.TokenDigest), lane.Worktree, lane.Branch, lane.Report, lane.RunMode, lane.Process, lane.Owner)
	return err
}

// ClearGeneration moves a lane to the generation a clear began, and stores what the ledger must
// hear, together. Its token stays, because its process did. Generation is the compare-and-set
// that keeps a repeated hook from a second generation.
func (j *Journal) ClearGeneration(ctx context.Context, lane lanes.Record, started, bound ledger.UploadEvent) (bool, error) {
	progress, err := json.Marshal(lane.Progress)
	if err != nil {
		return false, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE lanes SET generation = ?2, session_id = ?3, progress = ?4
 WHERE id = ?1 AND generation = ?5 AND session_id <> ?3`, lane.ID, lane.Generation, lane.SessionID, progress, lane.Generation-1)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return false, err
	}
	if _, err := insertEvent(ctx, tx, started); err != nil {
		return false, err
	}
	if _, err := insertEvent(ctx, tx, bound); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// BeginGeneration moves a lane to its next generation and stores that event together.
func (j *Journal) BeginGeneration(ctx context.Context, lane lanes.Record, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	progress, err := json.Marshal(lane.Progress)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	handoff, err := handoffColumn(lane.Handoff)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `UPDATE lanes
   SET generation = ?2, token_digest = NULL, session_id = '', resume_session = ?3, progress = ?4, handoff = ?5,
       process_generation = 0, respawns_seen = ?6 WHERE id = ?1`,
		lane.ID, lane.Generation, lane.ResumeSession, progress, handoff, lane.RespawnsSeen)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

func handoffColumn(progress *lanes.HandoffProgress) ([]byte, error) {
	if progress == nil {
		return nil, nil
	}
	return json.Marshal(progress)
}

// SaveHandoff replaces how far a lane's handoff got.
func (j *Journal) SaveHandoff(ctx context.Context, lane string, progress *lanes.HandoffProgress) error {
	raw, err := handoffColumn(progress)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `UPDATE lanes SET handoff = ?2 WHERE id = ?1`, lane, raw)
	return err
}

// StoreHandoff keeps a sealed handoff and its event together.
func (j *Journal) StoreHandoff(ctx context.Context, lane string, progress lanes.HandoffProgress, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	raw, err := handoffColumn(&progress)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE lanes SET handoff = ?2 WHERE id = ?1`, lane, raw); err != nil {
		return ledger.UploadEvent{}, err
	}
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

// BeginParking keeps a lane's wake, its handoff's start, and the event that carries the wake to
// the ledger, together.
func (j *Journal) BeginParking(ctx context.Context, lane string, parking lanes.ParkProgress, handoff lanes.HandoffProgress, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	progress, err := json.Marshal(parking)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	document, err := handoffColumn(&handoff)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE lanes SET parking = ?2, handoff = ?3 WHERE id = ?1`, lane, progress, document); err != nil {
		return ledger.UploadEvent{}, err
	}
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

// CommitParked marks a lane parked, ends its token, and stores the commit's event, together. The
// parking record keeps the event's position, so a refusal can be matched to it.
func (j *Journal) CommitParked(ctx context.Context, lane string, parking lanes.ParkProgress, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	parking.Seq = stored.Seq
	progress, err := json.Marshal(parking)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE lanes SET state = ?2, token_digest = NULL, parking = ?3 WHERE id = ?1`, lane, lanes.StateParked, progress)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

// BeginResume brings a parked lane back under a new claim as its next generation, and stores
// that event, together. A worker that never ran the lane has no row, so this makes one. The
// state moves in the same write, because a process that dies here leaves a run that starts from
// this row, and a lane that reads `parked` has no way forward from its own start.
func (j *Journal) BeginResume(ctx context.Context, lane lanes.Record, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	progress, err := json.Marshal(lane.Progress)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	handoff, err := handoffColumn(lane.Handoff)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	parking, err := json.Marshal(lane.Parking)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO lanes
  (id, work_order, claim, fence, generation, state, worktree, branch, resume_session, progress, handoff, parking, actual)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13)
ON CONFLICT (id) DO UPDATE SET work_order = ?2, claim = ?3, fence = ?4, generation = ?5, state = ?6, token_digest = NULL,
  session_id = '', resume_session = ?9, progress = ?10, handoff = ?11, parking = ?12, blocking = NULL, actual = ?13,
  process_generation = 0`,
		lane.ID, lane.WorkOrder, lane.ClaimID, lane.Fence, lane.Generation, lane.State, lane.Worktree, lane.Branch,
		lane.ResumeSession, progress, handoff, parking, lane.Actual)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

// SaveBlocking replaces the questions a lane cannot go on without.
func (j *Journal) SaveBlocking(ctx context.Context, lane string, questions []string) error {
	raw, err := json.Marshal(questions)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `UPDATE lanes SET blocking = ?2 WHERE id = ?1`, lane, raw)
	return err
}

// SaveGuard keeps how the last stop left a lane's worktree.
func (j *Journal) SaveGuard(ctx context.Context, lane string, result lanes.GuardResult) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `UPDATE lanes SET guard = ?2 WHERE id = ?1`, lane, raw)
	return err
}

// MarkArchived records a removal and its event together.
func (j *Journal) MarkArchived(ctx context.Context, lane string, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE lanes SET state = ?2 WHERE id = ?1`, lane, lanes.StateArchived); err != nil {
		return ledger.UploadEvent{}, err
	}
	stored, err := insertEvent(ctx, tx, event)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	return stored, tx.Commit()
}

// Allocate decides inside one transaction, so two lanes cannot read the same free block,
// and the unique index refuses them if a broken caller tries.
func (j *Journal) Allocate(ctx context.Context, request lanes.AllocationRequest) (lanes.Allocation, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return lanes.Allocation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	held, err := readAllocations(ctx, tx)
	if err != nil {
		return lanes.Allocation{}, err
	}
	for _, allocation := range held {
		if allocation.Lane == request.Lane {
			return allocation, nil
		}
	}
	base, fits := lanes.NextPortBase(held, request.Pool, request.Ports)
	if !fits {
		return lanes.Allocation{}, fmt.Errorf("%w: %d ports for lane %s", ports.ErrNoPorts, request.Ports, request.Lane)
	}
	allocation := lanes.Allocation{Lane: request.Lane, PortBase: base, Ports: request.Ports, ComposeProject: request.ComposeProject}
	_, err = tx.ExecContext(ctx, `INSERT INTO lane_allocations (lane, port_base, ports, compose_project) VALUES (?1, ?2, ?3, ?4)`,
		allocation.Lane, allocation.PortBase, allocation.Ports, allocation.ComposeProject)
	if err != nil {
		return lanes.Allocation{}, err
	}
	return allocation, tx.Commit()
}

// querier is a database or a transaction.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func readAllocations(ctx context.Context, db querier) ([]lanes.Allocation, error) {
	rows, err := db.QueryContext(ctx, `SELECT lane, port_base, ports, compose_project FROM lane_allocations ORDER BY port_base, lane`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.Allocation
	for rows.Next() {
		var allocation lanes.Allocation
		if err := rows.Scan(&allocation.Lane, &allocation.PortBase, &allocation.Ports, &allocation.ComposeProject); err != nil {
			return nil, err
		}
		out = append(out, allocation)
	}
	return out, rows.Err()
}

// Allocations lists what every lane holds.
func (j *Journal) Allocations(ctx context.Context) ([]lanes.Allocation, error) {
	return readAllocations(ctx, j.db)
}

// Release gives back what a lane holds.
func (j *Journal) Release(ctx context.Context, lane string) error {
	_, err := j.db.ExecContext(ctx, `DELETE FROM lane_allocations WHERE lane = ?1`, lane)
	return err
}

// ChargeRestart keeps one charge once.
func (j *Journal) ChargeRestart(ctx context.Context, lane string, charge lanes.Restart) (bool, error) {
	planned, reset := 0, 0
	if charge.Planned {
		planned = 1
	}
	if charge.Reset {
		reset = 1
	}
	result, err := j.db.ExecContext(ctx, `INSERT INTO lane_restarts (lane, key, at_ms, not_before_ms, planned, reset)
VALUES (?1, ?2, ?3, ?4, ?5, ?6) ON CONFLICT (lane, key) DO NOTHING`, lane, charge.Key, charge.AtMs, charge.NotBeforeMs, planned, reset)
	if err != nil {
		return false, err
	}
	first, err := result.RowsAffected()
	return first != 0, err
}

// Restarts lists a lane's charges.
func (j *Journal) Restarts(ctx context.Context, lane string) ([]lanes.Restart, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT key, at_ms, not_before_ms, planned, reset FROM lane_restarts WHERE lane = ?1 ORDER BY at_ms, key`, lane)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.Restart
	for rows.Next() {
		var charge lanes.Restart
		var planned, reset int64
		if err := rows.Scan(&charge.Key, &charge.AtMs, &charge.NotBeforeMs, &planned, &reset); err != nil {
			return nil, err
		}
		charge.Planned, charge.Reset = planned != 0, reset != 0
		out = append(out, charge)
	}
	return out, rows.Err()
}

// ClearRestarts takes a lane's charges away for a reset, and keeps the reset's number.
func (j *Journal) ClearRestarts(ctx context.Context, lane string, resets int64) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM lane_restarts WHERE lane = ?1`, lane); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE lanes SET resets_seen = ?2 WHERE id = ?1`, lane, resets); err != nil {
		return err
	}
	return tx.Commit()
}

// BeginLane stores a lane and its first event together.
func (j *Journal) BeginLane(ctx context.Context, lane lanes.Record, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := saveLane(ctx, tx, lane); err != nil {
		return ledger.UploadEvent{}, err
	}
	if event, err = insertEvent(ctx, tx, event); err != nil {
		return ledger.UploadEvent{}, err
	}
	return event, tx.Commit()
}

// EndLane ends a lane and stores its last event together.
func (j *Journal) EndLane(ctx context.Context, id string, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `UPDATE lanes SET state = ?1, token_digest = NULL WHERE id = ?2`, lanes.StateEnded, id)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM open_approvals WHERE lane = ?1`, id)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	if event, err = insertEvent(ctx, tx, event); err != nil {
		return ledger.UploadEvent{}, err
	}
	return event, tx.Commit()
}

func (j *Journal) laneWhere(ctx context.Context, condition, value string) (lanes.Record, error) {
	row := j.db.QueryRowContext(ctx, `SELECT `+laneColumns+` FROM lanes WHERE `+condition+` = ?1`, value)
	lane, err := scanLane(row)
	if errors.Is(err, sql.ErrNoRows) {
		return lanes.Record{}, fmt.Errorf("lane: %w", ports.ErrNotFound)
	}
	return lane, err
}

// Lane returns one lane.
func (j *Journal) Lane(ctx context.Context, id string) (lanes.Record, error) {
	return j.laneWhere(ctx, "id", id)
}

// LaneByToken returns the lane of a token digest.
func (j *Journal) LaneByToken(ctx context.Context, digest string) (lanes.Record, error) {
	return j.laneWhere(ctx, "token_digest", digest)
}

// Lanes lists every lane.
func (j *Journal) Lanes(ctx context.Context) ([]lanes.Record, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT `+laneColumns+` FROM lanes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.Record
	for rows.Next() {
		lane, err := scanLane(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lane)
	}
	return out, rows.Err()
}

// SaveReport keeps the first report.
func (j *Journal) SaveReport(ctx context.Context, lane string, report []byte) (bool, error) {
	result, err := j.db.ExecContext(ctx, `UPDATE lanes SET report = ?1 WHERE id = ?2 AND report IS NULL`, report, lane)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// ExpireToken ends a lane's token.
func (j *Journal) ExpireToken(ctx context.Context, lane string) error {
	_, err := j.db.ExecContext(ctx, `UPDATE lanes SET token_digest = NULL WHERE id = ?1`, lane)
	return err
}

// MergeHook stores one event, or adds a repeat to its first. Order does not matter,
// because hook payloads have no sequence and the bounds only widen.
func (j *Journal) MergeHook(ctx context.Context, event lanes.HookEvent) error {
	malformed := 0
	if event.Malformed {
		malformed = 1
	}
	_, err := j.db.ExecContext(ctx, `INSERT INTO hook_events
  (lane, generation, event, subject, session_id, tool, detail, malformed, socket, model, first_ms, last_ms, count)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?10, ?11, ?9, ?9, 1)
ON CONFLICT (lane, generation, event, subject) DO UPDATE SET
  first_ms = min(first_ms, ?9), last_ms = max(last_ms, ?9), count = count + 1,
  socket = CASE WHEN ?10 = '' THEN socket ELSE ?10 END, model = CASE WHEN ?11 = '' THEN model ELSE ?11 END`,
		event.Lane, event.Generation, event.Event, event.Subject, event.SessionID, event.Tool, event.Detail, malformed, event.AtMs,
		event.Socket, event.Model)
	return err
}

// HookEvents lists one lane's events.
func (j *Journal) HookEvents(ctx context.Context, lane string) ([]lanes.HookRecord, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT generation, event, subject, session_id, tool, detail, malformed, socket, model, first_ms, last_ms, count
  FROM hook_events WHERE lane = ?1 ORDER BY generation, first_ms, event, subject`, lane)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.HookRecord
	for rows.Next() {
		var r lanes.HookRecord
		var malformed int64
		err := rows.Scan(&r.Generation, &r.Event, &r.Subject, &r.SessionID, &r.Tool, &r.Detail, &malformed, &r.Socket, &r.Model, &r.FirstMs, &r.LastMs, &r.Count)
		if err != nil {
			return nil, err
		}
		r.Lane, r.Malformed = lane, malformed != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

const effectColumns = `key, kind, lane, generation, target, state, attempts, detail`

func scanEffect(row interface{ Scan(...any) error }) (lanes.Effect, error) {
	var effect lanes.Effect
	err := row.Scan(&effect.Key, &effect.Kind, &effect.Lane, &effect.Generation, &effect.Target,
		&effect.State, &effect.Attempts, &effect.Detail)
	return effect, err
}

// BeginEffect records an effect before it happens. The primary key is what makes a
// second attempt at one effect impossible to miss.
func (j *Journal) BeginEffect(ctx context.Context, effect lanes.Effect) (bool, error) {
	result, err := j.db.ExecContext(ctx, `INSERT INTO effects (key, kind, lane, generation, target, state)
VALUES (?1, ?2, ?3, ?4, ?5, ?6) ON CONFLICT (key) DO NOTHING`,
		effect.Key, effect.Kind, effect.Lane, effect.Generation, effect.Target, lanes.EffectStarted)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	return inserted > 0, err
}

// Effect returns one effect.
func (j *Journal) Effect(ctx context.Context, key string) (lanes.Effect, error) {
	effect, err := scanEffect(j.db.QueryRowContext(ctx, `SELECT `+effectColumns+` FROM effects WHERE key = ?1`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return lanes.Effect{}, fmt.Errorf("effect: %w", ports.ErrNotFound)
	}
	return effect, err
}

// Effects lists every effect.
func (j *Journal) Effects(ctx context.Context) ([]lanes.Effect, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT `+effectColumns+` FROM effects ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.Effect
	for rows.Next() {
		effect, err := scanEffect(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, effect)
	}
	return out, rows.Err()
}

// FinishEffect settles an effect that has no end yet.
func (j *Journal) FinishEffect(ctx context.Context, key string, state lanes.EffectState, detail string) error {
	_, err := j.db.ExecContext(ctx, `UPDATE effects SET state = ?1, detail = ?2 WHERE key = ?3 AND state IN (?4, ?5)`,
		state, detail, key, lanes.EffectStarted, lanes.EffectUnknown)
	return err
}

// RetryEffect moves an unknown effect back to started.
func (j *Journal) RetryEffect(ctx context.Context, key string) (bool, error) {
	result, err := j.db.ExecContext(ctx, `UPDATE effects SET state = ?1, attempts = attempts + 1 WHERE key = ?2 AND state = ?3`,
		lanes.EffectStarted, key, lanes.EffectUnknown)
	if err != nil {
		return false, err
	}
	moved, err := result.RowsAffected()
	return moved > 0, err
}

// MarkUnknown turns what a crash left started into unknown.
func (j *Journal) MarkUnknown(ctx context.Context) (int, error) {
	result, err := j.db.ExecContext(ctx, `UPDATE effects SET state = ?1 WHERE state = ?2`, lanes.EffectUnknown, lanes.EffectStarted)
	if err != nil {
		return 0, err
	}
	marked, err := result.RowsAffected()
	return int(marked), err
}

// BindGeneration records a session and its event once.
func (j *Journal) BindGeneration(ctx context.Context, lane, sessionID string, event ledger.UploadEvent) (bool, error) {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE lanes SET session_id = ?1 WHERE id = ?2 AND session_id = ''`, sessionID, lane)
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

// SaveActual replaces where a lane's state stands, and the state a compaction under way left.
func (j *Journal) SaveActual(ctx context.Context, lane string, state, from lanes.ActualState) error {
	_, err := j.db.ExecContext(ctx, `UPDATE lanes SET actual = ?1, compact_from = ?3 WHERE id = ?2`, state, lane, from)
	return err
}

// SaveProgress replaces how far a lane's session was read.
func (j *Journal) SaveProgress(ctx context.Context, lane string, progress lanes.Progress) error {
	raw, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `UPDATE lanes SET progress = ?1 WHERE id = ?2`, raw, lane)
	return err
}

var _ ports.Journal = (*Journal)(nil)
