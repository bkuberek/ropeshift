package entry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestTheRepositoryHereIsNamedAsTheIndexNamesIt holds the third step of FR-PROJ-4: the command finds the
// repository it runs in by the identity the repository index keys it by, from any directory inside it.
func TestTheRepositoryHereIsNamedAsTheIndexNamesIt(t *testing.T) {
	repo := gittest.New(t)
	for _, entry := range repo.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	root := repo.Commit("README.md", "hello\n")
	inside := filepath.Join(repo.Dir, "docs")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inside)
	if got, err := repositoryHere(t.Context()); err != nil || got != root {
		t.Errorf("with no remote: %q, %v, want the first commit %s", got, err, root)
	}
	repo.Git("remote", "add", "origin", "https://someone:secret@Example.com/acme/invoices.git")
	if got, err := repositoryHere(t.Context()); err != nil || got != "example.com/acme/invoices" {
		t.Errorf("with a remote: %q, %v", got, err)
	}
	t.Chdir(t.TempDir())
	if got, err := repositoryHere(t.Context()); err != nil || got != "" {
		t.Errorf("outside any repository: %q, %v", got, err)
	}
}

// TestAnArchivedProjectRunsNoTick holds ADR-0021's archive for the project the daemon serves: its ticks,
// intake among them, wait while it is archived and run again once it is restored.
func TestAnArchivedProjectRunsNoTick(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer store.Close()
	runs := 0
	ticks := whileActive(state, soloScope, []control.Tick{{Name: "intake", Every: time.Second, Run: func(context.Context) error {
		runs++
		return nil
	}}})
	run := func() {
		if err := ticks[0].Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	run()
	if _, err := state.SetState(t.Context(), soloScope, ledger.ProjectArchived, member); err != nil {
		t.Fatal(err)
	}
	run()
	if _, err := state.SetState(t.Context(), soloScope, ledger.ProjectActive, member); err != nil {
		t.Fatal(err)
	}
	run()
	if runs != 2 {
		t.Errorf("the tick ran %d times, want 2", runs)
	}
}
