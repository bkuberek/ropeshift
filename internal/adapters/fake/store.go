package fake

import (
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ports"
)

// Store is the in-memory Store: the SQLite adapter on an in-memory database, so
// one set of statements cannot drift from a second implementation (D6).
type Store struct {
	*sqlstore.Store
}

// NewStore returns an empty in-memory Store. The caller migrates it.
func NewStore() (*Store, error) {
	store, err := sqlite.OpenMemory(sqlstore.Options{})
	if err != nil {
		return nil, err
	}
	return &Store{Store: store}, nil
}

// NewMigratedStore returns an in-memory Store at this build's schema, with no workspace. Its database is
// its own copy of a ledger the process migrated once, so a test that does not test the migrations skips
// their cost; one that does uses NewStore and migrates.
func NewMigratedStore() (*Store, error) {
	db, err := openMigratedDB()
	if err != nil {
		return nil, err
	}
	return &Store{Store: sqlstore.New(db, sqlite.Dialect{}, sqlstore.Options{})}, nil
}

var _ ports.Store = (*Store)(nil)
