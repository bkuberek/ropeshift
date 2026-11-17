package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/version"
	"ropeshift.com/ropeshift/templates"
)

// userProfile is the template `init` writes, to profile.toml in the installation's state directory.
const userProfile = projectfiles.UserProfile

// daemonWait is how long `template show` waits for a daemon before it reads the templates of its own binary.
const daemonWait = 2 * time.Second

// templateListing is one template in the list `template show --json` prints.
type templateListing struct {
	Name     string `json:"name"`
	WritesTo string `json:"writes_to"`
}

// templateShowDetail is what only `template show --help` says.
const templateShowDetail = "A template prints as the example project acme renders it. " +
	"With no name, it lists every template, sorted. With --json, the list is an array of objects with name and writes_to: " +
	"the path the template's file has in a new project's folder, or for profile.toml its path in the state directory, " +
	"and empty for a template no project starts with. It reads the daemon's templates when the daemon on the control socket " +
	"runs this build, and the templates embedded in this binary otherwise, and prints the same either way. " +
	"It needs no daemon and no session."

// templateCopy is a set of templates `template show` reads: the daemon's, or the ones this binary embeds.
type templateCopy struct {
	names    []string
	writesTo func(name string) string
	text     func(name string) (string, error)
}

// embeddedTemplates is the copy this binary embeds.
func embeddedTemplates() templateCopy {
	return templateCopy{
		names:    templates.Names(),
		writesTo: projectfiles.WritesTo,
		text:     func(name string) (string, error) { return templates.Render(name, templates.Data{Example: true}) },
	}
}

// daemonTemplates reads the copy of the daemon on the control socket, with no session. It reports false when
// no daemon answers, the daemon serves no templates, or the daemon runs another build than this one: only the
// templates of this build are what its `new` writes.
func (c *invocation) daemonTemplates() (templateCopy, bool) {
	if c.socket == "" {
		return templateCopy{}, false
	}
	client := api.NewUnixClient(c.socket)
	defer client.Close()
	ctx, cancel := context.WithTimeout(c.ctx, daemonWait)
	defer cancel()
	list, err := client.Templates(ctx)
	if err != nil || len(list.Templates) == 0 || list.Version != version.Read("").Version {
		return templateCopy{}, false
	}
	names := make([]string, len(list.Templates))
	byName := make(map[string]api.Template, len(list.Templates))
	for i, template := range list.Templates {
		names[i] = template.Name
		byName[template.Name] = template
	}
	return templateCopy{
		names:    names,
		writesTo: func(name string) string { return byName[name].WritesTo },
		text:     func(name string) (string, error) { return byName[name].Content, nil },
	}, true
}

// templatesToRead is the daemon's copy when it runs this build, and the embedded copy otherwise.
func (c *invocation) templatesToRead() templateCopy {
	if copy, ok := c.daemonTemplates(); ok {
		return copy
	}
	return embeddedTemplates()
}

// templatesSection is the commands help prints under "Templates": the `new` commands, which write a new
// project's starting files, and `template show`. None acts on a project's records.
func templatesSection() []registered {
	show := command("template show", "[<name>] [--json]", "print a template as it renders for an example project, or list every template", (*invocation).templateShow)
	show.Detail = templateShowDetail
	return installation(append(newCommands(), show)...)
}

// templateShow is `ropeshift template show`: it reads the templates of the daemon on the control socket when
// that daemon runs this build, and the templates embedded in this binary otherwise.
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
	read := c.templatesToRead()
	listing := make([]templateListing, len(read.names))
	for i, name := range read.names {
		listing[i] = templateListing{Name: name, WritesTo: read.writesTo(name)}
	}
	return c.finish(nil, listing, func() string { return strings.Join(read.names, "\n") + "\n" })
}

// printTemplate prints one template as it renders with the example data.
func (c *invocation) printTemplate(name string) int {
	read := c.templatesToRead()
	if !slices.Contains(read.names, name) {
		return c.usageError("no template %q; the templates are:\n%s", name, strings.Join(read.names, "\n"))
	}
	text, err := read.text(name)
	if err != nil {
		fmt.Fprintf(c.stderr, "ropeshift %s: %v\n", c.name, err)
		return ExitError
	}
	return c.finish(nil, nil, func() string { return text })
}
