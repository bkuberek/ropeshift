package entry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
