package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// consolidate imports the design repository's history into an adopt lane's branch, after its worktree exists and
// before its inventory is written (FR-ADOPT-13). It reads the layout again as the plan did and compares digests,
// as the workspace files' restructuring does, and then makes the import as one bracketed effect: one merge commit,
// the workspace first and the design repository's tip second, that also holds the manifest, the Makefile, and the
// worktrees ignore line the plan lists. The merge is kept in the journal when it is made, and every later start of
// the lane reads it from there. A crash between the merge and its record leaves the merge on the branch, which the
// restart finds and keeps: the oldest merge with the import's subject that grew from the commit the branch began at,
// because a lane's own merge comes after it. The design repository's clone is only read.
func (r *laneRun) consolidate(ctx context.Context, choice planning.Restructure, worktree string) (workorders.Report, bool) {
	trees, offered := r.rt.Provisioner.Worktrees.(ports.HistoryImports)
	if !offered || r.rt.Layouts == nil || r.rt.Provisioner.Checkouts == nil {
		return r.failStart(workorders.ReasonSpawnFailed, errNoRestructure), true
	}
	commits, err := trees.Commits(ctx, worktree, r.lease.BaseCommit)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("looking for the import on the branch: %w", err)), true
	}
	made, recorded, found, err := r.keptImport(ctx, commits)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("reading the merge that imports the design history from the journal: %w", err)), true
	}
	if found {
		// The merge is on the branch. What the brief tells is read from it, because the person may have moved the
		// clones or committed to the design repository since, and the layout is read no more to compare.
		return r.tellConsolidation(ctx, worktree, made, choice.LeaveOut)
	}
	if recorded {
		// The worker made the import, and the branch no longer holds it: the lane moved its branch. No merge on the
		// branch is the worker's, so the lane reports none, the import reads as unread to the control plane, and a
		// member decides.
		r.w.config.Report(fmt.Errorf("lane %s: the branch no longer holds the merge that imports the design history, so the import is reported as unread", r.lease.Lane))
		return workorders.Report{}, false
	}
	if made, found := importCommit(commits, r.lease.BaseCommit); found {
		if err := r.keepImport(ctx, made.Hash); err != nil {
			return r.failStart(workorders.ReasonSpawnFailed, err), true
		}
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
	files, err := adoption.Files(ctx, plan, project, r.rt.Provisioner.Checkouts, r.rt.Render)
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
	made, found = importCommit(commits, r.lease.BaseCommit)
	if !found {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("the branch holds no merge commit that imports the history of %s: it held the design repository's tip already, or the import did not run", plan.Design.Name)), true
	}
	if err := r.keepImport(ctx, made.Hash); err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	return r.tellConsolidation(ctx, worktree, made, left)
}

// importCommit is the oldest commit of a branch, listed newest first, that imports a design repository's history: a
// merge commit with the import's subject that grew from base, the commit the branch began at, when there is one. The
// worker makes it before a lane starts, so a merge a lane makes later with that subject comes after it.
func importCommit(commits []ports.Commit, base string) (ports.Commit, bool) {
	for _, commit := range slices.Backward(commits) {
		if _, imports := planning.ImportedDesign(commit.Subject); imports && len(commit.Parents) == 2 && (base == "" || commit.Parents[0] == base) {
			return commit, true
		}
	}
	return ports.Commit{}, false
}

// importRecord labels the effect that keeps the merge the worker made to import a design repository's history. The
// effect's target is the merge's commit, so the journal holds it for every later start of the lane.
const importRecord = "import"

// keepImport records the commit of the merge that imports the design repository's history, as an effect of the
// lane's journal, so a later generation reads it instead of searching the branch.
func (r *laneRun) keepImport(ctx context.Context, commit string) error {
	if !ledger.CommitHash(commit) {
		return fmt.Errorf("the merge that imports the design history has the commit %q, which is not a commit ID", commit)
	}
	effect := lanes.NewEffect(lanes.EffectRestructure, r.lease.Lane, r.currentGeneration(), commit, importRecord)
	if _, err := r.w.bracket(ctx, effect, func() error { return nil }); err != nil {
		return fmt.Errorf("keeping the merge that imports the design history: %w", err)
	}
	return nil
}

// keptImport reads the journal's record of the import this lane's worker made, which the newest of its records
// names. It says whether the journal holds a completed record, and whether the branch holds the commit it names,
// which it returns. A branch that lacks the recorded commit is not searched for another: only a lane with no record
// is.
func (r *laneRun) keptImport(ctx context.Context, commits []ports.Commit) (made ports.Commit, recorded, found bool, err error) {
	effects, err := r.w.journal.Effects(ctx)
	if err != nil {
		return ports.Commit{}, false, false, err
	}
	for _, effect := range slices.Backward(effects) {
		if effect.Lane != r.lease.Lane || effect.Kind != lanes.EffectRestructure || effect.State != lanes.EffectCompleted || !isImportRecord(effect) {
			continue
		}
		for _, commit := range commits {
			if commit.Hash == effect.Target && len(commit.Parents) == 2 {
				return commit, true, true, nil
			}
		}
		return ports.Commit{}, true, false, nil
	}
	return ports.Commit{}, false, false, nil
}

// isImportRecord says an effect is the record of a merge that imports a design repository's history.
func isImportRecord(effect lanes.Effect) bool {
	return strings.HasSuffix(effect.Key, "/"+importRecord) && ledger.CommitHash(effect.Target)
}

// importMerge is the merge commit the worker made on this lane's branch to import a design repository's history, which
// the lane's end reports, and nothing when the lane imported none.
func (r *laneRun) importMerge() string {
	if done := r.restructuredOf(); done != nil && done.Consolidated != nil {
		return done.Commit
	}
	return ""
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
	// The design repository's instruction files are the ones at its top in any case, under the names it holds them by.
	for _, name := range instructionFiles {
		if at := slices.IndexFunc(theirs, func(tracked string) bool { return strings.EqualFold(tracked, name) }); at >= 0 {
			done.Consolidated.Instructions = append(done.Consolidated.Instructions, theirs[at])
		}
	}
	// The files the worker wrote in the merge commit are the instruction files neither repository held, which the
	// brief tells the lane to edit.
	if slices.Contains(made.Files, "AGENTS.md") && !ports.HoldsInstructions(ours) && !ports.HoldsInstructions(theirs) {
		done.Files = []string{"AGENTS.md", "CLAUDE.md"}
	}
	// The manifest is the worker's, and a design repository that brought its own at the same path made it its own.
	if adoption.Holds(made.Files, planning.ManifestPath) && !adoption.Holds(takes, planning.ManifestPath) {
		project, err := r.projectLabel(ctx)
		if err != nil {
			return r.failStart(workorders.ReasonSpawnFailed, err), true
		}
		done.Manifest = planning.DocumentRef(planning.FileRepositories, project)
	}
	r.setRestructured(done)
	return workorders.Report{}, false
}
