package postgres

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/stdlib"

	"ropeshift.com/ropeshift/internal/ledger"
)

// SQLDB opens a database/sql handle on the state's pool, for a test that looks at the server around the
// Stores. The caller closes it, which leaves the pool open.
func (s *State) SQLDB() *sql.DB { return stdlib.OpenDBFromPool(s.pool) }

// AcquiredConns is how many connections of the state's pool are in use. A caller that waits for the migration
// lock holds one.
func (s *State) AcquiredConns() int { return int(s.pool.Stat().AcquiredConns()) }

// The number of steps in creating and in removing a project.
const (
	CreateSteps = createSteps
	RemoveSteps = removeSteps
)

// CreateUpTo runs the first steps of creating a project and stops, as a crash after them would.
func (s *State) CreateUpTo(ctx context.Context, p ledger.Project, actor ledger.Actor, steps int) error {
	c, err := s.newCreation(ctx, p, actor)
	if err != nil {
		return err
	}
	defer func() {
		if c.store != nil {
			_ = c.store.Close()
		}
	}()
	return run(ctx, c.steps()[:steps])
}

// RemoveUpTo runs the first steps of removing a project and stops, as a crash after them would.
func (s *State) RemoveUpTo(ctx context.Context, p ledger.Project, actor ledger.Actor, steps int) error {
	r, err := s.newRemoval(p, actor)
	if err != nil {
		return err
	}
	return run(ctx, r.steps()[:steps])
}
