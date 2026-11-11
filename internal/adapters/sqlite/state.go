package sqlite

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

// ProjectsDir holds the ledger of each project an installation creates after its first, in the state
// directory (ADR-0021).
const ProjectsDir = "projects"

// State is an installation's state directory: its installation database, and the ledger of each
// project beside it, each a SQLite file with its own writer lock.
type State struct {
	Dir          string
	Installation *sqlstore.Installation
	opts         sqlstore.Options
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
	return &State{Dir: dir, Installation: installation, opts: opts}, nil
}

// Close closes the installation database. Each project's Store closes on its own.
func (s *State) Close() error { return s.Installation.Close() }

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
// the two leaves a file the registry does not know, and a retry registers it.
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
	return registered, store, nil
}
