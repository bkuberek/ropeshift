package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// consolidate imports the design repository's history into an adopt lane's branch, after its worktree exists and
// before its inventory is written (FR-ADOPT-13). It reads the layout again as the plan did and compares digests,
// as the workspace files' restructuring does, and then makes the import as one bracketed effect: one merge commit,
// the workspace first and the design repository's tip second, that also holds the manifest, the Makefile, and the
// worktrees ignore line the plan lists. A retry after a crash finds the merge on the branch and makes none. The
// design repository's clone is only read.
func (r *laneRun) consolidate(ctx context.Context, choice planning.Restructure, worktree string) (workorders.Report, bool) {
	trees, offered := r.rt.Provisioner.Worktrees.(ports.HistoryImports)
	if !offered || r.rt.Layouts == nil || r.rt.Provisioner.Checkouts == nil {
		return r.failStart(workorders.ReasonSpawnFailed, errNoRestructure), true
	}
	commits, err := trees.Commits(ctx, worktree, r.lease.BaseCommit)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("looking for the import on the branch: %w", err)), true
	}
	if made, found := importCommit(commits); found {
		// The merge is on the branch. What the brief tells is read from it, because the person may have moved the
		// clones or committed to the design repository since, and the layout is read no more to compare.
		return r.tellConsolidation(ctx, worktree, made, choice.LeaveOut)
	}
	plan, report, failed := r.verifyPlan(ctx, choice)
	if failed {
		return report, true
	}
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil {
		return r.layoutChanged(fmt.Errorf("the layout around %s reads as the %s shape with no design repository to import, and the restructuring chose a consolidation.%s",
			choice.Root, plan.Shape, rerun)), true
	}
	project, err := r.projectLabel(ctx)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	files, err := adoption.Files(ctx, plan, project, r.rt.Provisioner.Checkouts)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	tip, err := r.rt.Layouts.Tip(ctx, plan.Design.Path)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("reading the tip of %s: %w", plan.Design.Name, err)), true
	}
	var left []string
	for _, tree := range plan.CodeTrees {
		if tree.LeftOut {
			left = append(left, tree.Path)
		}
	}
	in := ports.HistoryImport{
		Source: plan.Design.Path, Commit: tip, Root: choice.DesignRoot, LeaveOut: left, Files: files, Subject: planning.ImportCommitSubject(plan.Design.Name),
	}
	effect := lanes.NewEffect(lanes.EffectRestructure, r.lease.Lane, r.currentGeneration(), worktree, "")
	_, err = r.w.bracket(ctx, effect, func() error {
		_, err := trees.ImportHistory(ctx, worktree, in)
		return err
	})
	var another *ports.HistoryRootError
	switch {
	case errors.As(err, &another):
		return r.layoutChanged(fmt.Errorf("the history of %s now begins at %s, and the plan stored %s as its first commit, so it is not the repository the plan was read from.%s",
			plan.Design.Name, another.Got, another.Want, rerun)), true
	case err != nil:
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("importing the history of %s: %w", plan.Design.Name, err)), true
	}
	commits, err = trees.Commits(ctx, worktree, r.lease.BaseCommit)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("looking for the import on the branch: %w", err)), true
	}
	made, found := importCommit(commits)
	if !found {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("the branch holds no merge commit that imports the history of %s: it held the design repository's tip already, or the import did not run", plan.Design.Name)), true
	}
	return r.tellConsolidation(ctx, worktree, made, left)
}

// importCommit is the newest commit of a branch that imports a design repository's history: a merge commit with the
// import's subject. Only the worker makes one before a lane starts.
func importCommit(commits []ports.Commit) (ports.Commit, bool) {
	at := slices.IndexFunc(commits, func(commit ports.Commit) bool {
		_, imports := planning.ImportedDesign(commit.Subject)
		return imports && len(commit.Parents) == 2
	})
	if at < 0 {
		return ports.Commit{}, false
	}
	return commits[at], true
}

// instructionFiles are the files a repository's agent instructions are written in, at its top.
var instructionFiles = []string{"AGENTS.md", "CLAUDE.md"}

// tellConsolidation keeps what the brief tells the lane about the merge, read from the merge itself: its commit and
// second parent, the paths both repositories held with the version kept, the trees left out, and the design
// repository's instruction files at the second parent.
func (r *laneRun) tellConsolidation(ctx context.Context, worktree string, made ports.Commit, leftOut []string) (workorders.Report, bool) {
	design, _ := planning.ImportedDesign(made.Subject)
	ours, err := r.rt.Layouts.Tracked(ctx, worktree, made.Parents[0])
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("reading what the workspace held before the import: %w", err)), true
	}
	theirs, err := r.rt.Layouts.Tracked(ctx, worktree, made.Parents[1])
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("reading what %s held: %w", design, err)), true
	}
	takes, collisions := adoption.Arrivals(ours, theirs, leftOut)
	done := lanes.Restructured{
		Commit: made.Hash, Subject: made.Subject,
		Consolidated: &lanes.Consolidated{Design: design, SecondParent: made.Parents[1], Collisions: collisions, LeftOut: slices.Clone(leftOut)},
	}
	for _, name := range instructionFiles {
		if slices.Contains(theirs, name) {
			done.Consolidated.Instructions = append(done.Consolidated.Instructions, name)
		}
	}
	// The manifest is the worker's, and a design repository that brought its own at the same path made it its own.
	if slices.Contains(made.Files, planning.ManifestPath) && !slices.Contains(takes, planning.ManifestPath) {
		project, err := r.projectLabel(ctx)
		if err != nil {
			return r.failStart(workorders.ReasonSpawnFailed, err), true
		}
		done.Manifest = planning.DocumentRef(planning.FileRepositories, project)
	}
	r.setRestructured(done)
	return workorders.Report{}, false
}
