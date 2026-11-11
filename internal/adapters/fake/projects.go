package fake

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Projects is an installation's registry held in memory, over Stores a test hands it. A backup is a
// mark of which project a path copied, and an import registers that project again with its ID and its
// Store, as a move to another installation would. Every change records an event.
type Projects struct {
	mu       sync.Mutex
	projects []ledger.Project
	stores   map[ledger.WorkspaceID]ports.Store
	index    []ledger.IndexedRepository
	backups  map[string]backedUp
	events   []string
	next     int
	// NewStore opens the ledger of a project Create makes. Nil gives such a project no Store.
	NewStore func(ledger.Project) (ports.Store, error)
}

type backedUp struct {
	project ledger.Project
	store   ports.Store
	index   []ledger.IndexedRepository
}

// NewProjects returns an empty registry.
func NewProjects() *Projects {
	return &Projects{stores: map[ledger.WorkspaceID]ports.Store{}, backups: map[string]backedUp{}}
}

// Add registers a project on a Store, as an installation's upgrade registers its first project.
func (f *Projects) Add(p ledger.Project, store ports.Store) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.State == "" {
		p.State = ledger.ProjectActive
	}
	f.projects = append(f.projects, p)
	f.stores[p.ID] = store
}

// Hold enters a repository in the index for a project.
func (f *Projects) Hold(entry ledger.IndexedRepository) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.index = append(f.index, entry)
}

// Carry hands a backup to another registry, as copying its file to another installation's machine does.
func (f *Projects) Carry(path string, to *Projects) {
	f.mu.Lock()
	copied, ok := f.backups[path]
	f.mu.Unlock()
	if !ok {
		return
	}
	to.mu.Lock()
	defer to.mu.Unlock()
	to.backups[path] = copied
}

// Events lists each change as "type id", in order.
func (f *Projects) Events() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

func (f *Projects) List(context.Context) ([]ledger.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.projects), nil
}

func (f *Projects) Named(_ context.Context, name string) (ledger.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.projects {
		if p.Name == name {
			return p, nil
		}
	}
	return ledger.Project{}, fmt.Errorf("project %s: %w", name, ports.ErrNotFound)
}

func (f *Projects) Open(_ context.Context, p ledger.Project) (ports.Store, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	store, ok := f.stores[p.ID]
	if !ok || store == nil {
		return nil, fmt.Errorf("project %s has no store: %w", p.ID, ports.ErrNotFound)
	}
	return store, nil
}

func (f *Projects) Index(context.Context) ([]ledger.IndexedRepository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.index), nil
}

func (f *Projects) Backup(_ context.Context, p ledger.Project, path string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, taken := f.backups[path]; taken {
		return 0, fmt.Errorf("%w: %s exists", ports.ErrBackupTarget, path)
	}
	var held []ledger.IndexedRepository
	for _, entry := range f.index {
		if entry.Project == p.ID {
			held = append(held, entry)
		}
	}
	f.backups[path] = backedUp{project: p, store: f.stores[p.ID], index: held}
	return 1, nil
}

func (f *Projects) BackupInstallation(context.Context, string) (int64, error) {
	return 0, ports.ErrNoBackup
}

// find returns where a project with an ID stands in the registry. The caller holds mu.
func (f *Projects) find(id ledger.WorkspaceID) (int, error) {
	at := slices.IndexFunc(f.projects, func(p ledger.Project) bool { return p.ID == id })
	if at < 0 {
		return 0, fmt.Errorf("project %s: %w", id, ports.ErrNotFound)
	}
	return at, nil
}

// named refuses a name another project holds. The caller holds mu.
func (f *Projects) named(name string, id ledger.WorkspaceID) error {
	if err := ledger.ValidateProjectName(name); err != nil {
		return err
	}
	for _, p := range f.projects {
		if p.Name == name && p.ID != id {
			return fmt.Errorf("%w: project %s is named %q", ledger.ErrProjectExists, p.ID, name)
		}
	}
	return nil
}

func (f *Projects) Create(_ context.Context, p ledger.Project, _ ledger.Actor) (ledger.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.ID == "" {
		f.next++
		p.ID = ledger.WorkspaceID("project-" + strconv.Itoa(f.next))
	}
	if err := f.named(p.Name, p.ID); err != nil {
		return ledger.Project{}, err
	}
	p.State, p.Database = ledger.ProjectActive, "projects/"+string(p.ID)
	if f.NewStore != nil {
		store, err := f.NewStore(p)
		if err != nil {
			return ledger.Project{}, err
		}
		f.stores[p.ID] = store
	}
	f.projects = append(f.projects, p)
	f.events = append(f.events, ledger.EventProjectCreated+" "+string(p.ID))
	return p, nil
}

func (f *Projects) Rename(_ context.Context, id ledger.WorkspaceID, name string, _ ledger.Actor) (ledger.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, err := f.find(id)
	if err != nil {
		return ledger.Project{}, err
	}
	if err := f.named(name, id); err != nil {
		return ledger.Project{}, err
	}
	if f.projects[at].Name != name {
		f.projects[at].Name = name
		f.events = append(f.events, ledger.EventProjectRenamed+" "+string(id))
	}
	return f.projects[at], nil
}

func (f *Projects) SetState(_ context.Context, id ledger.WorkspaceID, state ledger.ProjectState, _ ledger.Actor) (ledger.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, err := f.find(id)
	if err != nil {
		return ledger.Project{}, err
	}
	if f.projects[at].State != state {
		f.projects[at].State = state
		event := ledger.EventProjectRestored
		if state == ledger.ProjectArchived {
			event = ledger.EventProjectArchived
		}
		f.events = append(f.events, event+" "+string(id))
	}
	return f.projects[at], nil
}

func (f *Projects) Remove(_ context.Context, p ledger.Project, _ ledger.Actor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	at, err := f.find(p.ID)
	if err != nil {
		return err
	}
	f.projects = slices.Delete(f.projects, at, at+1)
	delete(f.stores, p.ID)
	f.index = slices.DeleteFunc(f.index, func(entry ledger.IndexedRepository) bool { return entry.Project == p.ID })
	f.events = append(f.events, ledger.EventProjectRemoved+" "+string(p.ID))
	return nil
}

func (f *Projects) Import(_ context.Context, backup, name string, _ ledger.Actor) (ledger.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied, ok := f.backups[backup]
	if !ok {
		return ledger.Project{}, fmt.Errorf("%w: no backup at %s", ledger.ErrInvalidProject, backup)
	}
	p := copied.project
	if _, err := f.find(p.ID); err == nil {
		return ledger.Project{}, fmt.Errorf("%w: the installation holds project ID %s", ledger.ErrProjectExists, p.ID)
	}
	if name != "" {
		p.Name = name
	}
	if err := f.named(p.Name, p.ID); err != nil {
		return ledger.Project{}, err
	}
	for _, entry := range copied.index {
		for _, held := range f.index {
			if held.Identity == entry.Identity {
				return ledger.Project{}, fmt.Errorf("%w: project %s holds %s", ledger.ErrRepositoryHeld, held.Project, entry.Identity)
			}
		}
	}
	p.State = ledger.ProjectActive
	f.projects = append(f.projects, p)
	f.stores[p.ID] = copied.store
	f.index = append(f.index, copied.index...)
	f.events = append(f.events, ledger.EventProjectImported+" "+string(p.ID))
	return p, nil
}

var _ ports.Projects = (*Projects)(nil)
