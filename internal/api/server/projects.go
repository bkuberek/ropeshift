package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// projectPrefix is where every project-scoped route lives, with the project's name after it (ADR-0021).
const projectPrefix = "/v1/projects/"

// router serves each project's routes on that project's ledger, and keeps one handler per project, so
// a call reaches only the database its path names.
type router struct {
	config Config
	// home is the project the plane protocol serves, whose handler the installation's routes and the
	// deprecated routes with no project use too.
	home *served
	mu   sync.Mutex
	// served holds the handler of each project that a call has reached, by the project's ID, which a
	// rename keeps.
	served map[ledger.WorkspaceID]*served
}

// served is one project's handler, with its routes as they read under /v1.
type served struct {
	handler *handler
	routes  *http.ServeMux
}

func newRouter(config Config, home *handler) *router {
	r := &router{config: config, served: map[ledger.WorkspaceID]*served{}}
	r.home = r.serve(home)
	r.served[config.Scope] = r.home
	return r
}

func (rt *router) serve(h *handler) *served {
	routes := http.NewServeMux()
	h.projectRoutes(routes)
	return &served{handler: h, routes: routes}
}

// project resolves the name a path holds to its project and its handler.
func (rt *router) project(ctx context.Context, name string) (ledger.Project, *served, error) {
	project, err := rt.config.Projects.Named(ctx, name)
	if err != nil {
		return ledger.Project{}, nil, err
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if s, ok := rt.served[project.ID]; ok {
		return project, s, nil
	}
	store, err := rt.config.Projects.Open(ctx, project)
	if err != nil {
		return ledger.Project{}, nil, err
	}
	config := rt.config
	config.Scope, config.Store = project.ID, store
	// Knowledge sources are the repositories the daemon reads, which its first project holds until a
	// project registers its own.
	config.Knowledge = nil
	// The first project's backup copies the first project's file, and no other's.
	config.Backup = nil
	if rt.config.Matching != nil {
		config.Matcher = rt.config.Matching(store)
	}
	s := rt.serve(newHandler(config))
	rt.served[project.ID] = s
	return project, s, nil
}

// ServeHTTP serves one call under /v1/projects/{project}/ on that project's routes, with the project's
// part of the path taken out.
func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	_, s, err := rt.project(r.Context(), name)
	if err != nil {
		failProject(w, name, err)
		return
	}
	inner, ok := withinProject(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.routes.ServeHTTP(w, inner)
}

// withinProject is a call as a project's routes read it: its path under /v1, with the project's two
// segments taken out of the escaped path, so an ID that holds a slash stays one segment.
func withinProject(r *http.Request) (*http.Request, bool) {
	segments := strings.SplitN(r.URL.EscapedPath(), "/", 5)
	if len(segments) < 5 || segments[1] != "v1" || segments[2] != "projects" {
		return nil, false
	}
	escaped := "/v1/" + segments[4]
	path, err := url.PathUnescape(escaped)
	if err != nil {
		return nil, false
	}
	inner := r.Clone(r.Context())
	inner.URL.Path, inner.URL.RawPath = path, ""
	if escaped != path {
		inner.URL.RawPath = escaped
	}
	return inner, true
}

// unscoped serves the routes that name no project on the plane protocol's project, for one release, so
// a worker one protocol version behind and a client written before projects keep working. Each answer
// says the route is deprecated.
func (rt *router) unscoped(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Deprecation", "true")
	rt.home.routes.ServeHTTP(w, r)
}

// failProject answers a call whose path names a project the installation does not hold, or whose ledger
// cannot open.
func failProject(w http.ResponseWriter, name string, err error) {
	if errors.Is(err, ports.ErrNotFound) {
		refuse(w, http.StatusNotFound, api.ErrorDetail{Code: api.CodeNoProject, Message: fmt.Sprintf("this installation holds no project named %q", name)})
		return
	}
	fail(w, err)
}

// registryRoutes are the installation's routes for its projects.
func (rt *router) registryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/projects", rt.listProjects)
	mux.HandleFunc("GET /v1/projects/{project}", rt.showProject)
}

// listProjects lists every project with the repositories it holds, which a client reads to find the
// project of the repository it runs in.
func (rt *router) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := rt.config.Projects.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	index, err := rt.config.Projects.Index(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	held := map[ledger.WorkspaceID][]string{}
	for _, entry := range index {
		held[entry.Project] = append(held[entry.Project], entry.Identity)
	}
	list := api.ProjectList{Projects: make([]api.Project, 0, len(projects))}
	for _, p := range projects {
		list.Projects = append(list.Projects, projectBody(p, held[p.ID]))
	}
	reply(w, http.StatusOK, list)
}

func projectBody(p ledger.Project, repositories []string) api.Project {
	if repositories == nil {
		repositories = []string{}
	}
	slices.Sort(repositories)
	return api.Project{
		ID: p.ID, Name: p.Name, DisplayName: p.DisplayName, State: p.State, CreatedAt: p.CreatedAt, Repositories: repositories,
	}
}

// showProject shows one project's record, its database, and the record of each repository it holds.
func (rt *router) showProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	project, s, err := rt.project(r.Context(), name)
	if err != nil {
		failProject(w, name, err)
		return
	}
	detail := api.ProjectDetail{Database: project.Database, Records: []ledger.Repository{}}
	var identities []string
	err = s.handler.Store.Read(r.Context(), project.ID, func(tx ports.ReadTx) error {
		records, err := tx.Repositories().Repositories()
		for _, record := range records {
			identities = append(identities, ledger.RepositoryIdentity(record.Remote, record.ID))
			detail.Records = append(detail.Records, record)
		}
		return err
	})
	if err != nil {
		fail(w, err)
		return
	}
	detail.Project = projectBody(project, identities)
	reply(w, http.StatusOK, detail)
}

// oneProject is the registry of a control plane that serves one ledger, named default.
type oneProject struct {
	project ledger.Project
	store   ports.Store
}

func (o oneProject) List(context.Context) ([]ledger.Project, error) {
	return []ledger.Project{o.project}, nil
}

func (o oneProject) Named(_ context.Context, name string) (ledger.Project, error) {
	if name != o.project.Name {
		return ledger.Project{}, fmt.Errorf("project %s: %w", name, ports.ErrNotFound)
	}
	return o.project, nil
}

func (o oneProject) Open(context.Context, ledger.Project) (ports.Store, error) { return o.store, nil }

func (o oneProject) Index(ctx context.Context) ([]ledger.IndexedRepository, error) {
	var out []ledger.IndexedRepository
	err := o.store.Read(ctx, o.project.ID, func(tx ports.ReadTx) error {
		records, err := tx.Repositories().Repositories()
		for _, record := range records {
			out = append(out, ledger.IndexedRepository{Identity: ledger.RepositoryIdentity(record.Remote, record.ID), Project: o.project.ID, Repository: record.ID})
		}
		return err
	})
	return out, err
}
