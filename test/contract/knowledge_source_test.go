package contract

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/repodocs"
	"ropeshift.dev/ropeshift/internal/adapters/repofiles"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/gittest"
)

func TestKnowledgeSourceContract(t *testing.T) {
	t.Run("repository documents", func(t *testing.T) {
		RunKnowledgeSource(t, func(t *testing.T) KnowledgeRig {
			repo := gittest.New(t)
			repo.Commit("README.txt", "seed\n")
			return KnowledgeRig{
				Source: repodocs.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()}),
				Commit: func(path, content string) { repo.Commit(path, content) },
			}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunKnowledgeSource(t, func(*testing.T) KnowledgeRig {
			source := fake.NewKnowledgeSource()
			return KnowledgeRig{Source: source, Commit: source.Write}
		})
	})
}

// TestAnUncommittedEditIsNotKnowledge holds the rule of ADR-0013 for documents: what a lane
// reads must name a commit.
func TestAnUncommittedEditIsNotKnowledge(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("design.md", "# Committed\n")
	if err := os.WriteFile(filepath.Join(repo.Dir, "design.md"), []byte("# Not committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := repodocs.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	description, err := source.Describe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	document, err := source.Read(t.Context(), keyOf(description, "design.md"), "")
	if err != nil {
		t.Fatal(err)
	}
	if document.Body != "# Committed\n" {
		t.Errorf("body %q", document.Body)
	}
}

// TestADirectoryLimitsWhatIsRead holds a source to the directory it was given.
func TestADirectoryLimitsWhatIsRead(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("docs/design.md", "# In\n")
	repo.Commit("other/notes.md", "# Out\n")
	source := repodocs.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env(), Directory: "docs"})
	list, err := source.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Key.NativeID != "docs/design.md" {
		t.Errorf("list %+v", list)
	}
}

func keyOf(description ports.SourceDescription, path string) ledger.ExternalKey {
	return ledger.ExternalKey{Adapter: description.Adapter, Instance: description.Instance, NativeID: path}
}
