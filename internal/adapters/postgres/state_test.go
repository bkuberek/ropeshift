//go:build postgres

package postgres_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if err := pgtest.StopShared(); err != nil {
		fmt.Fprintln(os.Stderr, "stop private Postgres:", err)
		code = 1
	}
	os.Exit(code)
}

var member = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}

// freshDatabase returns the connection string of an empty database on the shared private server.
func freshDatabase(t testing.TB) string {
	t.Helper()
	server, err := pgtest.Shared()
	if err != nil {
		t.Fatal(err)
	}
	name, err := server.NewDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return server.DSN(name)
}

func openState(t testing.TB, url string, opts postgres.Options) *postgres.State {
	t.Helper()
	state, err := postgres.Open(t.Context(), url, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func scalar(t testing.TB, db *sql.DB, query string, args ...any) string {
	t.Helper()
	var out string
	if err := db.QueryRowContext(t.Context(), query, args...).Scan(&out); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

// TestEachConnectionEndsAnIdleTransaction holds the design's rule for Postgres deployments: a process
// stopped inside a transaction stalls every writer in its workspace, so the server ends the session.
func TestEachConnectionEndsAnIdleTransaction(t *testing.T) {
	url := freshDatabase(t)

	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	if got := scalar(t, db, `SHOW idle_in_transaction_session_timeout`); got != "30s" {
		t.Errorf("the default timeout is %s, want 30s", got)
	}

	short := openState(t, url, postgres.Options{IdleInTransaction: 300 * time.Millisecond})
	shortDB := short.SQLDB()
	defer shortDB.Close()
	if got := scalar(t, shortDB, `SHOW idle_in_transaction_session_timeout`); got != "300ms" {
		t.Errorf("the timeout is %s, want 300ms", got)
	}
	tx, err := shortDB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if _, err := tx.ExecContext(t.Context(), `SELECT 1`); err == nil {
		t.Error("a transaction idle for longer than the timeout still runs")
	}
}

// TestTheInstallationLivesInItsOwnSchema holds that opening a state migrates the installation's schema
// and leaves the public schema empty.
func TestTheInstallationLivesInItsOwnSchema(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	want, err := (postgres.Dialect{}).InstallationMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT count(*) FROM installation.schema_migrations`); got != fmt.Sprint(len(want)) {
		t.Errorf("the installation applied %s migrations, want %d", got, len(want))
	}
	if got := scalar(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`); got != "0" {
		t.Errorf("the public schema holds %s tables", got)
	}
	projects, err := state.List(t.Context())
	if err != nil || len(projects) != 0 {
		t.Errorf("a new installation lists %+v, %v", projects, err)
	}
}

// TestMigrationWaitsForAnotherProcess holds the lock deterministically: while another session holds the
// migration lock, Open does not migrate, and it does as soon as the lock goes.
func TestMigrationWaitsForAnotherProcess(t *testing.T) {
	url := freshDatabase(t)
	other, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	conn, err := other.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, postgres.MigrationLock); err != nil {
		t.Fatal(err)
	}

	opened := make(chan error, 1)
	var state *postgres.State
	go func() {
		var err error
		state, err = postgres.Open(context.Background(), url, postgres.Options{})
		opened <- err
	}()
	select {
	case err := <-opened:
		t.Fatalf("Open finished while another process held the migration lock: %v", err)
	case <-time.After(750 * time.Millisecond):
	}
	if _, err := conn.ExecContext(t.Context(), `SELECT pg_advisory_unlock($1)`, postgres.MigrationLock); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Open did not finish after the lock went")
	}
	_ = state.Close()
}

// TestTwoProcessesMigrateOneEmptyDatabase starts several processes' worth of pools on one empty database
// at once, and every version applies once, for the installation and for a project's schema.
func TestTwoProcessesMigrateOneEmptyDatabase(t *testing.T) {
	url := freshDatabase(t)
	project := ledger.Project{ID: "pa", Name: "alpha", Database: "project_pa", State: ledger.ProjectActive}
	const processes = 6
	var wg sync.WaitGroup
	states := make([]*postgres.State, processes)
	errs := make([]error, processes)
	for i := range processes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state, err := postgres.Open(context.Background(), url, postgres.Options{})
			if err != nil {
				errs[i] = err
				return
			}
			states[i] = state
			// Creating a project that exists is a retry that changes nothing, and opens its Store.
			_, errs[i] = state.Create(context.Background(), project, member)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("process %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		for _, state := range states {
			if state != nil {
				_ = state.Close()
			}
		}
	})
	if t.Failed() {
		return
	}
	db := states[0].SQLDB()
	defer db.Close()
	ledgerMigrations, err := (postgres.Dialect{}).Migrations()
	if err != nil {
		t.Fatal(err)
	}
	installationMigrations, err := sqlite.NeutralInstallationMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT count(*) FROM project_pa.schema_migrations`); got != fmt.Sprint(len(ledgerMigrations)) {
		t.Errorf("the project applied %s migrations, want %d", got, len(ledgerMigrations))
	}
	if got := scalar(t, db, `SELECT count(*) FROM installation.schema_migrations`); got != fmt.Sprint(len(installationMigrations)) {
		t.Errorf("the installation applied %s migrations, want %d", got, len(installationMigrations))
	}
}
