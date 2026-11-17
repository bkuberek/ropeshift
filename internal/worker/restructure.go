package worker

import (
	"cmp"
	"context"
	"encoding/json"
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

// errNoRestructure says a lease asked for a restructuring this worker cannot make: it reads no layouts, finds no
// clones, or commits no files.
var errNoRestructure = errors.New("this worker cannot read layouts and commit files")

// rerun tells a person what to do about a layout that is not the plan's.
const rerun = " Run ropeshift adopt --restructure again to plan from the layout as it is."

// restructure makes the restructuring an adopt lane's work carries, after its worktree exists and before its
// inventory is written (FR-ADOPT-14). It reads the layout again as the plan did and compares digests, and when they
// differ the work fails with the reason layout_changed, naming the parts that differ. When they match it commits
// the files the plan lists to the lane's branch in one commit, as one bracketed effect, and a retry after a crash
// finds the commit and makes none. It reports whether the lane must not start, and the report that says why.
//
// Once the commit is on the branch the layout is read no more to compare: the person may have made the moves
// since, and the files are written.
func (r *laneRun) restructure(ctx context.Context, repository, worktree string) (workorders.Report, bool) {
	if len(r.lease.Restructure) == 0 {
		return workorders.Report{}, false
	}
	var choice planning.Restructure
	err := json.Unmarshal(r.lease.Restructure, &choice)
	if err == nil {
		err = choice.Validate()
	}
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("the restructuring the work kept cannot be read: %w", err)), true
	}
	if choice.Shape != planning.ShapePlanned && choice.Shape != planning.ShapeWorkspaceFiles {
		return workorders.Report{}, false
	}
	trees, offered := r.rt.Provisioner.Worktrees.(ports.FileCommits)
	if !offered || r.rt.Layouts == nil || r.rt.Provisioner.Checkouts == nil {
		return r.failStart(workorders.ReasonSpawnFailed, errNoRestructure), true
	}
	project, err := r.projectLabel(ctx)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	made, found, err := r.restructureCommit(ctx, trees, worktree)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	if found {
		// The files are on the branch. What the plan lists now is what the person still has to move.
		var moves []planning.Move
		if len(choice.Missing()) == 0 {
			if again, err := r.replan(ctx, choice); err == nil {
				moves = again.Moves
			}
		}
		r.setRestructured(describeRestructure(made, project, moves))
		return workorders.Report{}, false
	}
	if missing := choice.Missing(); len(missing) != 0 {
		return r.layoutChanged(fmt.Errorf("the restructuring this adoption kept has no %s: it was kept before the plan's request and the digests of "+
			"its parts were, so the layout cannot be read again as the plan read it.%s", strings.Join(missing, " and no "), rerun)), true
	}
	plan, err := r.replan(ctx, choice)
	if err != nil {
		return r.layoutChanged(fmt.Errorf("the layout around %s could not be read again as the plan read it: %w.%s", choice.Root, err, rerun)), true
	}
	if plan.Digest != choice.Digest {
		differs := plan.DiffersIn(choice.Parts)
		return r.layoutChanged(fmt.Errorf("the layout around %s is not the one the plan was read from: its %s differ. The plan's digest was %s, and the layout now gives %s.%s",
			choice.Root, spell(differs), choice.Digest, plan.Digest, rerun)), true
	}
	files, err := adoption.Files(ctx, plan, project, r.rt.Provisioner.Checkouts)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	if len(files) != 0 {
		effect := lanes.NewEffect(lanes.EffectRestructure, r.lease.Lane, r.currentGeneration(), worktree, "")
		_, err := r.w.bracket(ctx, effect, func() error {
			_, err := trees.CommitFiles(ctx, worktree, files, planning.WorkspaceCommitSubject)
			return err
		})
		if err != nil {
			return r.failStart(workorders.ReasonSpawnFailed, fmt.Errorf("committing the workspace files: %w", err)), true
		}
	}
	made, _, err = r.restructureCommit(ctx, trees, worktree)
	if err != nil {
		return r.failStart(workorders.ReasonSpawnFailed, err), true
	}
	r.setRestructured(describeRestructure(made, project, plan.Moves))
	return workorders.Report{}, false
}

// spell lists names as a sentence does: "a", "a and b", and "a, b, and c".
func spell(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
}

// layoutChanged fails the work with the reason layout_changed, and the detail says what differs and how to go on.
func (r *laneRun) layoutChanged(detail error) workorders.Report {
	report := r.failStart(workorders.ReasonLayoutChanged, detail)
	report.Detail = detail.Error()
	return report
}

// replan reads the layout as the plan did, from the request the work kept: the same root, paths, depth, and names,
// and the project's records as the lease gives them.
func (r *laneRun) replan(ctx context.Context, choice planning.Restructure) (planning.AdoptionPlan, error) {
	request := adoption.Request{Root: choice.Root, Paths: choice.Paths, Depth: choice.Depth, Planning: choice.Planning, Design: choice.Design}
	return adoption.Plan(ctx, request, r.rt.Provisioner.Checkouts, r.rt.Layouts, r.registered)
}

// registered says what a registration of the clones does, as the worker knows the project's records: a clone of a
// repository the project holds is registered under the name the project gives it, and any other is not. The
// control plane registered each clone before it made this work, so a clone the project does not hold was refused.
func (r *laneRun) registered(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
	names := map[string]string{r.lease.Repository: r.lease.RepositoryName}
	for _, read := range r.lease.Reads {
		names[read.Repository] = read.Name
	}
	var out []planning.PlannedRepository
	for _, one := range clones {
		name, held := names[one.Identity]
		if !held {
			out = append(out, planning.PlannedRepository{
				Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.RegistrationRefused), Reason: "the project holds no repository of it",
			})
			continue
		}
		out = append(out, planning.PlannedRepository{Name: cmp.Or(name, one.Name), Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
	}
	return out, nil
}

// projectLabel is the name of the lane's project, which the manifest takes for its id. An empty project names the
// plane's only project.
func (r *laneRun) projectLabel(ctx context.Context) (string, error) {
	state, err := r.w.desired(ctx)
	if err != nil {
		return "", err
	}
	for _, served := range state.Projects {
		if served.Name != "" && (served.ID == r.lease.Project || r.lease.Project == "" && len(state.Projects) == 1) {
			return served.Name, nil
		}
	}
	if r.lease.Project != "" {
		return r.lease.Project, nil
	}
	return "", errors.New("the project this lane works in has no name the manifest could take for its id")
}

// restructureCommit finds the commit that holds the workspace files on the lane's branch, beyond the commit the
// branch began at.
func (r *laneRun) restructureCommit(ctx context.Context, trees ports.FileCommits, worktree string) (ports.Commit, bool, error) {
	commits, err := trees.Commits(ctx, worktree, r.lease.BaseCommit)
	if err != nil {
		return ports.Commit{}, false, fmt.Errorf("looking for the workspace files on the branch: %w", err)
	}
	made, found := workspaceCommit(commits)
	return made, found, nil
}

// workspaceCommit is the newest commit of a branch that carries the workspace files' subject.
func workspaceCommit(commits []ports.Commit) (ports.Commit, bool) {
	at := slices.IndexFunc(commits, func(commit ports.Commit) bool { return commit.Subject == planning.WorkspaceCommitSubject })
	if at < 0 {
		return ports.Commit{}, false
	}
	return commits[at], true
}

// describeRestructure is what an adopt lane's brief says the worker did: the commit, if there is one, and the moves.
func describeRestructure(made ports.Commit, project string, moves []planning.Move) lanes.Restructured {
	done := lanes.Restructured{Commit: made.Hash, Subject: made.Subject, Files: made.Files, Moves: moves}
	if slices.Contains(made.Files, planning.ManifestPath) {
		done.Manifest = planning.DocumentRef(planning.FileRepositories, project)
	}
	return done
}

// setRestructured keeps what the worker did, which the brief tells the lane, and which every generation's brief
// repeats.
func (r *laneRun) setRestructured(done lanes.Restructured) {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	r.restructured = &done
}

// restructuredOf is what the worker did to this lane's repository, or nil when it did nothing.
func (r *laneRun) restructuredOf() *lanes.Restructured {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	return r.restructured
}

// findRestructure looks for the commit a crash may have left: a branch that holds the workspace files' commit
// shows the effect happened, and one that does not shows it did not, and a branch that cannot be read shows
// nothing.
func (w *Worker) findRestructure(ctx context.Context, effect lanes.Effect) lanes.Finding {
	trees, offered := w.config.Lanes.Provisioner.Worktrees.(ports.FileCommits)
	if !offered {
		return lanes.FoundUnknown
	}
	base := ""
	if state, err := w.desired(ctx); err == nil {
		for _, lease := range state.Leases {
			if lease.Lane == effect.Lane {
				base = lease.BaseCommit
			}
		}
	}
	commits, err := trees.Commits(ctx, effect.Target, base)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return lanes.FoundNothing
	case err != nil:
		w.config.Report(fmt.Errorf("effect %s: %w", effect.Key, err))
		return lanes.FoundUnknown
	}
	if _, found := workspaceCommit(commits); found {
		return lanes.Found
	}
	return lanes.FoundNothing
}
