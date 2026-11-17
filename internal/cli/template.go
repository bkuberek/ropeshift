package cli

import (
	"errors"
	"fmt"
	"strings"

	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
)

// userProfile is the template `init` writes, to profile.toml in the installation's state directory.
const userProfile = "user/profile.toml"

// writesTo is where the template named name is written: for a template a new project starts from, its path in
// the project's folder, as `new` writes it, and for the user profile, its path in the installation's state
// directory. It is empty for a template neither writes. The shapes of a project share a file name where they
// share a purpose, such as AGENTS.md, so one path names the template whichever shape writes it.
func writesTo(name string) string {
	if name == userProfile {
		return "profile.toml"
	}
	return projectfiles.Path(name)
}

// templateListing is one template in the list `template show --json` prints.
type templateListing struct {
	Name     string `json:"name"`
	WritesTo string `json:"writes_to"`
}

// templateShowDetail is what only `template show --help` says.
const templateShowDetail = "A template prints as the example project acme renders it, from the templates embedded in this binary. " +
	"With no name, it lists every template, sorted. With --json, the list is an array of objects with name and writes_to: " +
	"the path the template's file has in a new project's folder, or for profile.toml its path in the state directory, " +
	"and empty for a template no project starts with. It needs no daemon and no session."

// templatesSection is the commands help prints under "Templates". None acts on a project's records.
func templatesSection() []registered {
	show := command("template show", "[<name>] [--json]", "print a template as it renders for an example project, or list every template", (*invocation).templateShow)
	show.Detail = templateShowDetail
	return installation(show)
}

// templateShow is `ropeshift template show`: it reads the templates embedded in this binary, and nothing else.
func (c *invocation) templateShow(args []string) int {
	set := c.flags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	switch len(positional) {
	case 0:
		return c.listTemplates()
	case 1:
		if c.asJSON {
			return c.usageError("--json lists the templates and takes no name: a template prints as text")
		}
		return c.printTemplate(positional[0])
	}
	return c.usageError("unexpected argument %q: it takes one template name", positional[1])
}

// listTemplates prints every template's name, or with --json, each with where it is written.
func (c *invocation) listTemplates() int {
	names := templates.Names()
	listing := make([]templateListing, len(names))
	for i, name := range names {
		listing[i] = templateListing{Name: name, WritesTo: writesTo(name)}
	}
	return c.finish(nil, listing, func() string { return strings.Join(names, "\n") + "\n" })
}

// printTemplate prints one template as the templates package renders it with the example data.
func (c *invocation) printTemplate(name string) int {
	text, err := templates.Render(name, templates.Data{Example: true})
	if errors.Is(err, templates.ErrUnknown) {
		return c.usageError("no template %q; the templates are:\n%s", name, strings.Join(templates.Names(), "\n"))
	}
	if err != nil {
		fmt.Fprintf(c.stderr, "ropeshift %s: %v\n", c.name, err)
		return ExitError
	}
	return c.finish(nil, nil, func() string { return text })
}
