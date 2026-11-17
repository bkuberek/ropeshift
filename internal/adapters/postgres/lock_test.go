//go:build postgres

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

// otherSession is a connection of a process that is not the state under test.
func otherSession(t testing.TB, url string) (*sql.DB, *sql.Conn) {
	t.Helper()
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = db.Close()
	})
	return db, conn
}

// holdMigrationLock takes the migration lock on a session of its own, as another process in the middle of
// a migration does, and returns what gives it up. A test that returns first gives it up too.
func holdMigrationLock(t testing.TB, url string) (release func()) {
	t.Helper()
	_, conn := otherSession(t, url)
	if _, err := conn.ExecContext(t.Context(), `SELECT pg_advisory_lock($1)`, postgres.MigrationLock); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			if _, err := conn.ExecContext(context.WithoutCancel(t.Context()), `SELECT pg_advisory_unlock($1)`, postgres.MigrationLock); err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(release)
	return release
}

// waitForLockWaiter returns once a caller of the state waits for the migration lock, which it does with a
// connection of the pool in hand.
func waitForLockWaiter(t testing.TB, state *postgres.State) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for state.AcquiredConns() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("no caller waits for the lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// opening opens a project's Store on a goroutine and returns what Open returns on a channel.
func opening(ctx context.Context, state *postgres.State, p ledger.Project) <-chan error {
	out := make(chan error, 1)
	go func() {
		_, err := state.Open(ctx, p)
		out <- err
	}()
	return out
}

// TestACachedOpenDoesNotWaitForTheMigrationLock holds that a project whose Store the state already keeps is
// returned at once, while another caller's Open of a project it does not keep waits for a lock another
// process holds.
func TestACachedOpenDoesNotWaitForTheMigrationLock(t *testing.T) {
	url := freshDatabase(t)
	first := openState(t, url, postgres.Options{})
	alpha, _ := createProject(t, first, "pa", "alpha")
	beta, _ := createProject(t, first, "pb", "beta")

	// A second state is a second process, whose cache starts empty.
	state := openState(t, url, postgres.Options{})
	if _, err := state.Open(t.Context(), alpha); err != nil {
		t.Fatal(err)
	}
	holdMigrationLock(t, url)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiting := opening(ctx, state, beta)
	waitForLockWaiter(t, state)

	cached := opening(t.Context(), state, alpha)
	select {
	case err := <-cached:
		if err != nil {
			t.Errorf("the cached project: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("a cached project's Open waited behind another project's wait for the lock")
	}
	cancel()
	t.Logf("the Open that waited ended with %v", <-waiting)
}

// TestACallerThatGivesUpWaitingForTheLockReturnsAtOnce holds that a caller whose context ends returns,
// although another caller in the same process already waits for the lock ahead of it.
func TestACallerThatGivesUpWaitingForTheLockReturnsAtOnce(t *testing.T) {
	url := freshDatabase(t)
	first := openState(t, url, postgres.Options{})
	beta, _ := createProject(t, first, "pb", "beta")
	gamma, _ := createProject(t, first, "pc", "gamma")

	state := openState(t, url, postgres.Options{})
	holdMigrationLock(t, url)
	ahead, stop := context.WithCancel(t.Context())
	defer stop()
	waiting := opening(ahead, state, beta)
	waitForLockWaiter(t, state)

	behind, giveUp := context.WithCancel(t.Context())
	defer giveUp()
	second := opening(behind, state, gamma)
	time.Sleep(200 * time.Millisecond)
	giveUp()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a caller that gave up: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("a caller whose context ended still waits for the lock")
	}
	stop()
	t.Logf("the Open that waited ended with %v", <-waiting)
}

// failingMigration is a change to a project's ledger that the server refuses.
var failingMigration = sqlstore.Migration{Version: 9999, Name: "9999_fails", SQL: `SELECT * FROM no_such_table`}

// failingDialect is the Postgres dialect with a migration that fails added to every project's ledger.
type failingDialect struct{ postgres.Dialect }

func (d failingDialect) Migrations() ([]sqlstore.Migration, error) {
	list, err := d.Dialect.Migrations()
	return append(list, failingMigration), err
}

// TestAFailedMigrationReleasesTheLock holds that the lock goes with the caller that failed, on the pool's
// connection that held it, and does not wait for the connection to close.
func TestAFailedMigrationReleasesTheLock(t *testing.T) {
	url := freshDatabase(t)
	healthy := openState(t, url, postgres.Options{})
	alpha, _ := createProject(t, healthy, "pa", "alpha")
	state := openState(t, url, postgres.Options{Dialect: failingDialect{}})
	_, conn := otherSession(t, url)

	free := func(what string) {
		t.Helper()
		var got bool
		if err := conn.QueryRowContext(t.Context(), `SELECT pg_try_advisory_lock($1)`, postgres.MigrationLock).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Errorf("the migration lock is still held after %s", what)
			return
		}
		if _, err := conn.ExecContext(t.Context(), `SELECT pg_advisory_unlock($1)`, postgres.MigrationLock); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.Open(t.Context(), alpha); err == nil {
		t.Fatal("a project opened with a migration that fails")
	}
	free("Open of a project")
	if _, err := state.Create(t.Context(), ledger.Project{ID: "pb", Name: "beta"}, member); err == nil {
		t.Fatal("a project was created with a migration that fails")
	}
	free("Create of a project")
}

// hookedDialect is the Postgres dialect with a function that runs when a project's migrations are read,
// which a Create does under the migration lock.
type hookedDialect struct {
	postgres.Dialect
	onMigrations func()
}

func (d *hookedDialect) Migrations() ([]sqlstore.Migration, error) {
	if hook := d.onMigrations; hook != nil {
		d.onMigrations = nil
		hook()
	}
	return d.Dialect.Migrations()
}

// endLockHolder ends the server session that holds the migration lock in the test's database, and returns
// once the server has dropped it, as a lost connection does.
func endLockHolder(t testing.TB, admin *sql.DB) {
	t.Helper()
	var pid int
	err := admin.QueryRowContext(t.Context(), `SELECT pid FROM pg_locks
 WHERE locktype = 'advisory' AND granted AND (classid::bigint << 32 | objid::bigint) = $1
   AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`, postgres.MigrationLock).Scan(&pid)
	if err != nil {
		t.Fatalf("no session holds the migration lock: %v", err)
	}
	if _, err := admin.ExecContext(t.Context(), `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var alive int
		if err := admin.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&alive); err != nil {
			t.Fatal(err)
		}
		if alive == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the session that held the migration lock is still there")
		}
	}
}

// TestAFailedUnlockLeavesNoClosedStoreInTheCache holds that a Create whose lock cannot be released, because
// its session is gone, caches no Store: it closes the one it made, and the Open that follows opens a
// working one.
func TestAFailedUnlockLeavesNoClosedStoreInTheCache(t *testing.T) {
	url := freshDatabase(t)
	dialect := &hookedDialect{}
	state := openState(t, url, postgres.Options{Dialect: dialect})
	admin, _ := otherSession(t, url)
	dialect.onMigrations = func() { endLockHolder(t, admin) }

	if _, err := state.Create(t.Context(), ledger.Project{ID: "pa", Name: "alpha"}, member); err == nil {
		t.Fatal("Create reported no failure although its session was gone")
	}
	// The registry row was committed under the lock, so the project exists.
	alpha, err := state.Named(t.Context(), "alpha")
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), alpha)
	if err != nil {
		t.Fatal(err)
	}
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")
	if got := workOrderIDs(t, store, alpha.ID); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("work orders %v", got)
	}
}

// TestTheLockTimeoutAlsoBoundsTheWaitBehindACallerOfTheSameProcess holds that the deadline covers the whole
// wait, behind another caller of this process as well as behind another session, and that the error
// does not put the wait on another session.
func TestTheLockTimeoutAlsoBoundsTheWaitBehindACallerOfTheSameProcess(t *testing.T) {
	url := freshDatabase(t)
	first := openState(t, url, postgres.Options{})
	beta, _ := createProject(t, first, "pb", "beta")
	gamma, _ := createProject(t, first, "pc", "gamma")
	state := openState(t, url, postgres.Options{LockTimeout: 600 * time.Millisecond})
	holdMigrationLock(t, url)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ahead := opening(ctx, state, beta)
	waitForLockWaiter(t, state)
	behind := opening(ctx, state, gamma)
	for name, waiter := range map[string]<-chan error{"the caller ahead": ahead, "the caller behind it": behind} {
		err := <-waiter
		if !errors.Is(err, postgres.ErrLockTimeout) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(err.Error(), "another session") {
			t.Errorf("%s: the error puts the wait on another session: %v", name, err)
		}
	}
}

// TestAWaitForTheMigrationLockHasADeadline holds that a session that never lets go of the lock fails
// the caller with an error that says so, for Open of a state and for Open of a project.
func TestAWaitForTheMigrationLockHasADeadline(t *testing.T) {
	url := freshDatabase(t)
	first := openState(t, url, postgres.Options{})
	beta, _ := createProject(t, first, "pb", "beta")
	state := openState(t, url, postgres.Options{LockTimeout: 300 * time.Millisecond})
	holdMigrationLock(t, url)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if _, err := state.Open(ctx, beta); !errors.Is(err, postgres.ErrLockTimeout) {
		t.Errorf("Open of a project: %v", err)
	}
	if _, err := postgres.Open(ctx, url, postgres.Options{LockTimeout: 300 * time.Millisecond}); !errors.Is(err, postgres.ErrLockTimeout) {
		t.Errorf("Open of a state: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the two waits took %s", took)
	}
}
