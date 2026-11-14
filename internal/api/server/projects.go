package server

import (
	"cmp"
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
	// home is the project the plane protocol serves, whose handler the installation's routes use too.
	home *served
	mu   sync.Mutex
	// served holds the handler of each project that a call has reached, by the project's ID, which a
	// rename keeps.
	served map[ledger.WorkspaceID]*served
	// root is the whole API, which each project's handler holds to run a confirmed card's call again.
	root http.Handler
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
	if rt.config.Matching != nil {
		config.Matcher = rt.config.Matching(store)
	}
	h := newHandler(config)
	h.root, h.plane = rt.root, rt.home.handler.plane
	s := rt.serve(h)
	rt.served[project.ID] = s
	return project, s, nil
}

// ServeHTTP serves one call under /v1/projects/{project}/ on that project's routes, with the project's
// part of the path taken out.
func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("project")
	project, s, err := rt.project(r.Context(), name)
	if err != nil {
		failProject(w, name, err)
		return
	}
	inner, ok := withinProject(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if refuseArchived(w, inner, project) {
		return
	}
	s.routes.ServeHTTP(w, inner.WithContext(withProject(inner.Context(), project)))
}

// backupRoute is the one change an archived project takes, because a backup changes nothing in it.
const backupRoute = "/v1/backup"

// refuseArchived refuses a change to an archived project, which is read-only in every surface until it
// is restored, and reports whether it did.
func refuseArchived(w http.ResponseWriter, r *http.Request, p ledger.Project) bool {
	if p.State != ledger.ProjectArchived || !changes(r.Method) || r.URL.Path == backupRoute {
		return false
	}
	fail(w, fmt.Errorf("%w: restore project %s to change it", ledger.ErrProjectArchived, p.Name))
	return true
}

// forget drops the handler of a project the installation removed. The first project's handler still
// serves the installation's routes, whose records live in the installation's database.
func (rt *router) forget(id ledger.WorkspaceID) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.served, id)
	rt.home.handler.plane.Forget(id)
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
	mux.HandleFunc("GET "+api.NeedsYouPath, rt.combinedNeeds)
	mux.HandleFunc("GET /v1/projects/{project}", rt.showProject)
	rt.home.handler.installationBackupRoutes(mux)
}

func (h *handler) installationBackupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/installation/backup", h.member(h.backupInstallation))
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
		body := projectBody(p, held[p.ID])
		if p.State == ledger.ProjectActive {
			needs, err := rt.needsOf(r.Context(), p)
			if err != nil {
				fail(w, err)
				return
			}
			body.Waiting = len(needs.Items)
		}
		list.Projects = append(list.Projects, body)
	}
	reply(w, http.StatusOK, list)
}

// needsOf reads one project's Needs you, each item labeled with the project, as its own read.
func (rt *router) needsOf(ctx context.Context, p ledger.Project) (api.NeedsYou, error) {
	_, s, err := rt.project(ctx, p.Name)
	if err != nil {
		return api.NeedsYou{}, err
	}
	label := &api.ProjectLabel{ID: p.ID, Name: p.Name, DisplayName: p.DisplayName}
	var out api.NeedsYou
	err = s.handler.Store.Read(ctx, p.ID, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		now, err := tx.Now()
		if err != nil {
			return err
		}
		out.Stamp = api.Stamp{AsOf: now, Revision: workspace.Rev}
		if out.Items, err = s.handler.needs(tx, out.Stamp); err != nil {
			return err
		}
		for i := range out.Items {
			out.Items[i].Project = label
		}
		return nil
	})
	return out, err
}

// combinedNeeds is the combined Needs you: what waits on a member in every active project, read from each
// project's ledger now, the longest wait first, each item labeled with its project (FR-PROJ-8). An archived
// project is read-only, and nothing in it can be acted on, so it lists nothing.
func (rt *router) combinedNeeds(w http.ResponseWriter, r *http.Request) {
	projects, err := rt.config.Projects.List(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := api.NeedsYou{Items: []api.NeedItem{}, Projects: []api.NeedsProject{}}
	for _, p := range projects {
		if p.State != ledger.ProjectActive {
			continue
		}
		needs, err := rt.needsOf(r.Context(), p)
		if err != nil {
			fail(w, err)
			return
		}
		out.AsOf = max(out.AsOf, needs.AsOf)
		out.Items = append(out.Items, needs.Items...)
		out.Projects = append(out.Projects, api.NeedsProject{
			ProjectLabel: api.ProjectLabel{ID: p.ID, Name: p.Name, DisplayName: p.DisplayName}, Revision: needs.Revision, Waiting: len(needs.Items),
		})
	}
	slices.SortStableFunc(out.Items, func(a, b api.NeedItem) int { return cmp.Compare(a.Since, b.Since) })
	reply(w, http.StatusOK, out)
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
			if !record.Removed() {
				identities = append(identities, ledger.RepositoryIdentity(record.Remote, record.ID))
			}
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

// oneProject is the registry of a control plane that serves one ledger, named default, with no
// installation database of its own.
type oneProject struct {
	project ledger.Project
	store   ports.Store
	backup  ports.LedgerBackup
}

func (o oneProject) Backup(ctx context.Context, _ ledger.Project, path string) (int64, error) {
	if o.backup == nil {
		return 0, ports.ErrNoBackup
	}
	return o.backup.Backup(ctx, path)
}

func (o oneProject) BackupInstallation(context.Context, string) (int64, error) {
	return 0, ports.ErrNoBackup
}

// errOneLedger refuses a lifecycle step on a control plane that serves one ledger.
var errOneLedger = fmt.Errorf("%w: this control plane serves one ledger, with no registry of projects", ledger.ErrProjectState)

func (oneProject) Create(context.Context, ledger.Project, ledger.Actor) (ledger.Project, error) {
	return ledger.Project{}, errOneLedger
}

func (oneProject) Rename(context.Context, ledger.WorkspaceID, string, ledger.Actor) (ledger.Project, error) {
	return ledger.Project{}, errOneLedger
}

func (oneProject) SetState(context.Context, ledger.WorkspaceID, ledger.ProjectState, ledger.Actor) (ledger.Project, error) {
	return ledger.Project{}, errOneLedger
}

func (oneProject) Remove(context.Context, ledger.Project, ledger.Actor) error { return errOneLedger }

func (oneProject) Import(context.Context, string, string, ledger.Actor) (ledger.Project, error) {
	return ledger.Project{}, errOneLedger
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
			if !record.Removed() {
				out = append(out, ledger.IndexedRepository{Identity: ledger.RepositoryIdentity(record.Remote, record.ID), Project: o.project.ID, Repository: record.ID})
			}
		}
		return err
	})
	return out, err
}
