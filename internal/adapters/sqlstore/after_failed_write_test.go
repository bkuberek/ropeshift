package sqlstore_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var errCommit = errors.New("the commit is refused")

// afterFailedWrite is one call of Options.AfterFailedWrite, with what the repository index held when it came.
type afterFailedWrite struct {
	project   ledger.WorkspaceID
	cancelled bool
	index     []ledger.IndexedRepository
}

// writeRig is a project of an installation on SQLite whose Store reports each call of AfterFailedWrite.
type writeRig struct {
	store        *sqlstore.Store
	installation *sqlstore.Installation
	calls        []afterFailedWrite
}

const rigScope ledger.WorkspaceID = "w1"

func newWriteRig(t *testing.T) *writeRig {
	t.Helper()
	dir := t.TempDir()
	installation, err := sqlite.OpenInstallation(filepath.Join(dir, sqlite.InstallationFileName), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = installation.Close() })
	if err := installation.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	rig := &writeRig{installation: installation}
	store, err := sqlite.Open(filepath.Join(dir, "ledger.db"), sqlstore.Options{
		Installation: installation,
		AfterFailedWrite: func(ctx context.Context, project ledger.WorkspaceID) {
			index, err := installation.RepositoryIndex(ctx)
			if err != nil {
				t.Error(err)
			}
			rig.calls = append(rig.calls, afterFailedWrite{project: project, cancelled: ctx.Err() != nil, index: index})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: rigScope, Name: "W"}, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}); err != nil {
		t.Fatal(err)
	}
	rig.store = store
	return rig
}

// write runs a write of the project that appends one event, after change.
func (r *writeRig) write(ctx context.Context, change func(ports.WriteTx) error) error {
	return r.store.Write(ctx, rigScope, "test.write", func(tx ports.WriteTx) error {
		if err := change(tx); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"},
			Subject: ledger.Subject{Kind: "repository", ID: "r1"},
		})
		return err
	})
}

// register enters a repository in the project, which writes the repository index.
func register(tx ports.WriteTx) error {
	record := ledger.NewRepository("r1")
	record.Name, record.Remote, record.DefaultBranch = "app", "https://example.invalid/acme/app.git", "main"
	_, err := tx.Repositories().Insert(record)
	return err
}

// noRepository leaves the repository index alone.
func noRepository(ports.WriteTx) error { return nil }

// TestAfterFailedWriteCallsOnlyForAProjectCommitThatFailedAfterTheIndexCommitted holds the contract of
// Options.AfterFailedWrite: it is called when a write fails at the project's commit once the repository
// index committed its part, with the write's project, after that part is in the index, on a context the
// write's cancellation does not end, and for no other outcome.
func TestAfterFailedWriteCallsOnlyForAProjectCommitThatFailedAfterTheIndexCommitted(t *testing.T) {
	t.Run("a write that commits", func(t *testing.T) {
		t.Parallel()
		rig := newWriteRig(t)
		if err := rig.write(t.Context(), register); err != nil {
			t.Fatal(err)
		}
		if len(rig.calls) != 0 {
			t.Errorf("the hook was called for a write that committed: %+v", rig.calls)
		}
	})
	t.Run("a write that fails before it commits", func(t *testing.T) {
		t.Parallel()
		rig := newWriteRig(t)
		failed := errors.New("the write's function fails")
		err := rig.write(t.Context(), func(tx ports.WriteTx) error {
			if err := register(tx); err != nil {
				return err
			}
			return failed
		})
		if !errors.Is(err, failed) {
			t.Fatalf("the write ended with %v", err)
		}
		if len(rig.calls) != 0 {
			t.Errorf("the hook was called for a write whose function failed: %+v", rig.calls)
		}
		if index, err := rig.installation.RepositoryIndex(t.Context()); err != nil || len(index) != 0 {
			t.Errorf("a write that failed before it committed left the index at %+v: %v", index, err)
		}
	})
	t.Run("a project commit that fails when the index was not written", func(t *testing.T) {
		t.Parallel()
		rig := newWriteRig(t)
		rig.store.ReplaceProjectCommit(func() error { return errCommit })
		if err := rig.write(t.Context(), noRepository); !errors.Is(err, errCommit) {
			t.Fatalf("the write ended with %v", err)
		}
		if len(rig.calls) != 0 {
			t.Errorf("the hook was called for a write that never touched the index: %+v", rig.calls)
		}
	})
	t.Run("a project commit that fails after the index committed", func(t *testing.T) {
		t.Parallel()
		rig := newWriteRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		// The write's own context ends as its commit fails, which is one way a commit fails.
		rig.store.ReplaceProjectCommit(func() error {
			cancel()
			return errCommit
		})
		err := rig.write(ctx, register)
		if !errors.Is(err, errCommit) {
			t.Fatalf("the write ended with %v, want its own commit error", err)
		}
		if len(rig.calls) != 1 {
			t.Fatalf("the hook was called %d times: %+v", len(rig.calls), rig.calls)
		}
		call := rig.calls[0]
		if call.project != rigScope {
			t.Errorf("the hook was called for project %q, want %q", call.project, rigScope)
		}
		if call.cancelled {
			t.Error("the hook's context ended with the write's")
		}
		want := []ledger.IndexedRepository{{Identity: "example.invalid/acme/app", Project: rigScope, Repository: "r1"}}
		if !slices.Equal(call.index, want) {
			t.Errorf("the index when the hook was called: %+v, want %+v", call.index, want)
		}
		// The project's records did not commit, which is what the hook is there to repair.
		if err := rig.store.Read(t.Context(), rigScope, func(tx ports.ReadTx) error {
			records, err := tx.Repositories().Repositories()
			if len(records) != 0 {
				t.Errorf("the project holds %+v", records)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})
}
