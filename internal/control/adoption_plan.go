package control

import (
	"cmp"
	"context"
	"path"

	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// MaxAdoptionDepth is the most levels below a folder a plan searches for clones.
const MaxAdoptionDepth = adoption.MaxDepth

// Adoptions plans the adoption of a repository and the clones around it (FR-ADOPT-12).
type Adoptions struct {
	repositories *Repositories
}

// NewAdoptions returns the use cases over a project's repositories.
func NewAdoptions(repositories *Repositories) *Adoptions {
	return &Adoptions{repositories: repositories}
}

// AdoptionRequest is what a member asks to be planned.
type AdoptionRequest = adoption.Request

// Plan reads the layout around the clone adopt runs in, and the shape and changes it proposes, as the adoption
// package reads it. It records nothing: the registrations are a dry run over the project's records.
func (a *Adoptions) Plan(ctx context.Context, scope ledger.WorkspaceID, in AdoptionRequest, kept ports.Checkouts, layouts ports.Layouts, actor ledger.Actor) (planning.AdoptionPlan, error) {
	if !authority.Allowed(actor, authority.ActionPlanAdoption) || !authority.Allowed(actor, authority.ActionRegisterRepository) {
		return planning.AdoptionPlan{}, workorders.ErrForbidden
	}
	return adoption.Plan(ctx, in, kept, layouts, func(ctx context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
		return a.register(ctx, scope, in, clones, kept, actor)
	})
}

// register says what a registration of the clones would do, as a dry run. A project that does not exist yet has
// no record, so each clone reads as one that would register.
func (a *Adoptions) register(ctx context.Context, scope ledger.WorkspaceID, in AdoptionRequest, clones []adoption.Found, kept ports.Checkouts, actor ledger.Actor) ([]planning.PlannedRepository, error) {
	var out []planning.PlannedRepository
	if in.NewProject {
		for _, one := range clones {
			out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.Registered)})
		}
		return out, nil
	}
	paths := make([]string, 0, len(clones))
	for _, one := range clones {
		paths = append(paths, one.Path)
	}
	added, err := a.repositories.Add(ctx, scope, RepositoryAdd{Paths: paths, Depth: 1, DryRun: true}, kept, actor)
	if err != nil {
		return nil, err
	}
	for _, one := range added {
		repository := planning.PlannedRepository{Path: one.Path, Outcome: string(one.Outcome), Reason: one.Reason, Name: path.Base(one.Path)}
		if one.Repository.ID != "" {
			repository.ID = one.Repository.ID
			repository.Name = cmp.Or(one.Repository.Name, repository.Name)
		}
		out = append(out, repository)
	}
	return out, nil
}
