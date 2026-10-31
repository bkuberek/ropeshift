package contract

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
)

func TestStoreContract(t *testing.T) {
	for _, backend := range []Backend{SQLiteFile, SQLiteMemory} {
		t.Run(backend.Name, func(t *testing.T) {
			t.Parallel()
			Run(t, backend, AllChecks())
		})
	}
}

// TestFakeStoreIsTheAdapter runs a small path through the exported fake, which
// is what every core test will hold.
func TestFakeStoreIsTheAdapter(t *testing.T) {
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

func checkSchemaShape(t *testing.T, db *sql.DB) {
	t.Helper()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
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
