package control_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func repositoryNamesRig(t *testing.T) (*control.Repositories, *fake.Store) {
	t.Helper()
	ctx := t.Context()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, projectMember); err != nil {
		t.Fatal(err)
	}
	repositories := control.NewRepositories(store)
	if _, err := repositories.Detect(ctx, "w", control.DetectedRepository{ID: "repo-a", DefaultBranch: "main", Directory: "api"}, projectMember); err != nil {
		t.Fatal(err)
	}
	return repositories, store
}

func TestAChangeRefusesANewNameWithAHiddenCharacter(t *testing.T) {
	repositories, _ := repositoryNamesRig(t)
	for _, name := range []string{"api\u200b", "api\u3164", "api\xff"} {
		if _, err := repositories.Change(t.Context(), "w", "repo-a", control.RepositoryChange{Name: &name}, projectMember); !errors.Is(err, ledger.ErrInvalidRepository) {
			t.Errorf("a rename to %q: %v", name, err)
		}
	}
}

func TestAStoredRecordWithAHiddenCharacterNameAcceptsASettingsChange(t *testing.T) {
	ctx := t.Context()
	repositories, store := repositoryNamesRig(t)
	err := store.Write(ctx, "w", "test.seed", func(tx ports.WriteTx) error {
		record, err := tx.Repositories().Repository("repo-a")
		if err != nil {
			return err
		}
		record.Name = "api\u200b"
		if err := tx.Repositories().Settings(record); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: ledger.EventRepositoryChanged, Actor: projectMember,
			Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: record.ID}, Data: []byte("{}")})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "develop"
	record, err := repositories.Change(ctx, "w", "repo-a", control.RepositoryChange{Base: &base}, projectMember)
	if err != nil {
		t.Fatalf("a settings change on a stored name with a hidden character: %v", err)
	}
	if record.Name != "api\u200b" || record.Base() != "develop" {
		t.Errorf("the record reads %q on %q", record.Name, record.Base())
	}
}
