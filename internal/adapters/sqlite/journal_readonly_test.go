package sqlite_test

import (
	"os"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A journal opened read-only reads what a worker saved and refuses every write, so a reader such as the doctor
// cannot change the worker's state.
func TestAReadOnlyJournalReadsWhatWasSavedAndRefusesAWrite(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "journal.db")
	writer, err := sqlite.OpenJournal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved := ledger.DesiredState{Rev: 3, Projects: []ledger.DesiredProject{{ID: "prj-1", Name: "one", PlanningRepository: "plan"}}}
	if err := writer.SaveDesired(t.Context(), saved); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	journal, err := sqlite.OpenJournalReadOnly(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	state, err := journal.Desired(t.Context())
	if err != nil || state.Rev != saved.Rev || len(state.Projects) != 1 || state.Projects[0].PlanningRepository != "plan" {
		t.Fatalf("the read-only journal holds %+v (%v), want %+v", state, err, saved)
	}
	if err := journal.SaveDesired(t.Context(), ledger.DesiredState{Rev: 4}); err == nil {
		t.Fatal("a read-only journal took a write")
	}
	if state, err = journal.Desired(t.Context()); err != nil || state.Rev != saved.Rev {
		t.Errorf("the journal holds %+v (%v) after a refused write, want revision %d", state, err, saved.Rev)
	}
}

// A journal that is not there stays not there: opening it read-only creates no file, and nothing is read from it.
func TestAReadOnlyJournalCreatesNoFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "journal.db")
	journal, err := sqlite.OpenJournalReadOnly(file)
	if err == nil {
		t.Cleanup(func() { _ = journal.Close() })
		_, err = journal.Desired(t.Context())
	}
	if err == nil {
		t.Error("a journal nothing is at gave its desired state")
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the folder holds %v (%v), want nothing", entries, err)
	}
}
