package worker

import (
	"context"
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// WorktreeReport says where lane worktrees go on this machine: the root a profile puts in force and the layer
// that set it, and each checkout this worker keeps, in every project, for which that root resolves inside a clone
// this worker keeps. It asks the question a lane's start asks, for the project and folder of each checkout, so
// the doctor finds before a lane does that the lane would fail with worktree_inside_clone.
func WorktreeReport(ctx context.Context, profile lanes.Profile, kept ports.Checkouts) (lanes.WorktreeReport, error) {
	report := lanes.WorktreeReport{Root: profile.Worktree.Root, Source: profile.Worktree.RootSource}
	if profile.Worktree.Root == "" {
		return report, fmt.Errorf("%w: the profile names no worktree root", lanes.ErrBadProfile)
	}
	checkouts, err := kept.All(ctx)
	if err != nil {
		return report, err
	}
	clones, err := kept.Every(ctx)
	if err != nil {
		return report, err
	}
	for _, checkout := range checkouts {
		place, err := lanes.Place(profile.Worktree, checkout.Path, string(checkout.Project), "lane", "")
		if err != nil {
			return report, err
		}
		root := path.Dir(place.Path)
		clone, inside, err := ports.InsideClone(ctx, kept, clones, root)
		if err != nil {
			return report, err
		}
		if inside {
			report.Inside = append(report.Inside, lanes.RootInsideClone{Project: string(checkout.Project), Checkout: checkout.Path, Root: root, Clone: clone})
		}
	}
	return report, nil
}
