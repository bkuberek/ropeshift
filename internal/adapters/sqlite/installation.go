package sqlite

import "ropeshift.com/ropeshift/internal/adapters/sqlstore"

// InstallationFileName is the installation database in a state directory, beside the ledger of each
// project (ADR-0021).
const InstallationFileName = "installation.db"

// OpenInstallation returns the installation database on a SQLite file. Like a ledger it has its own
// writer lock, which only a change to the records every project shares takes, and never a project's
// transaction.
func OpenInstallation(file string, opts sqlstore.Options) (*sqlstore.Installation, error) {
	db, err := OpenDB(file)
	if err != nil {
		return nil, err
	}
	return sqlstore.NewInstallation(db, Dialect{}, opts), nil
}

// OpenMemoryInstallation returns an installation database held in memory, for a test.
func OpenMemoryInstallation(opts sqlstore.Options) (*sqlstore.Installation, error) {
	db, err := OpenMemoryDB()
	if err != nil {
		return nil, err
	}
	return sqlstore.NewInstallation(db, Dialect{}, opts), nil
}
