//go:build postgres

package postgres_test

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/ledger"
)

// deferRepositoryTrigger runs a function in a project's schema when a transaction that inserted a
// repository commits, which is after the write has committed its entry in the repository index. The body
// is the plpgsql the function runs. It returns what removes the trigger.
func deferRepositoryTrigger(t *testing.T, db *sql.DB, schema, body string) (lift func()) {
	t.Helper()
	for _, statement := range []string{
		fmt.Sprintf(`CREATE FUNCTION %s.at_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN %s RETURN NULL; END $$`, schema, body),
		fmt.Sprintf(`CREATE CONSTRAINT TRIGGER at_commit AFTER INSERT ON %s.repositories
  DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION %s.at_commit()`, schema, schema),
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		if _, err := db.ExecContext(t.Context(), fmt.Sprintf(`DROP TRIGGER at_commit ON %s.repositories`, schema)); err != nil {
			t.Fatal(err)
		}
	}
}

const invoices = "https://example.com/acme/invoices"

// TestAnEntryWhoseRecordNeverCommittedIsReleasedByTheNextOpen holds the repair of the repository index: a
// project write enters its repository in the index in a transaction that commits just before the
// project's, so a commit of the project's that fails leaves an entry whose record is missing, which blocks
// every other project from registering the repository. The next Open of a state releases it.
func TestAnEntryWhoseRecordNeverCommittedIsReleasedByTheNextOpen(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")

	lift := deferRepositoryTrigger(t, db, "project_pa", `RAISE EXCEPTION 'the commit is refused';`)
	if err := registerRepository(t.Context(), alphaStore, alpha.ID, invoices, nil); err == nil {
		t.Fatal("the project's commit was not refused")
	}
	lift()
	index, err := state.Index(t.Context())
	if err != nil || len(index) != 1 || index[0].Project != alpha.ID {
		t.Fatalf("the failed commit left the index at %+v, %v", index, err)
	}
	if err := registerRepository(t.Context(), betaStore, beta.ID, invoices, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
		t.Fatalf("another project registering the repository: %v", err)
	}

	next := openState(t, url, postgres.Options{})
	if index, err := next.Index(t.Context()); err != nil || len(index) != 0 {
		t.Errorf("the next Open left the index at %+v, %v", index, err)
	}
	opened, err := next.Open(t.Context(), beta)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRepository(t.Context(), opened, beta.ID, invoices, nil); err != nil {
		t.Errorf("registering the repository after the repair: %v", err)
	}
}

// TestAReconcileLeavesTheEntryOfAWriteThatIsCommitting holds that the repair never releases an entry whose
// record is about to commit: a write that holds its project's revision commits its entry first and its
// record after, and the repair waits for that revision, then finds the record.
func TestAReconcileLeavesTheEntryOfAWriteThatIsCommitting(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	deferRepositoryTrigger(t, db, "project_pa", `PERFORM pg_sleep(1.5);`)

	written := make(chan error, 1)
	go func() { written <- registerRepository(t.Context(), alphaStore, alpha.ID, invoices, nil) }()
	// The entry shows once the write committed it, with the project's commit still to come.
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if index, err := state.Index(t.Context()); err == nil && len(index) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the write never entered the index")
		}
	}
	next := openState(t, url, postgres.Options{})
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if index, err := next.Index(t.Context()); err != nil || len(index) != 1 || index[0].Project != alpha.ID {
		t.Errorf("the index after the write committed: %+v, %v", index, err)
	}
}

// TestOpenReleasesOnlyTheEntriesWithNoRecord holds that an entry whose record exists stays, and that an entry
// of a project the registry does not hold goes, with one whose project holds no such record.
func TestOpenReleasesOnlyTheEntriesWithNoRecord(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	alpha, store := createProject(t, state, "pa", "alpha")
	if err := registerRepository(t.Context(), store, alpha.ID, invoices, nil); err != nil {
		t.Fatal(err)
	}
	index, err := state.Index(t.Context())
	if err != nil || len(index) != 1 {
		t.Fatalf("index %+v, %v", index, err)
	}
	kept := index[0]
	seeded := []ledger.IndexedRepository{
		kept,
		{Identity: "example.com/acme/gone", Project: alpha.ID, Repository: "r-gone"},
		{Identity: "example.com/acme/ghost", Project: "pz", Repository: "r-ghost"},
	}
	if _, err := state.Installation.IndexRepositories(t.Context(), seeded); err != nil {
		t.Fatal(err)
	}

	next := openState(t, url, postgres.Options{})
	if got, err := next.Index(t.Context()); err != nil || len(got) != 1 || got[0] != kept {
		t.Errorf("the index after Open: %+v, %v, want only %+v", got, err, kept)
	}
}
