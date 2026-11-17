package entry

import (
	"fmt"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
)

// renderedTemplates is every template this binary embeds, sorted by name, each rendered with the example data
// as `ropeshift template show` prints it, with the shapes of `new` that write it and the path it is written
// to. The server renders it once at start and serves it to the web UI.
func renderedTemplates() ([]api.Template, error) {
	names := templates.Names()
	list := make([]api.Template, len(names))
	for i, name := range names {
		content, err := templates.Render(name, templates.Data{Example: true})
		if err != nil {
			return nil, fmt.Errorf("render template %s: %w", name, err)
		}
		shapes := projectfiles.ShapesOf(name)
		listed := make([]string, len(shapes))
		for j, shape := range shapes {
			listed[j] = string(shape)
		}
		list[i] = api.Template{Name: name, Shapes: listed, WritesTo: projectfiles.WritesTo(name), Content: content}
	}
	return list, nil
}
