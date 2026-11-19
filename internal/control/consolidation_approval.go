package control

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// importPaths bounds the paths a consolidation's approval reads of each side of the merge.
const importPaths = 100_000

// consolidationReading is what an adoption branch and this machine's clones say of a consolidation, read before the
// request's transaction: what its approval lists, and the record the clones show is the old design repository.
type consolidationReading struct {
	view *planning.Consolidation
	// design is the project's record of the design repository, when this worker keeps it at the clone the plan
	// was read from or the clone's remote is the record's. It is empty when the project holds none, or holds one
	// under the design repository's first commit that nothing shows is that clone.
	design string
}

// consolidationRead reads, from the repository's own history, what an adoption that consolidated a design
// repository merged on its branch (FR-ADOPT-13): the merge commit that imported the history, its second parent,
// and every path both repositories held. The trees left out are the ones the member chose. It also reads whether
// the record the project holds under the design repository's first commit is the clone the adoption imported,
// because two repositories cut from one first commit share it. What cannot be read is said, and the request is
// raised all the same. Work that consolidated nothing reads as no view.
func (i *Intake) consolidationRead(ctx context.Context, scope ledger.WorkspaceID, plan acceptedPlan) consolidationReading {
	choice := plan.order.Restructure
	if !choice.Consolidates() {
		return consolidationReading{}
	}
	view := &planning.Consolidation{Design: choice.DesignName(), Collisions: []planning.Collision{}, LeftOut: append([]string{}, choice.LeaveOut...)}
	view.Unread = i.readImport(ctx, scope, plan, view)
	design, why := i.confirmDesign(ctx, scope, *choice)
	view.RemovalUnread = why
	return consolidationReading{view: view, design: design}
}

// readImport fills in the merge, the design tip, and the collisions of a consolidation from the adoption branch,
// and says why it could not when it could not. The import is the oldest merge on the branch with the import's
// subject, which the worker made before the lane began, and its second parent must begin at the design
// repository's first commit, so a merge the lane made with that subject imports nothing here.
func (i *Intake) readImport(ctx context.Context, scope ledger.WorkspaceID, plan acceptedPlan, view *planning.Consolidation) string {
	if i.starts == nil {
		return "this control plane reads no repository's history"
	}
	imports, offered := i.starts.Bases.(ports.BranchImports)
	if !offered {
		return "this control plane's git reads cannot list a branch's commits"
	}
	path, err := i.starts.Checkout(ctx, scope, plan.order.RepositoryID)
	if err != nil {
		return err.Error()
	}
	// The branch is read from the commit its claim began it at, or from its newest commits when no claim says.
	var base string
	_ = i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		claim, err := tx.Claims().OfLane(plan.lane)
		base = claim.BaseCommit
		return err
	})
	commits, err := imports.BranchCommits(ctx, path, base, plan.commit)
	if err != nil {
		return fmt.Sprintf("git could not read the adoption branch at %s: %v", plan.commit, err)
	}
	merge, found := importCommit(commits)
	if !found {
		return fmt.Sprintf("the adoption branch holds no merge commit that imports the history of %s among its %d newest commits", view.Design, len(commits))
	}
	root, err := imports.FirstParentRoot(ctx, path, merge.Parents[1])
	if err != nil {
		return fmt.Sprintf("git could not read where the history merge commit %s imports begins: %v", merge.Hash, err)
	}
	if want := plan.order.Restructure.DesignRoot; root != want {
		return fmt.Sprintf("the merge commit %s brings a history whose first commit is %s, not the design repository's first commit %s, so it imports no history of the design repository",
			merge.Hash, root, want)
	}
	design, _ := planning.ImportedDesign(merge.Subject)
	ours, oursCut, err := imports.CommitPaths(ctx, path, merge.Parents[0], importPaths)
	if err != nil {
		return fmt.Sprintf("git could not read what the workspace held at %s: %v", merge.Parents[0], err)
	}
	theirs, theirsCut, err := imports.CommitPaths(ctx, path, merge.Parents[1], importPaths)
	if err != nil {
		return fmt.Sprintf("git could not read what %s held at %s: %v", design, merge.Parents[1], err)
	}
	view.Design, view.Merge, view.DesignTip = design, merge.Hash, merge.Parents[1]
	_, collisions := adoption.Arrivals(ours, theirs, view.LeftOut)
	view.CollisionsTruncated = oursCut || theirsCut || len(collisions) > planning.MaxCollisions
	if len(collisions) > planning.MaxCollisions {
		collisions = collisions[:planning.MaxCollisions]
	}
	if collisions != nil {
		view.Collisions = collisions
	}
	return ""
}

// importCommit is the oldest commit of a branch, listed newest first, that imports a design repository's history: a
// merge commit with the import's subject, which the worker makes before an adopt lane starts. A later one is the
// lane's own.
func importCommit(commits []ports.Commit) (ports.Commit, bool) {
	for _, commit := range slices.Backward(commits) {
		if _, imports := planning.ImportedDesign(commit.Subject); imports && len(commit.Parents) == 2 {
			return commit, true
		}
	}
	return ports.Commit{}, false
}

// confirmDesign reads whether the record the project holds under the design repository's first commit is the clone
// the adoption imported: this worker keeps the record at that clone, or the record's remote and the clone's name
// one repository. Two repositories cut from one first commit with different remotes, or with a remote missing, are
// not told apart that way, so approving removes nothing and the request says why. It names the confirmed record,
// or nothing with the reason.
func (i *Intake) confirmDesign(ctx context.Context, scope ledger.WorkspaceID, choice planning.Restructure) (string, string) {
	var record ledger.Repository
	err := i.store.Read(ctx, scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Repositories().Repository(choice.DesignRoot)
		return err
	})
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return "", ""
	case err != nil:
		return "", fmt.Sprintf("the project's record of the design repository could not be read, so approving removes nothing: %v", err)
	case record.Removed():
		return "", ""
	}
	name := cmp.Or(record.Name, record.ID)
	if i.kept == nil {
		return "", fmt.Sprintf("the project holds repository %s under the design repository's first commit %s, and this control plane keeps no clones that show it is the clone the adoption imported, "+
			"so approving removes nothing; remove it yourself with `ropeshift repository remove` if it is", name, ledger.ShortCommit(record.ID))
	}
	held, err := i.kept.Of(ctx, scope)
	if err != nil {
		return "", fmt.Sprintf("this worker's checkouts could not be read, so approving removes nothing: %v", err)
	}
	at, keeps := held[record.ID]
	if keeps && ports.SamePath(ctx, i.kept, at, choice.DesignPath) {
		return record.ID, ""
	}
	remote := cloneRemote(ctx, i.kept, choice.DesignPath)
	if record.Remote != "" && remote != "" && !ledger.RemotesDiffer(record.Remote, remote) {
		return record.ID, ""
	}
	where := "keeps no checkout of it"
	if keeps {
		where = "keeps its checkout at " + at
	}
	return "", fmt.Sprintf("the project holds repository %s under the design repository's first commit %s, and nothing shows it is the clone the adoption imported at %s: "+
		"this worker %s, and its remote %s is not the clone's remote %s, so the two may be repositories cut from one first commit. "+
		"Approving removes nothing; remove %s yourself with `ropeshift repository remove` if it is the design repository",
		name, ledger.ShortCommit(record.ID), choice.DesignPath, where, cmp.Or(record.Remote, "(none)"), cmp.Or(remote, "(none)"), name)
}

// cloneRemote is the remote of the clone at a path on this machine, or nothing when it has none or none is there.
func cloneRemote(ctx context.Context, kept ports.Checkouts, path string) string {
	clones, _, err := kept.Find(ctx, []string{path}, 1)
	if err != nil {
		return ""
	}
	for _, clone := range clones {
		if ports.SamePath(ctx, kept, clone.Path, path) {
			return clone.Remote
		}
	}
	return ""
}

// consolidationEffects is what approving a consolidating adoption does beyond the plan, as its request lists it:
// the old design repository, when the project holds it and the clones confirmed it, leaves the project, and at
// person_merges the person is told that only a merge commit keeps the design history.
func consolidationEffects(tx ports.WriteTx, order workorders.WorkOrder, read consolidationReading) (*planning.Consolidation, error) {
	if read.view == nil {
		return nil, nil
	}
	out := *read.view
	out.Collisions, out.LeftOut = slices.Clone(read.view.Collisions), slices.Clone(read.view.LeftOut)
	if read.design != "" {
		design, err := tx.Repositories().Repository(read.design)
		switch {
		case err == nil && !design.Removed() && design.ID != order.RepositoryID:
			out.Removes = &planning.RemovedRepository{ID: design.ID, Name: design.Name}
		case err != nil && !errors.Is(err, ports.ErrNotFound):
			return nil, err
		}
	}
	planned := ledger.Repository{ID: order.RepositoryID}
	switch record, err := tx.Repositories().Repository(order.RepositoryID); {
	case err == nil:
		planned = record
	case !errors.Is(err, ports.ErrNotFound):
		return nil, err
	}
	out.PersonMerges = planned.Level() == ledger.PersonMerges
	return &out, nil
}

// removeConsolidatedDesign is the effect a consolidation's approval lists of the old design repository: in the
// approval's transaction, the repository leaves the project and its record stays as history (ADR-0021), as a
// member's removal leaves it. A repository the request listed that is gone or removed since needs nothing, and one
// that open work still targets refuses the approval, with that work named.
func removeConsolidatedDesign(tx ports.WriteTx, approval authority.Approval, member ledger.Actor) error {
	adopted := planDetailOf(approval).Adoption
	if adopted == nil || adopted.Consolidation == nil || adopted.Consolidation.Removes == nil {
		return nil
	}
	removes := adopted.Consolidation.Removes
	if !authority.Allowed(member, authority.ActionRegisterRepository) {
		return fmt.Errorf("%w: approving this plan removes repository %s from the project, as its request lists, and only a member who may remove repositories can approve it",
			workorders.ErrForbidden, cmp.Or(removes.Name, removes.ID))
	}
	record, err := tx.Repositories().Repository(removes.ID)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil
	case err != nil:
		return err
	case record.Removed():
		return nil
	}
	if _, err := removeRepository(tx, removes.ID, member); err != nil {
		return fmt.Errorf("approving removes repository %s, which the consolidation imported: %w", cmp.Or(record.Name, removes.ID), err)
	}
	if !record.Planning {
		return nil
	}
	return planInWorkspace(tx, approval.WorkOrder, member)
}

// planInWorkspace makes the repository a consolidating adoption planned in the project's planning repository, once
// the design repository that was the planning repository leaves the project, so the project does not lose the
// repository it plans in.
func planInWorkspace(tx ports.WriteTx, workOrder string, member ledger.Actor) error {
	order, err := tx.WorkOrders().Get(workOrder)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	workspace, err := tx.Repositories().Repository(order.RepositoryID)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && (workspace.Removed() || workspace.Planning)) {
		return nil
	}
	if err != nil {
		return err
	}
	planned := workspace
	planned.Planning = true
	if err := saveSettings(tx, workspace, planned, member); err != nil {
		return err
	}
	return takePlanning(tx, workspace.ID, member)
}
