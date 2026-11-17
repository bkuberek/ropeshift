package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
)

// newDetail is what only the help of a `new` command says.
const newDetail = "It writes each file the folder lacks, from the templates embedded in this binary, creating the folder " +
	"and its parents when they are absent, and never replaces a file. The project's name is the folder's name in " +
	"lowercase letters, digits, and hyphens. It lists each file written and each file left because it exists, then " +
	"the commands that register the folder with a member's session, after git init, add, and commit when the folder " +
	"has no repository of its own. With --json it prints the folder, the project, the files written and left, and " +
	"the commands. It needs no daemon, no session, and no project, and runs no git command that changes a repository."

// newCommands are the `new` commands, one for each shape of project.
func newCommands() []registered {
	commands := []registered{
		command("new workspace", "<path> [--repository name=remote]...", "write a workspace's starting files: the planning repository of a project of several repositories", (*invocation).newWorkspace),
		command("new monorepo", "<path>", "write a monorepo's starting files: one repository that holds the plan and the code", (*invocation).newMonorepo),
		command("new repository", "<path> [--project P]", "write a code repository's starting files, for a project that plans in another repository", (*invocation).newRepository),
	}
	for i := range commands {
		commands[i].Detail = newDetail
	}
	return commands
}

func (c *invocation) newWorkspace(args []string) int {
	return c.newFolder(projectfiles.Workspace, args)
}

func (c *invocation) newMonorepo(args []string) int {
	return c.newFolder(projectfiles.Monorepo, args)
}

func (c *invocation) newRepository(args []string) int {
	return c.newFolder(projectfiles.Repository, args)
}

// newResult is what a `new` command did: the folder, the project its files name, the files it wrote and the
// files it left because they exist, by their paths in the folder, and the commands that register the folder.
type newResult struct {
	Path     string   `json:"path"`
	Project  string   `json:"project"`
	Written  []string `json:"written"`
	Left     []string `json:"left"`
	Commands []string `json:"commands"`
}

func (r newResult) text() string {
	var out strings.Builder
	for _, path := range r.Written {
		fmt.Fprintf(&out, "wrote\t%s\n", path)
	}
	for _, path := range r.Left {
		fmt.Fprintf(&out, "left\t%s\talready exists\n", path)
	}
	out.WriteString("\nTo register it, run these with a member's session:\n")
	for _, command := range r.Commands {
		fmt.Fprintf(&out, "  %s\n", command)
	}
	return out.String()
}

// newFolder writes a shape's starting files into the folder the command line names, then prints what it wrote
// and the commands that register the folder. Everything the command line asks is checked before anything is
// written.
func (c *invocation) newFolder(shape projectfiles.Shape, args []string) int {
	set := c.flags()
	var repositories repeated
	var project string
	switch shape {
	case projectfiles.Workspace:
		set.Var(&repositories, "repository", "a code repository as name=remote, cloned into repos/<name>, repeatable")
	case projectfiles.Repository:
		set.StringVar(&project, "project", "", "the project the printed commands register the repository in; the folder's name when absent")
	}
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	switch {
	case len(positional) == 0:
		return c.usageError("name the folder to write: %s %s <path>", program, c.name)
	case len(positional) > 1:
		return c.usageError("unexpected argument %q: it takes one folder", positional[1])
	}
	folder, err := filepath.Abs(positional[0])
	if err != nil {
		return c.usageError("the folder %q: %v", positional[0], err)
	}
	data := projectfiles.Data{Project: projectNameOf(filepath.Base(folder))}
	if data.Project == "" {
		return c.usageError("the folder %q makes no project name; rename it to a name with letters or digits", filepath.Base(folder))
	}
	for _, entry := range repositories {
		name, remote, found := strings.Cut(entry, "=")
		if !found {
			return c.usageError("--repository %q: give a code repository as name=remote", entry)
		}
		data.Repositories = append(data.Repositories, projectfiles.CodeRepository{Name: name, Remote: remote})
	}
	if project == "" {
		project = data.Project
	} else if err := ledger.ValidateProjectName(project); err != nil {
		return c.usageError("--project: %v", err)
	}
	files, err := projectfiles.Files(shape, data, renderTemplate)
	if errors.Is(err, projectfiles.ErrInvalid) {
		return c.usageError("%v", err)
	}
	if err != nil {
		return c.finish(err, nil, nil)
	}
	result := newResult{Path: folder, Project: data.Project, Written: []string{}, Left: []string{}}
	if err := writeAbsent(folder, files, &result); err != nil {
		return c.finish(err, nil, nil)
	}
	firstCommit, err := c.needsRepository(folder)
	if err != nil {
		return c.finish(err, nil, nil)
	}
	result.Commands = projectfiles.Commands(shape, folder, project, data.Repositories, firstCommit)
	return c.finish(nil, result, result.text)
}

// renderTemplate renders a template of this binary over a new project's starting data.
func renderTemplate(name string, data projectfiles.Data) (string, error) {
	converted := templates.Data{Project: data.Project}
	for _, r := range data.Repositories {
		converted.Repositories = append(converted.Repositories, templates.Repository{Name: r.Name, Remote: r.Remote})
	}
	return templates.Render(name, converted)
}

// writeAbsent writes each file the folder lacks, creating the folder, its parents, and the folders inside it
// that a file needs. Each file is opened only to be created, so one that exists is left as it is, whatever it
// holds, and is listed as left.
func writeAbsent(folder string, files []projectfiles.File, result *newResult) error {
	for _, file := range files {
		path := filepath.Join(folder, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			result.Left = append(result.Left, file.Path)
			continue
		}
		if err != nil {
			return err
		}
		_, err = out.WriteString(file.Text)
		if closed := out.Close(); err == nil {
			err = closed
		}
		if err != nil {
			return fmt.Errorf("write %s: %w", file.Path, err)
		}
		result.Written = append(result.Written, file.Path)
	}
	return nil
}

// needsRepository says, once the folder's files are written, whether the folder needs a repository of its
// own for its first commit. It does when no clone holds it, and when the clone that holds it ignores it, as a
// workspace's ignored repos/ holds a code repository's folder. A folder that is the top of a clone has one
// already, and the files of a folder a clone tracks belong to that clone.
func (c *invocation) needsRepository(folder string) (bool, error) {
	if c.cloneTop == nil {
		return true, nil
	}
	top, err := c.cloneTop(c.ctx, folder)
	if err != nil || top == "" {
		return err == nil, err
	}
	real, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return false, err
	}
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	if top == real || c.cloneIgnores == nil {
		return false, nil
	}
	return c.cloneIgnores(c.ctx, top, real)
}
