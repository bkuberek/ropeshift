package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
)

// EnvProject names the project a command acts on when it names none (FR-PROJ-4).
const EnvProject = "ROPESHIFT_PROJECT"

// ProjectsPath is the installation's registry of projects, and each project's routes sit under it, at
// ProjectsPath/{project}/ (ADR-0021).
const ProjectsPath = "/v1/projects"

// Project is one project of the installation, with the identity of each repository it holds.
type Project struct {
	ID           ledger.WorkspaceID  `json:"id"`
	Name         string              `json:"name"`
	DisplayName  string              `json:"display_name,omitempty"`
	State        ledger.ProjectState `json:"state"`
	CreatedAt    int64               `json:"created_at"`
	Repositories []string            `json:"repositories"`
}

// ProjectList is every project of the installation, oldest first.
type ProjectList struct {
	Projects []Project `json:"projects"`
}

// ProjectDetail is one project with its database and the record of each repository it holds.
type ProjectDetail struct {
	Project
	Database string              `json:"database"`
	Records  []ledger.Repository `json:"records"`
}

func projectPath(name string, rest ...string) string {
	path := ProjectsPath + "/" + url.PathEscape(name)
	for _, part := range rest {
		path += "/" + url.PathEscape(part)
	}
	return path
}

// Projects lists the installation's projects.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	var out ProjectList
	err := c.Do(ctx, http.MethodGet, ProjectsPath, nil, &out)
	return out.Projects, err
}

// ProjectRecord shows one project, its database, and its repositories.
func (c *Client) ProjectRecord(ctx context.Context, name string) (ProjectDetail, error) {
	var out ProjectDetail
	err := c.Do(ctx, http.MethodGet, projectPath(name), nil, &out)
	return out, err
}

// ProjectClient calls one project's routes, each under /v1/projects/{project}/, and the installation's
// routes through the Client it carries. A record of another project is out of its reach.
type ProjectClient struct {
	*Client
	name string
}

// Project returns a client of one project's routes, which shares this client's connection and session.
func (c *Client) Project(name string) *ProjectClient { return &ProjectClient{Client: c, name: name} }

// Name is the project this client calls.
func (p *ProjectClient) Name() string { return p.name }

// Route is where a project route, as it reads under /v1, lives for this project.
func (p *ProjectClient) Route(path string) string { return InProject(p.name, path) }

// InProject is where a project route, as it reads under /v1, lives for one project.
func InProject(project, path string) string {
	return projectPath(project) + strings.TrimPrefix(path, "/v1")
}

// do runs one call on a project route, given as it reads under /v1.
func (p *ProjectClient) do(ctx context.Context, method, path string, in, out any) error {
	return p.Do(ctx, method, p.Route(path), in, out)
}

// WithCredential returns a client of the same project that presents a worker's credential.
func (p *ProjectClient) WithCredential(credential string) *ProjectClient {
	return p.Client.WithCredential(credential).Project(p.name)
}
