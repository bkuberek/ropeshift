package postgres_test

import (
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
)

func TestPlaceholdersAreNumbered(t *testing.T) {
	if got := (postgres.Dialect{}).Placeholder(3); got != "$3" {
		t.Errorf("placeholder 3 is %q", got)
	}
}

// TestDatabaseTimeIsReadAtTheInstant holds the design's rule for leases: the SQL-standard functions
// return the start of the transaction, which is stale after a wait for the workspace lock.
func TestDatabaseTimeIsReadAtTheInstant(t *testing.T) {
	clock := (postgres.Dialect{}).ClockSQL()
	if !strings.Contains(clock, "clock_timestamp()") || strings.Contains(clock, "now()") {
		t.Errorf("clock SQL is %q", clock)
	}
}

func TestASchemaNameIsQuoted(t *testing.T) {
	got := (postgres.Dialect{}).SchemaSQL(`pro"ject`)
	if want := `SET LOCAL search_path TO "pro""ject"`; got != want {
		t.Errorf("schema SQL is %q, want %q", got, want)
	}
}

func TestSearchTermsJoinWithOr(t *testing.T) {
	if got := (postgres.Dialect{}).MatchQuery([]string{"retry", "budget"}); got != "'retry' | 'budget'" {
		t.Errorf("match query is %q", got)
	}
	if sql := (postgres.Dialect{}).MatchSQL(); !strings.Contains(sql, "ts_rank") || !strings.Contains(sql, "@@") {
		t.Errorf("match SQL does not rank with tsvector: %s", sql)
	}
}

// TestTheLedgerSchemaIsTheNeutralOnePlusItsOwn holds that the dialect runs every neutral migration, adds
// its search index at the version SQLite's counterpart holds, and shares no version with the neutral set.
func TestTheLedgerSchemaIsTheNeutralOnePlusItsOwn(t *testing.T) {
	migrations, err := (postgres.Dialect{}).Migrations()
	if err != nil {
		t.Fatal(err)
	}
	neutral, err := sqlite.NeutralMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != len(neutral)+1 {
		t.Fatalf("%d migrations, want the %d neutral ones and one search index", len(migrations), len(neutral))
	}
	versions := make([]int64, len(migrations))
	for i, m := range migrations {
		versions[i] = m.Version
	}
	if !slices.IsSorted(versions) || len(slices.Compact(slices.Clone(versions))) != len(versions) {
		t.Errorf("versions are not in order and unique: %v", versions)
	}
	for _, m := range migrations {
		if m.Version != 13 {
			continue
		}
		if m.Name != "0013_search_index" || !strings.Contains(m.SQL, "USING gin") {
			t.Errorf("version 13 is %s: %s", m.Name, m.SQL)
		}
		return
	}
	t.Error("no search index migration at version 13")
}

func TestTheInstallationSchemaIsTheNeutralOne(t *testing.T) {
	got, err := (postgres.Dialect{}).InstallationMigrations()
	if err != nil {
		t.Fatal(err)
	}
	want, err := sqlite.NeutralInstallationMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the installation runs %d migrations, want the %d neutral ones", len(got), len(want))
	}
}
