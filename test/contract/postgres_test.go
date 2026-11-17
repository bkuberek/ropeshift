//go:build postgres

package contract_test

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
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

// TestStoreContractPostgresProject runs every check on a project of an installation, whose schema and
// the installation's share one database (ADR-0021).
func TestStoreContractPostgresProject(t *testing.T) {
	contract.Run(t, pgtest.ProjectBackend, contract.AllChecks())
}

// TestMigrationChecksumsPostgres holds the migration checksums on Postgres, in a schema of their own.
func TestMigrationChecksumsPostgres(t *testing.T) {
	contract.RunMigrationChecks(t, pgtest.MigrationBackend)
}

// TestTwoFirstStartsAddTheChecksumColumnPostgres holds two starts at once on a database from before
// checksums: the second finds the column missing while the first adds it, waits on the first's lock, and
// still starts.
func TestTwoFirstStartsAddTheChecksumColumnPostgres(t *testing.T) {
	db := pgtest.MigrationBackend.Fresh(t)
	ctx := t.Context()
	store := sqlstore.New(db.DB, db.Dialect, sqlstore.Options{Schema: db.Schema})
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	table := db.Schema + ".schema_migrations"
	if _, err := db.DB.ExecContext(ctx, "ALTER TABLE "+table+" DROP COLUMN checksum"); err != nil {
		t.Fatal(err)
	}
	first, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback() }()
	if _, err := first.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN checksum TEXT"); err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	go func() { second <- store.Migrate(ctx) }()
	waitForLockWait(t, db.DB, second, "ALTER TABLE%")
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatalf("the second start returned %v, want it to start", err)
	}
}

// TestTwoFirstStartsMakeTheMigrationsTablePostgres holds two starts at once on a database with no table of
// applied migrations: the second begins to make the table while the first's transaction, which has not
// committed, is making it, and waits on it. When the first commits, the second finds the table there and
// still starts, where Postgres first answers it with a unique violation (SQLSTATE 23505).
func TestTwoFirstStartsMakeTheMigrationsTablePostgres(t *testing.T) {
	db := pgtest.MigrationBackend.Fresh(t)
	ctx := t.Context()
	first, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Rollback() }()
	// This is the table the first start makes, as the neutral schema's runner makes it.
	if _, err := first.ExecContext(ctx, "CREATE TABLE "+db.Schema+".schema_migrations ("+
		"version BIGINT NOT NULL PRIMARY KEY, name TEXT NOT NULL, applied_at BIGINT NOT NULL, checksum TEXT)"); err != nil {
		t.Fatal(err)
	}
	store := sqlstore.New(db.DB, db.Dialect, sqlstore.Options{Schema: db.Schema})
	second := make(chan error, 1)
	go func() { second <- store.Migrate(ctx) }()
	waitForLockWait(t, db.DB, second, "CREATE TABLE%")
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatalf("the second start returned %v, want it to start", err)
	}
}

// waitForLockWait waits until a session of the database waits on a lock while it runs a statement that
// matches one of the LIKE patterns. The patterns name statements of the start under test, so a lock wait of
// another session, such as an autovacuum's, never counts. It fails when the start returns on second first,
// or when no such session waits within 30 seconds.
func waitForLockWait(t *testing.T, db *sql.DB, second <-chan error, statementLike ...string) {
	t.Helper()
	matches := make([]string, len(statementLike))
	args := make([]any, len(statementLike))
	for i, like := range statementLike {
		matches[i] = fmt.Sprintf("query LIKE $%d", i+1)
		args[i] = like
	}
	query := `SELECT count(*) FROM pg_stat_activity
 WHERE datname = current_database() AND state = 'active' AND wait_event_type = 'Lock' AND (` + strings.Join(matches, " OR ") + `)`
	deadline := time.Now().Add(30 * time.Second)
	for waiting := 0; waiting == 0; {
		select {
		case err := <-second:
			t.Fatalf("the second start returned %v before it waited on the first", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the second start never waited on the first start's lock")
		}
		time.Sleep(10 * time.Millisecond)
		if err := db.QueryRowContext(t.Context(), query, args...).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
	}
}

// TestScopeMigrationPostgres runs the migration of ADR-0030's scope grammar on a real server.
func TestScopeMigrationPostgres(t *testing.T) {
	server, err := pgtest.Shared()
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
	defer db.Close()
	migrations, err := postgres.Dialect{}.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.ScopeRewriteCheck(t.Context(), db, scopeMigration(t, migrations)); err != nil {
		t.Fatal(err)
	}
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
	store := sqlstore.New(db, postgres.Dialect{}, sqlstore.Options{})
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	statements := sqlstore.Statements(postgres.Dialect{})
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

// TestEveryInstallationStatementPrepares sends every statement of the installation database to a real
// server.
func TestEveryInstallationStatementPrepares(t *testing.T) {
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
	installation := sqlstore.NewInstallation(db, postgres.Dialect{}, sqlstore.Options{})
	if err := installation.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	statements := sqlstore.InstallationStatements(postgres.Dialect{})
	if len(statements) < 10 {
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

// TestProjectsAreSchemasOfOneDatabase holds ADR-0021 on Postgres: each project is a schema, the
// installation's records are one more, the projects' Stores share one pool and set their schema on
// each transaction, a project cannot read another's records, and the records every project shares live
// in the installation's schema, where an upgrade moves them from a project's.
func TestProjectsAreSchemasOfOneDatabase(t *testing.T) {
	server, err := pgtest.Shared()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	name, err := server.NewDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("pgx", server.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// One state serves both projects on one pool of four connections, and the installation on a pool of its own.
	state, err := postgres.Open(ctx, server.DSN(name), postgres.Options{MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	installation := state.Installation
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	open := func(id ledger.WorkspaceID, project string) *sqlstore.Store {
		created, err := state.Create(ctx, ledger.Project{ID: id, Name: project}, actor)
		if err != nil {
			t.Fatal(err)
		}
		store, err := state.Open(ctx, created)
		if err != nil {
			t.Fatal(err)
		}
		return store.(*sqlstore.Store)
	}
	alpha := open("pa", "project-a")
	beta := open("pb", "project-b")

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store, scope := alpha, ledger.WorkspaceID("pa")
			if i%2 == 1 {
				store, scope = beta, "pb"
			}
			if _, err := control.NewWorkOrders(store).Create(ctx, scope, workorders.WorkOrder{
				ID: fmt.Sprintf("wo-%d", i), Kind: workorders.KindChange, Title: "T", MaxAttempts: 3,
			}, actor); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	count := func(query string) int64 {
		t.Helper()
		var n int64
		if err := admin.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if a, b := count("SELECT count(*) FROM project_pa.work_orders"), count("SELECT count(*) FROM project_pb.work_orders"); a != 4 || b != 4 {
		t.Errorf("project_pa holds %d work orders and project_pb %d, want 4 each", a, b)
	}
	if n := count("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'"); n != 0 {
		t.Errorf("the public schema holds %d tables, so a statement ran outside its schema", n)
	}
	if err := beta.Read(ctx, "pa", func(ports.ReadTx) error { return nil }); !errors.Is(err, ledger.ErrUnknownWorkspace) {
		t.Errorf("beta's schema opens alpha's workspace: %v", err)
	}

	err = alpha.Installation("pa").Write(ctx, "test.member", func(tx ports.SharedTx) error {
		if err := tx.Members().SaveMember(authority.MemberRecord{ID: "member-1", IdentitySubject: "local", DisplayName: "M", Status: "active", ProofDigest: "hash"}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.member", Actor: actor, Subject: ledger.Subject{Kind: "member", ID: "member-1"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := count("SELECT count(*) FROM installation.members"); n != 1 {
		t.Errorf("the installation's schema holds %d members", n)
	}
	if n := count("SELECT count(*) FROM project_pa.members"); n != 0 {
		t.Errorf("project_pa holds %d members", n)
	}
	if err := beta.Read(ctx, "pb", func(tx ports.ReadTx) error {
		_, err := tx.Members().Member("member-1")
		return err
	}); err != nil {
		t.Errorf("beta does not see the member: %v", err)
	}

	// A project of an earlier build keeps the shared records in its own schema, with no installation.
	if err := pgtest.CreateSchemas(ctx, admin, "project_c"); err != nil {
		t.Fatal(err)
	}
	legacy, err := postgres.OpenStore(server.DSN(name), postgres.Options{Store: sqlstore.Options{Schema: "project_c"}})
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if err := legacy.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.CreateWorkspace(ctx, ledger.Workspace{ID: "pc"}, actor); err != nil {
		t.Fatal(err)
	}
	err = legacy.Installation("pc").Write(ctx, "test.legacy", func(tx ports.SharedTx) error {
		if err := tx.Workers().Insert(ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-1", Name: "w"}, ProofDigest: "proof"}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.legacy", Actor: actor, Subject: ledger.Subject{Kind: "worker", ID: "worker-1"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := installation.CopyRecords(ctx, legacy, "pc", actor); err != nil {
			t.Fatal(err)
		}
		if err := installation.DropMovedRecords(ctx, legacy, "pc"); err != nil {
			t.Fatal(err)
		}
	}
	if n := count("SELECT count(*) FROM installation.workers"); n != 1 {
		t.Errorf("the upgrade moved %d workers into the installation's schema", n)
	}
	if n := count("SELECT count(*) FROM project_c.workers"); n != 0 {
		t.Errorf("project_c still holds %d workers", n)
	}
}
