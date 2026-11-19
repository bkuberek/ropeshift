// Package projectfiles is what a new project's folder starts with, for each shape of project: the file each
// template is written to, and the commands that register the result (FR-TPL-1, FR-TPL-2). It reads and writes
// no file and renders no template itself: the caller passes the renderer, and writes the files, so the command
// line and a worker writing for the web UI run one table and print the same commands.
package projectfiles

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Shape is the kind of folder a project starts with.
type Shape string

// The shapes of a new project.
const (
	// Workspace is a planning repository whose code repositories are cloned into its ignored repos/ folder.
	Workspace Shape = "workspace"
	// Monorepo is one repository that holds the plan and the code.
	Monorepo Shape = "monorepo"
	// Repository is a code repository of a project that plans elsewhere.
	Repository Shape = "repository"
)

// Shapes lists every shape, in the order help names them.
func Shapes() []Shape { return []Shape{Workspace, Monorepo, Repository} }

// Valid says the shape is one of Shapes.
func (s Shape) Valid() bool { return slices.Contains(Shapes(), s) }

// ErrInvalid is the error of a shape, or of code repositories, that no folder can be written for.
var ErrInvalid = errors.New("invalid project files")

// CodeRepository is one code repository a workspace names: its name in the project, and the remote its clone
// at repos/<name> comes from. A new code repository is written at repos/<name> with the files of the repository
// shape, and its remote is empty when it has none. Path is where an existing clone sits relative to the workspace
// when that is not repos/<name>, as an adoption finds one.
type CodeRepository struct {
	Name   string
	Remote string
	Path   string
	New    bool
}

// CodePath is where a workspace keeps the code repository named name, relative to the workspace.
func CodePath(name string) string { return "repos/" + name }

// Data is what every file of a folder renders over: the project's name, and a workspace's code repositories.
type Data struct {
	Project      string
	Repositories []CodeRepository
}

// Render returns the text of the template named name over data. The caller passes the templates package's
// renderer, which no domain package imports.
type Render func(name string, data Data) (string, error)

// File is one file of a new folder: its path in the folder, separated by slashes, the template it comes from,
// and its text.
type File struct {
	Path     string
	Template string
	Text     string
}

// row is one template a new folder starts with: the path every shape that writes it uses, and those shapes.
type row struct {
	template string
	path     string
	shapes   []Shape
}

// manifest is the template of the workspace's list of code repositories, which a workspace writes only when
// it names one.
const manifest = "workspace/plan/repositories.md"

// every is shorthand for a template all three shapes write.
var every = []Shape{Workspace, Monorepo, Repository}

// table is FR-TPL-1's table: each template a new folder starts with, where it is written, and by which shapes.
// A shape writes plan/ when it holds the plan, which a repository of a project planned elsewhere does not, and
// catalog-info.yaml and a pre-commit configuration when it holds code, which a workspace does not.
var table = []row{
	{"workspace/agents.md", "AGENTS.md", []Shape{Workspace}},
	{"monorepo/agents.md", "AGENTS.md", []Shape{Monorepo}},
	{"repository/agents.md", "AGENTS.md", []Shape{Repository}},
	{"claude.md", "CLAUDE.md", every},
	{"workspace/makefile", "Makefile", []Shape{Workspace}},
	{"workspace/gitignore", ".gitignore", []Shape{Workspace}},
	{"monorepo/gitignore", ".gitignore", []Shape{Monorepo}},
	{"repository/gitignore", ".gitignore", []Shape{Repository}},
	{"workspace/plan/goal.md", "plan/goal.md", []Shape{Workspace, Monorepo}},
	{"workspace/plan/plan.md", "plan/plan.md", []Shape{Workspace, Monorepo}},
	{"workspace/plan/requirements.md", "plan/requirements.md", []Shape{Workspace, Monorepo}},
	{manifest, "plan/repositories.md", []Shape{Workspace}},
	{"repository/catalog-info.yaml", "catalog-info.yaml", []Shape{Monorepo, Repository}},
	{"security.md", "SECURITY.md", every},
	{"codeowners", "CODEOWNERS", every},
	{"pre-commit-config.yaml", ".pre-commit-config.yaml", []Shape{Monorepo, Repository}},
}

// Path is where a shape writes the template named name in its folder, or empty when no shape writes it.
func Path(name string) string {
	for _, r := range table {
		if r.template == name {
			return r.path
		}
	}
	return ""
}

// ShapesOf lists the shapes that write the template named name, in the order of Shapes. It is empty, and not
// nil, for a template no shape writes.
func ShapesOf(name string) []Shape {
	shapes := []Shape{}
	for _, shape := range Shapes() {
		if slices.ContainsFunc(table, func(r row) bool { return r.template == name && slices.Contains(r.shapes, shape) }) {
			shapes = append(shapes, shape)
		}
	}
	return shapes
}

// UserProfile is the template `init` writes to the installation's state directory, which no shape writes.
const UserProfile = "user/profile.toml"

// WritesTo is where the template named name is written: its path in a new project's folder for a template a
// shape writes, and profile.toml, in the state directory, for the user profile. It is empty for a template
// nothing writes.
func WritesTo(name string) string {
	if name == UserProfile {
		return "profile.toml"
	}
	return Path(name)
}

// Check refuses a folder no shape writes: an unknown shape, code repositories on a shape other than a
// workspace, a name the ledger refuses as a repository's, a name a Makefile cannot carry as a word, a name given
// twice, and a repository whose remote is empty or is not a single-line git remote. A new repository may have no
// remote.
func Check(shape Shape, repositories []CodeRepository) error {
	if !shape.Valid() {
		return fmt.Errorf("%w: no shape %q; the shapes are workspace, monorepo, and repository", ErrInvalid, shape)
	}
	if len(repositories) > 0 && shape != Workspace {
		return fmt.Errorf("%w: a %s names no code repositories; a workspace does", ErrInvalid, shape)
	}
	seen := map[string]bool{}
	for _, repository := range repositories {
		if err := ledger.CheckRepositoryName(repository.Name); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if !SafeSegment(repository.Name) {
			return fmt.Errorf("%w: a repository name holds only letters, digits, and . _ -, because the Makefile carries it as a word, and %q holds another character", ErrInvalid, repository.Name)
		}
		if seen[repository.Name] {
			return fmt.Errorf("%w: repository %s is named twice", ErrInvalid, repository.Name)
		}
		seen[repository.Name] = true
		if repository.Remote == "" && repository.New {
			continue
		}
		if repository.Remote == "" {
			return fmt.Errorf("%w: repository %s has no remote", ErrInvalid, repository.Name)
		}
		if err := checkRemote(repository.Remote); err != nil {
			return fmt.Errorf("%w: repository %s: %w", ErrInvalid, repository.Name, err)
		}
	}
	return nil
}

// Files returns each file the shape starts its folder with, rendered over data, sorted by path. The manifest
// is among them only for a workspace that names a code repository. A shape or repositories Check refuses, and
// a template that does not render, return no file.
func Files(shape Shape, data Data, render Render) ([]File, error) {
	if err := Check(shape, data.Repositories); err != nil {
		return nil, err
	}
	var files []File
	for _, r := range table {
		if !slices.Contains(r.shapes, shape) || (r.template == manifest && len(data.Repositories) == 0) {
			continue
		}
		text, err := render(r.template, data)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", r.template, err)
		}
		files = append(files, File{Path: r.path, Template: r.template, Text: text})
	}
	for _, repository := range data.Repositories {
		if !repository.New {
			continue
		}
		own, err := Files(Repository, Data{Project: repository.Name}, render)
		if err != nil {
			return nil, fmt.Errorf("repository %s: %w", repository.Name, err)
		}
		for _, file := range own {
			file.Path = CodePath(repository.Name) + "/" + file.Path
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, nil
}

// Commands are what a person runs, with a member's session, to register the folder at path as the project
// named name. With firstCommit, the folder still needs a repository of its own, and the three git commands
// that make its first commit come first. Then a workspace clones its code repositories, when it names any,
// and creates the project and registers through its Makefile; a monorepo creates the project and registers
// itself; and a repository registers itself in a project that exists.
func Commands(shape Shape, path, name string, repositories []CodeRepository, firstCommit bool) []string {
	folder := quote(path)
	var commands []string
	if firstCommit {
		for _, repository := range repositories {
			if repository.New {
				commands = append(commands, FirstCommitCommands(path+"/"+CodePath(repository.Name), repository.Name)...)
			}
		}
		commands = append(commands, FirstCommitCommands(path, name)...)
	}
	create := "ropeshift project create " + name
	add := "ropeshift repository add " + folder + " --project " + name
	switch shape {
	case Workspace:
		if slices.ContainsFunc(repositories, func(r CodeRepository) bool { return !r.New }) {
			commands = append(commands, "make -C "+folder+" setup")
		}
		commands = append(commands, create, "make -C "+folder+" register")
	case Monorepo:
		commands = append(commands, create, add)
	case Repository:
		commands = append(commands, add)
	}
	return commands
}

// FirstCommitCommands are the three git commands that make a folder a repository and commit its files as the start
// of the project named name.
func FirstCommitCommands(path, name string) []string {
	folder := quote(path)
	return []string{
		"git -C " + folder + " init",
		"git -C " + folder + " add -A",
		"git -C " + folder + ` commit -m "chore: start ` + name + `"`,
	}
}

// Quote returns a path a POSIX shell reads as one word.
func Quote(path string) string { return quote(path) }

// CheckRemote refuses a remote that is not one line git clones from: a URL with an https, ssh, git, or file scheme,
// or scp-like [user@]host:path, in a short allowlist of characters, never a host that starts with a dash and never a
// transport::address that git reads as a remote helper, which can run a command.
func CheckRemote(remote string) error { return checkRemote(remote) }

// quote returns a path a POSIX shell reads as one word: as it is when every character is one no shell
// treats specially, and otherwise in single quotes, with each single quote closed, escaped, and reopened.
func quote(path string) string {
	if path != "" && strings.IndexFunc(path, func(r rune) bool { return !plain(r) }) < 0 {
		return path
	}
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// plain says a POSIX shell reads r as itself in a word with no quotes.
func plain(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_@%+=:,./-", r)
}
