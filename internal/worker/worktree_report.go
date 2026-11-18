package worker

import (
	"context"
	"fmt"
	"maps"
	"path"
	"slices"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// WorktreeReport says where lane worktrees go on this machine: the root a profile puts in force and the layer
// that set it, each checkout this worker keeps, in every project, for which that root resolves inside a clone
// this worker keeps, and each project's workspace with the root each of its checkouts gets. It asks the question a
// lane's start asks, for the project and folder of each checkout, so the doctor finds before a lane does that the
// lane would fail with worktree_inside_clone, and that a workspace does not ignore the folder for worktrees.
//
// planningOf names each project's planning repository by ID, for the workspace a project's planning clone makes. A
// project absent from it has no workspace but the one its profile names.
func WorktreeReport(ctx context.Context, profile lanes.Profile, kept ports.Checkouts, planningOf map[string]string) (lanes.WorktreeReport, error) {
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
	reports := map[string]*lanes.WorkspaceReport{}
	for _, project := range projectsOf(checkouts) {
		workspace, found, err := FindWorkspace(ctx, kept, profile, project, planningOf[project])
		if err != nil {
			return report, err
		}
		if found {
			reports[project] = workspaceReport(project, workspace)
		}
	}
	for _, checkout := range checkouts {
		project := string(checkout.Project)
		var workspace lanes.Workspace
		if held := reports[project]; held != nil {
			workspace = lanes.Workspace{Path: held.Path, Found: held.Found, Clone: held.Clone, Ignored: held.Ignored}
		}
		settings := profile.Worktree
		if workspace.Path != "" {
			if settings, _, err = ports.WorkspaceSettings(ctx, kept, profile.Worktree, workspace, checkout.Path); err != nil {
				return report, err
			}
		}
		place, err := lanes.Place(settings, checkout.Path, project, "lane", "")
		if err != nil {
			return report, err
		}
		root := path.Dir(place.Path)
		allowed := ""
		if workspace.Clone {
			allowed = workspace.Path
		}
		clone, inside, err := ports.InsideClone(ctx, kept, clones, root, allowed)
		if err != nil {
			return report, err
		}
		if inside {
			report.Inside = append(report.Inside, lanes.RootInsideClone{Project: project, Checkout: checkout.Path, Root: root, Clone: clone})
		}
		if held := reports[project]; held != nil {
			held.Roots = append(held.Roots, lanes.CheckoutRoot{Checkout: checkout.Path, Root: root})
		}
	}
	for _, project := range slices.Sorted(maps.Keys(reports)) {
		report.Workspaces = append(report.Workspaces, *reports[project])
	}
	return report, nil
}

// workspaceReport starts one project's section of the doctor's report, naming the line a clone that does not
// ignore the folder for worktrees lacks.
func workspaceReport(project string, workspace lanes.Workspace) *lanes.WorkspaceReport {
	held := &lanes.WorkspaceReport{
		Project: project, Path: workspace.Path, Found: workspace.Found, Clone: workspace.Clone, Ignored: workspace.Ignored, Roots: []lanes.CheckoutRoot{},
	}
	if workspace.Clone && !workspace.Ignored {
		held.Line = planning.WorktreesLine
	}
	return held
}

// projectsOf lists the projects the checkouts belong to, in order.
func projectsOf(checkouts []ports.Checkout) []string {
	seen := map[ledger.WorkspaceID]bool{}
	var projects []string
	for _, checkout := range checkouts {
		if !seen[checkout.Project] {
			seen[checkout.Project] = true
			projects = append(projects, string(checkout.Project))
		}
	}
	slices.Sort(projects)
	return projects
}
