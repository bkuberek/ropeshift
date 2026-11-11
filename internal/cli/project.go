package cli

import (
	"context"
	"fmt"
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
