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

// consolidationRead reads, from the repository's own history, what an adoption that consolidated a design
// repository merged on its branch (FR-ADOPT-13): the merge commit that imported the history, its second parent,
// and every path both repositories held. The trees left out are the ones the member chose. What cannot be read is
// said, and the request is raised all the same. Work that consolidated nothing reads as nil.
func (i *Intake) consolidationRead(ctx context.Context, scope ledger.WorkspaceID, plan acceptedPlan) *planning.Consolidation {
	choice := plan.order.Restructure
	if !choice.Consolidates() {
		return nil
	}
	read := &planning.Consolidation{Design: choice.DesignName(), Collisions: []planning.Collision{}, LeftOut: append([]string{}, choice.LeaveOut...)}
	read.Unread = i.readImport(ctx, scope, plan, read)
	return read
}

// readImport fills in the merge, the design tip, and the collisions of a consolidation from the adoption branch,
// and says why it could not when it could not.
func (i *Intake) readImport(ctx context.Context, scope ledger.WorkspaceID, plan acceptedPlan, read *planning.Consolidation) string {
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
		return fmt.Sprintf("the adoption branch holds no merge commit that imports the history of %s", read.Design)
	}
	read.Design, _ = planning.ImportedDesign(merge.Subject)
	read.Merge, read.DesignTip = merge.Hash, merge.Parents[1]
	ours, err := imports.CommitPaths(ctx, path, merge.Parents[0])
	if err != nil {
		return fmt.Sprintf("git could not read what the workspace held at %s: %v", merge.Parents[0], err)
	}
	theirs, err := imports.CommitPaths(ctx, path, merge.Parents[1])
	if err != nil {
		return fmt.Sprintf("git could not read what %s held at %s: %v", read.Design, merge.Parents[1], err)
	}
	if _, collisions := adoption.Arrivals(ours, theirs, read.LeftOut); collisions != nil {
		read.Collisions = collisions
	}
	return ""
}

// importCommit is the newest commit of a branch that imports a design repository's history: a merge commit with
// the import's subject, which only the worker makes before an adopt lane starts.
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

// consolidationEffects is what approving a consolidating adoption does beyond the plan, as its request lists it:
// the old design repository, when the project holds it, leaves the project, and at person_merges the person is
// told that only a merge commit keeps the design history.
func consolidationEffects(tx ports.WriteTx, order workorders.WorkOrder, read *planning.Consolidation) (*planning.Consolidation, error) {
	if read == nil {
		return nil, nil
	}
	out := *read
	out.Collisions, out.LeftOut = slices.Clone(read.Collisions), slices.Clone(read.LeftOut)
	design, err := tx.Repositories().Repository(order.Restructure.DesignRoot)
	switch {
	case err == nil && !design.Removed() && design.ID != order.RepositoryID:
		out.Removes = &planning.RemovedRepository{ID: design.ID, Name: design.Name}
	case err != nil && !errors.Is(err, ports.ErrNotFound):
		return nil, err
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
	if !authority.Allowed(member, authority.ActionRegisterRepository) {
		return workorders.ErrForbidden
	}
	id := adopted.Consolidation.Removes.ID
	record, err := tx.Repositories().Repository(id)
	switch {
	case errors.Is(err, ports.ErrNotFound):
		return nil
	case err != nil:
		return err
	case record.Removed():
		return nil
	}
	if _, err := removeRepository(tx, id, member); err != nil {
		return fmt.Errorf("approving removes repository %s, which the consolidation imported: %w", cmp.Or(record.Name, id), err)
	}
	return nil
}
