//go:build postgres

package contract

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/pgtest"
)

var (
	serverOnce sync.Once
	server     *pgtest.Server
	serverErr  error
)

func sharedServer() (*pgtest.Server, error) {
	serverOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		server, serverErr = pgtest.Start(ctx)
	})
	return server, serverErr
}

func TestMain(m *testing.M) {
	code := m.Run()
	if server != nil {
		if err := server.Stop(); err != nil {
			fmt.Fprintln(os.Stderr, "stop private Postgres:", err)
			code = 1
		}
	}
	os.Exit(code)
}

var postgresBackend = Backend{Name: "postgres", Fresh: func(t testing.TB) Database {
	srv, err := sharedServer()
	if err != nil {
		t.Fatal(err)
	}
	name, err := srv.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	offset := &atomic.Int64{}
	return Database{
		Restartable: true,
		ShiftClock:  func(delta int64) { offset.Add(delta) },
		Open: func(t testing.TB, opts sqlstore.Options) ports.Store {
			db, err := sql.Open("pgx", srv.DSN(name))
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(16)
			store := sqlstore.New(db, ShiftedDialect{pgtest.Dialect{}, offset}, opts)
			t.Cleanup(func() { _ = store.Close() })
			return store
		},
		Damage: func(_ testing.TB, statement string) error {
			db, err := sql.Open("pgx", srv.DSN(name))
			if err != nil {
				return err
			}
			defer db.Close()
			_, err = db.Exec(statement)
			return err
		},
	}
}}

func TestStoreContractPostgres(t *testing.T) {
	Run(t, postgresBackend, AllChecks())
}

// TestEveryStatementPrepares sends every statement to a real server, which is
// what finds a statement Postgres refuses to type (D7).
func TestEveryStatementPrepares(t *testing.T) {
	srv, err := sharedServer()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	name, err := srv.NewDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", srv.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlstore.New(db, pgtest.Dialect{}, sqlstore.Options{})
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	statements := sqlstore.Statements(pgtest.Dialect{})
	if len(statements) == 0 {
		t.Fatal("no statements")
	}
	for _, st := range statements {
		t.Run(st.Name, func(t *testing.T) {
			prepared, err := db.PrepareContext(ctx, st.Text)
			if err != nil {
				t.Fatalf("%v\n%s", err, st.Text)
			}
			if err := prepared.Close(); err != nil {
				t.Error(err)
			}
		})
	}
}
