package contract

import (
	"database/sql"
	"path/filepath"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/ledger"
)

func TestLedgerBackupContract(t *testing.T) {
	t.Run("sqlite online backup", func(t *testing.T) {
		RunLedgerBackup(t, func(t *testing.T) BackupRig {
			file := filepath.Join(t.TempDir(), "ledger.db")
			store, err := sqlite.Open(file, sqlstore.Options{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
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
