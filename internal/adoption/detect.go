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

// Found is a clone the detection found, with what it holds.
type Found struct {
	planning.LayoutClone
	clone   ports.Clone
	content ports.PlanContent
}

// Remote is the clone's remote, as git says of it, or empty for a clone with none.
func (f Found) Remote() string { return f.clone.Remote }

// DefaultBranch is the clone's default branch, which a registration records as the base of a repository it makes.
func (f Found) DefaultBranch() string { return f.clone.DefaultBranch }

// detect finds the clone adopt runs in, the clones in each directory it ignores, and the clones the paths name,
// each once, in order of path with the clone adopt runs in first. It also says which folders the root ignores
// hold clones, and which paths named hold none.
func detect(ctx context.Context, in Request, kept ports.Checkouts, layouts ports.Layouts) ([]Found, []ports.UnreadPath, []string, error) {
	depth := max(in.Depth, 1)
	roots, _, err := kept.Find(ctx, []string{in.Root}, 1)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(roots) != 1 || !ports.SamePath(ctx, kept, roots[0].Path, in.Root) {
		return nil, nil, nil, fmt.Errorf("%w: run adopt in the top of a git clone with a commit; %s is none", ledger.ErrInvalidRepository, in.Root)
	}
	top := roots[0]
	ignored, err := layouts.IgnoredDirectories(ctx, top.Path)
	if err != nil {
		return nil, nil, nil, err
	}
	var searched []string
	for _, dir := range ignored {
		// A .ropeshift folder holds lane worktrees, which are no clone to adopt (FR-PROF-6).
		if !ports.InRopeshiftFolder(dir) {
			searched = append(searched, path.Join(top.Path, dir))
		}
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

	found := []Found{{clone: top}}
	seen := map[string]bool{top.Path: true}
	var folders []string
	for _, clone := range clones {
		if seen[clone.Path] {
			continue
		}
		seen[clone.Path] = true
		one := Found{clone: clone}
		for _, folder := range searched {
			if strings.HasPrefix(clone.Path, folder+"/") {
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
func (f *Found) read(ctx context.Context, layouts ports.Layouts) error {
	var err error
	f.Name, f.Path, f.Identity = cmp.Or(f.clone.Directory, path.Base(f.clone.Path)), f.clone.Path, f.clone.ID
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
	*Found
	why string
}

// chooseDesign names the design repository: the clone `--design` names, else the one that holds plan content, else
// the one with the most committed Markdown documents. A tie names none, and the clones that tied come back.
func chooseDesign(found []Found, named string) (*designClone, []string, error) {
	if named != "" {
		one, err := byName(found, named, "--design")
		if err != nil {
			return nil, nil, err
		}
		return &designClone{Found: one, why: "named with --design"}, nil, nil
	}
	candidates := slices.Clone(found)
	reason := "it holds the most Markdown documents"
	if planned := slices.DeleteFunc(slices.Clone(found), func(one Found) bool { return !one.Plan }); len(planned) != 0 {
		candidates, reason = planned, "it holds plan content"
	}
	most := 0
	for _, one := range candidates {
		most = max(most, one.Documents)
	}
	if most == 0 {
		return nil, nil, nil
	}
	var winners []*Found
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
	return &designClone{Found: winners[0], why: fmt.Sprintf("%s (%d)", reason, most)}, nil, nil
}

// choosePlanning is the clone `--planning` names, or nil when it names none the detection found.
func choosePlanning(found []Found, named string) (*Found, error) {
	if named == "" {
		return nil, nil
	}
	matches := slices.DeleteFunc(slices.Clone(found), func(one Found) bool { return one.Name != named })
	if len(matches) > 1 {
		return nil, fmt.Errorf("%w: --planning %s names %d clones that were found", ledger.ErrInvalidRepository, named, len(matches))
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return &matches[0], nil
}

// byName is the one clone with a folder name.
func byName(found []Found, name, flag string) (*Found, error) {
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
func shapeOf(found []Found, design *designClone) planning.LayoutShape {
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
