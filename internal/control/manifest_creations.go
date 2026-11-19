package control

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Placing names the worker whose machine creates the repositories a plan approval names, which the record of each
// creation's start keeps. Nil names none.
func (r *Repositories) Placing(worker func(context.Context) (string, error)) *Repositories {
	r.worker = worker
	return r
}

// Creating says the creation of an approval's entry runs now in this process, which tells a creation that was
// interrupted from one in flight. A creation holds the mark from just after its start is recorded until its finish
// is.
func (r *Repositories) Creating(approval, name string) bool {
	r.creatingMu.Lock()
	defer r.creatingMu.Unlock()
	return r.creating[ledger.CreationKey(approval, name)]
}

// markCreating marks a creation's key as running now.
func (r *Repositories) markCreating(key string) {
	r.creatingMu.Lock()
	defer r.creatingMu.Unlock()
	if r.creating == nil {
		r.creating = map[string]bool{}
	}
	r.creating[key] = true
}

// clearCreating takes a creation's key out of those running.
func (r *Repositories) clearCreating(key string) {
	r.creatingMu.Lock()
	defer r.creatingMu.Unlock()
	delete(r.creating, key)
}
