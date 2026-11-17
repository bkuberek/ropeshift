package control

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// Where a restructuring puts what it adds to the planning repository, and the folder of clones the moves it
// lists lead into.
const (
	manifestPath    = "plan/repositories.md"
	codeFolder      = "repos"
	adoptionProgram = "ropeshift"
)

// MaxAdoptionDepth is the most levels below a folder a plan searches for clones. A folder the clone ignores may
// hold a dependency tree or a build output, so a request cannot ask for a walk as deep as it likes.
const MaxAdoptionDepth = 4

// Adoptions plans the adoption of a repository and the clones around it (FR-ADOPT-12).
type Adoptions struct {
	repositories *Repositories
}

// NewAdoptions returns the use cases over a project's repositories.
func NewAdoptions(repositories *Repositories) *Adoptions {
	return &Adoptions{repositories: repositories}
}

// AdoptionRequest is what a member asks to be planned.
type AdoptionRequest struct {
	// Root is the top of the clone adopt runs in, as an absolute path on the worker's machine.
	Root string
	// Paths are the paths named beside it, absolute, each a clone or a folder that holds clones.
	Paths []string
	// Depth is how many levels below a folder are searched for clones, at most MaxAdoptionDepth. Below 1
	// searches one.
	Depth int
	// Planning names the repository that holds the plan, by its clone's folder name.
	Planning string
	// Design names the design repository, by its clone's folder name.
	Design string
	// LeaveOut names the code trees of the design repository that a consolidation removes, relative to its top.
	LeaveOut []string
	// NewProject says the project does not exist yet, so no record is read.
	NewProject bool
}

// foundClone is a clone the detection found, with what it holds.
type foundClone struct {
	planning.LayoutClone
	clone   ports.Clone
	content ports.PlanContent
}

// Plan reads the layout around the clone adopt runs in, and the shape and changes it proposes. It reads the
// clone, every directory the clone ignores, to the depth, and every path named, as `repository add` reads them,
// and never the folder around the clone. It records nothing: the registrations are a dry run.
func (a *Adoptions) Plan(ctx context.Context, scope ledger.WorkspaceID, in AdoptionRequest, kept ports.Checkouts, layouts ports.Layouts, actor ledger.Actor) (planning.AdoptionPlan, error) {
	if !authority.Allowed(actor, authority.ActionPlanAdoption) || !authority.Allowed(actor, authority.ActionRegisterRepository) {
		return planning.AdoptionPlan{}, workorders.ErrForbidden
	}
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
			planned = design.foundClone
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
	registers := slices.DeleteFunc(slices.Clone(found), func(one foundClone) bool {
		return plan.Shape == planning.ShapeConsolidate && one.Path == design.Path
	})
	if plan.Repositories, err = a.register(ctx, scope, in, registers, kept, actor); err != nil {
		return plan, err
	}
	for _, path := range refused {
		plan.Repositories = append(plan.Repositories, planning.PlannedRepository{Path: path.Path, Outcome: string(ledger.RegistrationRefused), Reason: path.Reason})
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

// validate refuses a request that names no clone to run in, or a path that is not absolute.
func (in AdoptionRequest) validate() error {
	if in.Depth > MaxAdoptionDepth {
		return fmt.Errorf("%w: --depth is at most %d, and %d was asked", ledger.ErrInvalidRepository, MaxAdoptionDepth, in.Depth)
	}
	if !filepath.IsAbs(in.Root) {
		return fmt.Errorf("%w: the clone adopt runs in is not an absolute path: %q", ledger.ErrInvalidRepository, in.Root)
	}
	for _, path := range in.Paths {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%w: path %q is not absolute", ledger.ErrInvalidRepository, path)
		}
	}
	return nil
}

// detect finds the clone adopt runs in, the clones in each directory it ignores, and the clones the paths name,
// each once, in order of path with the clone adopt runs in first. It also says which folders the root ignores
// hold clones, and which paths named hold none.
func detect(ctx context.Context, in AdoptionRequest, kept ports.Checkouts, layouts ports.Layouts) ([]foundClone, []ports.UnreadPath, []string, error) {
	depth := max(in.Depth, 1)
	roots, _, err := kept.Find(ctx, []string{in.Root}, 1)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(roots) != 1 || !samePath(ctx, kept, roots[0].Path, in.Root) {
		return nil, nil, nil, fmt.Errorf("%w: run adopt in the top of a git clone with a commit; %s is none", ledger.ErrInvalidRepository, in.Root)
	}
	top := roots[0]
	ignored, err := layouts.IgnoredDirectories(ctx, top.Path)
	if err != nil {
		return nil, nil, nil, err
	}
	var searched []string
	for _, dir := range ignored {
		searched = append(searched, filepath.Join(top.Path, filepath.FromSlash(dir)))
	}
	var clones []ports.Clone
	if len(searched) != 0 {
		inside, _, err := kept.Find(ctx, searched, depth)
		if err != nil {
			return nil, nil, nil, err
		}
		clones = append(clones, inside...)
	}
	var refused []ports.UnreadPath
	if len(in.Paths) != 0 {
		named, unread, err := kept.Find(ctx, in.Paths, depth)
		if err != nil {
			return nil, nil, nil, err
		}
		clones = append(clones, named...)
		refused = unread
	}
	slices.SortFunc(refused, func(a, b ports.UnreadPath) int { return cmp.Compare(a.Path, b.Path) })
	slices.SortFunc(clones, func(a, b ports.Clone) int { return cmp.Compare(a.Path, b.Path) })

	found := []foundClone{{clone: top}}
	seen := map[string]bool{top.Path: true}
	var folders []string
	for _, clone := range clones {
		if seen[clone.Path] {
			continue
		}
		seen[clone.Path] = true
		one := foundClone{clone: clone}
		for _, folder := range searched {
			if strings.HasPrefix(clone.Path, folder+string(filepath.Separator)) {
				one.Where = planning.WhereIgnored
				if !slices.Contains(folders, folder) {
					folders = append(folders, folder)
				}
			}
		}
		if one.Where == "" {
			one.Where = planning.WherePath
		}
		found = append(found, one)
	}
	found[0].Where = planning.WhereHere
	slices.Sort(folders)
	for i := range found {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if err := found[i].read(ctx, layouts); err != nil {
			return nil, nil, nil, err
		}
	}
	return found, refused, folders, nil
}

// read fills in what the clone holds, at its default branch's tip.
func (f *foundClone) read(ctx context.Context, layouts ports.Layouts) error {
	var err error
	f.Name, f.Path, f.Identity = cmp.Or(f.clone.Directory, filepath.Base(f.clone.Path)), f.clone.Path, f.clone.ID
	if f.content, err = layouts.Plan(ctx, f.Path); err != nil {
		return err
	}
	f.Plan = f.content.Held()
	if f.Documents, err = layouts.Documents(ctx, f.Path); err != nil {
		return err
	}
	f.Instructions, err = layouts.Instructions(ctx, f.Path)
	return err
}

// designClone is a clone chosen as the design repository, and why.
type designClone struct {
	*foundClone
	why string
}

// chooseDesign names the design repository: the clone `--design` names, else the one that holds plan content, else
// the one with the most committed Markdown documents. A tie names none, and the clones that tied come back.
func chooseDesign(found []foundClone, named string) (*designClone, []string, error) {
	if named != "" {
		one, err := byName(found, named, "--design")
		if err != nil {
			return nil, nil, err
		}
		return &designClone{foundClone: one, why: "named with --design"}, nil, nil
	}
	candidates := slices.Clone(found)
	reason := "it holds the most Markdown documents"
	if planned := slices.DeleteFunc(slices.Clone(found), func(one foundClone) bool { return !one.Plan }); len(planned) != 0 {
		candidates, reason = planned, "it holds plan content"
	}
	most := 0
	for _, one := range candidates {
		most = max(most, one.Documents)
	}
	if most == 0 {
		return nil, nil, nil
	}
	var winners []*foundClone
	for i := range candidates {
		if candidates[i].Documents == most {
			winners = append(winners, &candidates[i])
		}
	}
	if len(winners) > 1 {
		var names []string
		for _, one := range winners {
			names = append(names, one.Name)
		}
		return nil, names, nil
	}
	if reason == "it holds plan content" && len(candidates) > 1 {
		reason += " and the most Markdown documents"
	}
	return &designClone{foundClone: winners[0], why: fmt.Sprintf("%s (%d)", reason, most)}, nil, nil
}

// choosePlanning is the clone `--planning` names, or nil when it names none the detection found.
func choosePlanning(found []foundClone, named string) (*foundClone, error) {
	if named == "" {
		return nil, nil
	}
	matches := slices.DeleteFunc(slices.Clone(found), func(one foundClone) bool { return one.Name != named })
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: --planning %s names %d clones that were found", ledger.ErrInvalidRepository, named, len(matches))
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return &matches[0], nil
}

// byName is the one clone with a folder name.
func byName(found []foundClone, name, flag string) (*foundClone, error) {
	var names []string
	var matches []int
	for i, one := range found {
		names = append(names, one.Name)
		if one.Name == name {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 1:
		return &found[matches[0]], nil
	case 0:
		return nil, fmt.Errorf("%w: %s %s names no clone that was found; they are %s", ledger.ErrInvalidRepository, flag, name, strings.Join(names, ", "))
	}
	return nil, fmt.Errorf("%w: %s %s names %d of the clones that were found", ledger.ErrInvalidRepository, flag, name, len(matches))
}

// shapeOf says which of the four shapes the layout is.
func shapeOf(found []foundClone, design *designClone) planning.LayoutShape {
	switch {
	case len(found) == 1:
		return planning.ShapeSingle
	case found[0].Plan:
		return planning.ShapePlanned
	case design != nil && design.Where == planning.WhereIgnored:
		return planning.ShapeConsolidate
	}
	return planning.ShapeWorkspaceFiles
}

// register says what a registration of the clones would do, as a dry run. A project that does not exist yet has
// no record, so each clone reads as one that would register.
func (a *Adoptions) register(ctx context.Context, scope ledger.WorkspaceID, in AdoptionRequest, clones []foundClone, kept ports.Checkouts, actor ledger.Actor) ([]planning.PlannedRepository, error) {
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

// restructuring fills in what `--restructure` would do in the shape: the files it adds to the planning
// repository, the paths a consolidation takes and where the two repositories collide, the design repository's code
// trees, and the moves it leaves to the person.
func restructuring(ctx context.Context, plan *planning.AdoptionPlan, in AdoptionRequest, found []foundClone, registers []foundClone, design *designClone, planned *foundClone, layouts ports.Layouts) error {
	if !plan.Shape.Restructures() {
		return nil
	}
	home := found[0]
	if planned != nil {
		home = *planned
	}
	held, err := newHome(ctx, home, layouts)
	if err != nil {
		return err
	}
	manifest := planning.PlannedFile{Path: manifestPath, Action: planning.ActionAdd, Detail: "the manifest of the project's repositories"}
	makefile := planning.PlannedFile{Path: "Makefile", Action: planning.ActionAdd, Detail: "setup, fetch, and status over the manifest"}
	switch plan.Shape {
	case planning.ShapePlanned:
		if held.lacksManifest() {
			plan.Files = append(plan.Files, manifest)
		}
	case planning.ShapeWorkspaceFiles:
		if held.lacksManifest() {
			plan.Files = append(plan.Files, manifest)
		}
		plan.Moves = movesOf(home, registers)
		if len(plan.Moves) != 0 && !held.ignores(codeFolder) {
			plan.Files = append(plan.Files, planning.PlannedFile{Path: ".gitignore", Action: planning.ActionAppend, Detail: codeFolder + "/"})
		}
		if !held.tracks("Makefile") {
			plan.Files = append(plan.Files, makefile)
		}
	case planning.ShapeConsolidate:
		if held.lacksManifest() {
			plan.Files = append(plan.Files, manifest)
		}
		if !held.tracks("Makefile") {
			plan.Files = append(plan.Files, makefile)
		}
		return consolidation(ctx, plan, in, design, held.tracked, layouts)
	}
	return nil
}

// planningHome is what the repository the plan lives in holds.
type planningHome struct {
	content ports.PlanContent
	tracked []string
	ignored []string
}

func newHome(ctx context.Context, home foundClone, layouts ports.Layouts) (planningHome, error) {
	var held planningHome
	tip, err := layouts.Tip(ctx, home.Path)
	if err != nil {
		return held, err
	}
	if held.tracked, err = layouts.Tracked(ctx, home.Path, tip); err != nil {
		return held, err
	}
	if held.ignored, err = layouts.IgnoredDirectories(ctx, home.Path); err != nil {
		return held, err
	}
	held.content = home.content
	return held, nil
}

func (h planningHome) lacksManifest() bool     { return !h.content.Manifest && !h.tracks(manifestPath) }
func (h planningHome) tracks(file string) bool { return slices.Contains(h.tracked, file) }
func (h planningHome) ignores(dir string) bool { return slices.Contains(h.ignored, dir) }

// movesOf lists, for each repository registered beside the planning repository's clone, the commands that move it
// into the clone's folder of clones and register it there.
func movesOf(home foundClone, registers []foundClone) []planning.Move {
	var moves []planning.Move
	for _, one := range registers {
		if one.Path == home.Path || strings.HasPrefix(one.Path, home.Path+string(filepath.Separator)) {
			continue
		}
		to := filepath.Join(home.Path, codeFolder, one.Name)
		moves = append(moves, planning.Move{
			Name: one.Name, From: one.Path, To: to,
			Commands: []string{fmt.Sprintf("mv %s %s", shellQuote(one.Path), shellQuote(to)), fmt.Sprintf("%s repository add %s", adoptionProgram, shellQuote(to))},
		})
	}
	slices.SortFunc(moves, func(a, b planning.Move) int { return cmp.Compare(a.Name, b.Name) })
	return moves
}

// shellQuote spells an argument so a POSIX shell reads it as one word: as it is when it holds only characters a
// shell leaves alone, and else in single quotes, a quote inside it closing and reopening them.
func shellQuote(argument string) string {
	if argument != "" && strings.Trim(argument, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-") == "" {
		return argument
	}
	return "'" + strings.ReplaceAll(argument, "'", `'\''`) + "'"
}

// consolidation lists what importing the design repository into the planning repository does: the code trees it
// could leave out, the paths it takes, and the paths both repositories hold with the version kept.
func consolidation(ctx context.Context, plan *planning.AdoptionPlan, in AdoptionRequest, design *designClone, homeTracked []string, layouts ports.Layouts) error {
	trees, err := layouts.CodeTrees(ctx, design.Path)
	if err != nil {
		return err
	}
	var left []string
	for _, name := range in.LeaveOut {
		tree := path.Clean(strings.TrimSuffix(filepath.ToSlash(name), "/"))
		if !slices.ContainsFunc(trees, func(held ports.CodeTree) bool { return held.Path == tree }) || tree == "." {
			return fmt.Errorf("%w: --leave-out %s names no code tree of %s that can be left out; it holds %s",
				ledger.ErrInvalidRepository, name, design.Name, treeNames(trees))
		}
		left = append(left, tree)
	}
	for _, tree := range trees {
		plan.CodeTrees = append(plan.CodeTrees, planning.PlannedCodeTree{
			Path: tree.Path, BuildFile: tree.BuildFile, Whole: tree.Path == ".", LeftOut: slices.Contains(left, tree.Path),
		})
	}
	tip, err := layouts.Tip(ctx, design.Path)
	if err != nil {
		return err
	}
	held, err := layouts.Tracked(ctx, design.Path, tip)
	if err != nil {
		return err
	}
	for _, tracked := range held {
		if slices.ContainsFunc(left, func(tree string) bool { return strings.HasPrefix(tracked, tree+"/") }) {
			continue
		}
		switch {
		case tracked == ".gitignore" && slices.Contains(homeTracked, tracked):
			plan.Collisions = append(plan.Collisions, planning.Collision{Path: tracked, Kept: planning.KeptUnion})
		case slices.Contains(homeTracked, tracked):
			plan.Collisions = append(plan.Collisions, planning.Collision{Path: tracked, Kept: planning.KeptWorkspace})
		default:
			plan.Takes = append(plan.Takes, tracked)
		}
	}
	return nil
}

// treeNames lists the code trees of a repository for a message.
func treeNames(trees []ports.CodeTree) string {
	if len(trees) == 0 {
		return "none"
	}
	var names []string
	for _, tree := range trees {
		names = append(names, tree.Path)
	}
	return strings.Join(names, ", ")
}
