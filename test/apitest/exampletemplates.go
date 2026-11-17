package apitest

import (
	"fmt"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
)

// ExampleTemplates is the list a daemon serves for the templates its binary embeds, derived from the table of
// the files each shape writes and not from the helpers that table offers: each template with its text rendered
// over the example data, the shapes whose files come from it, and the path those files have.
func ExampleTemplates() ([]api.Template, error) {
	shapes := map[string][]string{}
	paths := map[string]string{"user/profile.toml": "profile.toml"}
	for _, shape := range projectfiles.Shapes() {
		data := projectfiles.Data{Project: "acme"}
		if shape == projectfiles.Workspace {
			data.Repositories = []projectfiles.CodeRepository{{Name: "acme-api", Remote: "git@example.invalid:acme/acme-api.git"}}
		}
		files, err := projectfiles.Files(shape, data, func(name string, _ projectfiles.Data) (string, error) { return name, nil })
		if err != nil {
			return nil, fmt.Errorf("%s: %w", shape, err)
		}
		for _, file := range files {
			shapes[file.Template] = append(shapes[file.Template], string(shape))
			paths[file.Template] = file.Path
		}
	}
	var list []api.Template
	for _, name := range templates.Names() {
		content, err := templates.Render(name, templates.Data{Example: true})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		listed := shapes[name]
		if listed == nil {
			listed = []string{}
		}
		list = append(list, api.Template{Name: name, Shapes: listed, WritesTo: paths[name], Content: content})
	}
	return list, nil
}
