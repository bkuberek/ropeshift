package sqlite

import (
	"context"

	"ropeshift.com/ropeshift/internal/ledger"
)

// UpgradeSteps names the steps of an upgrade, in order.
func UpgradeSteps() []string {
	names := make([]string, len(upgradeSteps))
	for i, step := range upgradeSteps {
		names[i] = step.name
	}
	return names
}

// UpgradeThrough runs the first n steps of an upgrade and stops there, as a crash after the nth would,
// closing what it opened.
func UpgradeThrough(ctx context.Context, dir, ledgerName string, first ledger.Workspace, actor ledger.Actor, n int) error {
	u := &upgrade{dir: dir, ledgerName: ledgerName, first: first, actor: actor}
	defer u.close()
	return u.through(ctx, n)
}
