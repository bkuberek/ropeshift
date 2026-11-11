package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Installation is the installation database (ADR-0021): the registry of projects, the installation's
// own event log, the repository index, and the records every project shares. A project's Store reaches
// the shared records through it, and holds none of them in its own database.
type Installation struct {
	core *Store
}

// NewInstallation returns the installation database on an open database. It owns db from here on.
func NewInstallation(db *sql.DB, dialect Dialect, opts Options) *Installation {
	opts.Installation = nil
	return &Installation{core: New(db, dialect, opts)}
}

// Close closes the database.
func (in *Installation) Close() error { return in.core.Close() }

// Migrate brings the installation's schema to this build's version, apart from any project's.
func (in *Installation) Migrate(ctx context.Context) error {
	return in.core.migrate(ctx, in.core.dialect.InstallationMigrations)
}

// The operations of the installation database, as traces and crash steps name them.
const (
	opCreateProject = "project.create"
	opIndex         = "repository_index.rebuild"
)

const projectColumns = `id, name, display_name, state, database, created_at`

const installationEventColumns = `rev, id, type, actor_kind, actor_id, subject_kind, subject_id,
       data, occurred_at, recorded_at, traceparent`

var (
	stmtTakeInstallationRevision = defineInstallation("installation.take_revision",
		`UPDATE installation SET rev = rev + 1 WHERE id = 1 RETURNING rev`)

	stmtAppendInstallationEvent = defineInstallation("installation_event.append",
		`INSERT INTO installation_events
  (rev, id, type, actor_kind, actor_id, subject_kind, subject_id, data, occurred_at, recorded_at, traceparent)
VALUES
  (:rev, :id, :type, :actor_kind, :actor_id, :subject_kind, :subject_id, :data, :occurred_at, :recorded_at, :traceparent)`)

	stmtListInstallationEvents = defineInstallation("installation_event.list",
		`SELECT `+installationEventColumns+` FROM installation_events WHERE rev > :after ORDER BY rev LIMIT :limit`)

	stmtInsertProject = defineInstallation("project.insert",
		`INSERT INTO projects (`+projectColumns+`)
VALUES (:id, :name, :display_name, :state, :database, :created_at)
ON CONFLICT DO NOTHING`)

	stmtReadProject = defineInstallation("project.read",
		`SELECT `+projectColumns+` FROM projects WHERE id = :id`)

	stmtProjectNamed = defineInstallation("project.named",
		`SELECT `+projectColumns+` FROM projects WHERE name = :name`)

	stmtListProjects = defineInstallation("project.list",
		`SELECT `+projectColumns+` FROM projects ORDER BY created_at, id`)

	stmtClearRepositoryIndex = defineInstallation("repository_index.clear",
		`DELETE FROM repository_index`)

	stmtIndexRepository = defineInstallation("repository_index.insert",
		`INSERT INTO repository_index (identity, project_id, repository_id)
VALUES (:identity, :project_id, :repository_id)
ON CONFLICT DO NOTHING`)

	stmtListRepositoryIndex = defineInstallation("repository_index.list",
		`SELECT identity, project_id, repository_id FROM repository_index ORDER BY identity`)
)

// installationTx is one write transaction of the installation database, which holds its revision.
type installationTx struct {
	*session
	rev     int64
	revHeld bool
	now     int64
	events  int
}

// write runs fn in one transaction that takes the installation's revision first and reads the clock
// after it. It commits only when fn appended an event, and refuses a change with none, as a project's
// Write does.
func (in *Installation) write(ctx context.Context, op string, fn func(*installationTx) error) error {
	tx, err := in.core.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := &installationTx{session: in.core.session(ctx, tx, ModeWrite, op)}
	if err := t.takeRevision(); err != nil {
		return err
	}
	if t.now, err = t.readClock(); err != nil {
		return err
	}
	if err := fn(t); err != nil {
		return err
	}
	if t.events == 0 {
		if t.changed {
			return fmt.Errorf("%s: %w", op, ports.ErrNoEvent)
		}
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("%s: commit: %w", op, err)
	}
	return nil
}

func (t *installationTx) takeRevision() error {
	row, err := t.row(stmtTakeInstallationRevision, params{})
	if err != nil {
		return err
	}
	if err := row.Scan(&t.rev); err != nil {
		return fmt.Errorf("%s: %w", stmtTakeInstallationRevision.name, err)
	}
	t.revHeld = true
	return nil
}

// append adds one event to the installation's log, under the revision the transaction holds or the next.
func (t *installationTx) append(draft ledger.EventDraft) error {
	if err := draft.Validate(); err != nil {
		return err
	}
	if !t.revHeld {
		if err := t.takeRevision(); err != nil {
			return err
		}
	}
	t.revHeld = false
	if draft.ID == "" {
		var err error
		if draft.ID, err = newEventID(t.now); err != nil {
			return err
		}
	}
	data := string(draft.Data)
	if data == "" {
		data = "{}"
	}
	_, err := t.exec(stmtAppendInstallationEvent, params{
		"rev": t.rev, "id": draft.ID, "type": draft.Type,
		"actor_kind": string(draft.Actor.Kind), "actor_id": draft.Actor.ID,
		"subject_kind": draft.Subject.Kind, "subject_id": draft.Subject.ID,
		"data": data, "occurred_at": nullInt(draft.OccurredAt), "recorded_at": t.now, "traceparent": nullText(draft.Traceparent),
	})
	if err != nil {
		return err
	}
	t.events++
	return nil
}

// read runs fn on one read transaction of the installation database.
func (in *Installation) read(ctx context.Context, op string, fn func(*session) error) error {
	tx, err := in.core.begin(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	return fn(in.core.session(ctx, tx, ModeRead, op))
}

// projectCreated is the data of EventProjectCreated.
type projectCreated struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Database    string `json:"database"`
}

// CreateProject registers a project whose database exists at the current schema, and reports whether
// it was new. A retry of the same project changes nothing, and a project that takes another's ID, name,
// or database is refused with ErrProjectExists.
func (in *Installation) CreateProject(ctx context.Context, p ledger.Project, actor ledger.Actor) (ledger.Project, bool, error) {
	if p.State == "" {
		p.State = ledger.ProjectActive
	}
	if err := p.Validate(); err != nil {
		return ledger.Project{}, false, err
	}
	made := false
	err := in.write(ctx, opCreateProject, func(t *installationTx) error {
		if p.CreatedAt == 0 {
			p.CreatedAt = t.now
		}
		n, err := t.exec(stmtInsertProject, projectParams(p))
		if err != nil {
			return err
		}
		if n == 0 {
			kept, err := t.sameProject(p)
			if err != nil {
				return err
			}
			p = kept
			return nil
		}
		data, err := json.Marshal(projectCreated{Name: p.Name, DisplayName: p.DisplayName, Database: p.Database})
		if err != nil {
			return err
		}
		made = true
		return t.append(ledger.EventDraft{
			Type: ledger.EventProjectCreated, Actor: actor, Data: data,
			Subject: ledger.Subject{Kind: ledger.SubjectProject, ID: string(p.ID)},
		})
	})
	if err != nil {
		return ledger.Project{}, false, err
	}
	return p, made, nil
}

// sameProject reads the project a refused insert met, which is a retry only when it is the same one.
func (t *installationTx) sameProject(p ledger.Project) (ledger.Project, error) {
	kept, err := readProject(t.session, stmtReadProject, params{"id": string(p.ID)})
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return ledger.Project{}, fmt.Errorf("%w: another project holds the name %q or the database %q", ledger.ErrProjectExists, p.Name, p.Database)
	case err != nil:
		return ledger.Project{}, err
	case kept.Name != p.Name || kept.Database != p.Database:
		return ledger.Project{}, fmt.Errorf("%w: project %s is %q on %q", ledger.ErrProjectExists, p.ID, kept.Name, kept.Database)
	}
	return kept, nil
}

func projectParams(p ledger.Project) params {
	return params{
		"id": string(p.ID), "name": p.Name, "display_name": p.DisplayName, "state": string(p.State),
		"database": p.Database, "created_at": p.CreatedAt,
	}
}

func scanProject(row scanner) (ledger.Project, error) {
	var p ledger.Project
	var id, state string
	err := row.Scan(&id, &p.Name, &p.DisplayName, &state, &p.Database, &p.CreatedAt)
	p.ID, p.State = ledger.WorkspaceID(id), ledger.ProjectState(state)
	return p, err
}

func readProject(se *session, st *statement, p params) (ledger.Project, error) {
	row, err := se.row(st, p)
	if err != nil {
		return ledger.Project{}, err
	}
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ledger.Project{}, fmt.Errorf("project: %w", ports.ErrNotFound)
	}
	return project, err
}

// Project returns the project with a name, or ErrNotFound.
func (in *Installation) Project(ctx context.Context, name string) (ledger.Project, error) {
	var p ledger.Project
	err := in.read(ctx, "project.named", func(se *session) error {
		var err error
		p, err = readProject(se, stmtProjectNamed, params{"name": name})
		if errors.Is(err, ports.ErrNotFound) {
			return fmt.Errorf("project %s: %w", name, ports.ErrNotFound)
		}
		return err
	})
	return p, err
}

// Projects lists every project, oldest first.
func (in *Installation) Projects(ctx context.Context) ([]ledger.Project, error) {
	var out []ledger.Project
	err := in.read(ctx, "project.list", func(se *session) error {
		rows, err := se.rows(stmtListProjects, params{})
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanProject(rows)
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	return out, err
}

// Events returns up to limit events of the installation's log after a revision, in order.
func (in *Installation) Events(ctx context.Context, after int64, limit int) ([]ledger.Event, error) {
	var out []ledger.Event
	err := in.read(ctx, "installation_event.list", func(se *session) error {
		rows, err := se.rows(stmtListInstallationEvents, params{"after": after, "limit": int64(limit)})
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				return err
			}
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

// IndexRepositories replaces the repository index with entries, in their order: the first entry for an
// identity holds it, and every later one comes back as a conflict. The index is routing data rebuilt
// from the projects' records, so it appends no event.
func (in *Installation) IndexRepositories(ctx context.Context, entries []ledger.IndexedRepository) ([]ledger.IndexedRepository, error) {
	tx, err := in.core.begin(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	se := in.core.session(ctx, tx, ModeWrite, opIndex)
	if _, err := se.exec(stmtClearRepositoryIndex, params{}); err != nil {
		return nil, err
	}
	var conflicts []ledger.IndexedRepository
	for _, entry := range entries {
		n, err := se.exec(stmtIndexRepository, params{
			"identity": entry.Identity, "project_id": string(entry.Project), "repository_id": entry.Repository,
		})
		if err != nil {
			return nil, err
		}
		if n == 0 {
			conflicts = append(conflicts, entry)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("%s: commit: %w", opIndex, err)
	}
	return conflicts, nil
}

// RepositoryIndex lists which project holds each repository, by identity.
func (in *Installation) RepositoryIndex(ctx context.Context) ([]ledger.IndexedRepository, error) {
	var out []ledger.IndexedRepository
	err := in.read(ctx, "repository_index.list", func(se *session) error {
		rows, err := se.rows(stmtListRepositoryIndex, params{})
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry ledger.IndexedRepository
			var project string
			if err := rows.Scan(&entry.Identity, &project, &entry.Repository); err != nil {
				return err
			}
			entry.Project = ledger.WorkspaceID(project)
			out = append(out, entry)
		}
		return rows.Err()
	})
	return out, err
}
