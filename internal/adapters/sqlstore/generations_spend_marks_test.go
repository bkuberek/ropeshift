package sqlstore_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The migrations that give a spend mark the figures it keeps: 197 in a project's ledger and 86 in the installation.
const (
	markFiguresLedger       = 197
	markFiguresInstallation = 86
)

// olderLedger is a dialect whose project ledger stops short of a migration, as the build that wrote an older one did.
type olderLedger struct {
	sqlite.Dialect
	version int64
}

func (d olderLedger) Migrations() ([]sqlstore.Migration, error) {
	all, err := d.Dialect.Migrations()
	return keptBefore(all, d.version), err
}

func (d olderLedger) InstallationMigrations() ([]sqlstore.Migration, error) {
	all, err := d.Dialect.InstallationMigrations()
	return keptBefore(all, d.version), err
}

func keptBefore(all []sqlstore.Migration, version int64) []sqlstore.Migration {
	var kept []sqlstore.Migration
	for _, migration := range all {
		if migration.Version < version {
			kept = append(kept, migration)
		}
	}
	return kept
}

const (
	oldCharge = 12.0
	aScope    = "orchestrator:worker-a"
)

// noted is the event a write must leave.
var noted = ledger.EventDraft{Type: "test.noted", Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}, Subject: ledger.Subject{Kind: "test", ID: "a"}}

// markRead is what the mark of the seeded generation holds.
func markRead(t *testing.T, db *sql.DB, workspace string) (charged, floor float64) {
	t.Helper()
	err := db.QueryRowContext(t.Context(), `SELECT charged_usd, floor_usd FROM spend_marks WHERE workspace_id = ? AND scope = ? AND owner = 'lane-a' AND generation = 1`,
		workspace, aScope).Scan(&charged, &floor)
	if err != nil {
		t.Fatal(err)
	}
	return charged, floor
}

// A mark an older build wrote holds one charge, the highest its generation was told of. The migration that gives a mark the
// highest of each figure keeps that charge as the mark's floor, so no generation reads less after the upgrade, and a later
// report that charges less changes nothing.
func TestASpendMarkFromBeforeItsFiguresKeepsItsChargeAsTheFloor(t *testing.T) {
	later := []ledger.GenerationUsage{
		{CostUSD: 1, KnownTurns: 1},
		{CostUSD: 6, KnownTurns: 2, UnknownTurns: 1},
	}
	check := func(t *testing.T, db *sql.DB, workspace string, read func() float64, note func(ledger.GenerationUsage)) {
		t.Helper()
		if charged, floor := markRead(t, db, workspace); charged < oldCharge || floor < oldCharge {
			t.Fatalf("after the migration the mark holds a charge of %v and a floor of %v, want at least %v", charged, floor, oldCharge)
		}
		for _, usage := range later {
			note(usage)
			if charged, floor := markRead(t, db, workspace); charged < oldCharge || floor < oldCharge {
				t.Errorf("after a report of %+v the mark holds a charge of %v and a floor of %v, want at least %v", usage, charged, floor, oldCharge)
			}
			if got := read(); got != oldCharge {
				t.Errorf("after a report of %+v the day reads %v, want %v", usage, got, oldCharge)
			}
		}
	}
	seed := func(t *testing.T, db *sql.DB, workspace string) {
		t.Helper()
		for _, row := range []string{
			`INSERT INTO spend_marks (workspace_id, scope, owner, generation, charged_usd) VALUES ('` + workspace + `', '` + aScope + `', 'lane-a', 1, 12)`,
			`INSERT INTO spend_days (workspace_id, scope, day, charged_usd) VALUES ('` + workspace + `', '` + aScope + `', 0, 12)`,
		} {
			if _, err := db.ExecContext(t.Context(), row); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("the installation", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), sqlite.InstallationFileName)
		open := func(dialect sqlstore.Dialect) (*sqlstore.Installation, *sql.DB) {
			db, err := sqlite.OpenDB(file)
			if err != nil {
				t.Fatal(err)
			}
			installation := sqlstore.NewInstallation(db, dialect, sqlstore.Options{})
			if err := installation.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			return installation, db
		}
		old, oldDB := open(olderLedger{Dialect: sqlite.Dialect{}, version: markFiguresInstallation})
		seed(t, oldDB, string(ledger.InstallationScope))
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		current, db := open(sqlite.Dialect{})
		defer current.Close()
		check(t, db, string(ledger.InstallationScope), func() float64 {
			var total float64
			if err := current.Read(t.Context(), func(tx ports.SharedReadTx) error {
				var err error
				total, err = tx.Spend().SpendSince(aScope, 0)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			return total
		}, func(usage ledger.GenerationUsage) {
			if err := current.Write(t.Context(), "test.note", func(tx ports.SharedTx) error {
				if err := tx.Spend().NoteSpend(aScope, "lane-a", 1, usage); err != nil {
					return err
				}
				_, err := tx.Append(noted)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("a project's ledger", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "ledger.db")
		open := func(dialect sqlstore.Dialect) (*sqlstore.Store, *sql.DB) {
			db, err := sqlite.OpenDB(file)
			if err != nil {
				t.Fatal(err)
			}
			store := sqlstore.New(db, dialect, sqlstore.Options{})
			if err := store.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			return store, db
		}
		member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
		old, oldDB := open(olderLedger{Dialect: sqlite.Dialect{}, version: markFiguresLedger})
		if _, err := old.CreateWorkspace(t.Context(), ledger.Workspace{ID: "w"}, member); err != nil {
			t.Fatal(err)
		}
		seed(t, oldDB, "w")
		if err := old.Close(); err != nil {
			t.Fatal(err)
		}
		current, db := open(sqlite.Dialect{})
		defer current.Close()
		check(t, db, "w", func() float64 {
			var total float64
			if err := current.Read(t.Context(), "w", func(tx ports.ReadTx) error {
				var err error
				total, err = tx.Lanes().SpendSince(aScope, 0)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			return total
		}, func(usage ledger.GenerationUsage) {
			if err := current.Write(t.Context(), "w", "test.note", func(tx ports.WriteTx) error {
				if err := tx.Lanes().NoteSpend(aScope, "lane-a", 1, usage); err != nil {
					return err
				}
				_, err := tx.Append(noted)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	})
}
