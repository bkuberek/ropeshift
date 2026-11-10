package contract

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
)

// The dependencies table is made again with the kind in its key. The rows a ledger holds stay as they
// were, and two dependencies of different kinds can then name the same target.
func TestDependencyKindsMigrationKeepsRowsAndWidensTheKey(t *testing.T) {
	t.Parallel()
	db, err := sqlite.OpenDB(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`CREATE TABLE dependencies (
  workspace_id  TEXT NOT NULL,
  work_order_id TEXT NOT NULL,
  depends_on    TEXT NOT NULL,
  adapter       TEXT,
  instance      TEXT,
  kind          TEXT NOT NULL,
  PRIMARY KEY (workspace_id, work_order_id, depends_on)
)`,
		`INSERT INTO dependencies VALUES ('w', 'order-1', 'first', 'repo', 'one', 'blocking')`,
		`INSERT INTO dependencies VALUES ('w', 'order-2', '01ULID', NULL, NULL, 'blocking')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapters", "sqlite", "migrations", "0033_dependency_kinds.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.QueryRow(`SELECT count(*) FROM dependencies WHERE kind = 'blocking'`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("%d rows kept: %v", rows, err)
	}
	var adapter, instance *string
	if err := db.QueryRow(`SELECT adapter, instance FROM dependencies WHERE work_order_id = 'order-2'`).Scan(&adapter, &instance); err != nil || adapter != nil || instance != nil {
		t.Fatalf("the NULL source of a ledger-made dependency became %v, %v: %v", adapter, instance, err)
	}
	if _, err := db.Exec(`INSERT INTO dependencies VALUES ('w', 'order-1', 'first', 'repo', 'one', 'milestone')`); err != nil {
		t.Fatalf("a derived dependency on the same target as a written one: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO dependencies VALUES ('w', 'order-1', 'first', 'repo', 'one', 'milestone')`); err == nil {
		t.Fatal("the same dependency of the same kind went in twice")
	}
}
