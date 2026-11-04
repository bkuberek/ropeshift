package ports

import (
	"context"
	"errors"
)

// ErrBackupTarget means a path cannot take a backup: it is relative, a file is already there, or
// its directory does not exist. A backup never replaces a file.
var ErrBackupTarget = errors.New("a backup needs an absolute path to a new file in a directory that exists")

// LedgerBackup copies a running ledger to one file, consistent at one moment, with no stop and no
// pause in writes: SQLite's online backup path. Postgres uses the team's own tooling, so a control
// plane on Postgres has none.
type LedgerBackup interface {
	// Backup writes the copy at path, readable only by its owner, and returns its size in bytes.
	Backup(ctx context.Context, path string) (int64, error)
}
