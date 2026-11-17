package entry

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
)

// TestTheDaemonWarnsOfMigrationsALaterBuildApplied holds that the daemon starts on a database a later
// build migrated, and prints the warning that names the migrations this build does not ship.
func TestTheDaemonWarnsOfMigrationsALaterBuildApplied(t *testing.T) {
	t.Run("installation", func(t *testing.T) {
		dir := t.TempDir()
		state, store, err := openLedger(t.Context(), dir, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		_ = store.Close()
		_ = state.Close()
		recordLaterMigration(t, filepath.Join(dir, sqlite.InstallationFileName),
			`INSERT INTO schema_migrations (version, name, applied_at, checksum) VALUES (9001, '9001_later', 0, 'later')`)
		var stderr bytes.Buffer
		state, store, err = openLedger(t.Context(), dir, &stderr)
		if err != nil {
			t.Fatalf("the daemon returned %v, want it to start", err)
		}
		_ = store.Close()
		_ = state.Close()
		wantLaterWarning(t, stderr.String(), "installation")
	})
	t.Run("telemetry", func(t *testing.T) {
		r := newTelemetryRig(t)
		deps := telemetryDeps{Lookup: func(string) (string, bool) { return "", false }, Now: time.Now}
		telemetry, err := startTelemetry(t.Context(), r.paths, api.WebOff, deps, &r.stdout, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		telemetry.close()
		recordLaterMigration(t, filepath.Join(r.paths.StateDir, telemetryFileName),
			`INSERT INTO telemetry_migrations (name, checksum) VALUES ('telemetry_migrations/9001_later.sql', 'later')`)
		telemetry, err = startTelemetry(t.Context(), r.paths, api.WebOff, deps, &r.stdout, &r.stderr)
		if err != nil {
			t.Fatalf("the daemon returned %v, want it to start", err)
		}
		telemetry.close()
		wantLaterWarning(t, r.stderr.String(), "telemetry")
	})
	t.Run("journal", func(t *testing.T) {
		dir := t.TempDir()
		journal, err := openJournal(t.Context(), dir, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		_ = journal.Close()
		recordLaterMigration(t, filepath.Join(dir, api.JournalFileName),
			`INSERT INTO journal_migrations (name, checksum) VALUES ('journal_migrations/9001_later.sql', 'later')`)
		var stderr bytes.Buffer
		journal, err = openJournal(t.Context(), dir, &stderr)
		if err != nil {
			t.Fatalf("the daemon returned %v, want it to start", err)
		}
		_ = journal.Close()
		wantLaterWarning(t, stderr.String(), "journal")
	})
}

// recordLaterMigration records a migration in a database file as a later build would have.
func recordLaterMigration(t *testing.T, file, statement string) {
	t.Helper()
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(statement)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
}

// wantLaterWarning holds that the daemon printed one warning, naming the set and migration 9001.
func wantLaterWarning(t *testing.T, stderr, set string) {
	t.Helper()
	if strings.Count(stderr, "ropeshiftd: warning: ") != 1 || !strings.Contains(stderr, set) || !strings.Contains(stderr, "9001 (9001_later.sql)") {
		t.Fatalf("the daemon printed %q, want one warning that names %s migration 9001", stderr, set)
	}
}
