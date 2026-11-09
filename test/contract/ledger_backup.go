package contract

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// BackupRig is one backup, a way to change what it copies, and a way to read a copy back.
type BackupRig struct {
	Backup ports.LedgerBackup
	// Write records one mark in the ledger the backup copies.
	Write func(t *testing.T, mark string)
	// Read returns the marks a copy holds, after checking it is whole.
	Read func(t *testing.T, path string) []string
}

// RunLedgerBackup runs one contract on one backup. The SQLite adapter and the fake both pass
// it, so a test that leans on the fake leans on what the adapter does.
func RunLedgerBackup(t *testing.T, fresh func(*testing.T) BackupRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, BackupRig)
	}{
		{"a copy holds the ledger as it stood", checkBackupCopies},
		{"a file already there is never replaced", checkBackupKeepsAFile},
		{"a path that cannot take a new file is refused", checkBackupRefusesATarget},
		{"only the owner can read a copy", checkBackupIsPrivate},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) { check.run(t, t.Context(), fresh(t)) })
	}
}

func checkBackupCopies(t *testing.T, ctx context.Context, rig BackupRig) {
	dir := t.TempDir()
	rig.Write(t, "first")
	first := filepath.Join(dir, "first.db")
	if _, err := rig.Backup.Backup(ctx, first); err != nil {
		t.Fatal(err)
	}
	rig.Write(t, "second")
	second := filepath.Join(dir, "second.db")
	size, err := rig.Backup.Backup(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(second); err != nil || info.Size() != size {
		t.Errorf("reported %d bytes of a file that holds %v: %v", size, info, err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 2 {
		t.Errorf("a copy is one file, and the directory holds %v: %v", entries, err)
	}
	if got := rig.Read(t, first); !slices.Contains(got, "first") || slices.Contains(got, "second") {
		t.Errorf("the first copy holds %v", got)
	}
	if got := rig.Read(t, second); !slices.Contains(got, "first") || !slices.Contains(got, "second") {
		t.Errorf("the second copy holds %v", got)
	}
}

func checkBackupKeepsAFile(t *testing.T, ctx context.Context, rig BackupRig) {
	rig.Write(t, "first")
	path := filepath.Join(t.TempDir(), "kept.db")
	if err := os.WriteFile(path, []byte("a person's own file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.Backup.Backup(ctx, path); !errors.Is(err, ports.ErrBackupTarget) {
		t.Fatalf("a backup over a file: %v", err)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "a person's own file" {
		t.Fatalf("the file holds %q: %v", raw, err)
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
		t.Errorf("a refused backup left %v: %v", entries, err)
	}
}

func checkBackupRefusesATarget(t *testing.T, ctx context.Context, rig BackupRig) {
	rig.Write(t, "first")
	dir := t.TempDir()
	for _, path := range []string{"relative.db", filepath.Join(dir, "missing", "copy.db"), dir} {
		if _, err := rig.Backup.Backup(ctx, path); !errors.Is(err, ports.ErrBackupTarget) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, err := os.Stat("relative.db"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a relative path wrote a file: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("refused backups left %v: %v", entries, err)
	}
}

func checkBackupIsPrivate(t *testing.T, ctx context.Context, rig BackupRig) {
	rig.Write(t, "first")
	path := filepath.Join(t.TempDir(), "copy.db")
	if _, err := rig.Backup.Backup(ctx, path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("a copy of the ledger has mode %v", mode)
	}
}
