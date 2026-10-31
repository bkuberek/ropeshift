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

var _ ports.Journal = (*Journal)(nil)
