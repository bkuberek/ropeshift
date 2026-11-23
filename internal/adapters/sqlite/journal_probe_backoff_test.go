package sqlite_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
)

// A journal an earlier build kept holds the attempt it recorded, which has no backoff to keep: the columns the backoff
// needs arrive with zero, and the attempt reads as it did.
func TestAProbeAttemptKeptBeforeTheBackoffColumnsHasNoBackoff(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "journal.db")
	journal, err := sqlite.OpenJournal(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	earlier := map[string]string{}
	for name, text := range shippedFiles(t, fileSets[0]) {
		if !strings.Contains(name, "/0036_") {
			earlier[name] = text
		}
	}
	if len(earlier) != len(shippedFiles(t, fileSets[0]))-1 {
		t.Fatalf("a build before the backoff ships %d files, want one fewer than this build's", len(earlier))
	}
	if err := journal.MigrateFrom(t.Context(), mapFS(earlier)); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `INSERT INTO probe_attempt (id, version, began_ms) VALUES (1, '2.1.317', 1700000000123)`); err != nil {
		t.Fatal(err)
	}
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := journal.ProbeAttempt(t.Context())
	if err != nil || got.Version != "2.1.317" || !got.BeganAt.Equal(time.UnixMilli(1_700_000_000_123)) || got.Wait != 0 || !got.Next.IsZero() {
		t.Errorf("the attempt reads %+v, %v, want the version and time it was kept with and no backoff", got, err)
	}
}
