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
)

// restructuring fills in what `--restructure` would do in the shape: the files it adds to the planning
// repository, the paths a consolidation takes and where the two repositories collide, the design repository's code
// trees, and the moves it leaves to the person.
func restructuring(ctx context.Context, plan *planning.AdoptionPlan, in Request, found []Found, registers []Found, design *designClone, planned *Found, layouts ports.Layouts) error {
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
	manifest := planning.PlannedFile{Path: ManifestPath, Action: planning.ActionAdd, Detail: "the manifest of the project's repositories"}
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
		if len(plan.Moves) != 0 && !held.ignores(CodeFolder) {
			plan.Files = append(plan.Files, planning.PlannedFile{Path: ".gitignore", Action: planning.ActionAppend, Detail: CodeFolder + "/"})
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

func (h planningHome) lacksManifest() bool     { return !h.content.Manifest && !h.tracks(ManifestPath) }
func (h planningHome) tracks(file string) bool { return slices.Contains(h.tracked, file) }
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
