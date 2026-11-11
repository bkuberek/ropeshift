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
	installation := sqlstore.NewInstallation(db, pgtest.Dialect{}, sqlstore.Options{})
	if err := installation.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	statements := sqlstore.InstallationStatements(pgtest.Dialect{})
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
	if err := pgtest.CreateSchemas(ctx, admin, "installation", "project_a", "project_b", "project_c"); err != nil {
		t.Fatal(err)
	}
	installationDB, err := sql.Open("pgx", server.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	installation := sqlstore.NewInstallation(installationDB, pgtest.Dialect{}, sqlstore.Options{Schema: "installation"})
	defer installation.Close()
	if err := installation.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	projects, err := sql.Open("pgx", server.DSN(name))
	if err != nil {
		t.Fatal(err)
	}
	defer projects.Close()
	projects.SetMaxOpenConns(4)
	actor := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	open := func(id ledger.WorkspaceID, schema string, shared *sqlstore.Installation) *sqlstore.Store {
		store := sqlstore.New(projects, pgtest.Dialect{}, sqlstore.Options{Schema: schema, Installation: shared})
		if err := store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: id, Name: schema}, actor); err != nil {
			t.Fatal(err)
		}
		if _, _, err := installation.CreateProject(ctx, ledger.Project{ID: id, Name: strings.ReplaceAll(schema, "_", "-"), Database: schema}, actor); err != nil {
			t.Fatal(err)
		}
		return store
	}
	alpha := open("pa", "project_a", installation)
	beta := open("pb", "project_b", installation)

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
	if a, b := count("SELECT count(*) FROM project_a.work_orders"), count("SELECT count(*) FROM project_b.work_orders"); a != 4 || b != 4 {
		t.Errorf("project_a holds %d work orders and project_b %d, want 4 each", a, b)
	}
	if n := count("SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'"); n != 0 {
		t.Errorf("the public schema holds %d tables, so a statement ran outside its schema", n)
	}
	if err := beta.Read(ctx, "pa", func(ports.ReadTx) error { return nil }); !errors.Is(err, ledger.ErrUnknownWorkspace) {
		t.Errorf("beta's schema opens alpha's workspace: %v", err)
	}

	err = alpha.Write(ctx, "pa", "test.member", func(tx ports.WriteTx) error {
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
	if n := count("SELECT count(*) FROM project_a.members"); n != 0 {
		t.Errorf("project_a holds %d members", n)
	}
	if err := beta.Read(ctx, "pb", func(tx ports.ReadTx) error {
		_, err := tx.Members().Member("member-1")
		return err
	}); err != nil {
		t.Errorf("beta does not see the member: %v", err)
	}

	legacy := sqlstore.New(projects, pgtest.Dialect{}, sqlstore.Options{Schema: "project_c"})
	if err := legacy.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.CreateWorkspace(ctx, ledger.Workspace{ID: "pc"}, actor); err != nil {
		t.Fatal(err)
	}
	err = legacy.Write(ctx, "pc", "test.legacy", func(tx ports.WriteTx) error {
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
