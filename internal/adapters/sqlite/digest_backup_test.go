package sqlite_test

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func digestRow(kind, owner string, generation int64, body string) ledger.GenerationDigest {
	return ledger.GenerationDigest{OwnerKind: kind, OwnerID: owner, Generation: generation, Body: json.RawMessage(body)}
}

// TestADigestSurvivesAProjectsBackupAndItsImport is FR-SLOG-2: a lane's and an assistant's digests are records of
// the project's ledger, so a backup of it carries them and a project made from the backup reads them.
func TestADigestSurvivesAProjectsBackupAndItsImport(t *testing.T) {
	from := openState(t, t.TempDir())
	acme, store := createProject(t, from, "acme-id", "acme")
	lane := digestRow(ledger.DigestOwnerLane, "lane-1", 2, `{"source":"stream","turns":3,"tool_calls":{"Edit":2},"files_written":["a.go"],"started_at":1,"complete":true}`)
	assistant := digestRow(ledger.DigestOwnerAssistant, "conversation-1", 1, `{"source":"stream","turns":1,"tool_calls":{},"files_written":[],"started_at":1,"complete":true}`)
	err := store.Write(t.Context(), acme.ID, "test.digest", func(tx ports.WriteTx) error {
		for _, digest := range []ledger.GenerationDigest{lane, assistant, lane} {
			if err := tx.Lanes().SaveDigest(digest); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.digest", Actor: member, Subject: ledger.Subject{Kind: "test", ID: "digest"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "acme.db")
	if _, err := from.Backup(t.Context(), acme, backup); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, backup, "generation_digests"); n != 2 {
		t.Errorf("the backup holds %d digests, want the two this project's owners made", n)
	}

	to, home, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: "default", Name: "Default"}, member, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = to.Close()
		_ = home.Close()
	})
	imported, err := to.Import(t.Context(), backup, "", member)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := to.Open(t.Context(), imported)
	if err != nil {
		t.Fatal(err)
	}
	err = opened.Read(t.Context(), imported.ID, func(tx ports.ReadTx) error {
		for _, want := range []ledger.GenerationDigest{lane, assistant} {
			got, err := tx.Lanes().Digest(want.OwnerKind, want.OwnerID, want.Generation)
			if err != nil {
				return err
			}
			if string(got.Body) != string(want.Body) || got.RecordedAt == 0 {
				t.Errorf("after the import: %+v, want %s", got, want.Body)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAnOrchestratorsDigestIsInTheInstallationsBackup is the other half of FR-SLOG-2: an orchestrator serves every
// project, so its digest is an installation record, and no project's backup holds it.
func TestAnOrchestratorsDigestIsInTheInstallationsBackup(t *testing.T) {
	state := openState(t, t.TempDir())
	alpha, store := createProject(t, state, "pa", "alpha")
	orchestrator := digestRow(ledger.DigestOwnerOrchestrator, "orchestrator-1", 3, `{"source":"stream","turns":2,"tool_calls":{},"files_written":[],"started_at":1,"complete":true}`)
	err := store.Installation(alpha.ID).Write(t.Context(), "test.digest", func(tx ports.SharedTx) error {
		for range 2 {
			if err := tx.Workers().SaveOrchestratorDigest(orchestrator); err != nil {
				return err
			}
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.digest", Actor: member, Subject: ledger.Subject{Kind: "test", ID: "digest"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	installation := filepath.Join(t.TempDir(), "installation.db")
	if _, err := state.BackupInstallation(t.Context(), installation); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, installation, "generation_digests WHERE owner_kind = 'orchestrator'"); n != 1 {
		t.Errorf("the installation's backup holds %d orchestrator digests", n)
	}
	project := filepath.Join(t.TempDir(), "alpha.db")
	if _, err := state.Backup(t.Context(), alpha, project); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, project, "generation_digests"); n != 0 {
		t.Errorf("the project's backup holds %d digests of an orchestrator", n)
	}
}
