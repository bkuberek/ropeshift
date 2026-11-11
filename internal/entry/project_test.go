package entry

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestTheRepositoryHereIsNamedAsTheIndexNamesIt holds the third step of FR-PROJ-4: the command finds the
// repository it runs in by the identity the repository index keys it by, from any directory inside it.
func TestTheRepositoryHereIsNamedAsTheIndexNamesIt(t *testing.T) {
	repo := gittest.New(t)
	for _, entry := range repo.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	root := repo.Commit("README.md", "hello\n")
	inside := filepath.Join(repo.Dir, "docs")
	if err := os.MkdirAll(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(inside)
	if got, err := repositoryHere(t.Context()); err != nil || got != root {
		t.Errorf("with no remote: %q, %v, want the first commit %s", got, err, root)
	}
	repo.Git("remote", "add", "origin", "https://someone:secret@Example.com/acme/invoices.git")
	if got, err := repositoryHere(t.Context()); err != nil || got != "example.com/acme/invoices" {
		t.Errorf("with a remote: %q, %v", got, err)
	}
	t.Chdir(t.TempDir())
	if got, err := repositoryHere(t.Context()); err != nil || got != "" {
		t.Errorf("outside any repository: %q, %v", got, err)
	}
}

// registryOf is an installation of one project on a Store, as a test of one tick holds it.
func registryOf(store ports.Store, scope ledger.WorkspaceID) ports.Projects {
	registry := fake.NewProjects()
	registry.Add(ledger.Project{ID: scope, Name: ledger.DefaultProjectName, Database: "ledger.db"}, store)
	return registry
}

// TestEveryActiveProjectRunsItsTicks holds ADR-0021's ticks: each runs in every active project, in that
// project's own database, and an archived project runs none until it is restored.
func TestEveryActiveProjectRunsItsTicks(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer store.Close()
	if _, _, err := state.CreateProject(t.Context(), ledger.Project{ID: "beta-id", Name: "beta"}, member); err != nil {
		t.Fatal(err)
	}
	runs := map[ledger.WorkspaceID]int{}
	tick := eachProject(state, func(ctx context.Context, p ledger.Project, store ports.Store) error {
		return store.Read(ctx, p.ID, func(tx ports.ReadTx) error {
			workspace, err := tx.Workspace()
			runs[workspace.ID]++
			return err
		})
	})
	if err := tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SetState(t.Context(), "beta-id", ledger.ProjectArchived, member); err != nil {
		t.Fatal(err)
	}
	if err := tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := state.SetState(t.Context(), "beta-id", ledger.ProjectActive, member); err != nil {
		t.Fatal(err)
	}
	if err := tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runs[soloScope] != 3 || runs["beta-id"] != 2 {
		t.Errorf("the tick ran %v, want 3 in the default project and 2 in beta, which was archived once", runs)
	}
}

// TestIntakeReadsEachProjectsOwnCheckouts holds ADR-0022 on the daemon's side: intake reads, in each
// project, the repositories this worker keeps a checkout of there, and a lane finds a project's checkout
// only in that project.
func TestIntakeReadsEachProjectsOwnCheckouts(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer store.Close()
	if _, _, err := state.CreateProject(t.Context(), ledger.Project{ID: "beta-id", Name: "beta"}, member); err != nil {
		t.Fatal(err)
	}
	repo := gittest.New(t)
	for _, entry := range repo.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	id := repo.Commit("contract.md", contract.ContractText("beta-work", "Beta work", "Ship it."))
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}
	if err := kept.Keep(t.Context(), "beta-id", id, repo.Dir); err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(state, soloScope, soloOptions{intakeTick: time.Second}, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	orders := func(p ledger.Project) []workorders.WorkOrder {
		opened, err := state.Open(t.Context(), p)
		if err != nil {
			t.Fatal(err)
		}
		var rows []workorders.WorkOrder
		if err := opened.Read(t.Context(), p.ID, func(tx ports.ReadTx) error {
			rows, err = tx.WorkOrders().List()
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	beta, err := state.Named(t.Context(), "beta")
	if err != nil {
		t.Fatal(err)
	}
	home, err := state.Named(t.Context(), ledger.DefaultProjectName)
	if err != nil {
		t.Fatal(err)
	}
	if rows := orders(beta); len(rows) != 1 || rows[0].RepositoryID != id {
		t.Errorf("beta holds %+v", rows)
	}
	if rows := orders(home); len(rows) != 0 {
		t.Errorf("the first project read beta's checkout: %+v", rows)
	}
	find := checkoutOf(soloOptions{}, kept)
	if path, err := find(t.Context(), "beta-id", id); err != nil || path != repo.Dir {
		t.Errorf("beta's checkout: %q, %v", path, err)
	}
	if _, err := find(t.Context(), soloScope, id); err == nil {
		t.Error("the first project found beta's checkout")
	}
}
