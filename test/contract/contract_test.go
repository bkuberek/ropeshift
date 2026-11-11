package contract

import (
	"database/sql"
	"regexp"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func TestStoreContract(t *testing.T) {
	t.Parallel()
	for _, backend := range []Backend{SQLiteFile, SQLiteMemory, SQLiteProject} {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			Run(t, backend, AllChecks())
		})
	}
}

// TestFakeStoreIsTheAdapter runs a small path through the exported fake, which
// is what every core test will hold.
func TestFakeStoreIsTheAdapter(t *testing.T) {
	t.Parallel()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var s ports.Store = store
	ctx := t.Context()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}
	created, err := s.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, actor)
	if err != nil || !created {
		t.Fatalf("created %v: %v", created, err)
	}
}

// TestSchemaShape reads the migrated schema: every record table is scoped to a
// workspace (FR-LEDGER-2), and no column can hold a secret value (NFR-SEC-4).
func TestSchemaShape(t *testing.T) {
	t.Parallel()
	file := t.TempDir() + "/ledger.db"
	store, err := sqlite.Open(file, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	checkSchemaShape(t, db)
}

// secretColumn matches a column name that could hold a credential. A name ending
// in _ref holds a reference, which is what NFR-SEC-4 asks for.
var secretColumn = regexp.MustCompile(`(?i)(secret|password|passphrase|token|credential|api_?key)`)

// searchIndexTables are FTS5's own, which index search_documents and hold no record.
const searchIndexTables = "search_fts%"

func checkSchemaShape(t *testing.T, db *sql.DB) {
	t.Helper()
	tables, err := db.Query(`SELECT name FROM sqlite_master
 WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE ?1`, searchIndexTables)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	if len(names) < 3 {
		t.Fatalf("found tables %v", names)
	}
	for _, table := range names {
		columns, err := db.Query(`SELECT name, pk FROM pragma_table_info(?1)`, table)
		if err != nil {
			t.Fatal(err)
		}
		scoped := false
		for columns.Next() {
			var name string
			var pk int
			if err := columns.Scan(&name, &pk); err != nil {
				t.Fatal(err)
			}
			scoped = scoped || (name == "workspace_id" && pk > 0)
			if secretColumn.MatchString(name) && !strings.HasSuffix(name, "_ref") {
				t.Errorf("%s.%s looks like a secret value", table, name)
			}
		}
		if err := columns.Close(); err != nil {
			t.Fatal(err)
		}
		if !scoped && table != "workspaces" && table != "schema_migrations" {
			t.Errorf("table %s has no workspace_id in its primary key", table)
		}
	}
}

// installationOnlyTables are the installation database's own: the registry, its log, and the
// repository index (ADR-0021). Every other table there is one a ledger has too.
var installationOnlyTables = []string{"installation", "projects", "installation_events", "repository_index", "schema_migrations"}

// TestInstallationSchemaShape reads the migrated installation database: no column can hold a secret
// value (NFR-SEC-4), and each table it shares with a ledger keeps the ledger's columns and its
// workspace scope, so the Store's statements serve both.
func TestInstallationSchemaShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installation, err := sqlite.OpenInstallation(dir+"/"+sqlite.InstallationFileName, sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := installation.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := installation.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(dir+"/ledger.db", sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	shared := tableColumns(t, dir+"/"+sqlite.InstallationFileName)
	ledgerTables := tableColumns(t, dir+"/ledger.db")
	if len(shared) <= len(installationOnlyTables) {
		t.Fatalf("the installation holds %v", shared)
	}
	for table, columns := range shared {
		for _, column := range columns {
			if secretColumn.MatchString(column) && !strings.HasSuffix(column, "_ref") {
				t.Errorf("%s.%s looks like a secret value", table, column)
			}
		}
		if slices.Contains(installationOnlyTables, table) {
			if _, found := ledgerTables[table]; found && table != "schema_migrations" {
				t.Errorf("the ledger holds the installation's own table %s", table)
			}
			continue
		}
		want, found := ledgerTables[table]
		if !found {
			t.Errorf("table %s is neither the installation's own nor a ledger's", table)
			continue
		}
		if !slices.Equal(columns, want) {
			t.Errorf("%s has columns %v in the installation and %v in a ledger", table, columns, want)
		}
		if !slices.Contains(columns, "workspace_id") {
			t.Errorf("shared table %s has no workspace_id", table)
		}
	}
}

// tableColumns reads each table of a SQLite file and its columns, sorted.
func tableColumns(t *testing.T, file string) map[string][]string {
	t.Helper()
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT m.name, p.name FROM sqlite_master m, pragma_table_info(m.name) p
 WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND m.name NOT LIKE ?1`, searchIndexTables)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		out[table] = append(out[table], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, columns := range out {
		slices.Sort(columns)
	}
	return out
}
