package control

import (
	"context"
	"fmt"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Projects holds the use cases of a project's lifecycle (ADR-0021): create, rename, archive, restore,
// remove, and import. Each is a member's step, and the registry records each as an installation event.
type Projects struct {
	registry ports.Projects
	// served is the project this control plane's worker and ticks serve, which stays until they serve
	// every project.
	served ledger.WorkspaceID
}

// NewProjects returns the use cases over a registry, with the project its worker serves.
func NewProjects(registry ports.Projects, served ledger.WorkspaceID) *Projects {
	return &Projects{registry: registry, served: served}
}

// Removal is what a member gives to remove a project: its name again, and either a file for its backup
// or the choice to skip one.
type Removal struct {
	Confirm  string
	Backup   string
	NoBackup bool
}

func (p *Projects) allowed(actor ledger.Actor) error {
	if !authority.Allowed(actor, authority.ActionManageProject) {
		return workorders.ErrForbidden
	}
	return nil
}

// Create creates a project at the current schema with a name no other project holds.
func (p *Projects) Create(ctx context.Context, name, displayName string, actor ledger.Actor) (ledger.Project, error) {
	if err := p.allowed(actor); err != nil {
		return ledger.Project{}, err
	}
	if err := ledger.ValidateProjectName(name); err != nil {
		return ledger.Project{}, err
	}
	return p.registry.Create(ctx, ledger.Project{Name: name, DisplayName: displayName}, actor)
}

// Rename changes a project's name. Its ID, its database, and every record keep their identity.
func (p *Projects) Rename(ctx context.Context, name, newName string, actor ledger.Actor) (ledger.Project, error) {
	if err := p.allowed(actor); err != nil {
		return ledger.Project{}, err
	}
	project, err := p.registry.Named(ctx, name)
	if err != nil {
		return ledger.Project{}, err
	}
	return p.registry.Rename(ctx, project.ID, newName, actor)
}

// Archive makes a project read-only, once every lane of it ended: a person drains a project first, with
// fleet drain, because an archived project takes no claims and runs no ticks.
func (p *Projects) Archive(ctx context.Context, name string, actor ledger.Actor) (ledger.Project, error) {
	if err := p.allowed(actor); err != nil {
		return ledger.Project{}, err
	}
	project, err := p.registry.Named(ctx, name)
	if err != nil {
		return ledger.Project{}, err
	}
	if project.State == ledger.ProjectArchived {
		return project, nil
	}
	if err := p.lanesEnded(ctx, project); err != nil {
		return ledger.Project{}, err
	}
	return p.registry.SetState(ctx, project.ID, ledger.ProjectArchived, actor)
}

// lanesEnded refuses a project with a lane that has not ended, and names it.
func (p *Projects) lanesEnded(ctx context.Context, project ledger.Project) error {
	store, err := p.registry.Open(ctx, project)
	if err != nil {
		return err
	}
	return store.Read(ctx, project.ID, func(tx ports.ReadTx) error {
		rows, err := tx.Lanes().Rows()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.Status.ActualState != ledger.StateEnded {
				return fmt.Errorf("%w: lane %s of project %s has not ended: drain the project with fleet drain, and archive it once its lanes end",
					ledger.ErrProjectState, row.ID, project.Name)
			}
		}
		return nil
	})
}

// Restore returns an archived project to active.
func (p *Projects) Restore(ctx context.Context, name string, actor ledger.Actor) (ledger.Project, error) {
	if err := p.allowed(actor); err != nil {
		return ledger.Project{}, err
	}
	project, err := p.registry.Named(ctx, name)
	if err != nil {
		return ledger.Project{}, err
	}
	if project.State != ledger.ProjectArchived {
		return ledger.Project{}, fmt.Errorf("%w: project %s is %s, and only an archived project is restored", ledger.ErrProjectState, name, project.State)
	}
	return p.registry.SetState(ctx, project.ID, ledger.ProjectActive, actor)
}

// Remove deletes an archived project's database and drops it from the registry and the repository
// index, after writing its backup when the member named a file. The project this control plane's worker
// serves stays.
func (p *Projects) Remove(ctx context.Context, name string, removal Removal, actor ledger.Actor) error {
	if err := p.allowed(actor); err != nil {
		return err
	}
	if removal.Confirm != name {
		return fmt.Errorf("%w: confirm the removal with the project's name, %s", ledger.ErrInvalidProject, name)
	}
	if (removal.Backup == "") == !removal.NoBackup {
		return fmt.Errorf("%w: name a file for the project's backup, or choose no backup", ledger.ErrInvalidProject)
	}
	project, err := p.registry.Named(ctx, name)
	if err != nil {
		return err
	}
	if project.State != ledger.ProjectArchived {
		return fmt.Errorf("%w: project %s is %s: archive it before removing it", ledger.ErrProjectState, name, project.State)
	}
	if project.ID == p.served {
		return fmt.Errorf("%w: this daemon's worker serves project %s until it serves every project, so it stays", ledger.ErrProjectState, name)
	}
	if removal.Backup != "" {
		if _, err := p.registry.Backup(ctx, project, removal.Backup); err != nil {
			return err
		}
	}
	return p.registry.Remove(ctx, project, actor)
}

// Import registers a project from its backup, keeping its ID.
func (p *Projects) Import(ctx context.Context, backup, name string, actor ledger.Actor) (ledger.Project, error) {
	if err := p.allowed(actor); err != nil {
		return ledger.Project{}, err
	}
	if name != "" {
		if err := ledger.ValidateProjectName(name); err != nil {
			return ledger.Project{}, err
		}
	}
	return p.registry.Import(ctx, backup, name, actor)
}
