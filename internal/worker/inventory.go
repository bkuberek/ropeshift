package worker

import (
	"context"
	"errors"
	"fmt"
	"path"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// errNoInventory says a lease asked for an inventory this worker cannot take.
var errNoInventory = errors.New("this worker takes no inventory")

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
	requests := []ports.InventoryRequest{{
		Path: repository, Repository: r.lease.Repository, Name: r.lease.RepositoryName, BaseBranch: r.lease.BaseRef, BaseCommit: r.lease.BaseCommit,
	}}
	var inventory planning.Inventory
	for _, read := range r.lease.Reads {
		unread := planning.RepositoryInventory{Name: read.Name, Repository: read.Repository, BaseBranch: read.Ref, Detail: read.Detail}
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
				Detail: fmt.Sprintf("it could not be read: %v", err),
			}
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
