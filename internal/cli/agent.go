package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/BurntSushi/toml"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/roster"
)

// agentCommands are the agent roster's commands (ADR-0025). A command that changes the roster names its
// scope with --installation or --project, and never takes it from ROPESHIFT_PROJECT or the directory it
// runs in, because a change in the wrong scope silently changes another project's agents.
func agentCommands() []registered {
	return []registered{
		command("agent list", "[--installation]", "list the resolved agent roster, each agent with its state and source", (*invocation).agentList),
		command("agent show", "<name> [--installation]", "show one agent's definition, each field's source, and its model per driver", (*invocation).agentShow),
		command("agent set", "<name> <field>=<value>... (--installation | --project P)", "change an agent's fields; purpose, instructions, and report take @<file>", (*invocation).agentSet),
		command("agent add", "<name> [--from N | --file F] (--installation | --project P)", "add an agent, copied from another or read from a TOML file", (*invocation).agentAdd),
		command("agent disable", "<name> (--installation | --project P)", "stop sessions from receiving an agent nothing names", agentToggle(roster.OpDisable)),
		command("agent enable", "<name> (--installation | --project P)", "let sessions receive an agent again", agentToggle(roster.OpEnable)),
		command("agent reset", "<name> (--installation | --project P)", "remove a scope's changes to an agent, or an agent the scope added", agentToggle(roster.OpReset)),
		command("agent history", "[<name>] [--installation]", "read a scope's roster versions, each with what it changed", (*invocation).agentHistory),
		command("agent revert", "<version> (--installation | --project P)", "record an earlier roster version as the newest", (*invocation).agentRevert),
		command("agent guide", "(show | set @<file> | reset) [--installation | --project P]", "read, change, or reset the routing guide", (*invocation).agentGuide),
	}
}

// rosterScope is where an agent command reads or writes: the installation's layer, or a project's.
type rosterScope struct {
	installation bool
	project      *api.ProjectClient
	client       *api.Client
}

func (s rosterScope) close() { s.client.Close() }

func (s rosterScope) view(c *invocation) (api.RosterView, error) {
	if s.installation {
		return s.client.InstallationRoster(c.ctx)
	}
	return s.project.Roster(c.ctx)
}

func (s rosterScope) change(c *invocation, change roster.Change) (roster.Version, error) {
	if s.installation {
		return s.client.ChangeInstallationRoster(c.ctx, change)
	}
	return s.project.ChangeRoster(c.ctx, change)
}

// agentFlags adds --installation to a command's flags.
func (c *invocation) agentFlags() (*flag.FlagSet, *bool) {
	set := c.flags()
	return set, set.Bool("installation", false, "the installation's layer, which every project resolves under its own")
}

// readScope is where a read looks: the installation's layer when asked, and otherwise the project the
// command resolves, as every project command does.
func (c *invocation) readScope(installation bool) (rosterScope, int) {
	if installation {
		return rosterScope{installation: true, client: c.client()}, ExitOK
	}
	project, code := c.projectClient()
	if project == nil {
		return rosterScope{}, code
	}
	return rosterScope{project: project, client: project.Client}, ExitOK
}

// changeScope is where a change lands, which the command line must name: --installation, or --project
// given on it.
func (c *invocation) changeScope(installation bool) (rosterScope, int) {
	switch {
	case installation && c.project != "":
		return rosterScope{}, c.usageError("name one scope: --installation or --project, not both")
	case installation:
		return rosterScope{installation: true, client: c.client()}, ExitOK
	case c.project != "":
		client := c.client()
		return rosterScope{project: client.Project(c.project), client: client}, ExitOK
	}
	return rosterScope{}, c.usageError("a change to the roster names its scope: --installation, or --project with the project's name")
}

func (c *invocation) agentList(args []string) int {
	set, installation := c.agentFlags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 0 {
		return c.usageError("unexpected argument %q", positional[0])
	}
	scope, code := c.readScope(*installation)
	if code != ExitOK {
		return code
	}
	defer scope.close()
	view, err := scope.view(c)
	return c.finish(err, view, func() string { return rosterTable(view) })
}

func rosterTable(view api.RosterView) string {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tACCESS\tCLASS\tEFFORT\tAPPROVAL\tSTATE\tSOURCE")
	for _, agent := range view.Roster.Agents {
		state := "enabled"
		if !agent.Enabled {
			state = "disabled"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", agent.Name, agent.Kind, agent.Access, agent.Class, agent.Effort, agent.Approval, state, sourcesText(agent.Sources))
	}
	_ = w.Flush()
	fmt.Fprintf(&b, "routing guide from %s; roster %s (installation version %d, project version %d)\n",
		view.Roster.GuideSource, view.Roster.Digest, view.InstallationVersion, view.ProjectVersion)
	return b.String()
}

// sourcesText names every layer an agent's fields come from, the lowest first.
func sourcesText(sources map[string]string) string {
	var out []string
	for _, layer := range []string{roster.SourceBuiltin, roster.SourceInstallation, roster.SourceProject} {
		if slices.Contains(slices.Collect(maps.Values(sources)), layer) {
			out = append(out, layer)
		}
	}
	return strings.Join(out, "+")
}

func (c *invocation) agentShow(args []string) int {
	set, installation := c.agentFlags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("name one agent")
	}
	scope, code := c.readScope(*installation)
	if code != ExitOK {
		return code
	}
	defer scope.close()
	view, err := scope.view(c)
	if err != nil {
		return c.failure(err)
	}
	agent, found := view.Roster.Agent(positional[0])
	if !found {
		c.printError(api.ErrorDetail{Code: api.CodeNotFound, Message: fmt.Sprintf("the resolved roster holds no agent %q", positional[0])})
		return ExitNotFound
	}
	shown := struct {
		roster.ResolvedAgent
		Rendered map[string]string `json:"rendered_models,omitempty"`
	}{agent, view.Models[agent.Name]}
	return c.finish(nil, shown, func() string { return agentText(agent, view.Models[agent.Name]) })
}

func agentText(agent roster.ResolvedAgent, models map[string]string) string {
	var b bytes.Buffer
	w := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	row := func(field, value string) {
		fmt.Fprintf(w, "%s\t%s\t(%s)\n", field, cellText(value), agent.Sources[field])
	}
	fmt.Fprintf(w, "name\t%s\t\n", agent.Name)
	row("kind", agent.Kind)
	row("access", agent.Access)
	row("class", agent.Class)
	row("effort", agent.Effort)
	row("approval", agent.Approval)
	row("enabled", strconv.FormatBool(agent.Enabled))
	row("tools.allow", strings.Join(agent.Tools.Allow, ","))
	row("tools.deny", strings.Join(agent.Tools.Deny, ","))
	for _, driver := range slices.Sorted(maps.Keys(agent.Models)) {
		row("models."+driver, agent.Models[driver])
	}
	for _, driver := range slices.Sorted(maps.Keys(models)) {
		fmt.Fprintf(w, "runs on %s\t%s\t\n", driver, models[driver])
	}
	_ = w.Flush()
	for _, field := range []struct{ name, text string }{{"purpose", agent.Purpose}, {"instructions", agent.Instructions}, {"report", agent.Report}} {
		fmt.Fprintf(&b, "\n%s (%s):\n%s\n", field.name, agent.Sources[field.name], strings.TrimSpace(field.text))
	}
	return b.String()
}

// cellText shortens text a table row holds.
func cellText(text string) string {
	line, _, more := strings.Cut(strings.TrimSpace(text), "\n")
	if more || len(line) > 60 {
		return line[:min(len(line), 57)] + "..."
	}
	return line
}

func (c *invocation) agentSet(args []string) int {
	set, installation := c.agentFlags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) < 2 {
		return c.usageError("name an agent and at least one <field>=<value>")
	}
	fields := map[string]string{}
	for _, pair := range positional[1:] {
		field, value, found := strings.Cut(pair, "=")
		if !found || field == "" {
			return c.usageError("%q is not <field>=<value>", pair)
		}
		text, err := c.fileValue(field, value)
		if err != nil {
			return c.usageError("%v", err)
		}
		fields[field] = text
	}
	return c.changeRoster(*installation, roster.Change{Op: roster.OpSet, Agent: positional[0], Fields: fields})
}

// fileValue reads `@<file>` for the fields that hold text, and takes every other value as it stands.
func (c *invocation) fileValue(field, value string) (string, error) {
	path, isFile := strings.CutPrefix(value, "@")
	if !isFile || !slices.Contains([]string{"purpose", "instructions", "report"}, field) {
		return value, nil
	}
	raw, err := c.readLocal(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	return strings.TrimSpace(string(raw)), nil
}

func (c *invocation) agentAdd(args []string) int {
	set, installation := c.agentFlags()
	from := set.String("from", "", "the agent to copy")
	file := set.String("file", "", "a TOML file holding the agent's definition")
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("name the agent to add")
	}
	change := roster.Change{Op: roster.OpAdd, Agent: positional[0], From: *from}
	if *file != "" {
		definition, err := c.readAgentFile(*file)
		if err != nil {
			return c.usageError("%v", err)
		}
		change.Definition = &definition
	}
	return c.changeRoster(*installation, change)
}

// agentFile is an agent's definition as a TOML file holds it: one `[[agent]]` table, or its keys at the
// top of the file.
type agentFile struct {
	Name         string            `toml:"name"`
	Purpose      string            `toml:"purpose"`
	Kind         string            `toml:"kind"`
	Access       string            `toml:"access"`
	Class        string            `toml:"class"`
	Models       map[string]string `toml:"models"`
	Effort       string            `toml:"effort"`
	Approval     string            `toml:"approval"`
	Instructions string            `toml:"instructions"`
	Report       string            `toml:"report"`
	Enabled      *bool             `toml:"enabled"`
	Tools        struct {
		Allow []string `toml:"allow"`
		Deny  []string `toml:"deny"`
	} `toml:"tools"`
}

func (c *invocation) readAgentFile(path string) (roster.Agent, error) {
	raw, err := c.readLocal(path)
	if err != nil {
		return roster.Agent{}, err
	}
	var wrapped struct {
		Agent []agentFile `toml:"agent"`
	}
	meta, err := toml.Decode(string(raw), &wrapped)
	if err != nil {
		return roster.Agent{}, fmt.Errorf("%s: %w", path, err)
	}
	var one agentFile
	switch {
	case len(wrapped.Agent) == 1 && len(meta.Undecoded()) == 0:
		one = wrapped.Agent[0]
	case len(wrapped.Agent) > 1:
		return roster.Agent{}, errors.New(path + ": a file adds one agent")
	default:
		meta, err := toml.Decode(string(raw), &one)
		if err != nil {
			return roster.Agent{}, fmt.Errorf("%s: %w", path, err)
		}
		if unknown := meta.Undecoded(); len(unknown) > 0 {
			return roster.Agent{}, fmt.Errorf("%s: unknown key %s", path, unknown[0])
		}
	}
	enabled := one.Enabled == nil || *one.Enabled
	return roster.Agent{
		Name: one.Name, Purpose: strings.TrimSpace(one.Purpose), Kind: one.Kind, Access: one.Access, Class: one.Class, Models: one.Models,
		Effort: one.Effort, Approval: one.Approval, Instructions: strings.TrimSpace(one.Instructions), Report: strings.TrimSpace(one.Report),
		Enabled: enabled, Tools: roster.Tools{Allow: one.Tools.Allow, Deny: one.Tools.Deny},
	}, nil
}

// agentToggle runs disable, enable, or reset, which each name one agent.
func agentToggle(op string) func(*invocation, []string) int {
	return func(c *invocation, args []string) int {
		set, installation := c.agentFlags()
		positional, ok := c.parse(set, args)
		if !ok {
			return ExitUsage
		}
		if len(positional) != 1 {
			return c.usageError("name one agent")
		}
		return c.changeRoster(*installation, roster.Change{Op: op, Agent: positional[0]})
	}
}

func (c *invocation) changeRoster(installation bool, change roster.Change) int {
	scope, code := c.changeScope(installation)
	if code != ExitOK {
		return code
	}
	defer scope.close()
	version, err := scope.change(c, change)
	return c.finish(err, version, func() string { return versionLine(version) })
}

func versionLine(v roster.Version) string {
	return fmt.Sprintf("%s roster version %d: %s\n", v.Scope, v.Version, v.Summary)
}

func (c *invocation) agentHistory(args []string) int {
	set, installation := c.agentFlags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) > 1 {
		return c.usageError("name at most one agent")
	}
	agent := ""
	if len(positional) == 1 {
		agent = positional[0]
	}
	scope, code := c.readScope(*installation)
	if code != ExitOK {
		return code
	}
	defer scope.close()
	var history api.RosterHistory
	var err error
	if scope.installation {
		history, err = scope.client.InstallationRosterHistory(c.ctx, agent)
	} else {
		history, err = scope.project.RosterHistory(c.ctx, agent)
	}
	return c.finish(err, history, func() string { return historyText(history) })
}

func historyText(history api.RosterHistory) string {
	if len(history.Versions) == 0 {
		return "the " + history.Scope + " layer has no version: it is the layer below as it stands\n"
	}
	var b strings.Builder
	for _, entry := range history.Versions {
		fmt.Fprintf(&b, "version %d  %s  by %s", entry.Version.Version, entry.Summary, entry.AuthoredBy)
		if entry.PreparedBy != "" {
			fmt.Fprintf(&b, ", prepared by %s", entry.PreparedBy)
		}
		fmt.Fprintf(&b, "  %s\n", entry.Digest)
		for _, line := range entry.Difference {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	return b.String()
}

func (c *invocation) agentRevert(args []string) int {
	set, installation := c.agentFlags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) != 1 {
		return c.usageError("name the version to revert to")
	}
	version, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil || version < 1 {
		return c.usageError("a version is a number from 1, not %q", positional[0])
	}
	scope, code := c.changeScope(*installation)
	if code != ExitOK {
		return code
	}
	defer scope.close()
	var out roster.Version
	if scope.installation {
		out, err = scope.client.RevertInstallationRoster(c.ctx, version)
	} else {
		out, err = scope.project.RevertRoster(c.ctx, version)
	}
	return c.finish(err, out, func() string { return versionLine(out) })
}

func (c *invocation) agentGuide(args []string) int {
	set, installation := c.agentFlags()
	positional, ok := c.parse(set, args)
	if !ok {
		return ExitUsage
	}
	if len(positional) == 0 {
		return c.usageError("say show, set @<file>, or reset")
	}
	switch {
	case positional[0] == "show" && len(positional) == 1:
		scope, code := c.readScope(*installation)
		if code != ExitOK {
			return code
		}
		defer scope.close()
		view, err := scope.view(c)
		guide := struct {
			Guide  string `json:"guide"`
			Source string `json:"source"`
		}{view.Roster.Guide, view.Roster.GuideSource}
		return c.finish(err, guide, func() string { return guide.Guide + "\n\n(from " + guide.Source + ")\n" })
	case positional[0] == "set" && len(positional) == 2:
		text, err := c.fileValue("instructions", positional[1])
		if err != nil {
			return c.usageError("%v", err)
		}
		if !strings.HasPrefix(positional[1], "@") {
			return c.usageError("the guide is read from @<file>")
		}
		return c.changeRoster(*installation, roster.Change{Op: roster.OpGuideSet, Guide: text})
	case positional[0] == "reset" && len(positional) == 1:
		return c.changeRoster(*installation, roster.Change{Op: roster.OpGuideReset})
	}
	return c.usageError("say show, set @<file>, or reset")
}
