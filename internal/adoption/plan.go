package adoption

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// Register says what a registration of the clones would do, as a dry run, and records nothing. The control plane
// answers from the project's records, a worker from what it knows of them, and a project that does not exist
// yet has no record, so each clone reads as one that would register.
type Register func(ctx context.Context, clones []Found) ([]planning.PlannedRepository, error)

// Plan reads the layout around the clone adopt runs in, and the shape and changes it proposes. It reads the
// clone, every directory the clone ignores, to the depth, and every path named, as `repository add` reads them,
// and never the folder around the clone. It records nothing: the registrations are a dry run, and register says
// what each would do.
func Plan(ctx context.Context, in Request, kept ports.Checkouts, layouts ports.Layouts, register Register) (planning.AdoptionPlan, error) {
	if err := in.validate(); err != nil {
		return planning.AdoptionPlan{}, err
	}
	found, refused, folders, err := detect(ctx, in, kept, layouts)
	if err != nil {
		return planning.AdoptionPlan{}, err
	}
	root := found[0]
	plan := planning.AdoptionPlan{
		Layout:   planning.DetectedLayout{Root: root.Path, Folders: folders},
		Planning: planning.PlanningRepository{Name: root.Name, Path: root.Path, Why: "the clone adopt runs in"},
	}
	for _, one := range found {
		plan.Layout.Clones = append(plan.Layout.Clones, one.LayoutClone)
	}
	design, tied, err := chooseDesign(found, in.Design)
	if err != nil {
		return plan, err
	}
	planned, err := choosePlanning(found, in.Planning)
	if err != nil {
		return plan, err
	}
	if planned != nil {
		plan.Planning = planning.PlanningRepository{Name: planned.Name, Path: planned.Path, Why: "named with --planning"}
	} else if in.Planning != "" {
		plan.Planning = planning.PlanningRepository{Name: in.Planning, Why: "named with --planning, a repository the project already holds"}
	}
	plan.Shape = shapeOf(found, design)
	if in.Planning == "" && plan.Shape == planning.ShapeWorkspaceFiles && len(folders) == 0 {
		// The clone adopt runs in has no plan content and no folder of clones, so it is no workspace: the plan
		// lives in the design repository when one is chosen, and nothing settles it otherwise.
		switch {
		case design != nil && design.Path != root.Path:
			planned = design.Found
			plan.Planning = planning.PlanningRepository{Name: planned.Name, Path: planned.Path, Why: "the design repository, because no folder the clone adopt runs in ignores holds clones"}
		case design == nil:
			plan.Planning = planning.PlanningRepository{Why: "nothing settles it: the clone adopt runs in has no plan content or folder of clones, and no design repository was chosen", Undecided: true}
			plan.Notes = append(plan.Notes, "No repository is known to hold the plan; name it with --planning NAME.")
		}
	}
	if len(found) > 1 && design != nil {
		plan.Design = &planning.DesignRepository{Name: design.Name, Path: design.Path, Root: design.Identity, Why: design.why}
	}
	if tied != nil && plan.Shape != planning.ShapeSingle {
		plan.Notes = append(plan.Notes, fmt.Sprintf("More than one clone could be the design repository (%s); pass --design NAME to name it.", strings.Join(tied, ", ")))
	}
	if len(in.LeaveOut) != 0 && plan.Shape != planning.ShapeConsolidate {
		return plan, fmt.Errorf("%w: --leave-out removes a code tree in a consolidation, and this layout is the %s shape", ledger.ErrInvalidRepository, plan.Shape)
	}
	registers := slices.DeleteFunc(slices.Clone(found), func(one Found) bool {
		return plan.Shape == planning.ShapeConsolidate && one.Path == design.Path
	})
	if plan.Repositories, err = register(ctx, registers); err != nil {
		return plan, err
	}
	for _, unread := range refused {
		plan.Repositories = append(plan.Repositories, planning.PlannedRepository{Path: unread.Path, Outcome: string(ledger.RegistrationRefused), Reason: unread.Reason})
	}
	if plan.Shape == planning.ShapeConsolidate {
		plan.Repositories = append(plan.Repositories, planning.PlannedRepository{
			Name: design.Name, Path: design.Path, ID: design.Identity, Outcome: planning.OutcomeNotRegistered,
			Reason: "its history is imported into " + plan.Planning.Name + " by --restructure, so it is not registered",
		})
	}
	slices.SortFunc(plan.Repositories, func(a, b planning.PlannedRepository) int { return cmp.Compare(a.Path, b.Path) })
	if in.NewProject {
		plan.Notes = append(plan.Notes, "The project does not exist yet, so no record was read: each repository reads as one that would register, and a repository another project holds is refused when you adopt.")
	}
	if err := restructuring(ctx, &plan, in, found, registers, design, planned, layouts); err != nil {
		return plan, err
	}
	return plan.WithDigest(), nil
}
