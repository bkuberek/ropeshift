package entry

import (
	"context"
	"errors"
	"fmt"
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
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member, nil)
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
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member, nil)
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

// TestARecordStoredUnderTheLowestRootKeepsItsIDAndItsExternalKeys holds FR-REPO-14 on the daemon's side: a
// repository registered before the first-parent rule is named by its lowest root, and when it has merged an
// unrelated history whose root sorts lower, the checkout the worker keeps names that record, so intake still
// reads the repository under it, and every external key it made stays.
func TestARecordStoredUnderTheLowestRootKeepsItsIDAndItsExternalKeys(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer store.Close()
	if _, _, err := state.CreateProject(t.Context(), ledger.Project{ID: "beta-id", Name: "beta"}, member); err != nil {
		t.Fatal(err)
	}
	workspace, design := gittest.NewUnrelatedPair(t)
	for _, entry := range workspace.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	own, lowest := workspace.Roots("HEAD")[0], design.Roots("HEAD")[0]
	workspace.Commit("contract.md", contract.ContractText("beta-work", "Beta work", "Ship it."))
	workspace.MergeUnrelated(design)
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}
	run := func() {
		t.Helper()
		ticks := soloTicks(state, soloScope, soloOptions{intakeTick: time.Second}, kept, questions.BatchPolicy{}, nil)
		at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
		if err := ticks[at].Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	instances := func() []string {
		t.Helper()
		beta, err := state.Named(t.Context(), "beta")
		if err != nil {
			t.Fatal(err)
		}
		opened, err := state.Open(t.Context(), beta)
		if err != nil {
			t.Fatal(err)
		}
		var found []string
		if err := opened.Read(t.Context(), beta.ID, func(tx ports.ReadTx) error {
			links, err := tx.Planning().Links()
			for _, link := range links {
				found = append(found, link.Key.Instance)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		slices.Sort(found)
		return slices.Compact(found)
	}

	// The record was registered under the lowest root, and the worker's checkout map names it.
	if err := kept.Keep(t.Context(), "beta-id", lowest, workspace.Dir); err != nil {
		t.Fatal(err)
	}
	run()
	if got := instances(); !slices.Equal(got, []string{lowest}) {
		t.Fatalf("intake keyed the repository by %v, want its record's ID %s and not the first-parent root %s", got, lowest, own)
	}
	// A lane's lookup of the record by its ID finds the checkout.
	if path, err := checkoutOf(soloOptions{}, kept)(t.Context(), "beta-id", lowest); err != nil || path != workspace.Dir {
		t.Errorf("the checkout of the record reads %q: %v", path, err)
	}
}

// mergedSource is a first project that holds a record for each of its repositories by the ID given, and a
// workspace that merged an unrelated history whose first commit sorts lower than its own, with a contract
// committed before the merge.
type mergedSource struct {
	state     ports.Projects
	store     ports.Store
	workspace *gittest.Repo
	// own is the workspace's first-parent root, and lowest the root of the history it merged.
	own, lowest string
}

func newMergedSource(t *testing.T) mergedSource {
	t.Helper()
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	state, store, err := sqlite.Upgrade(t.Context(), t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close(); _ = store.Close() })
	workspace, design := gittest.NewUnrelatedPair(t)
	for _, entry := range workspace.Env() {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	own, lowest := workspace.Roots("HEAD")[0], design.Roots("HEAD")[0]
	workspace.Commit("contract.md", contract.ContractText("source-work", "Source work", "Ship it."))
	workspace.MergeUnrelated(design)
	return mergedSource{state: state, store: store, workspace: workspace, own: own, lowest: lowest}
}

// record makes the first project's record for a repository, as an earlier read of it did.
func (m mergedSource) record(t *testing.T, id string) {
	t.Helper()
	_, err := control.NewRepositories(m.store).Detect(t.Context(), soloScope, control.DetectedRepository{ID: id, DefaultBranch: "main"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		t.Fatal(err)
	}
}

func (m mergedSource) records(t *testing.T) (ids, instances []string) {
	t.Helper()
	if err := m.store.Read(t.Context(), soloScope, func(tx ports.ReadTx) error {
		all, err := tx.Repositories().Repositories()
		for _, record := range all {
			ids = append(ids, record.ID)
		}
		links, err2 := tx.Planning().Links()
		for _, link := range links {
			instances = append(instances, link.Key.Instance)
		}
		return errors.Join(err, err2)
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	slices.Sort(instances)
	return ids, slices.Compact(instances)
}

// A repository the daemon reads from --source, with no checkout kept, keeps the ID of the record registration
// would match it to, so an upgrade does not make a second record, with its own binding, cursor, and external
// keys, under the first-parent root.
func TestASourceWithNoCheckoutKeepsTheIDOfItsRecord(t *testing.T) {
	m := newMergedSource(t)
	m.record(t, m.lowest)
	opts := soloOptions{intakeTick: time.Second, sources: repeatedFlag{m.workspace.Dir}}
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}
	ticks := soloTicks(m.state, soloScope, opts, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	for range 2 {
		if err := ticks[at].Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	ids, instances := m.records(t)
	if !slices.Equal(ids, []string{m.lowest}) || !slices.Equal(instances, []string{m.lowest}) {
		t.Errorf("intake left records %v and external keys under %v, want the one record %s and its keys", ids, instances, m.lowest)
	}
}

// The documents of a --source repository are keyed under the same ID.
func TestAKnowledgeSourceWithNoCheckoutKeepsTheIDOfItsRecord(t *testing.T) {
	m := newMergedSource(t)
	m.record(t, m.lowest)
	opts := soloOptions{sources: repeatedFlag{m.workspace.Dir}, records: recordsIn(m.store, soloScope)}
	sources := soloKnowledge(opts)
	if len(sources) != 1 {
		t.Fatalf("the daemon reads %d knowledge sources", len(sources))
	}
	description, err := sources[0].Describe(t.Context())
	if err != nil || description.Instance != m.lowest {
		t.Errorf("the knowledge source names %q (%v), want its record's ID %s", description.Instance, err, m.lowest)
	}
}

// A lane for a repository resolves to a daemon source whose own record is that repository's, and not to
// another source that merely holds its first commit.
func TestALaneResolvesOnlyToACheckoutWhoseIdentityIsTheRecordsID(t *testing.T) {
	m := newMergedSource(t)
	m.record(t, m.lowest)
	m.record(t, m.own)
	find := checkoutOf(soloOptions{sources: repeatedFlag{m.workspace.Dir}, records: recordsIn(m.store, soloScope)}, &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)})
	if path, err := find(t.Context(), soloScope, m.own); err != nil || path != m.workspace.Dir {
		t.Errorf("the workspace's own record resolves to %q: %v", path, err)
	}
	if path, err := find(t.Context(), soloScope, m.lowest); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the merged history's record resolves to %q (%v), though the workspace has a record of its own", path, err)
	}
}

// A checkout the worker keeps whose directory a person deleted or moved is no checkout: the lookup says the
// worker keeps none, which holds the work as no_checkout and not as base_unavailable.
func TestACheckoutWhoseDirectoryIsGoneIsNone(t *testing.T) {
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}
	gone := filepath.Join(t.TempDir(), "billing")
	if err := os.MkdirAll(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := kept.Keep(t.Context(), "beta-id", "repo-billing", gone); err != nil {
		t.Fatal(err)
	}
	find := checkoutOf(soloOptions{}, kept)
	if path, err := find(t.Context(), "beta-id", "repo-billing"); err != nil || path != gone {
		t.Fatalf("a kept checkout reads %q: %v", path, err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if path, err := find(t.Context(), "beta-id", "repo-billing"); !errors.Is(err, ports.ErrNotFound) || !strings.Contains(fmt.Sprint(err), gone) {
		t.Errorf("a checkout whose directory is gone reads %q: %v", path, err)
	}
}
