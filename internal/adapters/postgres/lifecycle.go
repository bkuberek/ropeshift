package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ErrImportUnavailable means this version cannot register a project from a backup on Postgres.
var ErrImportUnavailable = errors.New("importing a project onto Postgres is not available in this version")

// schemaPrefix keeps a project's schema apart from the installation's and from any schema a person made.
const schemaPrefix = "project_"

// schemaLimit is the longest identifier Postgres keeps.
const schemaLimit = 63

// schemaName names the schema of a project. The ID becomes part of an identifier, so it holds only
// letters, digits, hyphens, and underscores, as it does in a SQLite file's name.
func schemaName(id ledger.WorkspaceID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ledger.ErrInvalidProject, err)
	}
	for _, c := range string(id) {
		if !identifierRune(c) {
			return "", fmt.Errorf("%w: project ID %q holds %q, which a schema name cannot", ledger.ErrInvalidProject, id, c)
		}
	}
	if len(schemaPrefix)+len(id) > schemaLimit {
		return "", fmt.Errorf("%w: project ID %q is longer than the %d characters a schema name allows", ledger.ErrInvalidProject, id, schemaLimit-len(schemaPrefix))
	}
	return schemaPrefix + string(id), nil
}

func identifierRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// schemaOf is the schema a project's record may name: the one its ID gives, and no other, so a record
// can never point a step of the lifecycle at a schema that is not the project's.
func schemaOf(p ledger.Project) (string, error) {
	schema, err := schemaName(p.ID)
	if err != nil {
		return "", err
	}
	if p.Database != schema {
		return "", fmt.Errorf("%w: project %s keeps its records in schema %q, not %q", ledger.ErrInvalidProject, p.ID, schema, p.Database)
	}
	return schema, nil
}

// step is one move of a project's lifecycle. Each is safe to repeat, so a crash after any of them leaves
// the next attempt to finish.
type step func(context.Context) error

func run(ctx context.Context, steps []step) error {
	for _, do := range steps {
		if err := do(ctx); err != nil {
			return err
		}
	}
	return nil
}

// creation is the creation of one project. The schema exists and is migrated before the registry names
// the project, as a SQLite ledger file does, so a crash between the two leaves a schema the registry does
// not know, and a retry registers it.
type creation struct {
	s          *State
	p          ledger.Project
	actor      ledger.Actor
	schema     string
	store      *sqlstore.Store
	registered ledger.Project
}

const createSteps = 4

// newCreation checks a project and settles its ID, its schema, and its state, before any step runs.
func (s *State) newCreation(ctx context.Context, p ledger.Project, actor ledger.Actor) (*creation, error) {
	if p.ID == "" {
		var entropy [ledger.ULIDEntropy]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return nil, err
		}
		p.ID = ledger.WorkspaceID(ledger.EncodeULID(time.Now().UnixMilli(), entropy))
	}
	schema, err := schemaName(p.ID)
	if err != nil {
		return nil, err
	}
	p.Database = schema
	if p.State == "" {
		p.State = ledger.ProjectActive
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if existing, err := s.Installation.Project(ctx, p.Name); err == nil && existing.ID != p.ID {
		return nil, fmt.Errorf("%w: project %s is named %q", ledger.ErrProjectExists, existing.ID, p.Name)
	}
	return &creation{s: s, p: p, actor: actor, schema: schema}, nil
}

func (c *creation) steps() []step {
	return []step{
		// The schema.
		func(ctx context.Context) error {
			return c.s.locked(ctx, func(ctx context.Context) error { return c.s.createSchema(ctx, c.schema) })
		},
		// Its tables, at this build's version.
		func(ctx context.Context) error {
			c.store = c.s.ledger(c.schema, c.s.opts)
			return c.s.locked(ctx, c.store.Migrate)
		},
		// The project's workspace, which a retry finds and leaves as it is.
		func(ctx context.Context) error {
			name := c.p.DisplayName
			if name == "" {
				name = c.p.Name
			}
			_, err := c.store.CreateWorkspace(ctx, ledger.Workspace{ID: c.p.ID, Name: name}, c.actor)
			return err
		},
		// The registry's row and event, which decide that the project exists.
		func(ctx context.Context) error {
			var err error
			c.registered, _, err = c.s.Installation.CreateProject(ctx, c.p, c.actor)
			return err
		},
	}
}

// Create makes a project's schema at the current version and registers it, giving a project with no ID
// one that no other installation gives.
func (s *State) Create(ctx context.Context, p ledger.Project, actor ledger.Actor) (ledger.Project, error) {
	c, err := s.newCreation(ctx, p, actor)
	if err != nil {
		return ledger.Project{}, err
	}
	if err := run(ctx, c.steps()); err != nil {
		if c.store != nil {
			_ = c.store.Close()
		}
		return ledger.Project{}, err
	}
	s.keep(c.registered.ID, c.store)
	return c.registered, nil
}

// keep holds a Store the state opened as the one Open returns for its project. A Store it already holds
// for the project stays, and the second closes.
func (s *State) keep(id ledger.WorkspaceID, store *sqlstore.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept, ok := s.stores[id]; ok && kept != store {
		_ = store.Close()
		return
	}
	s.stores[id] = store
}

// forget closes the Store the state keeps for a project, if it opened one.
func (s *State) forget(id ledger.WorkspaceID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.stores[id]; ok {
		_ = store.Close()
	}
	delete(s.stores, id)
}

// removal is the removal of one project. The registry forgets the project first, in one transaction with
// the event that records who removed it, and the schema goes after, so a crash between the two leaves a
// schema no project names, and never a project whose ledger is gone.
type removal struct {
	s      *State
	p      ledger.Project
	actor  ledger.Actor
	schema string
}

const removeSteps = 2

func (s *State) newRemoval(p ledger.Project, actor ledger.Actor) (*removal, error) {
	schema, err := schemaOf(p)
	if err != nil {
		return nil, err
	}
	return &removal{s: s, p: p, actor: actor, schema: schema}, nil
}

func (r *removal) steps() []step {
	return []step{
		// The registry's row, the project's repositories in the index, and the event.
		func(ctx context.Context) error {
			err := r.s.Installation.DropProject(ctx, r.p.ID, r.actor)
			if errors.Is(err, ports.ErrNotFound) {
				// A removal that stopped after this step left the schema, and the registry no longer holds
				// the project. With no schema either, there is nothing to remove.
				left, serr := r.s.schemaExists(ctx, r.schema)
				if serr != nil {
					return serr
				}
				if !left {
					return err
				}
				err = nil
			}
			if err != nil {
				return err
			}
			r.s.forget(r.p.ID)
			return nil
		},
		// The schema, with every table of the project's ledger. It goes under the lock every change to a
		// schema takes, so an Open or a Create in another process cannot make the schema again or migrate it
		// while it drops. An Open that runs after the registry forgot the project is refused.
		func(ctx context.Context) error {
			return r.s.locked(ctx, func(ctx context.Context) error {
				r.s.forget(r.p.ID)
				if _, err := r.s.pool.Exec(ctx, `DROP SCHEMA IF EXISTS `+quote(r.schema)+` CASCADE`); err != nil {
					return fmt.Errorf("postgres: drop schema %s: %w", r.schema, err)
				}
				return nil
			})
		},
	}
}

// Remove forgets a project and drops its schema, which a repeat of Remove finishes after a crash.
func (s *State) Remove(ctx context.Context, p ledger.Project, actor ledger.Actor) error {
	r, err := s.newRemoval(p, actor)
	if err != nil {
		return err
	}
	return run(ctx, r.steps())
}

func (s *State) schemaExists(ctx context.Context, name string) (bool, error) {
	var found bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, name).Scan(&found)
	return found, err
}

// List returns every project, oldest first.
func (s *State) List(ctx context.Context) ([]ledger.Project, error) {
	return s.Installation.Projects(ctx)
}

// Named returns the project with a name, or ErrNotFound.
func (s *State) Named(ctx context.Context, name string) (ledger.Project, error) {
	return s.Installation.Project(ctx, name)
}

// Index lists which project holds each repository.
func (s *State) Index(ctx context.Context) ([]ledger.IndexedRepository, error) {
	return s.Installation.RepositoryIndex(ctx)
}

// Rename gives a project a new name. The ID, the schema, and every record keep their identity.
func (s *State) Rename(ctx context.Context, id ledger.WorkspaceID, name string, actor ledger.Actor) (ledger.Project, error) {
	return s.Installation.RenameProject(ctx, id, name, actor)
}

// SetState archives or restores a project. Archiving keeps the schema, and the registry's state is what
// makes the project read-only, as it is on SQLite.
func (s *State) SetState(ctx context.Context, id ledger.WorkspaceID, state ledger.ProjectState, actor ledger.Actor) (ledger.Project, error) {
	return s.Installation.SetProjectState(ctx, id, state, actor)
}

// Import is not available on Postgres in this version.
func (s *State) Import(context.Context, string, string, ledger.Actor) (ledger.Project, error) {
	return ledger.Project{}, ErrImportUnavailable
}

// Backup returns ports.ErrNoBackup: a team backs up its Postgres database with its own tooling.
func (s *State) Backup(context.Context, ledger.Project, string) (int64, error) {
	return 0, ports.ErrNoBackup
}

// BackupInstallation returns ports.ErrNoBackup, as Backup does.
func (s *State) BackupInstallation(context.Context, string) (int64, error) {
	return 0, ports.ErrNoBackup
}

var _ ports.Projects = (*State)(nil)
