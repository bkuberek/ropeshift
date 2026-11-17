package entry

import (
	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/templates"
)

// renderProjectTemplate renders a template of this binary over a new project's starting data. The worker never
// imports the templates, so the renderer it writes a shape's files with is passed here, as `ropeshift new` has its
// own (FR-TPL-5).
func renderProjectTemplate(name string, data projectfiles.Data) (string, error) {
	converted := templates.Data{Project: data.Project}
	for _, r := range data.Repositories {
		converted.Repositories = append(converted.Repositories, templates.Repository{Name: r.Name, Remote: r.Remote})
	}
	return templates.Render(name, converted)
}

// soloTemplates is what a worker writes a member's new project folder with: this machine's folders and git, the
// checkouts it keeps, which no folder may lie in unless its clone ignores it, and a state directory for the
// journal of each request.
func soloTemplates(stateDir string, kept ports.Checkouts) *worker.TemplateRuntime {
	return &worker.TemplateRuntime{Folders: projectfolders.Folders{}, Checkouts: kept, Render: renderProjectTemplate, StateDir: stateDir}
}
