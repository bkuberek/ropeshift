package sqlite

import "ropeshift.com/ropeshift/internal/adapters/sqlstore"

// InstallationFileName is the installation database in a state directory, beside the ledger of each
// project (ADR-0021).
const InstallationFileName = "installation.db"

// OpenInstallation returns the installation database on a SQLite file. Like a ledger it has its own
// writer lock, which no project's transaction takes unless it touches the records every project shares.
func OpenInstallation(file string, opts sqlstore.Options) (*sqlstore.Installation, error) {
	db, err := OpenDB(file)
	if err != nil {
		return nil, err
	}
	return sqlstore.NewInstallation(db, Dialect{}, opts), nil
}
