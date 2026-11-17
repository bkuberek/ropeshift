//go:build postgres

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

const billing = "https://example.com/acme/billing"

// held is the entry a repository's record gives the repository index: the identity of its remote.
func held(project ledger.WorkspaceID, id, remote string) ledger.IndexedRepository {
	return ledger.IndexedRepository{Identity: ledger.RepositoryIdentity(remote, id), Project: project, Repository: id}
}

// changeRepository runs one change to a project's repository records in a write of its own, with the event
// a write needs.
func changeRepository(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, id string, change func(ports.RepositoryWriter) error) error {
	return store.Write(ctx, scope, "test.repository", func(tx ports.WriteTx) error {
		if err := change(tx.Repositories()); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: member, Subject: ledger.Subject{Kind: "repository", ID: id},
		})
		return err
	})
}

// insertRepository registers a repository record with a remote.
func insertRepository(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, id, name, remote string) error {
	return changeRepository(ctx, store, scope, id, func(w ports.RepositoryWriter) error {
		record := ledger.NewRepository(id)
		record.Name, record.Remote, record.DefaultBranch = name, remote, "main"
		_, err := w.Insert(record)
		return err
	})
}

// moveRemote has git say that a repository's remote is another.
func moveRemote(ctx context.Context, store ports.Store, scope ledger.WorkspaceID, id, remote string) error {
	return changeRepository(ctx, store, scope, id, func(w ports.RepositoryWriter) error {
		_, err := w.Detected(id, remote, "main")
		return err
	})
}

func repositoryIndex(t *testing.T, state *postgres.State) []ledger.IndexedRepository {
	t.Helper()
	index, err := state.Index(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return index
}

// TestARemoteMoveThatFailsToCommitIsRepairedByTheNextOpen holds that the index follows the records after a
// move of a repository's remote whose project commit failed, which the index side had already committed: it
// held the identity of the move that did not happen, and the identity the record still has was free for
// another project. The next Open puts the record's own identity back.
func TestARemoteMoveThatFailsToCommitIsRepairedByTheNextOpen(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r1", "invoices", invoices); err != nil {
		t.Fatal(err)
	}

	lift := deferRepositoryTrigger(t, db, "project_pa", "UPDATE", `RAISE EXCEPTION 'the commit is refused';`)
	if err := moveRemote(t.Context(), alphaStore, alpha.ID, "r1", billing); err == nil {
		t.Fatal("the project's commit was not refused")
	}
	lift()
	if got, want := repositoryIndex(t, state), []ledger.IndexedRepository{held(alpha.ID, "r1", billing)}; !slices.Equal(got, want) {
		t.Fatalf("the failed commit left the index at %+v, want %+v", got, want)
	}
	if err := registerRepository(t.Context(), betaStore, beta.ID, billing, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
		t.Fatalf("another project registering the identity of the move that did not happen: %v", err)
	}

	next := openState(t, url, postgres.Options{})
	if got, want := repositoryIndex(t, next), []ledger.IndexedRepository{held(alpha.ID, "r1", invoices)}; !slices.Equal(got, want) {
		t.Errorf("the next Open left the index at %+v, want %+v", got, want)
	}
	opened, err := next.Open(t.Context(), beta)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRepository(t.Context(), opened, beta.ID, invoices, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
		t.Errorf("another project registering the identity the record still has: %v", err)
	}
	if err := registerRepository(t.Context(), opened, beta.ID, billing, nil); err != nil {
		t.Errorf("another project registering the identity the move did not take: %v", err)
	}
}

// TestARestoreThatFailsToCommitIsRepairedByTheNextOpen holds the same for a Restore: it holds the
// repository's identity in the index before the project commits, so a failed commit leaves an entry for a
// record that is still removed, which blocks every other project. The next Open releases it.
func TestARestoreThatFailsToCommitIsRepairedByTheNextOpen(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r1", "invoices", invoices); err != nil {
		t.Fatal(err)
	}
	err := changeRepository(t.Context(), alphaStore, alpha.ID, "r1", func(w ports.RepositoryWriter) error { return w.Remove("r1") })
	if err != nil {
		t.Fatal(err)
	}
	if got := repositoryIndex(t, state); len(got) != 0 {
		t.Fatalf("a removed repository holds %+v", got)
	}

	lift := deferRepositoryTrigger(t, db, "project_pa", "UPDATE", `RAISE EXCEPTION 'the commit is refused';`)
	err = changeRepository(t.Context(), alphaStore, alpha.ID, "r1", func(w ports.RepositoryWriter) error { return w.Restore("r1") })
	if err == nil {
		t.Fatal("the project's commit was not refused")
	}
	lift()
	if got, want := repositoryIndex(t, state), []ledger.IndexedRepository{held(alpha.ID, "r1", invoices)}; !slices.Equal(got, want) {
		t.Fatalf("the failed commit left the index at %+v, want %+v", got, want)
	}
	if err := registerRepository(t.Context(), betaStore, beta.ID, invoices, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
		t.Fatalf("another project registering the repository of a record that is still removed: %v", err)
	}

	next := openState(t, url, postgres.Options{})
	if got := repositoryIndex(t, next); len(got) != 0 {
		t.Errorf("the next Open left the index at %+v", got)
	}
	opened, err := next.Open(t.Context(), beta)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRepository(t.Context(), opened, beta.ID, invoices, nil); err != nil {
		t.Errorf("registering the repository after the repair: %v", err)
	}
}

// TestTheRepairOfAProjectRunsInAnOpenState holds the repair a caller can run when a write of a project
// fails, without opening a state again: it takes the migration lock, and it brings the project's part of
// the index into agreement with its records. A project whose index is in agreement is left alone.
func TestTheRepairOfAProjectRunsInAnOpenState(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r1", "invoices", invoices); err != nil {
		t.Fatal(err)
	}
	if err := changeRepository(t.Context(), alphaStore, alpha.ID, "r1", func(w ports.RepositoryWriter) error { return w.Remove("r1") }); err != nil {
		t.Fatal(err)
	}
	lift := deferRepositoryTrigger(t, db, "project_pa", "UPDATE", `RAISE EXCEPTION 'the commit is refused';`)
	if err := changeRepository(t.Context(), alphaStore, alpha.ID, "r1", func(w ports.RepositoryWriter) error { return w.Restore("r1") }); err == nil {
		t.Fatal("the project's commit was not refused")
	}
	lift()

	release := holdMigrationLock(t, url)
	repaired := make(chan error, 1)
	go func() { repaired <- state.RepairProjectIndex(t.Context(), alpha) }()
	select {
	case err := <-repaired:
		t.Fatalf("the repair finished while another session held the migration lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	release()
	if err := <-repaired; err != nil {
		t.Fatal(err)
	}
	if got := repositoryIndex(t, state); len(got) != 0 {
		t.Errorf("the repair left the index at %+v, with the record still removed", got)
	}
	if err := registerRepository(t.Context(), betaStore, beta.ID, invoices, nil); err != nil {
		t.Errorf("registering the repository after the repair: %v", err)
	}
	if err := state.RepairProjectIndex(t.Context(), alpha); err != nil {
		t.Errorf("a repair of a project in agreement: %v", err)
	}
	if got, want := repositoryIndex(t, state), []ledger.IndexedRepository{held(beta.ID, "r-pb", invoices)}; !slices.Equal(got, want) {
		t.Errorf("a repair of a project in agreement left the index at %+v, want %+v", got, want)
	}
}

// commitHold is the advisory lock a held commit waits for.
const commitHold int64 = 0x636f6d6d6974

// holdCommits makes the commit of every write of a project's repository records wait for an advisory lock
// that the test holds. By then the write has committed its part of the repository index, and it still holds
// the project's revision. The returned function lets the commits finish, and a test that returns first
// lets them finish too.
func holdCommits(t *testing.T, url string, db *sql.DB, schema, event string) (release func()) {
	t.Helper()
	_, conn := otherSession(t, url)
	if _, err := conn.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, commitHold); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			if _, err := conn.ExecContext(context.WithoutCancel(t.Context()), `SELECT pg_advisory_unlock($1)`, commitHold); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(release)
	deferRepositoryTrigger(t, db, schema, event, fmt.Sprintf(`PERFORM pg_advisory_xact_lock(%d);`, commitHold))
	return release
}

// waitForIndex returns once the repository index reads as want.
func waitForIndex(t *testing.T, state *postgres.State, want []ledger.IndexedRepository) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); !slices.Equal(repositoryIndex(t, state), want); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the index never read %+v", want)
		}
	}
}

// opened is what postgres.Open returned on a goroutine.
type opened struct {
	state *postgres.State
	err   error
}

// openingState opens a state on a goroutine, as a second process starting does.
func openingState(t *testing.T, url string) <-chan opened {
	t.Helper()
	out := make(chan opened, 1)
	go func() {
		state, err := postgres.Open(t.Context(), url, postgres.Options{})
		out <- opened{state, err}
	}()
	return out
}

// finishOpening waits for a state that openingState opens, and closes it with the test.
func finishOpening(t *testing.T, out <-chan opened) *postgres.State {
	t.Helper()
	got := <-out
	if got.err != nil {
		t.Fatal(got.err)
	}
	t.Cleanup(func() { _ = got.state.Close() })
	return got.state
}

// waitForRepairToWait returns once a session of the database waits for a row lock in a statement that takes
// one, as the repair of the repository index does while a write holds the revision of a project. It fails
// if the Open that runs the repair returns first, which would be an Open that did not wait for the write.
func waitForRepairToWait(t *testing.T, db *sql.DB, starting <-chan opened) {
	t.Helper()
	const query = `SELECT count(*) FROM pg_stat_activity
 WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FOR UPDATE%'`
	for deadline := time.Now().Add(15 * time.Second); scalar(t, db, query) == "0"; time.Sleep(5 * time.Millisecond) {
		select {
		case got := <-starting:
			if got.state != nil {
				_ = got.state.Close()
			}
			t.Fatalf("Open returned without waiting for the write's revision: %v", got.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no session waits for the revision of the project")
		}
	}
}

// TestARebuildLeavesTheEntriesOfAMoveThatIsCommitting holds that a write in the middle of its commit keeps
// its entries: the rebuild waits for the project's revision, which the write holds until it commits, and
// then finds the record at the identity the write gave it. The test lets the write finish only once the
// rebuild is waiting for it.
func TestARebuildLeavesTheEntriesOfAMoveThatIsCommitting(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r1", "invoices", invoices); err != nil {
		t.Fatal(err)
	}
	release := holdCommits(t, url, db, "project_pa", "UPDATE")

	moved := make(chan error, 1)
	go func() { moved <- moveRemote(t.Context(), alphaStore, alpha.ID, "r1", billing) }()
	// The index shows the move once the write committed it, with the project's commit still to come.
	want := []ledger.IndexedRepository{held(alpha.ID, "r1", billing)}
	waitForIndex(t, state, want)
	next := openingState(t, url)
	waitForRepairToWait(t, db, next)
	release()
	if err := <-moved; err != nil {
		t.Fatal(err)
	}
	if got := repositoryIndex(t, finishOpening(t, next)); !slices.Equal(got, want) {
		t.Errorf("the index after the write committed: %+v, want %+v", got, want)
	}
}

// TestAnEntryThatNeverCommittedDoesNotTakeTheIdentityFromALaterRecord holds that a record whose repository
// another record's entry holds keeps it: a record of the project that failed to commit left its entry, and
// a record of the same repository that committed after it has no entry of its own, because the project
// already held the identity. The repair names the record that exists, and never leaves the identity free.
func TestAnEntryThatNeverCommittedDoesNotTakeTheIdentityFromALaterRecord(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, _ := createProject(t, state, "pb", "beta")

	lift := deferRepositoryTrigger(t, db, "project_pa", "INSERT", `RAISE EXCEPTION 'the commit is refused';`)
	if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r-x", "x", invoices); err == nil {
		t.Fatal("the project's commit was not refused")
	}
	lift()
	if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r-y", "y", invoices); err != nil {
		t.Fatal(err)
	}
	if got, want := repositoryIndex(t, state), []ledger.IndexedRepository{held(alpha.ID, "r-x", invoices)}; !slices.Equal(got, want) {
		t.Fatalf("the index reads %+v, want the entry of the record that never committed, %+v", got, want)
	}

	next := openState(t, url, postgres.Options{})
	if got, want := repositoryIndex(t, next), []ledger.IndexedRepository{held(alpha.ID, "r-y", invoices)}; !slices.Equal(got, want) {
		t.Errorf("the next Open left the index at %+v, want %+v", got, want)
	}
	opened, err := next.Open(t.Context(), beta)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerRepository(t.Context(), opened, beta.ID, invoices, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
		t.Errorf("another project registering the repository a record still holds: %v", err)
	}
}

// TestAnEntryTheRemovalOfARecordTookIsPutBack holds the other direction: a record of a project loses its
// entry when another record of the same repository, which took the entry first, is removed, and when the
// removal's project commit fails after the index side committed. The next Open holds the identity again for
// the record that is still in the project.
func TestAnEntryTheRemovalOfARecordTookIsPutBack(t *testing.T) {
	remove := func(id string) func(ports.RepositoryWriter) error {
		return func(w ports.RepositoryWriter) error { return w.Remove(id) }
	}
	t.Run("a removal whose commit failed", func(t *testing.T) {
		url := freshDatabase(t)
		state := openState(t, url, postgres.Options{})
		db := state.SQLDB()
		defer db.Close()
		alpha, alphaStore := createProject(t, state, "pa", "alpha")
		beta, _ := createProject(t, state, "pb", "beta")
		if err := insertRepository(t.Context(), alphaStore, alpha.ID, "r1", "invoices", invoices); err != nil {
			t.Fatal(err)
		}
		lift := deferRepositoryTrigger(t, db, "project_pa", "UPDATE", `RAISE EXCEPTION 'the commit is refused';`)
		if err := changeRepository(t.Context(), alphaStore, alpha.ID, "r1", remove("r1")); err == nil {
			t.Fatal("the project's commit was not refused")
		}
		lift()
		if got := repositoryIndex(t, state); len(got) != 0 {
			t.Fatalf("the failed removal left the index at %+v, with the record still in the project", got)
		}

		next := openState(t, url, postgres.Options{})
		if got, want := repositoryIndex(t, next), []ledger.IndexedRepository{held(alpha.ID, "r1", invoices)}; !slices.Equal(got, want) {
			t.Errorf("the next Open left the index at %+v, want %+v", got, want)
		}
		opened, err := next.Open(t.Context(), beta)
		if err != nil {
			t.Fatal(err)
		}
		if err := registerRepository(t.Context(), opened, beta.ID, invoices, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
			t.Errorf("another project registering the repository of a record that is in its project: %v", err)
		}
	})
	t.Run("a removal of a record whose repository another record holds", func(t *testing.T) {
		url := freshDatabase(t)
		state := openState(t, url, postgres.Options{})
		alpha, store := createProject(t, state, "pa", "alpha")
		beta, _ := createProject(t, state, "pb", "beta")
		if err := insertRepository(t.Context(), store, alpha.ID, "r-x", "x", invoices); err != nil {
			t.Fatal(err)
		}
		if err := insertRepository(t.Context(), store, alpha.ID, "r-y", "y", invoices+".git"); err != nil {
			t.Fatal(err)
		}
		if err := changeRepository(t.Context(), store, alpha.ID, "r-x", remove("r-x")); err != nil {
			t.Fatal(err)
		}
		if got := repositoryIndex(t, state); len(got) != 0 {
			t.Fatalf("the removal left the index at %+v, with a record of the repository in the project", got)
		}

		next := openState(t, url, postgres.Options{})
		if got, want := repositoryIndex(t, next), []ledger.IndexedRepository{held(alpha.ID, "r-y", invoices)}; !slices.Equal(got, want) {
			t.Errorf("the next Open left the index at %+v, want %+v", got, want)
		}
		opened, err := next.Open(t.Context(), beta)
		if err != nil {
			t.Fatal(err)
		}
		if err := registerRepository(t.Context(), opened, beta.ID, invoices, nil); !errors.Is(err, ledger.ErrRepositoryHeld) {
			t.Errorf("another project registering the repository of a record that is in its project: %v", err)
		}
	})
}

// TestARebuildKeepsAnEntryThatNamesAnotherRecordOfTheSameRepository holds that two records of one
// repository in a project, which hold one entry between them, are in agreement, so Open leaves the entry
// with the record that took it first.
func TestARebuildKeepsAnEntryThatNamesAnotherRecordOfTheSameRepository(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	alpha, store := createProject(t, state, "pa", "alpha")
	if err := insertRepository(t.Context(), store, alpha.ID, "r-b", "second clone", invoices); err != nil {
		t.Fatal(err)
	}
	if err := insertRepository(t.Context(), store, alpha.ID, "r-a", "first clone", invoices+".git"); err != nil {
		t.Fatal(err)
	}
	want := []ledger.IndexedRepository{held(alpha.ID, "r-b", invoices)}
	if got := repositoryIndex(t, state); !slices.Equal(got, want) {
		t.Fatalf("two records of one repository left the index at %+v, want %+v", got, want)
	}

	next := openState(t, url, postgres.Options{})
	if got := repositoryIndex(t, next); !slices.Equal(got, want) {
		t.Errorf("the next Open left the index at %+v, want %+v", got, want)
	}
}
