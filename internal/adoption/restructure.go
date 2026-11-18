package adoption

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// The templates of the files a layout lacks. Each is written to the path projectfiles gives it, which is where
// `ropeshift new` writes it.
const (
	templateWorkspaceAgents  = "workspace/agents.md"
	templateMonorepoAgents   = "monorepo/agents.md"
	templateRepositoryAgents = "repository/agents.md"
	templateClaude           = "claude.md"
	templateMakefile         = "workspace/makefile"
	templateGitignore        = "workspace/gitignore"
)

// restructuring fills in what `--restructure` would do in the shape: the files it adds to the planning
// repository, against the tree the repository holds once any consolidation is imported, the paths a consolidation
// takes and where the two repositories collide, the design repository's code trees, and the moves it leaves to
// the person. A single repository is offered only the instruction files it lacks.
func restructuring(ctx context.Context, plan *planning.AdoptionPlan, in Request, found []Found, registers []Found, design *designClone, planned *Found, kept ports.Checkouts, layouts ports.Layouts) error {
	if !plan.Shape.Valid() {
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
	if plan.Shape == planning.ShapeSingle {
		plan.Files = append(plan.Files, instructionFiles(plan.Shape, held.tracked)...)
		return nil
	}
	var lines []string
	switch plan.Shape {
	case planning.ShapeWorkspaceFiles:
		plan.Moves = movesOf(home, registers)
		if len(plan.Moves) != 0 && !held.ignores(CodeFolder) {
			lines = append(lines, planning.CodeFolderLine)
		}
	case planning.ShapeConsolidate:
		// The files are listed against the tree the merge makes, so a manifest, a Makefile, an ignore file, or an
		// instruction file the design repository brings is not added beside it.
		if err := consolidation(ctx, plan, in, design, held.tracked, layouts); err != nil {
			return err
		}
		held.tracked = append(slices.Clone(held.tracked), plan.Takes...)
	}
	if held.lacksManifest() {
		plan.Files = append(plan.Files, planning.PlannedFile{Path: ManifestPath, Action: planning.ActionAdd, Detail: "the manifest of the project's repositories"})
	}
	if err := ignoring(ctx, plan, home, lines, held.tracked, kept); err != nil {
		return err
	}
	if !held.holdsMakefile() {
		plan.Files = append(plan.Files, planning.PlannedFile{
			Path: projectfiles.Path(templateMakefile), Action: planning.ActionAdd, Detail: "setup, fetch, and status over the manifest", Template: templateMakefile,
		})
	}
	plan.Files = append(plan.Files, instructionFiles(plan.Shape, held.tracked)...)
	return nil
}

// instructionFiles lists the agent instructions a repository that holds none lacks: AGENTS.md, from the template of
// the shape, and a CLAUDE.md that imports it. A repository that holds either file is left as it is, so no second
// file is written beside instructions it already has.
func instructionFiles(shape planning.LayoutShape, tracked []string) []planning.PlannedFile {
	if ports.HoldsInstructions(tracked) {
		return nil
	}
	agents := templateWorkspaceAgents
	if shape == planning.ShapeSingle {
		agents = templateMonorepoAgents
	}
	return []planning.PlannedFile{
		{Path: projectfiles.Path(agents), Action: planning.ActionAdd, Detail: "the project's instructions for agents", Template: agents},
		{Path: projectfiles.Path(templateClaude), Action: planning.ActionAdd, Detail: "imports AGENTS.md", Template: templateClaude},
	}
}

// instructionSteps lists, as steps for the person, each code repository whose clone holds no instruction file, with the
// command that writes the repository template to it, unless a file is there already, so the command never truncates
// an uncommitted one. A clone the plan moves is named where the move leads, because the person runs the moves
// first. Adoption writes to no clone but the planning repository's (FR-ADOPT-3), so the commands are the person's. The planning repository, the design repository a consolidation
// imports, and a clone the registration refuses are no code repository of the project.
func instructionSteps(plan planning.AdoptionPlan, found []Found, design *designClone) []planning.Step {
	if plan.Planning.Undecided {
		return nil
	}
	refused := map[string]bool{}
	for _, repository := range plan.Repositories {
		refused[repository.Path] = repository.Outcome == string(ledger.RegistrationRefused)
	}
	leadsTo := map[string]string{}
	for _, move := range plan.Moves {
		leadsTo[move.From] = move.To
	}
	var steps []planning.Step
	for _, one := range found {
		switch {
		case one.Instructions, one.Path == plan.Planning.Path, refused[one.Path]:
			continue
		case plan.Shape == planning.ShapeConsolidate && design != nil && one.Path == design.Path:
			continue
		}
		at := one.Path
		if to, moved := leadsTo[at]; moved {
			at = to
		}
		file := shellQuote(path.Join(at, projectfiles.Path(templateRepositoryAgents)))
		steps = append(steps, planning.Step{
			Name: one.Name, Path: at,
			Commands: []string{fmt.Sprintf("[ -e %s ] || %s template show %s > %s", file, Program, templateRepositoryAgents, file)},
		})
	}
	slices.SortFunc(steps, func(a, b planning.Step) int { return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.Path, b.Path)) })
	return steps
}

// ignoring lists the ignore file's change: the whole file from the template when the tree lacks one, and else the
// lines a restructuring needs already, and the line that leaves a lane's worktree folder out of the planning
// repository, unless its clone ignores that folder. The clone says so through the one query of what a repository
// ignores, and never by a look at its ignore file, because a rule can sit in a global file or an exclude file. A
// path inside the folder is asked about, because a folder the repository ignores ignores everything in it.
func ignoring(ctx context.Context, plan *planning.AdoptionPlan, home Found, lines []string, tree []string, kept ports.Checkouts) error {
	ignoreFile, held := heldAs(tree, planning.GitignorePath)
	if !held {
		plan.Files = append(plan.Files, planning.PlannedFile{
			Path: projectfiles.Path(templateGitignore), Action: planning.ActionAdd, Detail: "ignores the folder of clones and the folder lane worktrees go in", Template: templateGitignore,
		})
		return nil
	}
	ignored, err := kept.Ignored(ctx, home.Path, path.Join(planning.WorktreesFolder, "lane"))
	if err != nil {
		return err
	}
	if !ignored {
		lines = append(lines, planning.WorktreesLine)
	}
	if len(lines) != 0 {
		plan.Files = append(plan.Files, planning.PlannedFile{
			Path: ignoreFile, Action: planning.ActionAppend, Detail: strings.Join(lines, ", "), Lines: lines,
		})
	}
	return nil
}

// planningHome is what the repository the plan lives in holds.
type planningHome struct {
	content ports.PlanContent
	tracked []string
	ignored []string
}

func newHome(ctx context.Context, home Found, layouts ports.Layouts) (planningHome, error) {
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

func (h planningHome) lacksManifest() bool { return !h.content.Manifest && !h.tracks(ManifestPath) }
func (h planningHome) tracks(file string) bool {
	_, held := heldAs(h.tracked, file)
	return held
}

// holdsMakefile says make would read a Makefile here: GNUmakefile, makefile, or Makefile, in any case.
func (h planningHome) holdsMakefile() bool {
	return h.tracks("GNUmakefile") || h.tracks(planning.MakefilePath)
}

// heldAs is the path a list of tracked paths holds a file under, in any case, because on a file system that ignores
// case a file under another case is the file, and a second one beside it would collide with it.
func heldAs(tracked []string, file string) (string, bool) {
	for _, held := range tracked {
		if strings.EqualFold(held, file) {
			return held, true
		}
	}
	return "", false
}
func (h planningHome) ignores(dir string) bool { return slices.Contains(h.ignored, dir) }

// movesOf lists, for each repository registered beside the planning repository's clone, the commands that move it
// into the clone's folder of clones and register it there.
func movesOf(home Found, registers []Found) []planning.Move {
	var moves []planning.Move
	for _, one := range registers {
		if one.Path == home.Path || strings.HasPrefix(one.Path, home.Path+"/") {
			continue
		}
		to := path.Join(home.Path, CodeFolder, one.Name)
		moves = append(moves, planning.Move{
			Name: one.Name, From: one.Path, To: to,
			Commands: []string{fmt.Sprintf("mv %s %s", shellQuote(one.Path), shellQuote(to)), fmt.Sprintf("%s repository add %s", Program, shellQuote(to))},
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
func consolidation(ctx context.Context, plan *planning.AdoptionPlan, in Request, design *designClone, homeTracked []string, layouts ports.Layouts) error {
	trees, err := layouts.CodeTrees(ctx, design.Path)
	if err != nil {
		return err
	}
	var left []string
	for _, name := range in.LeaveOut {
		tree := path.Clean(strings.TrimSuffix(name, "/"))
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
	plan.Takes, plan.Collisions = Arrivals(homeTracked, held, left)
	return nil
}

// Arrivals sorts what a design repository's tip holds, given what the workspace holds and the code trees left
// out, as a consolidation merges it: the paths the merge takes from the design repository, and the paths both
// hold, each with the version kept, the workspace's, or for the ignore file at the top the union of both files'
// lines. A path inside a tree left out arrives in neither list. The plan and the brief of the session that
// onboards the merge both read it, so they say the same.
func Arrivals(ours, theirs, leftOut []string) (takes []string, collisions []planning.Collision) {
	for _, tracked := range theirs {
		if slices.ContainsFunc(leftOut, func(tree string) bool { return strings.HasPrefix(tracked, tree+"/") }) {
			continue
		}
		switch {
		case tracked == planning.GitignorePath && slices.Contains(ours, tracked):
			collisions = append(collisions, planning.Collision{Path: tracked, Kept: planning.KeptUnion})
		case slices.Contains(ours, tracked):
			collisions = append(collisions, planning.Collision{Path: tracked, Kept: planning.KeptWorkspace})
		default:
			takes = append(takes, tracked)
		}
	}
	return takes, collisions
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
