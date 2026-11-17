package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// MigrationLock is the key of the advisory lock every process takes around a migration. Advisory locks
// belong to one database, so one fixed key serves every installation on a server, and a second process
// that starts at once waits until the first has applied each version.
const MigrationLock int64 = 0x726f7065736869

// DefaultLockTimeout is how long a caller waits for the migration lock when Options.LockTimeout is zero.
// The wait includes the turn behind the other callers of the process and the time another process holds the
// lock, such as a long migration, so it also caps how long a start of the server waits behind that process.
const DefaultLockTimeout = time.Minute

// ErrLockTimeout means a caller did not get the migration lock within Options.LockTimeout, because another
// session held it, or because callers of this process took their turns for that long.
var ErrLockTimeout = errors.New("postgres: the wait for the migration lock timed out")

const (
	// lockPoll is how often a caller that waits for the lock tries it again.
	lockPoll = 50 * time.Millisecond
	// unlockLimit bounds the release of the lock, after which the session ends, which releases it too.
	unlockLimit = 10 * time.Second
)

// locked runs fn alone among every process that runs a migration on this database. The lock belongs to
// one session, which fn does not use: fn runs on the pool's other connections, so the lock holds for
// as long as fn does and never nests inside a transaction of fn. One caller at a time takes it in a
// process, so two waiting for the lock never hold every connection of a small pool between them. The wait
// ends with the caller's context, and with Options.LockTimeout.
func (s *State) locked(ctx context.Context, fn func(context.Context) error) (err error) {
	release, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	return fn(ctx)
}

// lock takes this process's turn and then the advisory lock, and returns what gives both up.
func (s *State) lock(ctx context.Context) (func() error, error) {
	wait, cancel := s.waiting(ctx)
	defer cancel()
	select {
	case s.migrating <- struct{}{}:
	case <-wait.Done():
		return nil, s.waitFailed(ctx, wait, wait.Err())
	}
	conn, err := s.pool.Acquire(wait)
	if err != nil {
		<-s.migrating
		return nil, s.waitFailed(ctx, wait, err)
	}
	if held, err := s.advisory(wait, conn); err != nil {
		if held {
			// The server may have granted the lock after the caller stopped listening. Ending the session
			// releases it, so a connection that goes back to the pool never keeps it.
			_ = conn.Hijack().Close(context.WithoutCancel(ctx))
		} else {
			conn.Release()
		}
		<-s.migrating
		return nil, s.waitFailed(ctx, wait, err)
	}
	return func() error {
		defer func() { <-s.migrating }()
		// The unlock runs even when the caller's context ended. A session that cannot release its lock is
		// closed, which releases it, and never goes back to the pool holding it.
		release, stop := context.WithTimeout(context.WithoutCancel(ctx), unlockLimit)
		defer stop()
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, MigrationLock); err != nil {
			_ = conn.Hijack().Close(release)
			return fmt.Errorf("postgres: migration unlock: %w", err)
		}
		conn.Release()
		return nil
	}, nil
}

// waiting bounds the wait for the lock, and not the work done under it.
func (s *State) waiting(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.lockTimeout > 0 {
		return context.WithTimeout(ctx, s.lockTimeout)
	}
	return context.WithCancel(ctx)
}

// waitFailed names why a caller did not get the lock: the caller's own context, or the end of the wait.
func (s *State) waitFailed(ctx, wait context.Context, err error) error {
	if ctx.Err() == nil && errors.Is(wait.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w after %s", ErrLockTimeout, s.lockTimeout)
	}
	return fmt.Errorf("postgres: migration lock: %w", err)
}

// advisory takes the lock on a session by trying it until it is free, so the wait ends with the context
// and not with the holder. It reports whether a failed attempt may still have taken the lock.
func (s *State) advisory(ctx context.Context, conn *pgxpool.Conn) (held bool, err error) {
	for {
		var got bool
		if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, MigrationLock).Scan(&got); err != nil {
			return true, err
		}
		if got {
			return false, nil
		}
		timer := time.NewTimer(lockPoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

// kept returns the Store the state holds for a project, if it holds one.
func (s *State) kept(id ledger.WorkspaceID) (*sqlstore.Store, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	store, ok := s.stores[id]
	return store, ok
}

// Open returns a project's Store, making its schema and bringing it to this build's version on first
// use, and keeping it open after. A project whose Store the state holds comes back at once, with no wait
// for any lock. Any other must be one the registry holds, which Open reads under the lock that Create and
// Remove take, so it never makes the schema of a project a removal has forgotten.
func (s *State) Open(ctx context.Context, p ledger.Project) (ports.Store, error) {
	if store, ok := s.kept(p.ID); ok {
		return store, nil
	}
	schema, err := schemaOf(p)
	if err != nil {
		return nil, err
	}
	var store *sqlstore.Store
	err = s.locked(ctx, func(ctx context.Context) error {
		// A caller ahead of this one may have opened the project while this one waited.
		if kept, ok := s.kept(p.ID); ok {
			store = kept
			return nil
		}
		if err := s.registered(ctx, p.ID, schema); err != nil {
			return err
		}
		opened, err := s.migrated(ctx, schema)
		if err != nil {
			return err
		}
		s.keep(p.ID, opened)
		store = opened
		return nil
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}

// registered holds that the registry names a project, on the schema its record gives.
func (s *State) registered(ctx context.Context, id ledger.WorkspaceID, schema string) error {
	held, err := s.Installation.ProjectByID(ctx, id)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return fmt.Errorf("%w: the registry holds no project %s", ports.ErrNotFound, id)
	case err != nil:
		return err
	case held.Database != schema:
		return fmt.Errorf("%w: the registry keeps project %s in schema %q, not %q", ledger.ErrInvalidProject, id, held.Database, schema)
	}
	return nil
}

// migrated opens a project's Store at this build's schema. The caller holds the migration lock.
func (s *State) migrated(ctx context.Context, schema string) (*sqlstore.Store, error) {
	if err := s.createSchema(ctx, schema); err != nil {
		return nil, err
	}
	store := s.ledger(schema, s.opts)
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}
	return store, nil
}
