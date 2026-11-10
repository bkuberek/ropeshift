package contract

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
)

func TestLedgerBackupContract(t *testing.T) {
	t.Parallel()
	t.Run("sqlite online backup", func(t *testing.T) {
		RunLedgerBackup(t, func(t *testing.T) BackupRig {
			file := filepath.Join(t.TempDir(), "ledger.db")
			if err := writeMigrated(file); err != nil {
				t.Fatal(err)
			}
			store, err := sqlite.Open(file, sqlstore.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			// Every migration finds itself applied, which is the path a restarted ledger takes.
			if err := store.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			return BackupRig{
				Backup: sqlite.LedgerBackup{File: file},
				Write: func(t *testing.T, mark string) {
					t.Helper()
					if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: ledger.WorkspaceID(mark), Name: mark}, ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}); err != nil {
						t.Fatal(err)
					}
				},
				Read: readWorkspaces,
			}
		})
	})
	t.Run("fake", func(t *testing.T) {
		RunLedgerBackup(t, func(*testing.T) BackupRig {
			backup := fake.NewLedgerBackup()
			return BackupRig{
				Backup: backup,
				Write:  func(_ *testing.T, mark string) { backup.Mark(mark) },
				Read: func(t *testing.T, path string) []string {
					t.Helper()
					marks, err := fake.ReadLedgerBackup(path)
					if err != nil {
						t.Fatal(err)
					}
					return marks
				},
			}
		})
	})
}

// readWorkspaces opens a copy on its own, read only, checks SQLite finds it whole, and lists its
// workspaces, which is where the rig writes its marks.
func readWorkspaces(t *testing.T, path string) []string {
	t.Helper()
	header := make([]byte, 100)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadFull(file, header)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Bytes 18 and 19 of a SQLite header are 2 for a database in WAL mode, which wants a log beside it.
	if header[18] != 1 || header[19] != 1 {
		t.Errorf("the copy is in WAL mode, so it is not one file: header bytes %d and %d", header[18], header[19])
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity of %s: %q, %v", path, integrity, err)
	}
	rows, err := db.QueryContext(t.Context(), "SELECT id FROM workspaces ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}
