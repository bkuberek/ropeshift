package sqlstore

import (
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// sharedSpend is the installation's tallies of spend in one transaction: what each machine's orchestrator was charged, by
// UTC day. It runs the statements of a project's tally on the same two tables, under the installation's workspace, so a
// store with no installation keeps them beside its own.
type sharedSpend struct {
	*session
	scope ledger.WorkspaceID
	// now is database time for a write, and zero in a read, which never adds to a day.
	now int64
}

func (t sharedSpend) NoteSpend(scope, owner string, generation int64, usage ledger.GenerationUsage) error {
	return noteSpend(t.session, t.scope, func() (int64, error) { return t.now, nil }, scope, owner, generation, usage)
}

func (t sharedSpend) SpendSince(scope string, from int64) (float64, error) {
	return spendSince(t.session, t.scope, scope, from)
}

func (t sharedSpend) SpendOn(scope string, day int64) (float64, error) {
	row, err := t.row(stmtSpendOn, params{"workspace_id": string(t.scope), "scope": scope, "day": day})
	if err != nil {
		return 0, err
	}
	var total float64
	return total, row.Scan(&total)
}

func (t installationReadTx) Spend() ports.SpendReader {
	return sharedSpend{session: t.session, scope: ledger.InstallationScope}
}

func (t *installationTx) Spend() ports.SpendWriter {
	return sharedSpend{session: t.session, scope: ledger.InstallationScope, now: t.now}
}

func (r ownSharedRead) Spend() ports.SpendReader {
	return sharedSpend{session: r.t.session, scope: r.t.scope}
}

func (w ownSharedWrite) Spend() ports.SpendWriter {
	return sharedSpend{session: w.t.session, scope: w.t.scope, now: w.t.now}
}
