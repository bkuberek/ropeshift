package worker

import (
	"context"
	"errors"
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// errNoInventory says a lease asked for an inventory this worker cannot take.
var errNoInventory = errors.New("this worker takes no inventory")

// unreadReason is why the open pull requests of a repository the inventory could not read are not listed.
const unreadReason = "the repository could not be read"

func omitPullRequests(reason string) []planning.InventoryOmission {
	return []planning.InventoryOmission{{Part: planning.PartPullRequests, Reason: reason}}
}

// listPullRequests lists the open pull requests of a repository whose remote names a forge repository, when this
// worker's sign-in for that forge is healthy, and otherwise records them as omitted with the reason: no
// binding, no forge on this worker, a sign-in that cannot run, or a listing the forge refused.
func (rt *LaneRuntime) listPullRequests(ctx context.Context, checkout string, found *planning.RepositoryInventory) {
	binding, err := ledger.ParseForgeBinding(ledger.BindingFromRemote(found.Remote))
	if err != nil {
		found.Omitted = omitPullRequests("no forge binding: the repository's remote names no forge repository")
		return
	}
	if rt.Forge == nil {
		found.Omitted = omitPullRequests("this worker has no forge adapter")
		return
	}
	repo := ports.ForgeRepo{Dir: checkout, Binding: binding}
	health, err := rt.Forge.Health(ctx, repo)
	switch {
	case err != nil:
		found.Omitted = omitPullRequests(fmt.Sprintf("the forge's health could not be read: %v", err))
		return
	case health.State != ledger.ForgeHealthy:
		found.Omitted = omitPullRequests(fmt.Sprintf("the forge is %s on this worker: %s", health.State, health.Detail))
		return
	}
	pulls, more, err := rt.Forge.OpenPullRequests(ctx, repo)
	if err != nil {
		found.Omitted = omitPullRequests(fmt.Sprintf("the open pull requests could not be listed: %v", err))
		return
	}
	found.PullRequests, found.MorePullRequests = pulls, more
}

// writeInventory writes the inventory of the lane's repository, at the commit its branch began from, and of
// each repository it reads, at the commit its claim recorded, into the lane's context folder before the lane
// starts (ADR-0023). No model takes part, so the same commits give the same file. A repository that cannot be
// read is in the inventory with the reason, and a lease that asks for none takes none.
func (r *laneRun) writeInventory(ctx context.Context, repository, worktree string) error {
	if !r.lease.Inventory {
		return nil
	}
	trees, offered := r.rt.Provisioner.Worktrees.(ports.ReviewTrees)
	if r.rt.Inventory == nil || !offered {
		return errNoInventory
	}
	// A consolidation's merge commit holds the design repository's documents, so the repository the lane plans in
	// is read at it, and the session onboards what was imported (FR-ADOPT-13). Any other lane reads the commit its
	// branch began from.
	base := r.lease.BaseCommit
	if done := r.restructuredOf(); done != nil && done.Consolidated != nil {
		base = done.Commit
	}
	requests := []ports.InventoryRequest{{
		Path: repository, Repository: r.lease.Repository, Name: r.lease.RepositoryName, BaseBranch: r.lease.BaseRef, BaseCommit: base,
	}}
	var inventory planning.Inventory
	for _, read := range r.lease.Reads {
		unread := planning.RepositoryInventory{Name: read.Name, Repository: read.Repository, BaseBranch: read.Ref, Detail: read.Detail}
		unread.Omitted = omitPullRequests(unreadReason)
		if read.Commit == "" {
			inventory.Repositories = append(inventory.Repositories, unread)
			continue
		}
		checkout, err := r.rt.Repository(ctx, r.lease.Project, read.Repository)
		if err != nil {
			unread.Detail = fmt.Sprintf("this machine keeps no checkout of it: %v", err)
			inventory.Repositories = append(inventory.Repositories, unread)
			continue
		}
		requests = append(requests, ports.InventoryRequest{
			Path: checkout, Repository: read.Repository, Name: read.Name, BaseBranch: read.Ref, BaseCommit: read.Commit,
		})
	}
	for _, request := range requests {
		found, err := r.rt.Inventory.Take(ctx, request)
		if err != nil {
			found = planning.RepositoryInventory{
				Name: request.Name, Repository: request.Repository, BaseBranch: request.BaseBranch, BaseCommit: request.BaseCommit,
				Detail: fmt.Sprintf("it could not be read: %v", err), Omitted: omitPullRequests(unreadReason),
			}
		} else {
			r.rt.listPullRequests(ctx, request.Path, &found)
		}
		inventory.Repositories = append(inventory.Repositories, found)
	}
	raw, err := planning.RenderInventory(inventory)
	if err != nil {
		return err
	}
	_, err = trees.WriteContextFile(ctx, worktree, path.Base(lanes.InventoryFile), string(raw))
	return err
}
