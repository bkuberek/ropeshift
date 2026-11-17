package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/ports"
)

// A worker that never joined a server holds no server address, and says so as a record that is not there.
func TestAJournalOfAWorkerThatNeverJoinedHoldsNoServer(t *testing.T) {
	t.Parallel()
	journal, err := sqlite.OpenMemoryJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ServerURL(t.Context()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a new journal holds a server: %v", err)
	}
}

// The server a worker joined is in its journal, so a restart reaches the same one, and a second join replaces it.
func TestAJournalKeepsTheServerAWorkerJoined(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "journal.db")
	open := func() *sqlite.Journal {
		journal, err := sqlite.OpenJournal(file)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = journal.Close() })
		if err := journal.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		return journal
	}
	first := open()
	if err := first.SaveServerURL(t.Context(), "http://127.0.0.1:7400"); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := open()
	got, err := second.ServerURL(t.Context())
	if err != nil || got != "http://127.0.0.1:7400" {
		t.Fatalf("after a restart the journal holds %q, %v", got, err)
	}
	if err := second.SaveServerURL(t.Context(), "http://127.0.0.1:7401"); err != nil {
		t.Fatal(err)
	}
	if got, err = second.ServerURL(t.Context()); err != nil || got != "http://127.0.0.1:7401" {
		t.Errorf("after a second join the journal holds %q, %v", got, err)
	}
}

// The fingerprint of the certificate authority a worker pinned when it joined is in its journal with the address,
// so a restart pins the same one, and a second join replaces both. A join that pinned nothing holds the empty
// fingerprint, which says to verify through the system's roots.
func TestAJournalKeepsTheFingerprintAWorkerPinned(t *testing.T) {
	t.Parallel()
	const pin = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	file := filepath.Join(t.TempDir(), "journal.db")
	open := func() *sqlite.Journal {
		journal, err := sqlite.OpenJournal(file)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = journal.Close() })
		if err := journal.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
		return journal
	}
	first := open()
	if _, err := first.ServerCA(t.Context()); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a journal that joined no server holds a fingerprint: %v", err)
	}
	if err := first.SaveServerLink(t.Context(), "https://ropeshift.lan:7400", pin); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second := open()
	if got, err := second.ServerURL(t.Context()); err != nil || got != "https://ropeshift.lan:7400" {
		t.Fatalf("after a restart the journal holds the address %q, %v", got, err)
	}
	if got, err := second.ServerCA(t.Context()); err != nil || got != pin {
		t.Fatalf("after a restart the journal holds the fingerprint %q, %v", got, err)
	}
	if err := second.SaveServerLink(t.Context(), "https://ropeshift.example:443", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := second.ServerCA(t.Context()); err != nil || got != "" {
		t.Errorf("a join that pinned nothing leaves the fingerprint %q, %v", got, err)
	}
	// An address saved alone is no longer pinned to the authority of the one before it.
	if err := second.SaveServerLink(t.Context(), "https://ropeshift.lan:7400", pin); err != nil {
		t.Fatal(err)
	}
	if err := second.SaveServerURL(t.Context(), "http://127.0.0.1:7400"); err != nil {
		t.Fatal(err)
	}
	if got, err := second.ServerCA(t.Context()); err != nil || got != "" {
		t.Errorf("an address saved with no fingerprint leaves the fingerprint %q, %v", got, err)
	}
}

// A journal that held an address before the fingerprint had a place keeps the address, and holds no fingerprint,
// which is how a worker that joined over loopback reads.
func TestAJournalThatHeldAnAddressAloneHoldsNoFingerprint(t *testing.T) {
	t.Parallel()
	journal, err := sqlite.OpenMemoryJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := journal.SaveServerURL(t.Context(), "http://127.0.0.1:7400"); err != nil {
		t.Fatal(err)
	}
	if got, err := journal.ServerCA(t.Context()); err != nil || got != "" {
		t.Errorf("a journal with an address alone holds the fingerprint %q, %v", got, err)
	}
	if got, err := journal.ServerURL(t.Context()); err != nil || got != "http://127.0.0.1:7400" {
		t.Errorf("the address is %q, %v", got, err)
	}
}

// Migrating a journal again changes nothing it holds.
func TestMigratingAJournalAgainKeepsTheServer(t *testing.T) {
	t.Parallel()
	journal, err := sqlite.OpenMemoryJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	for range 2 {
		if err := journal.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.SaveServerURL(t.Context(), "http://127.0.0.1:7400"); err != nil {
		t.Fatal(err)
	}
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := journal.ServerURL(t.Context()); err != nil || got != "http://127.0.0.1:7400" {
		t.Errorf("the journal holds %q, %v", got, err)
	}
}
