package fake

import (
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ports"
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

var _ ports.Store = (*Store)(nil)
