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

// FoldPath is a path with its case removed: two paths that fold alike are one path on a file system that ignores
// case. It folds ASCII and simple Unicode case alone and does not normalize, so a name in NFC and the same name in
// NFD are two paths.
func FoldPath(name string) string { return strings.ToLower(name) }

// The subject of the merge commit a consolidation makes on the adoption branch is "chore: import the <design>
// history into the workspace", where <design> is the design repository's clone folder name (FR-ADOPT-13).
const (
	importSubjectPrefix = "chore: import the "
	importSubjectSuffix = " history into the workspace"
)

// ImportCommitSubject is the subject of the merge commit that imports the history of the design repository
// named design.
func ImportCommitSubject(design string) string {
	return importSubjectPrefix + design + importSubjectSuffix
}

// ImportedDesign names the design repository a merge commit's subject says it imported, and reports whether the
// subject is an import's.
func ImportedDesign(subject string) (string, bool) {
	rest, found := strings.CutPrefix(subject, importSubjectPrefix)
	if !found {
		return "", false
	}
	design, found := strings.CutSuffix(rest, importSubjectSuffix)
	return design, found && design != ""
}

// WorkspaceRepository is one code repository of the project, as the manifest lists it: its name, its remote, and
// the path of its clone relative to the planning repository's clone.
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
