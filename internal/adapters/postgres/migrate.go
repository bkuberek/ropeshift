package postgres

import (
	"context"
	"errors"
	"fmt"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// MigrationLock is the key of the advisory lock every process takes around a migration. Advisory locks
// belong to one database, so one fixed key serves every installation on a server, and a second process
// that starts at once waits until the first has applied each version.
const MigrationLock int64 = 0x726f7065736869

// locked runs fn alone among every process that runs a migration on this database. The lock belongs to
// one session, which fn does not use: fn runs on the pool's other connections, so the lock holds for
// as long as fn does and never nests inside a transaction of fn. One caller at a time takes it in a
// process, so two waiting for the lock never hold every connection of a small pool between them.
func (s *State) locked(ctx context.Context, fn func(context.Context) error) (err error) {
	s.migrating.Lock()
	defer s.migrating.Unlock()
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, MigrationLock); err != nil {
		conn.Release()
		return fmt.Errorf("postgres: migration lock: %w", err)
	}
	defer func() {
		// The unlock runs even when the caller's context ended. A session that cannot release its lock is
		// closed, which releases it, and never goes back to the pool holding it.
		release := context.WithoutCancel(ctx)
		if _, uerr := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, MigrationLock); uerr != nil {
			_ = conn.Hijack().Close(release)
			err = errors.Join(err, fmt.Errorf("postgres: migration unlock: %w", uerr))
			return
		}
		conn.Release()
	}()
	return fn(ctx)
}

// Open returns a project's Store, making its schema and bringing it to this build's version on first
// use, and keeping it open after.
func (s *State) Open(ctx context.Context, p ledger.Project) (ports.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if store, ok := s.stores[p.ID]; ok {
		return store, nil
	}
	store, err := s.openLedger(ctx, p)
	if err != nil {
		return nil, err
	}
	s.stores[p.ID] = store
	return store, nil
}

// openLedger opens a project's Store at this build's schema.
func (s *State) openLedger(ctx context.Context, p ledger.Project) (*sqlstore.Store, error) {
	schema, err := schemaOf(p)
	if err != nil {
		return nil, err
	}
	var store *sqlstore.Store
	err = s.locked(ctx, func(ctx context.Context) error {
		if err := s.createSchema(ctx, schema); err != nil {
			return err
		}
		store = s.ledger(schema, s.opts)
		if err := store.Migrate(ctx); err != nil {
			_ = store.Close()
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}
