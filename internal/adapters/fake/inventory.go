package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// Inventory answers with what a test put for each repository, by its identity, and records what it was
// asked. A repository it holds nothing for is ErrNotFound.
type Inventory struct {
	mu    sync.Mutex
	held  map[string]planning.RepositoryInventory
	asked []ports.InventoryRequest
}

// NewInventory returns an inventory that holds nothing.
func NewInventory() *Inventory {
	return &Inventory{held: map[string]planning.RepositoryInventory{}}
}

// Put sets what an inventory of a repository finds.
func (i *Inventory) Put(repository string, found planning.RepositoryInventory) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.held[repository] = found
}

// Take answers with what was put for the repository, under the name and base the request gives.
func (i *Inventory) Take(_ context.Context, request ports.InventoryRequest) (planning.RepositoryInventory, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.asked = append(i.asked, request)
	found, ok := i.held[request.Repository]
	if !ok {
		return planning.RepositoryInventory{}, fmt.Errorf("repository %s: %w", request.Repository, ports.ErrNotFound)
	}
	found.Name, found.Repository = request.Name, request.Repository
	found.BaseBranch, found.BaseCommit = request.BaseBranch, request.BaseCommit
	return found, nil
}

// Asked lists every request, in order.
func (i *Inventory) Asked() []ports.InventoryRequest {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.asked)
}

var _ ports.Inventory = (*Inventory)(nil)
