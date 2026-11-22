package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// CloneEdits reads what a person's clone on a worker's machine holds that intake and lanes cannot read (FR-UI-14). It
// only reads: it fetches nothing and changes no ref, index, or file.
type CloneEdits interface {
	// Edits lists the paths of the clone whose top is at clone that git reports as changed or untracked, and those
	// that commits of the checked-out branch changed which no branch of its remote holds, at most limit of them. Cut
	// says git listed more. A path that holds no clone is ErrNotFound.
	Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error)
	// Diverged reads what the lane's branch in the clone and the base branch it began from each changed since they
	// last shared a commit, at most limit paths of each side. The base is the remote's copy of baseBranch as the last
	// fetch left it, or the clone's own branch when it has no remote, and its side leaves out every merge and every
	// commit a merge of ropeshift made.
	// Commits the ledger records as landed work, and the commits of each range it records as landed, are no person's
	// either. A range whose ends the clone does not hold, or whose first end is no ancestor of its last, leaves out nothing
	// and counts as none. The divergence says Unranged when a commit Landed names as the merge commit of a pull request
	// with no range, or whose range is no usable one, lies on the base's side.
	// Only a ref git says is missing, or two that share no history, is ErrNotFound; any other failure is an error.
	Diverged(ctx context.Context, clone, branch, baseBranch string, landed ledger.Landed, limit int) (ledger.Divergence, error)
	// Contents reads the blob each path's file in the clone would be stored as, with no filter run and no object
	// written, and empty for a path whose file is gone. A symlink reads as the blob of its link text and what it
	// points at is never opened, and a submodule reads as the commit the index records for it.
	Contents(ctx context.Context, clone string, paths []string) (map[string]string, error)
	// Holds says the lane's branch holds the ancestor branch. An ancestor branch the clone does not hold is held by
	// no branch, and a lane's branch the clone does not hold is ErrNotFound.
	Holds(ctx context.Context, clone, branch, ancestor string) (bool, error)
}
