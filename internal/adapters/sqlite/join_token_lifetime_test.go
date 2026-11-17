package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
)

// before is a dialect whose installation database stops short of a migration, as the build that wrote an older
// installation did.
type before struct {
	sqlite.Dialect
	version int64
}

func (d before) InstallationMigrations() ([]sqlstore.Migration, error) {
	all, err := d.Dialect.InstallationMigrations()
	if err != nil {
		return nil, err
	}
	var kept []sqlstore.Migration
	for _, migration := range all {
		if migration.Version < d.version {
			kept = append(kept, migration)
		}
	}
	return kept, nil
}

// A join grant an older installation holds has no lifetime to end and no revocation, which is what a column of zero
// says, and the migration that adds both keeps the grant as it was, used or not.
func TestAJoinTokenFromBeforeLifetimesNeverExpiresAndStaysAsItWas(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), sqlite.InstallationFileName)
	open := func(dialect sqlstore.Dialect) (*sqlstore.Installation, *sql.DB) {
		t.Helper()
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

	old, oldDB := open(before{Dialect: sqlite.Dialect{}, version: 70})
	if _, err := oldDB.ExecContext(t.Context(), `SELECT expires_at FROM join_grants`); err == nil {
		t.Fatal("the older schema already holds a join token's expiry")
	}
	for _, row := range []string{
		`INSERT INTO join_grants (workspace_id, id, digest, created_by) VALUES ('w', 'unused', 'digest-1', 'member-1')`,
		`INSERT INTO join_grants (workspace_id, id, digest, created_by, used_by) VALUES ('w', 'used', 'digest-2', 'member-1', 'worker-1')`,
	} {
		if _, err := oldDB.ExecContext(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	current, db := open(sqlite.Dialect{})
	defer current.Close()
	for id, used := range map[string]string{"unused": "", "used": "worker-1"} {
		var expires, revoked int64
		var usedBy sql.NullString
		err := db.QueryRowContext(t.Context(), `SELECT expires_at, revoked_at, used_by FROM join_grants WHERE id = ?`, id).Scan(&expires, &revoked, &usedBy)
		if err != nil {
			t.Fatal(err)
		}
		if expires != 0 || revoked != 0 || usedBy.String != used {
			t.Errorf("the %s grant holds expires_at %d, revoked_at %d, used_by %q", id, expires, revoked, usedBy.String)
		}
	}
	// Migrating again changes nothing, and the new columns are in the checksummed list of what was applied.
	if err := current.Migrate(t.Context()); err != nil {
		t.Fatalf("a second migration: %v", err)
	}
	var applied int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM schema_migrations WHERE version = 70 AND checksum IS NOT NULL AND checksum <> ''`).Scan(&applied); err != nil || applied != 1 {
		t.Errorf("the migration 70 is recorded with a checksum %d times: %v", applied, err)
	}
}
