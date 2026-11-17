package contract_test

import (
	"database/sql"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/test/contract"
)

// scopeMigration is the text of the migration that gives each stored decision scope its prefix.
func scopeMigration(t *testing.T, migrations []sqlstore.Migration) string {
	t.Helper()
	for _, m := range migrations {
		if m.Version == contract.ScopeMigrationVersion {
			return m.SQL
		}
	}
	t.Fatalf("no migration %04d", contract.ScopeMigrationVersion)
	return ""
}

// TestScopeMigrationSQLite runs the migration of ADR-0030's scope grammar on SQLite.
func TestScopeMigrationSQLite(t *testing.T) {
	migrations, err := sqlite.NeutralMigrations()
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := contract.ScopeRewriteCheck(t.Context(), db, scopeMigration(t, migrations)); err != nil {
		t.Fatal(err)
	}
}
