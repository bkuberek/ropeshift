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
	// MaxConns bounds the pool the installation and every project share. Zero is 16, and the pool is
	// never smaller than 2, because a migration holds one connection for its lock and runs on another.
	MaxConns int
	// Dialect replaces Dialect{}, so a test can wrap it to move database time.
	Dialect sqlstore.Dialect
	// Store is what every Store the state opens takes, such as a trace. The state sets Installation and
	// Schema itself.
	Store sqlstore.Options
}

// State is an installation on Postgres: its installation schema, and the schema of each project, all
// served by one connection pool. It is the counterpart of sqlite.State.
type State struct {
	// Installation holds the registry and the records every project shares.
	Installation *sqlstore.Installation
	pool         *pgxpool.Pool
	dialect      sqlstore.Dialect
	opts         sqlstore.Options

	// migrating orders the callers of locked within this process.
	migrating sync.Mutex

	// mu guards the Stores the state keeps open, one per project, so every call to a project shares them.
	mu     sync.Mutex
	stores map[ledger.WorkspaceID]*sqlstore.Store
}

// Open connects to the database a URL names, makes the installation's schema, and brings it to this
// build's version. The URL is a postgres:// URL or a key/value connection string. A project's Store
// opened through the state reaches the shared records there.
func Open(ctx context.Context, url string, opts Options) (*State, error) {
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
	dialect := opts.Dialect
	if dialect == nil {
		dialect = Dialect{}
	}
	s := &State{pool: pool, dialect: dialect, opts: opts.Store, stores: map[ledger.WorkspaceID]*sqlstore.Store{}}
	s.opts.Installation, s.opts.Schema = nil, ""
	s.Installation = sqlstore.NewInstallation(stdlib.OpenDBFromPool(pool), dialect, sqlstore.Options{Schema: InstallationSchema, Trace: s.opts.Trace})
	err = s.locked(ctx, func(ctx context.Context) error {
		if err := s.createSchema(ctx, InstallationSchema); err != nil {
			return err
		}
		return s.Installation.Migrate(ctx)
	})
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
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
	return errors.Join(failed...)
}

// Ledger returns a Store on the pool, in a schema it makes if the database has none, and reaching the
// installation's records. The Store is not migrated, and the caller closes it, which leaves the pool and
// every other Store open.
func (s *State) Ledger(ctx context.Context, schema string, opts sqlstore.Options) (*sqlstore.Store, error) {
	err := s.locked(ctx, func(ctx context.Context) error { return s.createSchema(ctx, schema) })
	if err != nil {
		return nil, err
	}
	return s.ledger(schema, opts), nil
}

func (s *State) ledger(schema string, opts sqlstore.Options) *sqlstore.Store {
	opts.Installation, opts.Schema = s.Installation, schema
	return sqlstore.New(stdlib.OpenDBFromPool(s.pool), s.dialect, opts)
}

// OpenStore opens one ledger in the default schema of the database a URL names, which keeps the records
// every project shares in itself and has no installation. The Store owns its connections and is not
// migrated.
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
