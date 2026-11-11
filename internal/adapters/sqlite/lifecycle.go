package sqlite

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Create creates a project's ledger and registers it. A project with no ID gets a ULID, which no other
// installation gives, so its backup imports anywhere with its ID.
func (s *State) Create(ctx context.Context, p ledger.Project, actor ledger.Actor) (ledger.Project, error) {
	if p.ID == "" {
		var entropy [ledger.ULIDEntropy]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return ledger.Project{}, err
		}
		p.ID = ledger.WorkspaceID(ledger.EncodeULID(time.Now().UnixMilli(), entropy))
	}
	created, _, err := s.CreateProject(ctx, p, actor)
	return created, err
}

// Rename gives a project a new name. The ID, the database, and every record keep their identity.
func (s *State) Rename(ctx context.Context, id ledger.WorkspaceID, name string, actor ledger.Actor) (ledger.Project, error) {
	return s.Installation.RenameProject(ctx, id, name, actor)
}

// SetState archives or restores a project.
func (s *State) SetState(ctx context.Context, id ledger.WorkspaceID, state ledger.ProjectState, actor ledger.Actor) (ledger.Project, error) {
	return s.Installation.SetProjectState(ctx, id, state, actor)
}

// Remove forgets a project and deletes its database. The registry and the index forget it first, in one
// transaction with the event that records who removed it, so a crash before the file goes leaves a file
// no project names, and never a project whose ledger is gone.
func (s *State) Remove(ctx context.Context, p ledger.Project, actor ledger.Actor) error {
	file, err := s.file(p.Database)
	if err != nil {
		return err
	}
	if err := s.Installation.DropProject(ctx, p.ID, actor); err != nil {
		return err
	}
	s.forget(p.ID)
	return removeLedgerFile(file)
}

// forget closes the ledger the state keeps for a project, if it opened it.
func (s *State) forget(id ledger.WorkspaceID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.stores[id]; ok && s.owned[id] {
		_ = store.Close()
	}
	delete(s.stores, id)
	delete(s.owned, id)
}

// removeLedgerFile deletes a SQLite file and the journal files beside it.
func removeLedgerFile(file string) error {
	var failed []error
	for _, name := range []string{file, file + "-wal", file + "-shm", file + "-journal"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			failed = append(failed, err)
		}
	}
	return errors.Join(failed...)
}

// Import registers a project from its backup, keeping the ID the backup holds. The name is the one
// given, or the backup's own when it is a slug. The copy lands in the projects folder at this build's
// schema before the registry names it, and a refusal or a failure removes it again.
func (s *State) Import(ctx context.Context, backup string, name string, actor ledger.Actor) (ledger.Project, error) {
	workspace, err := backedUpWorkspace(backup)
	if err != nil {
		return ledger.Project{}, err
	}
	p := ledger.Project{ID: workspace.ID, Name: name, State: ledger.ProjectActive}
	if p.Name == "" {
		if ledger.ValidateProjectName(workspace.Name) != nil {
			return ledger.Project{}, fmt.Errorf("%w: the backup names its project %q, which is no name a project takes: name it", ledger.ErrInvalidProject, workspace.Name)
		}
		p.Name = workspace.Name
	}
	if workspace.Name != p.Name {
		p.DisplayName = workspace.Name
	}
	if p.Database, err = projectDatabase(p.ID); err != nil {
		return ledger.Project{}, err
	}
	if err := p.Validate(); err != nil {
		return ledger.Project{}, err
	}
	if err := s.importable(ctx, p); err != nil {
		return ledger.Project{}, err
	}
	store, err := s.copyIn(ctx, backup, p.Database)
	if err != nil {
		return ledger.Project{}, err
	}
	registered, err := s.register(ctx, store, p, actor)
	if err != nil {
		_ = store.Close()
		if file, ferr := s.file(p.Database); ferr == nil {
			_ = removeLedgerFile(file)
		}
		return ledger.Project{}, err
	}
	s.keep(registered.ID, store)
	return registered, nil
}

// importable refuses a project whose ID or name the installation holds, before anything is copied.
func (s *State) importable(ctx context.Context, p ledger.Project) error {
	projects, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, held := range projects {
		switch {
		case held.ID == p.ID:
			return fmt.Errorf("%w: the installation holds project ID %s, named %s", ledger.ErrProjectExists, p.ID, held.Name)
		case held.Name == p.Name:
			return fmt.Errorf("%w: project %s is named %q", ledger.ErrProjectExists, held.ID, p.Name)
		}
	}
	return nil
}

// copyIn copies a backup to a project's database through the online backup, and opens it at this build's
// schema. A file the registry does not name, which an import a crash cut short left, gives way.
func (s *State) copyIn(ctx context.Context, backup, database string) (*sqlstore.Store, error) {
	file, err := s.file(database)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	if err := removeLedgerFile(file); err != nil {
		return nil, err
	}
	if _, err := (LedgerBackup{File: backup}).Backup(ctx, file); err != nil {
		return nil, err
	}
	store, err := s.openLedger(ctx, database)
	if err != nil {
		_ = removeLedgerFile(file)
		return nil, err
	}
	return store, nil
}

// register checks that the copy holds the one workspace the backup named, and registers it with its
// repositories.
func (s *State) register(ctx context.Context, store *sqlstore.Store, p ledger.Project, actor ledger.Actor) (ledger.Project, error) {
	held, err := store.Workspaces(ctx)
	if err != nil {
		return ledger.Project{}, err
	}
	if len(held) != 1 || held[0] != p.ID {
		return ledger.Project{}, fmt.Errorf("%w: the backup holds workspaces %v, and a project is one", ledger.ErrInvalidProject, held)
	}
	var repositories []ledger.IndexedRepository
	err = store.Read(ctx, p.ID, func(tx ports.ReadTx) error {
		records, err := tx.Repositories().Repositories()
		for _, r := range records {
			if !r.Removed() {
				repositories = append(repositories, ledger.IndexedRepository{Identity: ledger.RepositoryIdentity(r.Remote, r.ID), Project: p.ID, Repository: r.ID})
			}
		}
		return err
	})
	if err != nil {
		return ledger.Project{}, err
	}
	return s.Installation.ImportProject(ctx, p, repositories, actor)
}

// backedUpWorkspace reads the one workspace a backup holds, without changing the file.
func backedUpWorkspace(backup string) (ledger.Workspace, error) {
	if _, err := os.Stat(backup); err != nil {
		return ledger.Workspace{}, fmt.Errorf("%w: %v", ledger.ErrInvalidProject, err)
	}
	db, err := OpenDB(backup)
	if err != nil {
		return ledger.Workspace{}, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, name FROM workspaces ORDER BY id`)
	if err != nil {
		return ledger.Workspace{}, fmt.Errorf("%w: %s is no ledger: %v", ledger.ErrInvalidProject, backup, err)
	}
	defer rows.Close()
	var found []ledger.Workspace
	for rows.Next() {
		var w ledger.Workspace
		var id string
		if err := rows.Scan(&id, &w.Name); err != nil {
			return ledger.Workspace{}, err
		}
		w.ID = ledger.WorkspaceID(id)
		found = append(found, w)
	}
	if err := rows.Err(); err != nil {
		return ledger.Workspace{}, err
	}
	if len(found) != 1 {
		return ledger.Workspace{}, fmt.Errorf("%w: %s holds %d workspaces, and a project's backup holds one", ledger.ErrInvalidProject, backup, len(found))
	}
	return found[0], nil
}
