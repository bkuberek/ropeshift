package sqlite_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

func indexed(t *testing.T, projects ports.Projects) []string {
	t.Helper()
	index, err := projects.Index(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entry := range index {
		out = append(out, entry.Identity+" "+string(entry.Project)+" "+entry.Repository)
	}
	slices.Sort(out)
	return out
}

func detectErr(store ports.Store, t *testing.T, scope ledger.WorkspaceID, id, remote string) error {
	_, err := control.NewRepositories(store).Detect(t.Context(), scope, control.DetectedRepository{ID: id, Remote: remote, DefaultBranch: "main"}, member)
	return err
}

// TestARepositoryAnotherProjectHoldsIsRefused holds FR-PROJ-6: a repository belongs to one project, by the
// identity of its remote in any of its spellings, and a second project's registration names the first.
func TestARepositoryAnotherProjectHoldsIsRefused(t *testing.T) {
	state := openState(t, t.TempDir())
	_, alpha := createProject(t, state, "pa", "alpha")
	_, beta := createProject(t, state, "pb", "beta")
	if err := detectErr(alpha, t, "pa", "r1", "git@example.com:acme/invoices.git"); err != nil {
		t.Fatal(err)
	}
	err := detectErr(beta, t, "pb", "r2", "https://someone@example.com/acme/invoices")
	if !errors.Is(err, ledger.ErrRepositoryHeld) || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("got %v, want a refusal that names alpha", err)
	}
	if _, err := control.NewRepositories(beta).Get(t.Context(), "pb", "r2"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the refused record stayed: %v", err)
	}
	if got := indexed(t, state); !slices.Equal(got, []string{"example.com/acme/invoices pa r1"}) {
		t.Errorf("index %v", got)
	}
	// A second clone of the repository in its own project is the same repository.
	if err := detectErr(alpha, t, "pa", "r1", "https://example.com/acme/invoices.git"); err != nil {
		t.Errorf("the same repository again in its project: %v", err)
	}
}

// TestTheIndexFollowsTheRecords keeps the repository index current while the daemon runs, not only when it
// starts: a new record holds its identity at once, and a remote that moves takes its identity along.
func TestTheIndexFollowsTheRecords(t *testing.T) {
	state := openState(t, t.TempDir())
	_, alpha := createProject(t, state, "pa", "alpha")
	_, beta := createProject(t, state, "pb", "beta")
	if err := detectErr(alpha, t, "pa", "r1", "https://example.com/acme/invoices"); err != nil {
		t.Fatal(err)
	}
	if err := detectErr(alpha, t, "pa", "r1", "https://example.com/acme/billing"); err != nil {
		t.Fatal(err)
	}
	if got := indexed(t, state); !slices.Equal(got, []string{"example.com/acme/billing pa r1"}) {
		t.Errorf("index %v", got)
	}
	if err := detectErr(beta, t, "pb", "r2", "https://example.com/acme/invoices"); err != nil {
		t.Errorf("the identity the remote left: %v", err)
	}
	if err := detectErr(beta, t, "pb", "r3", ""); err != nil {
		t.Errorf("a repository with no remote: %v", err)
	}
	want := []string{"example.com/acme/billing pa r1", "example.com/acme/invoices pb r2", "r3 pb r3"}
	if got := indexed(t, state); !slices.Equal(got, want) {
		t.Errorf("index %v", got)
	}
}

// TestIntakeRefusesASourceAnotherProjectHolds is the path the daemon's --source takes: intake into one
// project of a repository another project holds is refused, names that project, and records nothing.
func TestIntakeRefusesASourceAnotherProjectHolds(t *testing.T) {
	state := openState(t, t.TempDir())
	_, alpha := createProject(t, state, "pa", "alpha")
	_, beta := createProject(t, state, "pb", "beta")
	if err := detectErr(beta, t, "pb", "r9", "git@example.com:acme/invoices.git"); err != nil {
		t.Fatal(err)
	}
	source := fake.NewWorkSource()
	source.SetRepository("https://example.com/acme/invoices", "main")
	source.Write("contracts/retry.md", []byte("---\nid: retry\ntitle: Retry\n---\nRetry.\n"))
	_, err := control.NewIntake(alpha).Poll(t.Context(), "pa", source)
	if !errors.Is(err, ledger.ErrRepositoryHeld) || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("got %v, want a refusal that names beta", err)
	}
	if orders := workOrderIDs(t, alpha, "pa"); len(orders) != 0 {
		t.Errorf("intake recorded %v", orders)
	}
}

// workOrderIDs lists the work orders a project holds.
func workOrderIDs(t *testing.T, store ports.Store, scope ledger.WorkspaceID) []string {
	t.Helper()
	var ids []string
	err := store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		rows, err := tx.WorkOrders().List()
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}
