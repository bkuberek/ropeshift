package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/test/apitest"
)

// TestBackupNamesAFileFromWhereItRuns holds the path to the shell's own directory, because the daemon
// that writes the copy runs in another one.
func TestBackupNamesAFileFromWhereItRuns(t *testing.T) {
	backup := fake.NewLedgerBackup()
	backup.Mark("a record")
	s := &session{t: t, daemon: apitest.StartWith(t, apitest.Options{Backup: backup})}
	dir := t.TempDir()
	t.Chdir(dir)
	code, stdout, stderr := s.run("backup", "copy.db")
	want := filepath.Join(dir, "copy.db")
	if code != cli.ExitOK || !strings.Contains(stdout, want) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if paths := backup.Paths(); len(paths) != 1 || paths[0] != want {
		t.Fatalf("the daemon was asked for %v", paths)
	}
	if code, _, stderr := s.run("backup", "copy.db"); code != cli.ExitUsage || !strings.Contains(stderr, "exists") {
		t.Fatalf("a second copy at the same path: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := s.run("backup"); code != cli.ExitUsage {
		t.Fatalf("no path: exit %d", code)
	}
}

// TestBackupNeedsAMember holds that a copy of every record, and of the passphrase's hash, is a
// member's to ask for, though it changes nothing in the ledger.
func TestBackupNeedsAMember(t *testing.T) {
	backup := fake.NewLedgerBackup()
	s := &session{t: t, daemon: apitest.StartWith(t, apitest.Options{Backup: backup})}
	path := filepath.Join(t.TempDir(), "copy.db")
	if code, _, stderr := s.runAs("", nil, "backup", path); code != cli.ExitForbidden {
		t.Fatalf("no session: exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a refused backup wrote a file: %v", err)
	}
}

// TestBackupSaysWhenThereIsNone is a control plane with no online backup, as on Postgres.
func TestBackupSaysWhenThereIsNone(t *testing.T) {
	s := newSession(t)
	code, _, stderr := s.run("backup", filepath.Join(t.TempDir(), "copy.db"))
	if code != cli.ExitError || !strings.Contains(stderr, "own tooling") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
