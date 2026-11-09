package sqlstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Store is the Store port over one database.
type Store struct {
	db       *sql.DB
	dialect  Dialect
	trace    func(TraceEntry)
	compiled sync.Map
	nextTx   atomic.Uint64
}

// New returns a Store on an open database. It owns db from here on.
func New(db *sql.DB, dialect Dialect, opts Options) *Store {
	return &Store{db: db, dialect: dialect, trace: opts.Trace}
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// runner is what a statement runs on.
type runner interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// session is one transaction, or one bare connection during a migration.
type session struct {
	store *Store
	ctx   context.Context
	on    runner
	id    uint64
	mode  Mode
	op    string
	// changed records that a statement touched a row, so a commit can refuse a
	// change with no event.
	changed bool
}

func (s *Store) session(ctx context.Context, on runner, mode Mode, op string) *session {
	return &session{store: s, ctx: ctx, on: on, id: s.nextTx.Add(1), mode: mode, op: op}
}

func (se *session) note(name string) {
	if se.store.trace != nil {
		se.store.trace(TraceEntry{Tx: se.id, Mode: se.mode, Op: se.op, Statement: name})
	}
}

func (s *Store) prepared(text string) compiled {
	if c, ok := s.compiled.Load(text); ok {
		return c.(compiled)
	}
	c := compile(expandMatch(text, s.dialect), s.dialect.Placeholder)
	s.compiled.Store(text, c)
	return c
}

func (se *session) bound(st *statement, p params) (string, []any, error) {
	c := se.store.prepared(rewritten(st.name, st.text))
	args, err := c.bind(p)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", st.name, err)
	}
	se.note(st.name)
	return c.text, args, nil
}

// exec runs a statement and reports how many rows it touched.
func (se *session) exec(st *statement, p params) (int64, error) {
	text, args, err := se.bound(st, p)
	if err != nil {
		return 0, err
	}
	result, err := se.on.ExecContext(se.ctx, text, args...)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.name, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", st.name, err)
	}
	se.changed = se.changed || n > 0
	return n, nil
}

// row runs a statement that returns one row.
func (se *session) row(st *statement, p params) (*sql.Row, error) {
	text, args, err := se.bound(st, p)
	if err != nil {
		return nil, err
	}
	return se.on.QueryRowContext(se.ctx, text, args...), nil
}

// rows runs a statement that returns many rows.
func (se *session) rows(st *statement, p params) (*sql.Rows, error) {
	text, args, err := se.bound(st, p)
	if err != nil {
		return nil, err
	}
	rows, err := se.on.QueryContext(se.ctx, text, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", st.name, err)
	}
	return rows, nil
}

// readClock reads database time. The dialect supplies the text, and a trace sees
// it as one statement.
func (se *session) readClock() (int64, error) {
	se.note(StatementReadClock)
	var now int64
	if err := se.on.QueryRowContext(se.ctx, se.store.dialect.ClockSQL()).Scan(&now); err != nil {
		return 0, fmt.Errorf("%s: %w", StatementReadClock, err)
	}
	return now, nil
}

// Migrate applies each schema change once, in its own transaction.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, createMigrations); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	migrations, err := s.dialect.Migrations()
	if err != nil {
		return err
	}
	slices.SortFunc(migrations, func(a, b Migration) int { return int(a.Version - b.Version) })
	for _, m := range migrations {
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migrate %s: %w", m.Name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m Migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	se := s.session(ctx, tx, ModeMigrate, m.Name)
	row, err := se.row(stmtMigrationApplied, params{"version": m.Version})
	if err != nil {
		return err
	}
	var applied int64
	if err := row.Scan(&applied); err != nil {
		return err
	}
	if applied > 0 {
		return nil
	}
	for _, text := range splitStatements(m.SQL) {
		if _, err := tx.ExecContext(ctx, text); err != nil {
			return fmt.Errorf("%w: %s", err, text)
		}
	}
	now, err := se.readClock()
	if err != nil {
		return err
	}
	if _, err := se.exec(stmtRecordMigration, params{"version": m.Version, "name": m.Name, "applied_at": now}); err != nil {
		return err
	}
	return tx.Commit()
}

// splitStatements cuts a migration file at each statement end. The one statement that
// holds a semicolon is a trigger, whose body runs to its END.
func splitStatements(text string) []string {
	var kept []string
	for line := range strings.SplitSeq(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			kept = append(kept, line)
		}
	}
	var out []string
	open := ""
	for part := range strings.SplitSeq(strings.Join(kept, "\n"), ";") {
		statement := strings.TrimSpace(open + part)
		if strings.HasPrefix(statement, "CREATE TRIGGER") && !strings.HasSuffix(statement, "END") {
			open = statement + ";"
			continue
		}
		open = ""
		if statement != "" {
			out = append(out, statement)
		}
	}
	return out
}

// CreateWorkspace records a workspace in one transaction whose first statement
// creates the row or increments its revision. A workspace that already exists
// rolls back, so a retry leaves no gap.
func (s *Store) CreateWorkspace(ctx context.Context, w ledger.Workspace, actor ledger.Actor) (bool, error) {
	if err := w.ID.Validate(); err != nil {
		return false, err
	}
	const op = "workspace.create"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	t := &writeTx{session: s.session(ctx, tx, ModeWrite, op), scope: w.ID}
	mode := w.FleetMode
	if mode == "" {
		mode = ledger.FleetRun
	}
	row, err := t.row(stmtCreateWorkspace, params{
		"workspace_id": string(w.ID), "name": w.Name, "fleet_mode": string(mode), "wip_limit": w.WIPLimit,
		"repository_limit": w.RepositoryLimit, "worker_limit": w.WorkerLimit,
	})
	if err != nil {
		return false, err
	}
	if err := row.Scan(&t.rev); err != nil {
		return false, fmt.Errorf("%s: %w", op, err)
	}
	if t.rev != 1 {
		return false, nil
	}
	t.revHeld = true
	if t.now, err = t.readClock(); err != nil {
		return false, err
	}
	_, err = t.Append(ledger.EventDraft{
		Type:    "workspace.created",
		Actor:   actor,
		Subject: ledger.Subject{Kind: "workspace", ID: string(w.ID)},
	})
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Read runs fn on one read transaction, so every read in fn sees one view.
func (s *Store) Read(ctx context.Context, scope ledger.WorkspaceID, fn func(ports.ReadTx) error) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := &readTx{session: s.session(ctx, tx, ModeRead, "read"), scope: scope}
	if _, err := t.Workspace(); err != nil {
		return err
	}
	return fn(t)
}

// Write runs fn in one write transaction.
func (s *Store) Write(ctx context.Context, scope ledger.WorkspaceID, op string, fn func(ports.WriteTx) error) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	crashpoint.Hit(op, "begun")
	t := &writeTx{session: s.session(ctx, tx, ModeWrite, op), scope: scope}
	if err := t.open(); err != nil {
		return err
	}
	crashpoint.Hit(op, "opened")
	if err := fn(t); err != nil {
		return err
	}
	if t.events == 0 && !commitsWithoutEvents() {
		if t.changed {
			return fmt.Errorf("%s: %w", op, ports.ErrNoEvent)
		}
		return nil
	}
	crashpoint.Hit(op, "before-commit")
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", op, err)
	}
	crashpoint.Hit(op, "committed")
	return nil
}

// readTx is one consistent view.
type readTx struct {
	*session
	scope ledger.WorkspaceID
}

func (t *readTx) Workspace() (ledger.Workspace, error) {
	return readWorkspace(t.session, t.scope)
}

func (t *readTx) Events(after int64, limit int) ([]ledger.Event, error) {
	return listEvents(t.session, t.scope, after, limit)
}

func (t *readTx) EventsAbout(subject ledger.Subject) ([]ledger.Event, error) {
	rows, err := t.rows(stmtEventsAbout, params{"workspace_id": string(t.scope), "subject_kind": subject.Kind, "subject_id": subject.ID})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []ledger.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (t *readTx) Now() (int64, error) { return t.readClock() }

func (t *readTx) WorkOrders() ports.WorkOrderReader {
	return workOrderTx{session: t.session, scope: t.scope}
}

func (t *readTx) Claims() ports.ClaimReader {
	return claimTx{session: t.session, scope: t.scope}
}

func (t *readTx) Lanes() ports.LaneReader {
	return laneTx{session: t.session, scope: t.scope}
}

func (t *readTx) Planning() ports.PlanningReader {
	return planningTx{session: t.session, scope: t.scope}
}

func (t *readTx) Workers() ports.WorkerReader {
	return workerTx{session: t.session, scope: t.scope}
}

func (t *readTx) Wakes() ports.WakeReader {
	return wakeTx{session: t.session, scope: t.scope}
}

// writeTx is one write transaction.
type writeTx struct {
	*session
	scope   ledger.WorkspaceID
	now     int64
	rev     int64
	revHeld bool
	events  int
}

// openRevisionFirst takes the revision and then reads the clock. On Postgres at
// read committed the row lock this takes is what serializes every check in the
// transaction with the writes after it.
func (t *writeTx) openRevisionFirst() error {
	if err := t.takeRevision(); err != nil {
		return err
	}
	var err error
	t.now, err = t.readClock()
	return err
}

func (t *writeTx) takeRevision() error {
	row, err := t.row(stmtTakeRevision, params{"workspace_id": string(t.scope)})
	if err != nil {
		return err
	}
	switch err := row.Scan(&t.rev); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: %s", ledger.ErrUnknownWorkspace, t.scope)
	case err != nil:
		return fmt.Errorf("%s: %w", StatementTakeRevision, err)
	}
	t.revHeld = true
	return nil
}

func (t *writeTx) Now() int64 { return t.now }

func (t *writeTx) Workspace() (ledger.Workspace, error) {
	return readWorkspace(t.session, t.scope)
}

// SaveFleet replaces a workspace's fleet mode and caps.
func (t *writeTx) SaveFleet(mode ledger.FleetMode, caps ledger.Caps) error {
	_, err := t.exec(stmtSaveFleet, params{
		"workspace_id": string(t.scope), "fleet_mode": string(mode), "wip_limit": caps.WIPLimit,
		"repository_limit": caps.RepositoryLimit, "worker_limit": caps.WorkerLimit,
	})
	return err
}

func (t *writeTx) Events(after int64, limit int) ([]ledger.Event, error) {
	return listEvents(t.session, t.scope, after, limit)
}

func (t *writeTx) Event(id string) (ledger.Event, error) {
	row, err := t.row(stmtFindEvent, params{"workspace_id": string(t.scope), "id": id})
	if err != nil {
		return ledger.Event{}, err
	}
	e, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Event{}, fmt.Errorf("event %s: %w", id, ports.ErrNotFound)
	}
	return e, err
}

func (t *writeTx) WorkOrders() ports.WorkOrderWriter {
	return workOrderTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Leases() ports.LeaseWriter {
	return leaseTx{session: t.session, scope: t.scope, now: t.now}
}

func (t *writeTx) Claims() ports.ClaimWriter {
	return claimTx{session: t.session, scope: t.scope, now: t.now}
}

func (t *writeTx) Lanes() ports.LaneWriter {
	return laneTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Planning() ports.PlanningWriter {
	return planningTx{session: t.session, scope: t.scope}
}

func (t *writeTx) Workers() ports.WorkerWriter {
	return workerTx{session: t.session, scope: t.scope, now: t.now}
}

func (t *writeTx) Wakes() ports.WakeWriter {
	return wakeTx{session: t.session, scope: t.scope, now: t.now}
}

// Append adds one event. The transaction's first event uses the revision taken at
// the start, and each later one increments it again.
func (t *writeTx) Append(draft ledger.EventDraft) (ledger.Event, error) {
	if err := draft.Validate(); err != nil {
		return ledger.Event{}, err
	}
	if !t.revHeld {
		if err := t.takeRevision(); err != nil {
			return ledger.Event{}, err
		}
	}
	t.revHeld = false
	if draft.ID == "" {
		var err error
		if draft.ID, err = newEventID(t.now); err != nil {
			return ledger.Event{}, err
		}
	}
	data := string(draft.Data)
	if data == "" {
		data = "{}"
	}
	_, err := t.exec(stmtAppendEvent, params{
		"workspace_id": string(t.scope),
		"rev":          t.rev,
		"id":           draft.ID,
		"type":         draft.Type,
		"actor_kind":   string(draft.Actor.Kind),
		"actor_id":     draft.Actor.ID,
		"subject_kind": draft.Subject.Kind,
		"subject_id":   draft.Subject.ID,
		"data":         data,
		"occurred_at":  nullInt(draft.OccurredAt),
		"recorded_at":  t.now,
		"traceparent":  nullText(draft.Traceparent),
	})
	if err != nil {
		return ledger.Event{}, err
	}
	t.events++
	return ledger.Event{
		Rev:         t.rev,
		ID:          draft.ID,
		Type:        draft.Type,
		Actor:       draft.Actor,
		Subject:     draft.Subject,
		Data:        []byte(data),
		OccurredAt:  draft.OccurredAt,
		RecordedAt:  t.now,
		Traceparent: draft.Traceparent,
	}, nil
}

func newEventID(now int64) (string, error) {
	var entropy [ledger.ULIDEntropy]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return ledger.EncodeULID(now, entropy), nil
}

func readWorkspace(se *session, scope ledger.WorkspaceID) (ledger.Workspace, error) {
	row, err := se.row(stmtReadWorkspace, params{"workspace_id": string(scope)})
	if err != nil {
		return ledger.Workspace{}, err
	}
	var w ledger.Workspace
	var id, mode string
	switch err := row.Scan(&id, &w.Name, &mode, &w.Rev, &w.PolicyVersion, &w.WIPLimit, &w.RepositoryLimit, &w.WorkerLimit); {
	case errors.Is(err, sql.ErrNoRows):
		return ledger.Workspace{}, fmt.Errorf("%w: %s", ledger.ErrUnknownWorkspace, scope)
	case err != nil:
		return ledger.Workspace{}, fmt.Errorf("%s: %w", stmtReadWorkspace.name, err)
	}
	w.ID, w.FleetMode = ledger.WorkspaceID(id), ledger.FleetMode(mode)
	return w, nil
}

func listEvents(se *session, scope ledger.WorkspaceID, after int64, limit int) ([]ledger.Event, error) {
	rows, err := se.rows(stmtListEvents, params{"workspace_id": string(scope), "after": after, "limit": int64(limit)})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []ledger.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func scanEvent(row scanner) (ledger.Event, error) {
	var e ledger.Event
	var actorKind, data string
	var occurredAt sql.NullInt64
	var traceparent sql.NullString
	err := row.Scan(&e.Rev, &e.ID, &e.Type, &actorKind, &e.Actor.ID, &e.Subject.Kind, &e.Subject.ID,
		&data, &occurredAt, &e.RecordedAt, &traceparent)
	if err != nil {
		return ledger.Event{}, err
	}
	e.Actor.Kind = ledger.ActorKind(actorKind)
	e.Data = []byte(data)
	e.OccurredAt = occurredAt.Int64
	e.Traceparent = traceparent.String
	return e, nil
}

func nullText(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullInt(n int64) sql.NullInt64 {
	return sql.NullInt64{Int64: n, Valid: n != 0}
}

var _ ports.Store = (*Store)(nil)
