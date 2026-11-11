package ports

import (
	"context"

	"ropeshift.com/ropeshift/internal/planning"
)

// InventoryRequest names one repository to inventory: the checkout to read it from, what the ledger calls
// it, and the base branch and the commit to read it at.
type InventoryRequest struct {
	Path       string
	Repository string
	Name       string
	BaseBranch string
	BaseCommit string
}

// Inventory reads what a repository holds for an adopt lane before it starts (ADR-0023): what the repository
// is, its Markdown documents and the files already in the repository file format at the base commit, and
// every branch the base does not hold. No model takes part and nothing it does changes the repository, so
// the same commits and the same refs give the same answer. A base commit the repository does not hold is
// ErrNotFound.
type Inventory interface {
	Take(ctx context.Context, request InventoryRequest) (planning.RepositoryInventory, error)
}
