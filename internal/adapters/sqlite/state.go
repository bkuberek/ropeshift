package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sync"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ProjectsDir holds the ledger of each project an installation creates after its first, in the state
// directory (ADR-0021).
const ProjectsDir = "projects"

// State is an installation's state directory: its installation database, and the ledger of each
// project beside it, each a SQLite file with its own writer lock.
type State struct {
	Dir          string
	Installation *sqlstore.Installation
	// IndexConflicts are the repositories the last rebuild of the repository index found in a second
	// project, which the first project to hold each one keeps.
	IndexConflicts []ledger.IndexedRepository
	opts           sqlstore.Options

	// mu guards the ledgers the state keeps open, one Store per project, so every call to a project
	// shares one pool.
	mu     sync.Mutex
	stores map[ledger.WorkspaceID]*sqlstore.Store
	// owned are the ledgers the state opened itself, which it closes. A caller's own Store it only lends.
	owned map[ledger.WorkspaceID]bool
}

// OpenState opens the installation database of a state directory, creating it when it is missing, and
// brings its schema to this build's version. A project's Store opened through the state reaches the
// shared records there.
func OpenState(ctx context.Context, dir string, opts sqlstore.Options) (*State, error) {
	installation, err := OpenInstallation(filepath.Join(dir, InstallationFileName), opts)
	if err != nil {
		return nil, err
	}
	if err := installation.Migrate(ctx); err != nil {
		_ = installation.Close()
		return nil, err
	}
	opts.Installation = installation
	return &State{
		Dir: dir, Installation: installation, opts: opts,
		stores: map[ledger.WorkspaceID]*sqlstore.Store{}, owned: map[ledger.WorkspaceID]bool{},
	}, nil
}

// Close closes every ledger the state opened, and then the installation database. A Store the caller
// lent it stays open.
func (s *State) Close() error {
	s.mu.Lock()
	var failed []error
	for id, store := range s.stores {
		if s.owned[id] {
			failed = append(failed, store.Close())
		}
	}
	s.stores, s.owned = map[ledger.WorkspaceID]*sqlstore.Store{}, map[ledger.WorkspaceID]bool{}
	s.mu.Unlock()
	return errors.Join(append(failed, s.Installation.Close())...)
}

// Lend keeps a project's Store, opened and closed by the caller, as the one Open returns for it.
func (s *State) Lend(id ledger.WorkspaceID, store *sqlstore.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stores[id], s.owned[id] = store, false
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

// Open returns a project's Store, opening its ledger on first use and keeping it open after.
func (s *State) Open(ctx context.Context, p ledger.Project) (ports.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.stores[p.ID]; ok {
		return store, nil
	}
	store, err := s.openLedger(ctx, p.Database)
	if err != nil {
		return nil, err
	}
	s.stores[p.ID], s.owned[p.ID] = store, true
	return store, nil
}

// file resolves a project's database to its file. It must name a file inside the state directory.
func (s *State) file(database string) (string, error) {
	local := filepath.FromSlash(database)
	if !filepath.IsLocal(local) {
		return "", fmt.Errorf("%w: database %q is not inside the state directory", ledger.ErrInvalidProject, database)
	}
	return filepath.Join(s.Dir, local), nil
}

// openLedger opens a ledger file, routed to the installation, at this build's schema.
func (s *State) openLedger(ctx context.Context, database string) (*sqlstore.Store, error) {
	file, err := s.file(database)
	if err != nil {
		return nil, err
	}
	store, err := Open(file, s.opts)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}

// OpenProject opens a registered project's ledger.
func (s *State) OpenProject(ctx context.Context, p ledger.Project) (*sqlstore.Store, error) {
	return s.openLedger(ctx, p.Database)
}

// projectDatabase is where a new project's ledger lives, relative to the state directory. The ID
// becomes a file name, so it holds only letters, digits, hyphens, and underscores.
func projectDatabase(id ledger.WorkspaceID) (string, error) {
	if err := id.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ledger.ErrInvalidProject, err)
	}
	for _, c := range string(id) {
		if !fileNameRune(c) {
			return "", fmt.Errorf("%w: project ID %q holds %q, which a file name cannot", ledger.ErrInvalidProject, id, c)
		}
	}
	return path.Join(ProjectsDir, string(id)+".db"), nil
}

// fileNameRune reports a character a project's file name may hold on every platform.
func fileNameRune(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// CreateProject creates a project's ledger in the projects folder at this build's schema, with its
// workspace, and then registers it. The ledger exists before the registry names it, so a crash between
// the two leaves a file the registry does not know, and a retry registers it. The state keeps the Store
// it returns, and closes it.
func (s *State) CreateProject(ctx context.Context, p ledger.Project, actor ledger.Actor) (ledger.Project, *sqlstore.Store, error) {
	database, err := projectDatabase(p.ID)
	if err != nil {
		return ledger.Project{}, nil, err
	}
	p.Database = database
	if p.State == "" {
		p.State = ledger.ProjectActive
	}
	if err := p.Validate(); err != nil {
		return ledger.Project{}, nil, err
	}
	if existing, err := s.Installation.Project(ctx, p.Name); err == nil && existing.ID != p.ID {
		return ledger.Project{}, nil, fmt.Errorf("%w: project %s is named %q", ledger.ErrProjectExists, existing.ID, p.Name)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, ProjectsDir), 0o700); err != nil {
		return ledger.Project{}, nil, err
	}
	store, err := s.openLedger(ctx, database)
	if err != nil {
		return ledger.Project{}, nil, err
	}
	name := p.DisplayName
	if name == "" {
		name = p.Name
	}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: p.ID, Name: name}, actor); err != nil {
		_ = store.Close()
		return ledger.Project{}, nil, err
	}
	registered, _, err := s.Installation.CreateProject(ctx, p, actor)
	if err != nil {
		_ = store.Close()
		return ledger.Project{}, nil, err
	}
	s.keep(registered.ID, store)
	return registered, store, nil
}

// keep holds a ledger the state opened as the one Open returns for its project. A ledger it already
// holds for the project stays, and the second closes.
func (s *State) keep(id ledger.WorkspaceID, store *sqlstore.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept, ok := s.stores[id]; ok && kept != store {
		_ = store.Close()
		return
	}
	s.stores[id], s.owned[id] = store, true
}

// Backup copies a project's ledger through the online backup, while the daemon goes on writing.
func (s *State) Backup(ctx context.Context, p ledger.Project, path string) (int64, error) {
	file, err := s.file(p.Database)
	if err != nil {
		return 0, err
	}
	return LedgerBackup{File: file}.Backup(ctx, path)
}

// BackupInstallation copies the installation database: the registry, the members and the passphrase's
// hash, and the workers.
func (s *State) BackupInstallation(ctx context.Context, path string) (int64, error) {
	return LedgerBackup{File: filepath.Join(s.Dir, InstallationFileName)}.Backup(ctx, path)
}

var _ ports.Projects = (*State)(nil)
