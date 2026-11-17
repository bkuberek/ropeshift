package fake

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestMigratedStoresAreIsolated writes a workspace to one copy of the migrated ledger and finds it in no
// other copy: neither one made before the write nor one made after it.
func TestMigratedStoresAreIsolated(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	actor := ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}
	first, before := migratedStore(t), migratedStore(t)
	if created, err := first.CreateWorkspace(ctx, ledger.Workspace{ID: "w", Name: "w"}, actor); err != nil || !created {
		t.Fatalf("the first copy created its workspace: %v, %v", created, err)
	}
	if created, err := first.CreateWorkspace(ctx, ledger.Workspace{ID: "w", Name: "w"}, actor); err != nil || created {
		t.Fatalf("the first copy did not find its own workspace: created %v, %v", created, err)
	}
	for name, other := range map[string]*Store{"made before the write": before, "made after the write": migratedStore(t)} {
		if created, err := other.CreateWorkspace(ctx, ledger.Workspace{ID: "w", Name: "w"}, actor); err != nil || !created {
			t.Errorf("a copy %s found the first copy's workspace: created %v, %v", name, created, err)
		}
	}
}

// TestAMigratedStoreIsAtTheLatestMigration reads the migrations a copy records: every one this build
// ships, by version, name, and checksum, up to the latest. Its schema is the one migrating an empty
// database gives, and Migrate finds every migration applied and applies none again.
func TestAMigratedStoreIsAtTheLatestMigration(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	shipped, err := sqlite.Dialect{}.Migrations()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, m := range shipped {
		want = append(want, fmt.Sprintf("%d %s %s", m.Version, m.Name, sqlstore.Checksum(m.SQL)))
	}
	db, err := openMigratedDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	recorded := query(t, db, `SELECT version || ' ' || name || ' ' || checksum FROM schema_migrations ORDER BY version`)
	if !slices.Equal(recorded, want) {
		t.Fatalf("a copy records the migrations\n%v\nand the build ships\n%v", recorded, want)
	}
	latest := query(t, db, `SELECT MAX(version) FROM schema_migrations`)
	if wantLatest := fmt.Sprint(shipped[len(shipped)-1].Version); !slices.Equal(latest, []string{wantLatest}) {
		t.Errorf("a copy is at version %v, and the latest migration is %s", latest, wantLatest)
	}

	fresh, err := sqlite.OpenMemoryDB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	if err := sqlstore.New(fresh, sqlite.Dialect{}, sqlstore.Options{}).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	const schema = `SELECT type || ' ' || name || ' ' || tbl_name || ' ' || COALESCE(sql, '') FROM sqlite_schema ORDER BY type, name`
	if got, want := query(t, db, schema), query(t, fresh, schema); !slices.Equal(got, want) {
		t.Errorf("a copy's schema\n%v\ndiffers from a migrated empty database's\n%v", got, want)
	}

	const applied = `SELECT version || ' ' || applied_at FROM schema_migrations ORDER BY version`
	before := query(t, db, applied)
	if err := sqlstore.New(db, sqlite.Dialect{}, sqlstore.Options{}).Migrate(ctx); err != nil {
		t.Fatalf("migrating a copy: %v", err)
	}
	if after := query(t, db, applied); !slices.Equal(after, before) {
		t.Errorf("migrating a copy applied migrations again:\n%v\nwas\n%v", after, before)
	}
}

// migratedStore is a new copy of the migrated ledger, which the test closes.
func migratedStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewMigratedStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// query reads the one text column of every row a statement returns.
func query(t *testing.T, db *sql.DB, statement string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), statement)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
