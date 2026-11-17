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

// The Makefile is a literal list of the manifest's repositories over three targets, and the same repositories
// give the same file, byte for byte.
func TestTheMakefileListsTheRepositoriesAsALiteralList(t *testing.T) {
	const want = "# The workspace Makefile: clone, fetch, and show the status of every repository of the project.\n" +
		"#\n" +
		"# Ropeshift never runs this file. REPOSITORIES lists the repositories of the manifest, plan/repositories.md, each\n" +
		"# as name=path=remote with the path relative to this folder. Keep the two in step.\n" +
		"\n" +
		"REPOSITORIES := 'api=repos/api=git@example.invalid:acme/api.git' \\\n" +
		"  'web=repos/web=git@example.invalid:acme/web.git'\n" +
		"\n" +
		".PHONY: setup fetch status\n" +
		"\n" +
		"# Clone each repository that has no clone yet.\n" +
		"setup:\n" +
		"\t@for entry in $(REPOSITORIES); do \\\n" +
		"\t  name=$${entry%%=*}; rest=$${entry#*=}; dir=$${rest%%=*}; remote=$${rest#*=}; \\\n" +
		"\t  if [ -e \"$$dir/.git\" ]; then echo \"$$name: present\"; \\\n" +
		"\t  elif [ -z \"$$remote\" ]; then echo \"$$name: no remote, clone it into $$dir by hand\"; \\\n" +
		"\t  else mkdir -p \"$$(dirname \"$$dir\")\" && git clone \"$$remote\" \"$$dir\" || exit 1; fi; \\\n" +
		"\tdone\n" +
		"\n" +
		"# Fetch this repository, when it has a remote, and every repository of the list.\n" +
		"fetch:\n" +
		"\t@if [ -n \"$$(git remote)\" ]; then git fetch --all --prune --quiet && echo \"workspace: fetched\"; \\\n" +
		"\telse echo \"workspace: no remote\"; fi\n" +
		"\t@for entry in $(REPOSITORIES); do \\\n" +
		"\t  name=$${entry%%=*}; rest=$${entry#*=}; dir=$${rest%%=*}; \\\n" +
		"\t  if [ ! -e \"$$dir/.git\" ]; then echo \"$$name: missing, run make setup\"; \\\n" +
		"\t  elif git -C \"$$dir\" fetch --all --prune --quiet; then echo \"$$name: fetched\"; \\\n" +
		"\t  else echo \"$$name: fetch failed\"; fi; \\\n" +
		"\tdone\n" +
		"\n" +
		"# Show the branch and the number of changed files of this repository and of every repository of the list.\n" +
		"status:\n" +
		"\t@echo \"workspace: $$(git status --short --branch | head -n 1), $$(git status --porcelain | wc -l | tr -d ' ') changed\"\n" +
		"\t@for entry in $(REPOSITORIES); do \\\n" +
		"\t  name=$${entry%%=*}; rest=$${entry#*=}; dir=$${rest%%=*}; \\\n" +
		"\t  if [ ! -e \"$$dir/.git\" ]; then echo \"$$name: missing\"; continue; fi; \\\n" +
		"\t  head=$$(git -C \"$$dir\" status --short --branch | head -n 1); \\\n" +
		"\t  changed=$$(git -C \"$$dir\" status --porcelain | wc -l | tr -d ' '); \\\n" +
		"\t  echo \"$$name: $$head, $$changed changed\"; \\\n" +
		"\tdone\n"
	if got := RenderWorkspaceMakefile(aWorkspace()); got != want {
		t.Errorf("the Makefile reads:\n%s\nwant:\n%s", got, want)
	}
}

// A value that a shell or make would read as more than one word, a comment, or a variable is quoted for the
// shell, and make is told to leave it alone.
func TestTheMakefileQuotesWhatAShellOrMakeWouldReadAsMore(t *testing.T) {
	got := RenderWorkspaceMakefile([]WorkspaceRepository{{Name: "api", Path: "repos/api", Remote: "https://host/a b#c$d'e.git"}})
	want := `REPOSITORIES := 'api=repos/api=https://host/a b\#c$$d'\''e.git'` + "\n"
	if !strings.Contains(got, want) {
		t.Errorf("the Makefile holds no %q:\n%s", want, got)
	}
	if empty := RenderWorkspaceMakefile(nil); !strings.Contains(empty, "REPOSITORIES :=\n") {
		t.Errorf("a Makefile with no repository has the list\n%s", empty)
	}
}
