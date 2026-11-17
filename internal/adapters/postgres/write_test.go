//go:build postgres

package postgres_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// registerRepository is a project write that reads a record every project shares, which runs on the
// installation's side, and then registers a repository, which writes the installation's repository index.
// between, when given, runs after the read and before the registration.
func registerRepository(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, remote string, between func()) error {
	return store.Write(ctx, scope, "test.register", func(tx ports.WriteTx) error {
		if _, err := tx.Members().CountMembers(); err != nil {
			return err
		}
		if between != nil {
			between()
		}
		record := ledger.NewRepository("r-" + string(scope))
		record.Name, record.Remote, record.DefaultBranch = "repo", remote, "main"
		if _, err := tx.Repositories().Insert(record); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: member, Subject: ledger.Subject{Kind: "repository", ID: record.ID},
		})
		return err
	})
}

// TestAProjectWriteHoldsOneInstallationConnection holds that a write that reads the shared records and then
// writes the repository index holds one connection of the installation's pool at a time. With a pool of two,
// two such writes in step each hold a connection when they reach the index, and a write that needs a second
// waits for the other's, which waits for the first's.
func TestAProjectWriteHoldsOneInstallationConnection(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{MaxConns: 2})
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var step sync.WaitGroup
	step.Add(2)
	inStep := func() {
		step.Done()
		step.Wait()
	}
	writes := []struct {
		store  ports.Store
		scope  ledger.WorkspaceID
		remote string
	}{
		{alphaStore, alpha.ID, "https://example.com/acme/invoices"},
		{betaStore, beta.ID, "https://example.com/acme/billing"},
	}
	errs := make([]error, len(writes))
	var wg sync.WaitGroup
	for i, w := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = registerRepository(ctx, w.store, w.scope, w.remote, inStep)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("write %d: %v", i, err)
		}
	}
	if index, err := state.Index(t.Context()); err != nil || len(index) != len(writes) {
		t.Errorf("index %+v, %v", index, err)
	}
}

// TestManyWritesThatTouchTheIndexShareASmallPool runs more such writes than the pool has connections, with
// no step between them. Every write finishes.
func TestManyWritesThatTouchTheIndexShareASmallPool(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{MaxConns: 2})
	const projects = 6
	scopes := make([]ledger.WorkspaceID, projects)
	stores := make([]ports.Store, projects)
	for i := range projects {
		scopes[i] = ledger.WorkspaceID(fmt.Sprintf("p%d", i))
		_, stores[i] = createProject(t, state, scopes[i], fmt.Sprintf("project-%d", i))
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			remote := fmt.Sprintf("https://example.com/acme/repo-%d", i)
			if err := registerRepository(ctx, stores[i], scopes[i], remote, nil); err != nil {
				t.Errorf("project %s: %v", scopes[i], err)
			}
		}()
	}
	wg.Wait()
	if index, err := state.Index(t.Context()); err != nil || len(index) != projects {
		t.Errorf("index %+v, %v", index, err)
	}
}

// TestTheInstallationSessionEndsBeforeTheProjectCommits holds that a write gives up its installation
// connection as soon as its function returns, and does not hold it through the project's commit. A commit
// can take a while, and a session left idle in a transaction for that long is one the server may end.
func TestTheInstallationSessionEndsBeforeTheProjectCommits(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, store := createProject(t, state, "pa", "alpha")
	// A deferred trigger runs when the transaction commits, which makes the project's commit slow.
	for _, statement := range []string{
		`CREATE FUNCTION project_pa.slow_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1.5); RETURN NULL; END $$`,
		`CREATE CONSTRAINT TRIGGER slow_commit AFTER INSERT ON project_pa.events DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION project_pa.slow_commit()`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}

	var committing, idleWhileCommitting int
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(25 * time.Millisecond):
			}
			var commits, idle int
			err := db.QueryRowContext(t.Context(), `SELECT
  count(*) FILTER (WHERE state = 'active' AND lower(query) = 'commit'),
  count(*) FILTER (WHERE state = 'idle in transaction')
FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`).Scan(&commits, &idle)
			if err != nil {
				return
			}
			if commits > 0 {
				committing++
				idleWhileCommitting = max(idleWhileCommitting, idle)
			}
		}
	}()
	err := store.Write(t.Context(), alpha.ID, "test.read", func(tx ports.WriteTx) error {
		if _, err := tx.Members().CountMembers(); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: member, Subject: ledger.Subject{Kind: "repository", ID: "r1"},
		})
		return err
	})
	close(stop)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if committing == 0 {
		t.Fatal("the poll never saw the project's commit run")
	}
	if idleWhileCommitting != 0 {
		t.Errorf("%d sessions sat idle in a transaction while the project committed", idleWhileCommitting)
	}
}

// TestAStatementLongerThanTheIdleTimeoutBetweenTwoInstallationReadsFailsTheWrite holds the bound a write
// has: the installation's session and the project's both sit inside a transaction, and each is idle while
// the other runs a statement. A project statement that runs longer than the idle timeout between two uses
// of the installation ends the installation's session, and the write fails whole.
func TestAStatementLongerThanTheIdleTimeoutBetweenTwoInstallationReadsFailsTheWrite(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{IdleInTransaction: 400 * time.Millisecond})
	db := state.SQLDB()
	defer db.Close()
	alpha, store := createProject(t, state, "pa", "alpha")
	for _, statement := range []string{
		`CREATE FUNCTION project_pa.slow_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(1); RETURN NEW; END $$`,
		`CREATE TRIGGER slow_insert BEFORE INSERT ON project_pa.events FOR EACH ROW EXECUTE FUNCTION project_pa.slow_insert()`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	err := store.Write(t.Context(), alpha.ID, "test.slow", func(tx ports.WriteTx) error {
		if _, err := tx.Members().CountMembers(); err != nil {
			return err
		}
		if _, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: member, Subject: ledger.Subject{Kind: "repository", ID: "r1"},
		}); err != nil {
			return err
		}
		_, err := tx.Members().CountMembers()
		return err
	})
	if err == nil {
		t.Fatal("a write whose installation session sat idle past the timeout finished")
	}
	if got := scalar(t, db, `SELECT count(*) FROM project_pa.events WHERE type = $1`, ledger.EventRepositoryDetected); got != "0" {
		t.Errorf("the failed write left %s events", got)
	}
}
