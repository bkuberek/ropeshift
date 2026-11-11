package cli

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
)

// projectClient returns a client of the project a command acts on, which it chooses in the order of
// FR-PROJ-4: --project, then ROPESHIFT_PROJECT, then the project that holds the repository the command
// runs in, then the only active project. When none of these names one it says so, lists the projects,
// and returns nil with the exit code.
func (c *invocation) projectClient() (*api.ProjectClient, int) {
	client := c.client()
	name, code := c.chooseProject(client)
	if code != ExitOK {
		client.Close()
		return nil, code
	}
	return client.Project(name), ExitOK
}

func (c *invocation) chooseProject(client *api.Client) (string, int) {
	if c.project != "" {
		return c.project, ExitOK
	}
	if c.envProject != "" {
		return c.envProject, ExitOK
	}
	projects, err := client.Projects(c.ctx)
	if err != nil {
		return "", c.failure(err)
	}
	if name := c.projectHere(projects); name != "" {
		return name, ExitOK
	}
	var active []string
	for _, p := range projects {
		if p.State == ledger.ProjectActive {
			active = append(active, p.Name)
		}
	}
	if len(active) == 1 {
		return active[0], ExitOK
	}
	c.printError(api.ErrorDetail{
		Code:    api.CodeNoProject,
		Message: "name the project with --project or " + api.EnvProject + ", or run this in a repository a project holds: " + projectNames(projects),
	})
	return "", ExitUsage
}

// projectHere is the project that holds the repository the command runs in, or empty. It asks git only
// when some project holds a repository.
func (c *invocation) projectHere(projects []api.Project) string {
	if c.repositoryHere == nil || !slices.ContainsFunc(projects, func(p api.Project) bool { return len(p.Repositories) > 0 }) {
		return ""
	}
	identity, err := c.repositoryHere(c.ctx)
	if err != nil || identity == "" {
		return ""
	}
	for _, p := range projects {
		if slices.Contains(p.Repositories, identity) {
			return p.Name
		}
	}
	return ""
}

// projectNames lists the projects in one line, each with its state.
func projectNames(projects []api.Project) string {
	if len(projects) == 0 {
		return "this installation holds no project"
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, fmt.Sprintf("%s (%s)", p.Name, p.State))
	}
	return "this installation holds " + strings.Join(names, ", ")
}

// noProject explains a project the installation does not hold, and lists the ones it does.
func (c *invocation) noProject(detail api.ErrorDetail) int {
	client := api.NewUnixClient(c.socket)
	defer client.Close()
	if projects, err := client.Projects(context.WithoutCancel(c.ctx)); err == nil {
		detail.Message += "; " + projectNames(projects)
	}
	c.printError(detail)
	return ExitUsage
}

func projectLine(p api.Project) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\n", p.Name, p.State, dashIfEmpty(p.DisplayName), api.Count(int64(len(p.Repositories)), "repository", "repositories"))
}

func (c *invocation) projectList(args []string) int {
	client, ok, code := c.noArgumentsAtInstallation(args)
	if !ok {
		return code
	}
	defer client.Close()
	projects, err := client.Projects(c.ctx)
	list := api.ProjectList{Projects: projects}
	if list.Projects == nil {
		list.Projects = []api.Project{}
	}
	return c.finish(err, list, func() string {
		var out strings.Builder
		for _, p := range list.Projects {
			out.WriteString(projectLine(p))
		}
		return out.String()
	})
}

func (c *invocation) projectShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("this takes one project")
	}
	client := c.client()
	defer client.Close()
	detail, err := client.ProjectRecord(c.ctx, positional[0])
	return c.finish(err, detail, func() string {
		var out strings.Builder
		fmt.Fprintf(&out, "project\t%s\n", detail.Name)
		fmt.Fprintf(&out, "id\t%s\n", detail.ID)
		fmt.Fprintf(&out, "display name\t%s\n", dashIfEmpty(detail.DisplayName))
		fmt.Fprintf(&out, "state\t%s\n", detail.State)
		fmt.Fprintf(&out, "database\t%s\n", detail.Database)
		for _, record := range detail.Records {
			fmt.Fprintf(&out, "repository\t%s\t%s\n", shortRepository(record.ID), dashIfEmpty(record.Remote))
		}
		return out.String()
	})
}

// positionals reads a command that takes exactly n arguments.
func (c *invocation) positionals(set *flag.FlagSet, args []string, n int, what string) ([]string, int) {
	positional, ok := c.parse(set, args)
	if !ok {
		return nil, ExitUsage
	}
	if len(positional) != n {
		return nil, c.usageError("this takes %s", what)
	}
	return positional, ExitOK
}

func (c *invocation) projectCreate(args []string) int {
	set := c.flags()
	display := set.String("display-name", "", "the name to show, which may hold spaces and capitals")
	positional, code := c.positionals(set, args, 1, "one name")
	if positional == nil {
		return code
	}
	client := c.client()
	defer client.Close()
	created, err := client.CreateProject(c.ctx, api.CreateProject{Name: positional[0], DisplayName: *display})
	return c.finish(err, created, func() string { return fmt.Sprintf("created project %s, %s\n", created.Name, created.ID) })
}

func (c *invocation) projectRename(args []string) int {
	positional, code := c.positionals(c.flags(), args, 2, "the project's name and its new name")
	if positional == nil {
		return code
	}
	client := c.client()
	defer client.Close()
	renamed, err := client.RenameProject(c.ctx, positional[0], api.RenameProject{Name: positional[1]})
	return c.finish(err, renamed, func() string { return fmt.Sprintf("renamed project %s to %s\n", positional[0], renamed.Name) })
}

// setsProject runs a step that moves a project to a state: archive or restore.
func setsProject(step func(*api.Client, context.Context, string) (api.Project, error), done string) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		positional, code := c.positionals(c.flags(), args, 1, "one project")
		if positional == nil {
			return code
		}
		client := c.client()
		defer client.Close()
		project, err := step(client, c.ctx, positional[0])
		return c.finish(err, project, func() string { return fmt.Sprintf("%s project %s\n", done, project.Name) })
	}
}

// projectRemove deletes an archived project, which asks for its name again and for a choice about a
// backup, because nothing brings a removed project back but its backup.
func (c *invocation) projectRemove(args []string) int {
	set := c.flags()
	confirm := set.String("confirm", "", "the project's name again")
	backup := set.String("backup", "", "a new file for the project's backup, written first")
	noBackup := set.Bool("no-backup", false, "remove the project with no backup")
	positional, code := c.positionals(set, args, 1, "one project")
	if positional == nil {
		return code
	}
	if *confirm != positional[0] {
		return c.usageError("confirm the removal with --confirm %s", positional[0])
	}
	if (*backup == "") == !*noBackup {
		return c.usageError("name a file with --backup, or choose --no-backup")
	}
	removal := api.RemoveProject{Confirm: *confirm, NoBackup: *noBackup}
	if *backup != "" {
		path, err := filepath.Abs(*backup)
		if err != nil {
			return c.usageError("%v", err)
		}
		removal.Backup = path
	}
	client := c.client()
	defer client.Close()
	err := client.RemoveProject(c.ctx, positional[0], removal)
	return c.finish(err, removal, func() string {
		if removal.Backup != "" {
			return fmt.Sprintf("removed project %s, with its backup at %s\n", positional[0], removal.Backup)
		}
		return fmt.Sprintf("removed project %s\n", positional[0])
	})
}

// projectImport registers a project from its backup. A relative path is read from where the command
// runs, because the daemon runs somewhere else.
func (c *invocation) projectImport(args []string) int {
	set := c.flags()
	name := set.String("name", "", "the name to register it under, by default the backup's own")
	positional, code := c.positionals(set, args, 1, "one backup file")
	if positional == nil {
		return code
	}
	path, err := filepath.Abs(positional[0])
	if err != nil {
		return c.usageError("%v", err)
	}
	client := c.client()
	defer client.Close()
	imported, err := client.ImportProject(c.ctx, api.ImportProject{Path: path, Name: *name})
	return c.finish(err, imported, func() string { return fmt.Sprintf("imported project %s, %s\n", imported.Name, imported.ID) })
}
