package api

import (
	"context"
	"net/http"
)

// TemplatesPath lists the project templates the daemon's binary embeds.
const TemplatesPath = "/v1/templates"

// Template is one project template, as the daemon's binary renders it with the example data.
type Template struct {
	// Name is the template's path without its suffix, such as workspace/agents.md.
	Name string `json:"name"`
	// Shapes are the `new` shapes that write the template, in the order workspace, monorepo, repository. It is
	// empty for a template no shape writes.
	Shapes []string `json:"shapes"`
	// WritesTo is the path the template's file has in a new project's folder, profile.toml for the user profile
	// in the state directory, and empty for a template nothing writes.
	WritesTo string `json:"writes_to"`
	// Content is the text `ropeshift template show` prints for the template.
	Content string `json:"content"`
}

// TemplateList is every template of the daemon's binary, sorted by name, with the version of that binary.
type TemplateList struct {
	// Version is the build version of the daemon's binary, so a reader can tell whether the templates are the
	// ones its own binary embeds.
	Version   string     `json:"version"`
	Templates []Template `json:"templates"`
}

// Templates reads the templates the daemon's binary embeds.
func (c *Client) Templates(ctx context.Context) (TemplateList, error) {
	var out TemplateList
	err := c.Do(ctx, http.MethodGet, TemplatesPath, nil, &out)
	return out, err
}
