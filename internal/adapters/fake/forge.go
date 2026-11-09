package fake

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

type forgeKey struct {
	scope   ledger.WorkspaceID
	request ports.ForgeRequest
}

// Forge is a Forge held in memory. A test sets what ran on a commit, and Fail stands for a
// forge that cannot be reached.
type Forge struct {
	mu     sync.Mutex
	checks map[forgeKey][]ports.ForgeCheck
	// Fail is what every read returns while it is set.
	Fail error
}

var _ ports.Forge = (*Forge)(nil)

// Set replaces what ran on a commit.
func (f *Forge) Set(scope ledger.WorkspaceID, request ports.ForgeRequest, checks ...ports.ForgeCheck) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checks == nil {
		f.checks = map[forgeKey][]ports.ForgeCheck{}
	}
	f.checks[forgeKey{scope, request}] = slices.Clone(checks)
}

// Checks lists what ran on a commit, by name.
func (f *Forge) Checks(_ context.Context, scope ledger.WorkspaceID, request ports.ForgeRequest) ([]ports.ForgeCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return nil, f.Fail
	}
	out := slices.Clone(f.checks[forgeKey{scope, request}])
	slices.SortFunc(out, func(a, b ports.ForgeCheck) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}
