package contract

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/repodocs"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
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

// TestAFileSaysWhatItIsBeforeItsPathDoes holds a repository file's own word over where it sits.
func TestAFileSaysWhatItIsBeforeItsPathDoes(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("docs/notes.md", "+++\nkind = \"requirements\"\nid = \"reqs\"\ntitle = \"Requirements\"\n\n[[requirement]]\nid = \"R-1\"\ntext = \"One.\"\n+++\n")
	repo.Commit("docs/design/plan.md", "+++\nkind = \"plan\"\nid = \"plan-1\"\ntitle = \"The plan\"\n+++\n")
	repo.Commit("docs/design/a.md", DocumentText("spec", "a", "A spec filed as a design", "", "Prose."))
	repo.Commit("docs/specs/b.md", DocumentText("architecture", "b", "An architecture filed as a spec", "", "Prose."))
	repo.Commit("docs/c.md", DocumentText("experience-design", "c", "An experience design", "", "Prose."))
	repo.Commit("docs/d.md", DocumentText("milestone", "d", "A milestone", "", "Prose."))
	repo.Commit("docs/design/e.md", "# An unparsed design\n")
	source := repodocs.New(repofiles.Config{Repository: repo.Dir, Env: repo.Env()})
	list, err := source.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]planning.DocumentKind{
		"docs/notes.md": planning.DocumentRequirements, "docs/design/plan.md": planning.DocumentOther,
		"docs/design/a.md": planning.DocumentSpec, "docs/specs/b.md": planning.DocumentArchitecture,
		"docs/c.md": planning.DocumentExperienceDesign, "docs/d.md": planning.DocumentOther,
		// A file that is not one of ours keeps the kind its path gives it.
		"docs/design/e.md": planning.DocumentDesign,
	}
	if len(list) != len(want) {
		t.Fatalf("list %+v", list)
	}
	titles := map[string]string{}
	for _, document := range list {
		titles[document.Key.NativeID] = document.Title
		if document.Kind != want[document.Key.NativeID] {
			t.Errorf("%s lists as %q", document.Key.NativeID, document.Kind)
		}
	}
	if titles["docs/design/a.md"] != "A spec filed as a design" || titles["docs/d.md"] != "A milestone" {
		t.Errorf("titles %v", titles)
	}
}

func keyOf(description ports.SourceDescription, path string) ledger.ExternalKey {
	return ledger.ExternalKey{Adapter: description.Adapter, Instance: description.Instance, NativeID: path}
}
