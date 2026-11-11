package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// TestAProjectsBackupHoldsThatProjectAlone holds FR-PROJ-9: a project's backup is its own ledger, with
// none of another project's records and none of the installation's.
func TestAProjectsBackupHoldsThatProjectAlone(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	newWorkOrder(t, alphaStore, alpha.ID, "wo-alpha", "Add the retry budget")
	newWorkOrder(t, betaStore, beta.ID, "wo-beta", "Ship the invoice export")
	writeShared(t, alphaStore, alpha.ID)

	copied := filepath.Join(t.TempDir(), "alpha.db")
	size, err := state.Backup(t.Context(), alpha, copied)
	if err != nil || size == 0 {
		t.Fatalf("backup: %d bytes, %v", size, err)
	}
	if n := countRows(t, copied, "work_orders WHERE id = 'wo-alpha'"); n != 1 {
		t.Errorf("the copy holds %d of alpha's work orders", n)
	}
	if n := countRows(t, copied, "work_orders WHERE id = 'wo-beta'"); n != 0 {
		t.Errorf("the copy holds beta's work order")
	}
	if n := countRows(t, copied, "workspaces WHERE id = 'pa'"); n != 1 {
		t.Errorf("the copy holds %d workspaces named pa", n)
	}
	for _, table := range sharedTables {
		if n := countRows(t, copied, table); n != 0 {
			t.Errorf("the copy holds %d rows of %s, which the installation keeps", n, table)
		}
	}
	if _, err := state.Backup(t.Context(), alpha, copied); !errors.Is(err, ports.ErrBackupTarget) {
		t.Errorf("a second copy at the same path: %v", err)
	}
}

// TestTheInstallationsBackupHoldsItsRecords is the other half of FR-PROJ-9: the members, workers, and
// registry, and no project's ledger.
func TestTheInstallationsBackupHoldsItsRecords(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	newWorkOrder(t, alphaStore, alpha.ID, "wo-alpha", "Add the retry budget")
	writeShared(t, alphaStore, alpha.ID)

	copied := filepath.Join(t.TempDir(), "installation.db")
	if _, err := state.BackupInstallation(t.Context(), copied); err != nil {
		t.Fatal(err)
	}
	for _, table := range sharedTables {
		if n := countRows(t, copied, table); n != 1 {
			t.Errorf("the copy holds %d rows of %s", n, table)
		}
	}
	if n := countRows(t, copied, "projects WHERE name = 'alpha'"); n != 1 {
		t.Errorf("the copy's registry holds %d projects named alpha", n)
	}
	if n := countRows(t, copied, "sqlite_master WHERE name = 'work_orders'"); n != 0 {
		t.Errorf("the copy holds a project's work orders")
	}
}
