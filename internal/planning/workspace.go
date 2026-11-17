package planning

import (
	"fmt"
	"slices"
	"strings"
)

// The files an adoption adds to the planning repository (FR-ADOPT-14), and the commit that holds them.
const (
	// WorkspaceCommitSubject is the subject of the one commit the worker makes on the adoption branch.
	WorkspaceCommitSubject = "chore: add the workspace files"
	// ManifestPath is where the manifest of the project's repositories goes in the planning repository.
	ManifestPath = "plan/repositories.md"
	// MakefilePath is where the workspace's Makefile goes.
	MakefilePath = "Makefile"
	// GitignorePath is the ignore file the lines below are added to.
	GitignorePath = ".gitignore"
	// CodeFolder is the folder of the planning repository that the clones are moved into, which its ignore file
	// leaves out.
	CodeFolder = "repos"
	// WorktreesFolder is the folder a lane's worktree goes in when a profile gives it a root inside the clone.
	WorktreesFolder = ".ropeshift/worktrees"
	// CodeFolderLine and WorktreesLine are the ignore lines that leave the two folders out.
	CodeFolderLine = CodeFolder + "/"
	WorktreesLine  = "/" + WorktreesFolder + "/"
)

// WorkspaceRepository is one code repository of the project, as the manifest and the Makefile list it: its name,
// its remote, and the path of its clone relative to the planning repository's clone.
type WorkspaceRepository struct {
	Name   string
	Remote string
	Path   string
}

// byName lists the repositories in order of name, so the same plan gives the same files.
func byName(repositories []WorkspaceRepository) []WorkspaceRepository {
	sorted := slices.Clone(repositories)
	slices.SortStableFunc(sorted, func(a, b WorkspaceRepository) int { return strings.Compare(a.Name, b.Name) })
	return sorted
}

// RenderRepositoriesFile is the manifest of a project's code repositories as an adoption writes it: a file of
// kind repositories whose id is the project's name, with no plan, and a table for each repository holding its name, its
// remote, its path relative to the planning repository's clone, and an empty description for a person to fill.
func RenderRepositoriesFile(project string, repositories []WorkspaceRepository) string {
	var out strings.Builder
	out.WriteString("+++\nkind = \"repositories\"\nid = " + tomlString(project) + "\n")
	for _, repository := range byName(repositories) {
		out.WriteString("\n[[repository]]\nname = " + tomlString(repository.Name) + "\n")
		if repository.Remote != "" {
			out.WriteString("remote = " + tomlString(repository.Remote) + "\n")
		}
		out.WriteString("path = " + tomlString(repository.Path) + "\ndescription = \"\"\n")
	}
	out.WriteString("+++\n\nThe code repositories of the " + project + " project. The planning repository, which holds this file, is not listed.\n")
	return out.String()
}

// tomlString writes text as a TOML basic string, quotes included.
func tomlString(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range strings.ToValidUTF8(text, "�") {
		switch {
		case r == '"' || r == '\\':
			out.WriteByte('\\')
			out.WriteRune(r)
		case r == '\n':
			out.WriteString(`\n`)
		case r == '\r':
			out.WriteString(`\r`)
		case r == '\t':
			out.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&out, `\u%04X`, r)
		default:
			out.WriteRune(r)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// makeEntry writes one repository of the Makefile's list as name=path=remote: quoted for the shell that reads it,
// and with what make reads for itself, a variable and a comment, spelled so make leaves it alone.
func makeEntry(repository WorkspaceRepository) string {
	entry := repository.Name + "=" + repository.Path + "=" + repository.Remote
	quoted := "'" + strings.ReplaceAll(entry, "'", `'\''`) + "'"
	return strings.NewReplacer("$", "$$", "#", `\#`).Replace(quoted)
}

// workspaceMakefile is the Makefile of the workspace with the list of repositories left to fill in. A recipe's
// lines begin with a tab.
const workspaceMakefile = `# The workspace Makefile: clone, fetch, and show the status of every repository of the project.
#
# Ropeshift never runs this file. REPOSITORIES lists the repositories of the manifest, plan/repositories.md, each
# as name=path=remote with the path relative to this folder. Keep the two in step.

%s

.PHONY: setup fetch status

# Clone each repository that has no clone yet.
setup:
	@for entry in $(REPOSITORIES); do \
	  name=$${entry%%%%=*}; rest=$${entry#*=}; dir=$${rest%%%%=*}; remote=$${rest#*=}; \
	  if [ -e "$$dir/.git" ]; then echo "$$name: present"; \
	  elif [ -z "$$remote" ]; then echo "$$name: no remote, clone it into $$dir by hand"; \
	  else mkdir -p "$$(dirname "$$dir")" && git clone "$$remote" "$$dir" || exit 1; fi; \
	done

# Fetch this repository, when it has a remote, and every repository of the list.
fetch:
	@if [ -n "$$(git remote)" ]; then git fetch --all --prune --quiet && echo "workspace: fetched"; \
	else echo "workspace: no remote"; fi
	@for entry in $(REPOSITORIES); do \
	  name=$${entry%%%%=*}; rest=$${entry#*=}; dir=$${rest%%%%=*}; \
	  if [ ! -e "$$dir/.git" ]; then echo "$$name: missing, run make setup"; \
	  elif git -C "$$dir" fetch --all --prune --quiet; then echo "$$name: fetched"; \
	  else echo "$$name: fetch failed"; fi; \
	done

# Show the branch and the number of changed files of this repository and of every repository of the list.
status:
	@echo "workspace: $$(git status --short --branch | head -n 1), $$(git status --porcelain | wc -l | tr -d ' ') changed"
	@for entry in $(REPOSITORIES); do \
	  name=$${entry%%%%=*}; rest=$${entry#*=}; dir=$${rest%%%%=*}; \
	  if [ ! -e "$$dir/.git" ]; then echo "$$name: missing"; continue; fi; \
	  head=$$(git -C "$$dir" status --short --branch | head -n 1); \
	  changed=$$(git -C "$$dir" status --porcelain | wc -l | tr -d ' '); \
	  echo "$$name: $$head, $$changed changed"; \
	done
`

// RenderWorkspaceMakefile is the Makefile an adoption adds: setup, fetch, and status over the manifest's
// repositories, as a literal list in order of name, with nothing read from the manifest at run time.
func RenderWorkspaceMakefile(repositories []WorkspaceRepository) string {
	list := "REPOSITORIES :="
	for i, repository := range byName(repositories) {
		if i == 0 {
			list += " " + makeEntry(repository)
			continue
		}
		list += " \\\n  " + makeEntry(repository)
	}
	return fmt.Sprintf(workspaceMakefile, list)
}
