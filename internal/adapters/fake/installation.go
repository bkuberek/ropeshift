package fake

import (
	"context"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ports"
)

// Installation is the records every project shares, held in memory: the SQLite adapter's installation
// database on an in-memory database, so its statements cannot drift from a second implementation (D6).
type Installation struct {
	*sqlstore.Installation
}

// NewInstallation returns an empty installation database at this build's schema.
func NewInstallation(ctx context.Context) (*Installation, error) {
	installation, err := sqlite.OpenMemoryInstallation(sqlstore.Options{})
	if err != nil {
		return nil, err
	}
	if err := installation.Migrate(ctx); err != nil {
		_ = installation.Close()
		return nil, err
	}
	return &Installation{Installation: installation}, nil
}

var _ ports.Installation = (*Installation)(nil)
