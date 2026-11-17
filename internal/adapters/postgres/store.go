package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

// InstallationSchema is the schema of the installation's own records: the registry of projects, members,
// workers, and the repository index (ADR-0021). Each project has a schema of its own beside it.
const InstallationSchema = "installation"

// DefaultIdleInTransaction is how long the server lets a session sit inside a transaction before it ends
// the session. A process stopped inside a transaction stalls every writer in its workspace, because the
// revision is taken first (ADR-0010).
const DefaultIdleInTransaction = 30 * time.Second

const defaultMaxConns = 16

// defaultDropWait is how long the drop of a schema waits for a lock on one of its tables, such as the one a
// session with a transaction open on a table of the project holds. The drop runs under the migration lock,
// so every other caller waits for as long as it does.
const defaultDropWait = 30 * time.Second

// defaultDropLimit is how long the whole drop of a schema may run, whatever it waits for or does. A drop
// removes the files of the schema's tables, which takes no time that grows with their rows, so a drop that
// runs this long is stuck, as one behind a lock a session never lets go of, or in a trigger of the database.
const defaultDropLimit = time.Minute

// idleSetting is the server setting that ends a session left inside a transaction.
const idleSetting = "idle_in_transaction_session_timeout"

// idleMillis renders the timeout as the milliseconds the server's setting takes.
func idleMillis(d time.Duration) string {
	switch {
	case d == 0:
		d = DefaultIdleInTransaction
	case d < 0:
		d = 0
	}
	return strconv.FormatInt(d.Milliseconds(), 10)
}

// Options are the settings of a connection to Postgres.
type Options struct {
	// IdleInTransaction is the idle_in_transaction_session_timeout every connection sets. Zero is
	// DefaultIdleInTransaction, and a negative value turns the timeout off.
	IdleInTransaction time.Duration
	// MaxConns bounds the pool every project shares, and the installation's own pool. Zero is 16, and a
	// pool is never smaller than 2, because a migration holds one connection for its lock and runs on
	// another.
	MaxConns int
	// LockTimeout bounds how long a caller waits for the migration lock: its turn behind the other callers
	// of this process, and the time another process holds the lock while it makes a schema or migrates
	// one. It does not bound the work a caller does once it holds the lock. Zero is DefaultLockTimeout,
	// and a negative value waits for as long as the caller's context lasts.
	LockTimeout time.Duration
	// Dialect replaces Dialect{}, so a test can wrap it to move database time.
	Dialect sqlstore.Dialect
	// Store is what every Store the state opens takes, such as a trace. The state sets Installation and
	// Schema itself.
	Store sqlstore.Options
}

// State is an installation on Postgres: its installation schema, and the schema of each project. Every
// project's Store shares one connection pool, and the installation has a pool of its own. It is the
// counterpart of sqlite.State.
type State struct {
	// Installation holds the registry and the records every project shares.
	Installation     *sqlstore.Installation
	pool             *pgxpool.Pool
	installationPool *pgxpool.Pool
	dialect          sqlstore.Dialect
	opts             sqlstore.Options

	// migrating orders the callers of locked within this process. A caller takes its turn by sending on it,
	// which a caller whose context ends stops waiting for.
	migrating   chan struct{}
	lockTimeout time.Duration
	// dropWait bounds how long the drop of a schema waits for a lock on one of its tables, and dropLimit
	// bounds the whole statement.
	dropWait, dropLimit time.Duration
	// repairWait bounds the repair of the repository index after a failed write, from its first wait to its
	// last statement.
	repairWait time.Duration
	// rollbackWait bounds the rollback of a transaction that did not commit.
	rollbackWait time.Duration
	// reported is the set of conflicts of the repository index the state last warned about, by project, so
	// that a conflict that stays is reported once. reportedMu guards it.
	reportedMu sync.Mutex
	reported   map[ledger.WorkspaceID]string
	// afterFailedWrite repairs the repository index after a write that failed once the index had committed.
	// Nil does nothing, which a test uses to look at what the failed write left.
	afterFailedWrite func(context.Context, ledger.WorkspaceID)

	// mu guards the Stores the state keeps open, one per project, so every call to a project shares them.
	// No caller waits for the migration lock while it holds mu.
	mu     sync.Mutex
	stores map[ledger.WorkspaceID]*sqlstore.Store
}

// Open connects to the database a URL names, makes the installation's schema, and brings it to this
// build's version. It drops the schemas that a generated project ID names and that no registered project
// names, which only a crash in the middle of a Create or a Remove leaves, and it releases the entries of the
// repository index that no record backs, which only a crash or a failed commit in the middle of a project's
// write leaves. The URL is a postgres:// URL or a key/value connection string. A project's Store opened
// through the state reaches the shared records there.
func Open(ctx context.Context, url string, opts Options) (*State, error) {
	pool, err := newPool(ctx, url, opts)
	if err != nil {
		return nil, err
	}
	// A project's transaction holds a connection while it reads the shared records through a transaction
	// of the installation's. On one pool, enough writers would each hold a connection and wait for a
	// second, and none would get it. The installation's own pool breaks that wait: a transaction of the
	// installation never waits for a project's connection.
	installationPool, err := newPool(ctx, url, opts)
	if err != nil {
		pool.Close()
		return nil, err
	}
	dialect := opts.Dialect
	if dialect == nil {
		dialect = Dialect{}
	}
	s := &State{
		pool: pool, installationPool: installationPool, dialect: dialect, opts: opts.Store,
		migrating: make(chan struct{}, 1), lockTimeout: lockTimeout(opts.LockTimeout), dropWait: defaultDropWait, dropLimit: defaultDropLimit,
		stores: map[ledger.WorkspaceID]*sqlstore.Store{}, reported: map[ledger.WorkspaceID]string{},
	}
	s.opts.Installation, s.opts.Schema, s.opts.AfterFailedWrite = nil, "", nil
	s.afterFailedWrite = s.repairAfterFailedWrite
	s.repairWait = repairBound(s.lockTimeout)
	s.rollbackWait = unlockLimit
	s.Installation = sqlstore.NewInstallation(stdlib.OpenDBFromPool(installationPool), dialect, sqlstore.Options{Schema: InstallationSchema, Trace: s.opts.Trace})
	err = s.locked(ctx, func(ctx context.Context) error {
		if err := s.createSchema(ctx, InstallationSchema); err != nil {
			return err
		}
		if err := s.Installation.Migrate(ctx); err != nil {
			return err
		}
		if err := s.sweep(ctx); err != nil {
			return err
		}
		return s.reconcileIndex(ctx)
	})
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// newPool connects a pool whose every connection sets the idle-in-transaction timeout.
func newPool(ctx context.Context, url string, opts Options) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	config.MaxConns = int32(max(2, cmpOr(opts.MaxConns, defaultMaxConns)))
	config.ConnConfig.RuntimeParams[idleSetting] = idleMillis(opts.IdleInTransaction)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return pool, nil
}

// lockTimeout resolves Options.LockTimeout: zero is the default, and a negative value is no limit.
func lockTimeout(d time.Duration) time.Duration {
	switch {
	case d == 0:
		return DefaultLockTimeout
	case d < 0:
		return 0
	}
	return d
}

func cmpOr(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

// Close closes every Store the state opened, the installation, and then the pool.
func (s *State) Close() error {
	s.mu.Lock()
	var failed []error
	for _, store := range s.stores {
		failed = append(failed, store.Close())
	}
	s.stores = map[ledger.WorkspaceID]*sqlstore.Store{}
	s.mu.Unlock()
	failed = append(failed, s.Installation.Close())
	s.pool.Close()
	s.installationPool.Close()
	return errors.Join(failed...)
}

// Ledger returns a Store on the pool, in a schema it makes if the database has none, and reaching the
// installation's records. The Store is not migrated, and the caller closes it, which leaves the pool and
// every other Store open. A schema it makes under a project's prefix is a schema no project names, which
// the next Open of a State drops.
func (s *State) Ledger(ctx context.Context, schema string, opts sqlstore.Options) (*sqlstore.Store, error) {
	err := s.locked(ctx, func(ctx context.Context) error { return s.createSchema(ctx, schema) })
	if err != nil {
		return nil, err
	}
	return s.ledger(schema, opts), nil
}

// ledger opens a project's Store in its schema. Its warnings name the schema, so those of one project and
// another tell apart.
func (s *State) ledger(schema string, opts sqlstore.Options) *sqlstore.Store {
	opts.Installation, opts.Schema = s.Installation, schema
	opts.AfterFailedWrite = func(ctx context.Context, project ledger.WorkspaceID) {
		if repair := s.afterFailedWrite; repair != nil {
			repair(ctx, project)
		}
	}
	opts.Warn = sqlstore.WarnFor(opts.Warn, schema)
	return sqlstore.New(stdlib.OpenDBFromPool(s.pool), s.dialect, opts)
}

// OpenStore opens one ledger on connections of its own, in the schema opts.Store names or the database's
// default one. With no installation in opts.Store it keeps the records every project shares in itself, as
// a ledger of an earlier build does. The Store owns its connections and is not migrated.
func OpenStore(url string, opts Options) (*sqlstore.Store, error) {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	dialect := opts.Dialect
	if dialect == nil {
		dialect = Dialect{}
	}
	config.RuntimeParams[idleSetting] = idleMillis(opts.IdleInTransaction)
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(cmpOr(opts.MaxConns, defaultMaxConns))
	return sqlstore.New(db, dialect, opts.Store), nil
}

// createSchema makes a schema when the database has none. Callers hold the migration lock, because two
// sessions that create one at once can fail.
func (s *State) createSchema(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS `+quote(name))
	if err != nil {
		return fmt.Errorf("postgres: create schema %s: %w", name, err)
	}
	return nil
}

// dropSchema drops a schema with every table in it, when the database has it. The drop waits for the locks
// of the schema's tables for at most dropWait and then fails, so a session with a transaction open on one of
// them cannot hold up every caller of the migration lock, which the callers of dropSchema hold. The whole
// statement is bounded as well, by dropLimit, because a drop can be stuck at more than a lock, such as in a
// trigger of the database. A failed drop leaves the schema as it was, and the same call finishes it once its
// cause is gone.
func (s *State) dropSchema(ctx context.Context, name string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: drop schema %s: %w", name, err)
	}
	defer s.rollback(ctx, tx)
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = %d`, s.dropWait.Milliseconds())); err != nil {
		return fmt.Errorf("postgres: drop schema %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL statement_timeout = %d`, s.dropLimit.Milliseconds())); err != nil {
		return fmt.Errorf("postgres: drop schema %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `DROP SCHEMA IF EXISTS `+quote(name)+` CASCADE`); err != nil {
		return fmt.Errorf("postgres: drop schema %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: drop schema %s: %w", name, err)
	}
	return nil
}

// rollback ends a transaction that did not commit, although the caller's context ended, and for rollbackWait
// at most. A connection that cannot answer the rollback is closed when the bound passes, which ends the
// transaction on the server too, and never goes back to the pool with the transaction open.
func (s *State) rollback(ctx context.Context, tx pgx.Tx) {
	ctx, stop := context.WithTimeout(context.WithoutCancel(ctx), s.rollbackWait)
	defer stop()
	_ = tx.Rollback(ctx)
}

// quote renders an identifier.
func quote(name string) string {
	out := []byte{'"'}
	for _, c := range []byte(name) {
		if c == '"' {
			out = append(out, '"')
		}
		out = append(out, c)
	}
	return string(append(out, '"'))
}
