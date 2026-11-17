package cli_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/templates"
)

// bare runs a command as a process that holds no session, no delegate credential, and no control plane
// socket would, which is all `template show` may need.
func bare(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr, cli.Options{})
	return code, stdout.String(), stderr.String()
}

// FR-TPL-3: a template prints exactly as the templates package renders it with the example data, which is what
// `new` writes from starting data in the same shape.
func TestTemplateShowPrintsEachTemplateAsRenderReturnsIt(t *testing.T) {
	names := templates.Names()
	if len(names) < 20 {
		t.Fatalf("%d templates, so the package read too few", len(names))
	}
	for _, name := range names {
		want, err := templates.Render(name, templates.Data{Example: true})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		code, stdout, stderr := bare("template", "show", name)
		if code != cli.ExitOK || stderr != "" {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr)
			continue
		}
		if stdout != want {
			t.Errorf("%s: printed %d bytes that are not what Render returns, %d bytes", name, len(stdout), len(want))
		}
	}
}

// With no name, every name prints on a line of its own, sorted.
func TestTemplateShowWithNoNameListsEveryName(t *testing.T) {
	code, stdout, stderr := bare("template", "show")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := strings.Join(templates.Names(), "\n") + "\n"
	if stdout != want {
		t.Errorf("listed\n%s\nwant\n%s", stdout, want)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if !slices.IsSorted(lines) {
		t.Errorf("the names are not sorted: %v", lines)
	}
}

// listing is one element of the JSON list.
type listing struct {
	Name     string `json:"name"`
	WritesTo string `json:"writes_to"`
}

// With --json, the list is an array of objects with name and writes_to: the path `new` or `init` writes the
// template to, and empty for one neither writes.
func TestTemplateShowJSONListsEachNameWithWhereItIsWritten(t *testing.T) {
	code, stdout, stderr := bare("template", "show", "--json")
	if code != cli.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var got []listing
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("%v: %s", err, stdout)
	}
	names := templates.Names()
	if len(got) != len(names) {
		t.Fatalf("%d entries for %d templates", len(got), len(names))
	}
	byName := map[string]string{}
	for i, entry := range got {
		if entry.Name != names[i] {
			t.Errorf("entry %d is %q, want %q", i, entry.Name, names[i])
		}
		byName[entry.Name] = entry.WritesTo
	}
	// Every template a shape of `new` or `init` writes, with its path. P4e's table is the other half of this.
	written := map[string]string{
		"workspace/agents.md":            "AGENTS.md",
		"monorepo/agents.md":             "AGENTS.md",
		"repository/agents.md":           "AGENTS.md",
		"claude.md":                      "CLAUDE.md",
		"workspace/makefile":             "Makefile",
		"workspace/gitignore":            ".gitignore",
		"monorepo/gitignore":             ".gitignore",
		"repository/gitignore":           ".gitignore",
		"workspace/plan/goal.md":         "plan/goal.md",
		"workspace/plan/plan.md":         "plan/plan.md",
		"workspace/plan/requirements.md": "plan/requirements.md",
		"workspace/plan/repositories.md": "plan/repositories.md",
		"repository/catalog-info.yaml":   "catalog-info.yaml",
		"security.md":                    "SECURITY.md",
		"codeowners":                     "CODEOWNERS",
		"pre-commit-config.yaml":         ".pre-commit-config.yaml",
		"user/profile.toml":              "profile.toml",
	}
	for name, path := range written {
		if byName[name] != path {
			t.Errorf("%s writes to %q, want %q", name, byName[name], path)
		}
	}
	for name, path := range byName {
		if _, ok := written[name]; !ok && path != "" {
			t.Errorf("%s writes to %q, and no shape writes it", name, path)
		}
	}
	for _, name := range []string{"dependabot.yml", "workspace/plan/contract.md", "workspace/docs/decision.md", "repository/profile.toml", "policies/team/lane-policy.toml"} {
		if path, ok := byName[name]; !ok || path != "" {
			t.Errorf("%s is %q (listed %v), want a listed template that nothing writes", name, path, ok)
		}
	}
	// An empty path is an empty string and not null, so a script reads it with no check.
	if !strings.Contains(stdout, `"writes_to":""`) {
		t.Errorf("a template nothing writes has no empty writes_to: %s", stdout)
	}
}

// An unknown name is a usage error that lists the names, so a person finds the one they meant.
func TestTemplateShowRefusesAnUnknownNameAndListsTheNames(t *testing.T) {
	code, stdout, stderr := bare("template", "show", "workspace/agent.md")
	if code != cli.ExitUsage || stdout != "" {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	if !strings.Contains(stderr, `"workspace/agent.md"`) {
		t.Errorf("stderr does not name the template asked for: %q", stderr)
	}
	for _, name := range templates.Names() {
		if !strings.Contains(stderr, name+"\n") {
			t.Errorf("stderr does not list %s: %q", name, stderr)
		}
	}
}

// The list is a list, and a name is one template: either way the command refuses what does not fit.
func TestTemplateShowRefusesWhatDoesNotFit(t *testing.T) {
	for name, args := range map[string][]string{
		"two names":              {"template", "show", "claude.md", "security.md"},
		"a name with --json":     {"template", "show", "claude.md", "--json"},
		"a project":              {"template", "show", "--project", "acme"},
		"an unknown flag":        {"template", "show", "--format", "x"},
		"a bare template group":  {"template"},
		"an unknown subcommand":  {"template", "list"},
		"a name that is a group": {"template", "show", "workspace"},
	} {
		code, stdout, stderr := bare(args...)
		if code != cli.ExitUsage || stdout != "" || stderr == "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
	}
	if code, _, _ := bare("template", "show", "-h"); code != cli.ExitOK {
		t.Errorf("template show -h exits %d", code)
	}
}

// FR-TPL-1's no-daemon rule: with no socket, with a socket that nothing listens on, and with no session, the
// command reads nothing but the binary.
func TestTemplateShowNeedsNoDaemonAndNoSession(t *testing.T) {
	want, err := templates.Render("claude.md", templates.Data{Example: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, options := range map[string]cli.Options{
		"no socket":          {},
		"a dead socket":      {Socket: filepath.Join(t.TempDir(), "no-daemon.sock")},
		"a stale session":    {Socket: filepath.Join(t.TempDir(), "no-daemon.sock"), Session: "stale"},
		"a delegate":         {Socket: filepath.Join(t.TempDir(), "no-daemon.sock"), Delegate: "dlg_stale"},
		"an unnamed project": {Project: "no-such-project"},
	} {
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{"template", "show", "claude.md"}, &stdout, &stderr, options)
		if code != cli.ExitOK || stdout.String() != want || stderr.Len() != 0 {
			t.Errorf("%s: exit %d, stderr %q", name, code, stderr.String())
		}
	}
}

// The command sits in its own help section, "Templates", after "This machine", and takes no project.
func TestTemplateShowIsInTheTemplatesSectionAfterThisMachine(t *testing.T) {
	groups := cli.Groups()
	last := len(groups) - 1
	if last < 1 || groups[last].Title != "Templates" || groups[last-1].Title != "This machine" {
		titles := make([]string, len(groups))
		for i, g := range groups {
			titles[i] = g.Title
		}
		t.Fatalf("the sections end %v", titles[max(0, last-2):])
	}
	var found bool
	for _, command := range groups[last].Commands {
		found = found || command.Name == "template show"
	}
	if !found {
		t.Errorf("the Templates section holds %+v", groups[last].Commands)
	}
	// A command on the installation's records takes no --project; a project command's usage names it.
	_, stdout, _ := bare("template", "show", "-h")
	if strings.Contains(stdout, "-project") {
		t.Errorf("template show takes a project:\n%s", stdout)
	}
}
