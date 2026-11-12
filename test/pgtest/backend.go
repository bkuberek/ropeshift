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

// Backend runs the contract suite on the shared server.
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
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(16)
			store := sqlstore.New(db, contract.ShiftedDialect{Dialect: Dialect{}, Offset: offset}, opts)
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
	InstallationSchema = "installation"
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

// ProjectBackend runs the contract suite on a project of an installation: the project's schema and the
// installation's in one database, each Store setting its schema on every transaction, with the records
// every project shares in the installation's schema.
var ProjectBackend = contract.Backend{Name: "postgres-project", Fresh: func(t testing.TB) contract.Database {
	server, err := Shared()
	if err != nil {
		t.Fatal(err)
	}
	name, err := server.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", server.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := CreateSchemas(t.Context(), admin, InstallationSchema, ProjectSchema); err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return contract.Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			installationDB, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				t.Fatal(err)
			}
			// The installation reads the same shifted clock, because a run window it holds ends by that time.
			installation := sqlstore.NewInstallation(installationDB, contract.ShiftedDialect{Dialect: Dialect{}, Offset: offset}, sqlstore.Options{Schema: InstallationSchema})
			t.Cleanup(func() { _ = installation.Close() })
			if err := installation.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(16)
			opts.Installation, opts.Schema = installation, ProjectSchema
			store := sqlstore.New(db, contract.ShiftedDialect{Dialect: Dialect{}, Offset: offset}, opts)
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(_ testing.TB, statement string) error {
			db, err := sql.Open("pgx", server.DSN(name))
			if err != nil {
				return err
			}
			defer db.Close()
			conn, err := db.Conn(context.Background())
			if err != nil {
				return err
			}
			defer conn.Close()
			if _, err := conn.ExecContext(context.Background(), `SET search_path TO "`+ProjectSchema+`"`); err != nil {
				return err
			}
			_, err = conn.ExecContext(context.Background(), statement)
			return err
		},
	}
}}
