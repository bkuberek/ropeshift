package planning

import (
	"strings"
	"testing"
)

func aWorkspace() []WorkspaceRepository {
	return []WorkspaceRepository{
		{Name: "web", Remote: "git@example.invalid:acme/web.git", Path: "repos/web"},
		{Name: "api", Remote: "git@example.invalid:acme/api.git", Path: "repos/api"},
	}
}

// The manifest is the repository file format's own: kind repositories, the project's name for its id, no
// plan, and one table for each repository with its remote, its path relative to the planning clone, and an
// empty description. It lists the repositories by name, so the same plan gives the same file.
func TestTheManifestListsEachRepositoryByName(t *testing.T) {
	const want = "+++\n" +
		"kind = \"repositories\"\n" +
		"id = \"shop\"\n" +
		"\n" +
		"[[repository]]\n" +
		"name = \"api\"\n" +
		"remote = \"git@example.invalid:acme/api.git\"\n" +
		"path = \"repos/api\"\n" +
		"description = \"\"\n" +
		"\n" +
		"[[repository]]\n" +
		"name = \"web\"\n" +
		"remote = \"git@example.invalid:acme/web.git\"\n" +
		"path = \"repos/web\"\n" +
		"description = \"\"\n" +
		"+++\n" +
		"\n" +
		"The code repositories of the shop project. The planning repository, which holds this file, is not listed.\n"
	if got := RenderRepositoriesFile("shop", aWorkspace()); got != want {
		t.Errorf("the manifest reads:\n%s\nwant:\n%s", got, want)
	}
	if again := RenderRepositoriesFile("shop", aWorkspace()); again != RenderRepositoriesFile("shop", aWorkspace()) {
		t.Error("two manifests of one plan differ")
	}
}

// A repository with no remote has no remote line, because an empty one says nothing a reader can use, and a
// name, a remote, or a path with a quote or a backslash in it is written as TOML escapes it.
func TestTheManifestSpellsWhatIsGivenAsTOML(t *testing.T) {
	got := RenderRepositoriesFile(`sh"op`, []WorkspaceRepository{{Name: "api", Path: `repos/a\pi`}})
	for _, want := range []string{`id = "sh\"op"`, `path = "repos/a\\pi"`, "name = \"api\"\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("the manifest holds no %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "remote") {
		t.Errorf("a repository with no remote has a remote line:\n%s", got)
	}
}

// The merge commit of a consolidation names the design repository, and only a subject of that form reads back as
// one, so a worker finds its own merge on a branch and takes no other commit for it.
func TestTheImportCommitSubjectNamesTheDesignRepositoryAndReadsBack(t *testing.T) {
	if got := ImportCommitSubject("design"); got != "chore: import the design history into the workspace" {
		t.Errorf("the subject reads %q", got)
	}
	if design, ok := ImportedDesign(ImportCommitSubject("acme-design")); !ok || design != "acme-design" {
		t.Errorf("the subject reads back as %q, %v", design, ok)
	}
	for _, subject := range []string{WorkspaceCommitSubject, "chore: import the  history into the workspace", "chore: import the design history", "import the design history into the workspace", ""} {
		if design, ok := ImportedDesign(subject); ok {
			t.Errorf("%q reads as the import of %q", subject, design)
		}
	}
}
