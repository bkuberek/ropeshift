package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Projects is an installation's registry of projects, with the ledger of each (ADR-0021). The API routes
// every project-scoped call through it, so a call reaches only the ledger of the project its path names.
// Every change to the registry records an event in the installation's log.
type Projects interface {
	// List returns every project of the installation, oldest first.
	List(ctx context.Context) ([]ledger.Project, error)
	// Named returns the project with a name, or ErrNotFound.
	Named(ctx context.Context, name string) (ledger.Project, error)
	// Open returns a project's Store, opening its database on first use. The registry keeps it open while
	// it holds the project, and the caller never closes it.
	Open(ctx context.Context, p ledger.Project) (Store, error)
	// Index lists which project holds each repository, by its identity.
	Index(ctx context.Context) ([]ledger.IndexedRepository, error)
	// Backup copies a project's ledger to a new file while it runs, as LedgerBackup does, and
	// BackupInstallation copies the installation's own records. Each returns ErrNoBackup on a control
	// plane with no online backup.
	Backup(ctx context.Context, p ledger.Project, path string) (int64, error)
	BackupInstallation(ctx context.Context, path string) (int64, error)

	// Create creates a project's database at the current schema and registers it, giving a project with
	// no ID one that no other installation gives.
	Create(ctx context.Context, p ledger.Project, actor ledger.Actor) (ledger.Project, error)
	// Rename gives a project a new name, which no other project holds.
	Rename(ctx context.Context, id ledger.WorkspaceID, name string, actor ledger.Actor) (ledger.Project, error)
	// SetState archives or restores a project.
	SetState(ctx context.Context, id ledger.WorkspaceID, state ledger.ProjectState, actor ledger.Actor) (ledger.Project, error)
	// Remove deletes a project's database and drops it from the registry and the repository index.
	Remove(ctx context.Context, p ledger.Project, actor ledger.Actor) error
	// Import registers a project from its backup with the ID the backup holds, and the name given or the
	// backup's own. It refuses an ID or a name the installation holds, and a repository another project
	// holds, naming that project.
	Import(ctx context.Context, backup, name string, actor ledger.Actor) (ledger.Project, error)
}
