// Package templates holds the project templates: the files a project starts from, as Go text/templates
// embedded in the binary. Every file here except this one, its tests, and README.md ends in .template, so
// intake never reads one as a plan file and an agent CLI never loads one as instructions.
package templates

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"text/template"
)

// suffix ends the file name of every template. A template's name is its path without it.
const suffix = ".template"

// A directory named here embeds with everything under it, so a template in a new directory needs the
// directory listed. The templates test fails for a template that Names does not list.
//
//go:embed *.template monorepo policies repository user workspace
var files embed.FS

// ErrUnknown is the error of a name that no template has.
var ErrUnknown = errors.New("no such template")

// Repository is one code repository of a project: its name, and the remote it is cloned from.
type Repository struct {
	Name   string
	Remote string
}

// Data is what a template renders over.
//
// With Example true, a template renders the example the templates describe: the project acme, with the
// repositories acme-api and acme-web. Project and Repositories are then ignored.
//
// With Example false, a template renders the starting file for the project named Project, with one entry for
// each of Repositories, or none.
type Data struct {
	Project      string
	Repositories []Repository
	Example      bool
	// WorktreeRoot is the worktree root user/profile.toml sets. Empty leaves the root commented out, so the
	// built-in root holds.
	WorktreeRoot string
}

// example is the data of the acme example.
var example = Data{
	Project: "acme",
	Repositories: []Repository{
		{Name: "acme-api", Remote: "git@github.com:acme/acme-api.git"},
		{Name: "acme-web", Remote: "git@github.com:acme/acme-web.git"},
	},
	Example: true,
}

// Workspace is the name of the planning repository's folder: the project's name, and in the example, the
// project's name with -workspace after it.
func (d Data) Workspace() string {
	if d.Example {
		return d.Project + "-workspace"
	}
	return d.Project
}

// Names lists every template by its path without .template, sorted: workspace/agents.md,
// workspace/plan/contract.md, user/profile.toml, policies/team/decision-policy.toml, and the others.
func Names() []string {
	var names []string
	_ = fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name, ok := strings.CutSuffix(path, suffix); ok && !entry.IsDir() {
			names = append(names, name)
		}
		return nil
	})
	slices.Sort(names)
	return names
}

// tomlString is the template function that writes text as a TOML basic string, quotes included: the quote, the
// backslash, and every control character are escaped, and bytes that are not UTF-8 become U+FFFD, so no text
// ends the string, adds a line, or makes the file invalid.
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

// Render returns the text of the template named name, rendered over data. A name that no template has is
// ErrUnknown.
func Render(name string, data Data) (string, error) {
	source, err := fs.ReadFile(files, name+suffix)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	if data.Example {
		data = example
	}
	parsed, err := template.New(name).Option("missingkey=error").Funcs(template.FuncMap{"tomlString": tomlString}).Parse(string(source))
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	var out strings.Builder
	if err := parsed.Execute(&out, data); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return out.String(), nil
}
