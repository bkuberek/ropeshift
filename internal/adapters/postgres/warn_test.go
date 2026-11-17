//go:build postgres

package postgres_test

import (
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestAProjectsWarningNamesItsSchema holds that the warning for a project whose schema a later build
// migrated starts with the schema's name, so the warnings of several projects tell apart.
func TestAProjectsWarningNamesItsSchema(t *testing.T) {
	url := freshDatabase(t)
	first := openState(t, url, postgres.Options{})
	project, err := first.Create(t.Context(), ledger.Project{ID: "later", Name: "later"}, member)
	if err != nil {
		t.Fatal(err)
	}
	db := first.SQLDB()
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO `+project.Database+
		`.schema_migrations (version, name, applied_at, checksum) VALUES (9001, '9001_later', 0, 'recorded-by-a-later-build')`); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	var warnings []string
	second := openState(t, url, postgres.Options{Store: sqlstore.Options{Warn: func(warning string) {
		warnings = append(warnings, warning)
	}}})
	if _, err := second.Open(t.Context(), project); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0], project.Database+": ") || !strings.Contains(warnings[0], "9001 (9001_later.sql)") {
		t.Fatalf("the start warned %q, want one warning that starts with %q and names migration 9001", warnings, project.Database+": ")
	}
}
