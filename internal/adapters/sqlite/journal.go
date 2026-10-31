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
	result, err := j.db.ExecContext(ctx, `INSERT INTO events
  (epoch, type, work_order, claim_id, fence, lane, generation, payload, occurred_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`,
		event.Epoch, event.Type, event.WorkOrder, event.ClaimID, event.Fence, event.Lane, event.Generation, event.Payload, event.OccurredAt)
	if err != nil {
		return ledger.UploadEvent{}, err
	}
	if event.Seq, err = result.LastInsertId(); err != nil {
		return ledger.UploadEvent{}, err
	}
	crashpoint.Hit("journal.append", "appended")
	return event, nil
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

const laneColumns = `id, work_order, claim, fence, generation, state, token_digest, worktree, branch, report`

func scanLane(row interface{ Scan(...any) error }) (lanes.Record, error) {
	var lane lanes.Record
	var digest sql.NullString
	err := row.Scan(&lane.ID, &lane.WorkOrder, &lane.ClaimID, &lane.Fence, &lane.Generation, &lane.State,
		&digest, &lane.Worktree, &lane.Branch, &lane.Report)
	lane.TokenDigest = digest.String
	return lane, err
}

func nullable(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

// SaveLane creates or replaces a lane. It leaves a report alone, because a report
// arrives on another goroutine.
func (j *Journal) SaveLane(ctx context.Context, lane lanes.Record) error {
	_, err := j.db.ExecContext(ctx, `INSERT INTO lanes (`+laneColumns+`)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)
ON CONFLICT (id) DO UPDATE SET work_order = ?2, claim = ?3, fence = ?4, generation = ?5, state = ?6,
  token_digest = ?7, worktree = ?8, branch = ?9, report = coalesce(lanes.report, ?10)`,
		lane.ID, lane.WorkOrder, lane.ClaimID, lane.Fence, lane.Generation, lane.State,
		nullable(lane.TokenDigest), lane.Worktree, lane.Branch, lane.Report)
	return err
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
  (lane, generation, event, subject, session_id, tool, detail, malformed, first_ms, last_ms, count)
VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?9, 1)
ON CONFLICT (lane, generation, event, subject) DO UPDATE SET
  first_ms = min(first_ms, ?9), last_ms = max(last_ms, ?9), count = count + 1`,
		event.Lane, event.Generation, event.Event, event.Subject, event.SessionID, event.Tool, event.Detail, malformed, event.AtMs)
	return err
}

// HookEvents lists one lane's events.
func (j *Journal) HookEvents(ctx context.Context, lane string) ([]lanes.HookRecord, error) {
	rows, err := j.db.QueryContext(ctx, `SELECT generation, event, subject, session_id, tool, detail, malformed, first_ms, last_ms, count
  FROM hook_events WHERE lane = ?1 ORDER BY generation, first_ms, event, subject`, lane)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanes.HookRecord
	for rows.Next() {
		var r lanes.HookRecord
		var malformed int64
		err := rows.Scan(&r.Generation, &r.Event, &r.Subject, &r.SessionID, &r.Tool, &r.Detail, &malformed, &r.FirstMs, &r.LastMs, &r.Count)
		if err != nil {
			return nil, err
		}
		r.Lane, r.Malformed = lane, malformed != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

var _ ports.Journal = (*Journal)(nil)
