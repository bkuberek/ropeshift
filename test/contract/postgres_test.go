//go:build postgres

package contract_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/pgtest"
)

func TestMain(m *testing.M) {
	code := doubles.Main(m)
	if err := pgtest.StopShared(); err != nil {
		fmt.Fprintln(os.Stderr, "stop private Postgres:", err)
		code = 1
	}
	os.Exit(code)
}

func TestStoreContractPostgres(t *testing.T) {
	contract.Run(t, pgtest.Backend, contract.AllChecks())
}

// TestEveryStatementPrepares sends every statement to a real server, which is
// what finds a statement Postgres refuses to type (D7).
func TestEveryStatementPrepares(t *testing.T) {
	server, err := pgtest.Shared()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	name, err := server.NewDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", server.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := sqlstore.New(db, pgtest.Dialect{}, sqlstore.Options{})
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	statements := sqlstore.Statements(pgtest.Dialect{})
	if len(statements) < 30 {
		t.Fatalf("only %d statements", len(statements))
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
