package sqlite

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/crashpoint"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// defaultDisplayName is what the first project of an installation shows.
const defaultDisplayName = "Default"

// Upgrade brings a state directory to this build at every start (ADR-0021). An installation with one
// ledger becomes the project `default`, which keeps the ledger's workspace ID, its file, and every
// record, with the records every project shares moved to the installation database. A fresh
// installation gets the same: its first project's ledger is the ledger file, with first as its
// workspace. The steps each repeat safely after a crash, and a start after a finished upgrade changes
// nothing but the repository index, which every start rebuilds. It returns the state and the first
// project's Store, which the caller closes, and which the state lends to every call to that project.
func Upgrade(ctx context.Context, dir, ledgerName string, first ledger.Workspace, actor ledger.Actor) (*State, *sqlstore.Store, error) {
	u := &upgrade{dir: dir, ledgerName: ledgerName, first: first, actor: actor}
	if err := u.through(ctx, len(upgradeSteps)); err != nil {
		u.close()
		return nil, nil, err
	}
	return u.state, u.store, nil
}

// upgrade is one start of a state directory.
type upgrade struct {
	dir        string
	ledgerName string
	first      ledger.Workspace
	actor      ledger.Actor
	state      *State
	store      *sqlstore.Store
	// scope is the workspace the ledger holds, which its project keeps as its ID.
	scope ledger.WorkspaceID
}

// upgradeStep is one step of an upgrade, which ends at a commit or before any write.
type upgradeStep struct {
	name string
	run  func(*upgrade, context.Context) error
}

// upgradeSteps are the steps of an upgrade, in order. Each is named where a crash test kills a process
// after it.
var upgradeSteps = []upgradeStep{
	{"migrated", (*upgrade).openInstallation},
	{"opened", (*upgrade).openLedger},
	{"registered", (*upgrade).register},
	{"copied", (*upgrade).copyRecords},
	{"deleted", (*upgrade).dropRecords},
	{"indexed", (*upgrade).index},
}

// through runs the first n steps.
func (u *upgrade) through(ctx context.Context, n int) error {
	for _, step := range upgradeSteps[:n] {
		if err := step.run(u, ctx); err != nil {
			return fmt.Errorf("upgrade %s: %s: %w", u.dir, step.name, err)
		}
	}
	return nil
}

// close closes what the steps opened.
func (u *upgrade) close() {
	if u.store != nil {
		_ = u.store.Close()
	}
	if u.state != nil {
		_ = u.state.Close()
	}
}

// openInstallation opens the installation database, creating it when it is missing, at this build's
// schema.
func (u *upgrade) openInstallation(ctx context.Context) error {
	state, err := OpenState(ctx, u.dir, sqlstore.Options{})
	if err != nil {
		return err
	}
	u.state = state
	crashpoint.Hit("installation.upgrade", "migrated")
	return nil
}

// openLedger opens the ledger file at this build's schema and reads its workspace. A new ledger gets the
// first workspace.
func (u *upgrade) openLedger(ctx context.Context) error {
	store, err := u.state.openLedger(ctx, u.ledgerName)
	if err != nil {
		return err
	}
	u.store = store
	held, err := store.Workspaces(ctx)
	if err != nil {
		return err
	}
	switch len(held) {
	case 0:
		if _, err := store.CreateWorkspace(ctx, u.first, u.actor); err != nil {
			return err
		}
		u.scope = u.first.ID
	case 1:
		u.scope = held[0]
	default:
		return fmt.Errorf("the ledger holds %d workspaces, and a project is one", len(held))
	}
	u.state.Lend(u.scope, store)
	crashpoint.Hit("installation.upgrade", "opened")
	return nil
}

// register makes the ledger the project `default`, unless the registry already names it.
func (u *upgrade) register(ctx context.Context) error {
	projects, err := u.state.Installation.Projects(ctx)
	if err != nil {
		return err
	}
	for _, p := range projects {
		if p.Database != u.ledgerName {
			continue
		}
		if p.ID != u.scope {
			return fmt.Errorf("the registry names project %s for %s, and the ledger holds workspace %s", p.ID, u.ledgerName, u.scope)
		}
		crashpoint.Hit("installation.upgrade", "registered")
		return nil
	}
	_, _, err = u.state.Installation.CreateProject(ctx, ledger.Project{
		ID: u.scope, Name: ledger.DefaultProjectName, DisplayName: defaultDisplayName, Database: u.ledgerName,
	}, u.actor)
	if err != nil {
		return err
	}
	crashpoint.Hit("installation.upgrade", "registered")
	return nil
}

// copyRecords copies the shared records the ledger still holds into the installation.
func (u *upgrade) copyRecords(ctx context.Context) error {
	if _, err := u.state.Installation.CopyRecords(ctx, u.store, u.scope, u.actor); err != nil {
		return err
	}
	crashpoint.Hit("installation.upgrade", "copied")
	return nil
}

// dropRecords deletes from the ledger the shared records the installation now holds.
func (u *upgrade) dropRecords(ctx context.Context) error {
	if err := u.state.Installation.DropMovedRecords(ctx, u.store, u.scope); err != nil {
		return err
	}
	crashpoint.Hit("installation.upgrade", "deleted")
	return nil
}

// index rebuilds the repository index from every project's repository records, oldest project first,
// so the first project to hold a repository keeps it. The state remembers the rest.
func (u *upgrade) index(ctx context.Context) error {
	projects, err := u.state.Installation.Projects(ctx)
	if err != nil {
		return err
	}
	var entries []ledger.IndexedRepository
	for _, p := range projects {
		held, err := u.repositories(ctx, p)
		if err != nil {
			return fmt.Errorf("project %s: %w", p.Name, err)
		}
		entries = append(entries, held...)
	}
	conflicts, err := u.state.Installation.IndexRepositories(ctx, entries)
	if err != nil {
		return err
	}
	u.state.IndexConflicts = conflicts
	crashpoint.Hit("installation.upgrade", "indexed")
	return nil
}

// repositories reads a project's repository records as index entries.
func (u *upgrade) repositories(ctx context.Context, p ledger.Project) ([]ledger.IndexedRepository, error) {
	store := u.store
	if p.Database != u.ledgerName {
		opened, err := u.state.OpenProject(ctx, p)
		if err != nil {
			return nil, err
		}
		defer opened.Close()
		store = opened
	}
	var out []ledger.IndexedRepository
	err := store.Read(ctx, p.ID, func(tx ports.ReadTx) error {
		records, err := tx.Repositories().Repositories()
		for _, r := range records {
			if !r.Removed() {
				out = append(out, ledger.IndexedRepository{Identity: ledger.RepositoryIdentity(r.Remote, r.ID), Project: p.ID, Repository: r.ID})
			}
		}
		return err
	})
	return out, err
}
