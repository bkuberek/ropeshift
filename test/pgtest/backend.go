//go:build postgres

package pgtest

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/contract"
)

var (
	sharedOnce sync.Once
	shared     *Server
	sharedErr  error
)

// Shared returns the one server of this test binary, starting it on first use.
func Shared() (*Server, error) {
	sharedOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		shared, sharedErr = Start(ctx)
	})
	return shared, sharedErr
}

// StopShared stops the server, if one started. A TestMain calls it.
func StopShared() error {
	if shared == nil {
		return nil
	}
	return shared.Stop()
}

// Backend runs the contract suite on the shared server, on a ledger in a database's default schema that
// keeps the records every project shares in itself.
var Backend = contract.Backend{Name: "postgres", Fresh: func(t testing.TB) contract.Database {
	server, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	name, err := server.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return contract.Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			store, err := postgres.OpenStore(server.DSN(name), postgres.Options{
				Dialect: contract.ShiftedDialect{Dialect: postgres.Dialect{}, Offset: offset},
				Store:   opts,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(_ testing.TB, statement string) error {
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(statement)
			return err
		},
	}
}}

// The schemas of an installation on one Postgres database (ADR-0021): the installation's own, and
// one per project.
const (
	InstallationSchema = postgres.InstallationSchema
	ProjectSchema      = "project"
)

// CreateSchemas makes schemas in a database, as an installation on Postgres does for itself and for
// each project it creates.
func CreateSchemas(ctx context.Context, db *sql.DB, names ...string) error {
	for _, name := range names {
		if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS "`+strings.ReplaceAll(name, `"`, `""`)+`"`); err != nil {
			return err
		}
	}
	return nil
}

// MigrationBackend is an empty database on the shared server for each migration check, whose stores
// keep their tables in a schema, as each project and the installation do on one database (ADR-0021).
var MigrationBackend = contract.MigrationBackend{Name: "postgres", Fresh: func(t testing.TB) contract.MigrationDatabase {
	server, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	name, err := server.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", server.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := CreateSchemas(t.Context(), db, ProjectSchema); err != nil {
		t.Fatal(err)
	}
	return contract.MigrationDatabase{DB: db, Dialect: postgres.Dialect{}, Schema: ProjectSchema}
}}

// ProjectBackend runs the contract suite on a project of an installation: the project's schema and the
// installation's in one database, the project's Store on the pool every project shares and the installation
// on a pool of its own, each Store setting its schema on every transaction, with the records every project
// shares in the installation's schema.
var ProjectBackend = contract.Backend{Name: "postgres-project", Fresh: func(t testing.TB) contract.Database {
	server, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	name, err := server.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return contract.Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			// The installation reads the same shifted clock, because a run window it holds ends by that time.
			state, err := postgres.Open(t.Context(), server.DSN(name), postgres.Options{
				Dialect: contract.ShiftedDialect{Dialect: postgres.Dialect{}, Offset: offset},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = state.Close() })
			store, err := state.Ledger(t.Context(), ProjectSchema, opts)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(_ testing.TB, statement string) error {
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				return err
			}
			defer db.Close()
			// The statement runs in the project's schema, which SET LOCAL scopes to this transaction.
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.Exec(postgres.Dialect{}.SchemaSQL(ProjectSchema)); err != nil {
				return err
			}
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
			return tx.Commit()
		},
	}
}}
