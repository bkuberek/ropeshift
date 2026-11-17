package contract

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// Interposed is a store whose writes to the installation's records a check can step into, with nothing compiled into
// an adapter. Before runs ahead of each write, told its operation, which lets another write land between a check and
// the write that relies on it. Within, when not nil, stands in for the transaction each write's work is given, which
// lets a check fail one step of that work. A check sets them before the calls it steps into, on the goroutine that
// makes those calls.
type Interposed struct {
	ports.Store
	Before func(op string)
	Within func(tx ports.SharedTx) ports.SharedTx
}

// Installation is the installation's records, whose writes run Before and Within.
func (s *Interposed) Installation(scope ledger.WorkspaceID) ports.Installation {
	return interposedInstallation{Installation: s.Store.Installation(scope), store: s}
}

type interposedInstallation struct {
	ports.Installation
	store *Interposed
}

func (i interposedInstallation) Write(ctx context.Context, op string, fn func(ports.SharedTx) error) error {
	if i.store.Before != nil {
		i.store.Before(op)
	}
	return i.Installation.Write(ctx, op, func(tx ports.SharedTx) error {
		if i.store.Within != nil {
			tx = i.store.Within(tx)
		}
		return fn(tx)
	})
}
