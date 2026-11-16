package sqlite

import (
	"context"
	"io/fs"

	"ropeshift.com/ropeshift/internal/ledger"
)

// JournalFiles and TelemetryFiles are the schema files this build ships for the journal and the
// telemetry store.
var JournalFiles, TelemetryFiles fs.FS = journalFiles, telemetryFiles

// MigrateFrom migrates the journal as a build that ships files would.
func (j *Journal) MigrateFrom(ctx context.Context, files fs.FS) error {
	m := journalMigrations
	m.files = files
	return m.migrate(ctx, j.db)
}

// MigrateFrom migrates the telemetry store as a build that ships files would.
func (s *TelemetryStore) MigrateFrom(ctx context.Context, files fs.FS) error {
	m := telemetryMigrations
	m.files = files
	return m.migrate(ctx, s.db)
}

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
