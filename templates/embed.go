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
	parsed, err := template.New(name).Option("missingkey=error").Parse(string(source))
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	var out strings.Builder
	if err := parsed.Execute(&out, data); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return out.String(), nil
}
