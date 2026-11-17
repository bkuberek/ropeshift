package entry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/repofiles"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
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
	state             ports.Projects
	store             ports.Store
	workspace, design *gittest.Repo
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
	return mergedSource{state: state, store: store, workspace: workspace, design: design, own: own, lowest: lowest}
}

// record makes the first project's record for a repository, as an earlier read of it did.
func (m mergedSource) record(t *testing.T, id string) {
	t.Helper()
	_, err := control.NewRepositories(m.store).Detect(t.Context(), soloScope, control.DetectedRepository{ID: id, DefaultBranch: "main"}, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
	if err != nil {
		t.Fatal(err)
	}
}

// legacyRecord makes the first project's record for a repository from before its identity was its first-parent
// root, which a daemon source matches by any of its roots.
func (m mergedSource) legacyRecord(t *testing.T, id string) {
	t.Helper()
	err := m.store.Write(t.Context(), soloScope, "test.legacy", func(tx ports.WriteTx) error {
		record := ledger.NewRepository(id)
		record.IdentityLegacy, record.DefaultBranch = true, "main"
		record.Name = ledger.NameRepository("", "", id, func(string) bool { return false })
		if _, err := tx.Repositories().Insert(record); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryRegistered, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"},
			Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: id}, Data: []byte(`{}`),
		})
		return err
	})
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
	m.legacyRecord(t, m.lowest)
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

// A history merged in after the record was registered under the lowest root it had leaves the lowest root with no
// record, and a daemon source with no checkout is still that record's repository.
func TestASourceWhoseLowestRootNamesNoRecordKeepsTheIDOfItsRecord(t *testing.T) {
	m := newMergedSource(t)
	m.legacyRecord(t, m.lowest)
	m.workspace.MergeUnrelated(gittest.NewWithRootBelow(t, m.own, m.lowest))
	opts := soloOptions{intakeTick: time.Second, sources: repeatedFlag{m.workspace.Dir}}
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)}
	ticks := soloTicks(m.state, soloScope, opts, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	ids, instances := m.records(t)
	if !slices.Equal(ids, []string{m.lowest}) || !slices.Equal(instances, []string{m.lowest}) {
		t.Errorf("intake left records %v and external keys under %v, want the one record %s", ids, instances, m.lowest)
	}
}

// The documents of a --source repository are keyed under the same ID.
func TestAKnowledgeSourceWithNoCheckoutKeepsTheIDOfItsRecord(t *testing.T) {
	m := newMergedSource(t)
	m.legacyRecord(t, m.lowest)
	opts := soloOptions{sources: repeatedFlag{m.workspace.Dir}, records: recordsIn(m.store, soloScope)}
	sources := soloKnowledge(opts, &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName)})
	if len(sources) != 1 {
		t.Fatalf("the daemon reads %d knowledge sources", len(sources))
	}
	description, err := sources[0].Describe(t.Context())
	if err != nil || description.Instance != m.lowest {
		t.Errorf("the knowledge source names %q (%v), want its record's ID %s", description.Instance, err, m.lowest)
	}
}

// A clone of a record kept at another path counts only while that path exists and still holds the record's first
// commit.
func TestAClonePathCountsAsKeptElsewhereOnlyWhileItHoldsTheRecordsFirstCommit(t *testing.T) {
	m := newMergedSource(t)
	design := gittest.New(t)
	design.Commit("DESIGN.md", "design\n")
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}
	id := design.Roots("HEAD")[0]
	if err := kept.Keep(t.Context(), soloScope, id, design.Dir); err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		path string
		want bool
	}{
		"another path holds it": {m.workspace.Dir, true},
		"the record's own path": {design.Dir, false},
		"a path nothing is at":  {filepath.Join(t.TempDir(), "absent"), true},
	} {
		got, held, err := keptElsewhere(kept, soloScope, tt.path)(t.Context(), id)
		if err != nil || held != tt.want || (held && !sameDirectory(got.Path, design.Dir)) {
			t.Errorf("%s: kept elsewhere %+v, %v (%v), want %v at %s", name, got, held, err, tt.want, design.Dir)
		}
	}
	// Another repository now stands at the path this worker kept for the record, so the path holds none of it.
	replacement := gittest.New(t)
	replacement.Commit("OTHER.md", "another repository\n")
	if err := kept.Keep(t.Context(), soloScope, "replaced", replacement.Dir); err != nil {
		t.Fatal(err)
	}
	if got, held, err := keptElsewhere(kept, soloScope, m.workspace.Dir)(t.Context(), "replaced"); err != nil || held {
		t.Errorf("a kept path another clone replaced counts as kept elsewhere: %+v, %v (%v)", got, held, err)
	}
	if err := os.RemoveAll(design.Dir); err != nil {
		t.Fatal(err)
	}
	if got, held, err := keptElsewhere(kept, soloScope, m.workspace.Dir)(t.Context(), id); err != nil || held {
		t.Errorf("a kept path that is gone counts as kept elsewhere: %+v, %v (%v)", got, held, err)
	}
	if got, held, err := keptElsewhere(kept, soloScope, m.workspace.Dir)(t.Context(), "no-such-record"); err != nil || held {
		t.Errorf("a record this worker keeps no clone of counts as kept elsewhere: %+v, %v (%v)", got, held, err)
	}
}

// sameDirectory reports whether two paths name one directory, however each is spelled: a temporary directory
// is /var/... on a machine whose /var is a link to /private/var, and the product reports the real path.
func sameDirectory(a, b string) bool {
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}

// aliasOf returns a spelling of a directory that leads through a symbolic link, as a temporary directory does on
// a machine whose /var is a link to /private/var.
func aliasOf(t *testing.T, dir string) string {
	t.Helper()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(dir), alias); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(alias, filepath.Base(dir))
}

// A path is the directory it names, however it is spelled: a checkout kept through a symbolic link is the clone
// at the path it leads to, and a source given through one is the checkout kept at the real path.
func TestKeptPathsAreComparedByTheDirectoryTheyNameAndNotHowTheyAreSpelled(t *testing.T) {
	m := newMergedSource(t)
	design := gittest.New(t)
	design.Commit("DESIGN.md", "design\n")
	id := design.Roots("HEAD")[0]
	spelled := aliasOf(t, design.Dir)
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}

	// Kept through the link, and another clone asks.
	if err := kept.Keep(t.Context(), soloScope, id, spelled); err != nil {
		t.Fatal(err)
	}
	if got, held, err := keptElsewhere(kept, soloScope, m.workspace.Dir)(t.Context(), id); err != nil || !held || !sameDirectory(got.Path, design.Dir) {
		t.Errorf("a clone kept through a link reads %+v, %v (%v), want it kept at %s", got, held, err, design.Dir)
	}
	// The clone the record is kept through the link for is not another clone.
	if got, held, err := keptElsewhere(kept, soloScope, design.Dir)(t.Context(), id); err != nil || held {
		t.Errorf("the clone kept through a link counts as kept elsewhere for its own real path: %+v, %v (%v)", got, held, err)
	}
	if got := checkoutIdentity(kept, soloScope, design.Dir)(t.Context()); got != id {
		t.Errorf("the real path of a checkout kept through a link has the identity %q, want %s", got, id)
	}

	// Kept at the real path, and a source or a clone is given through the link.
	if err := kept.Keep(t.Context(), soloScope, id, design.Dir); err != nil {
		t.Fatal(err)
	}
	if got, held, err := keptElsewhere(kept, soloScope, spelled)(t.Context(), id); err != nil || held {
		t.Errorf("a path through a link to the kept clone counts as kept elsewhere: %+v, %v (%v)", got, held, err)
	}
	if got := checkoutIdentity(kept, soloScope, spelled)(t.Context()); got != id {
		t.Errorf("a path through a link to a kept checkout has the identity %q, want %s", got, id)
	}

	// A daemon source given through a link to a kept checkout is read once, under its record's ID.
	if err := kept.Keep(t.Context(), soloScope, m.lowest, m.workspace.Dir); err != nil {
		t.Fatal(err)
	}
	sources, err := projectSources(t.Context(), soloOptions{sources: repeatedFlag{aliasOf(t, m.workspace.Dir)}}, kept, soloScope, soloScope, nil)
	if err != nil || len(sources) != 2 {
		t.Fatalf("the project reads %d sources (%v), want its two checkouts, each once", len(sources), err)
	}
	var named []string
	for _, source := range sources {
		description, err := source.Describe(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		named = append(named, description.Instance)
	}
	if slices.Sort(named); !slices.Equal(named, slices.Sorted(slices.Values([]string{id, m.lowest}))) {
		t.Errorf("the sources are named %v, want %v", named, []string{id, m.lowest})
	}
}

// foldingCase is a machine whose disk ignores case, as APFS does by default: two paths that differ only in case
// name one directory.
type foldingCase struct{ ports.Checkouts }

func (f foldingCase) SameFile(ctx context.Context, a, b string) (bool, error) {
	if strings.EqualFold(a, b) {
		return true, nil
	}
	return f.Checkouts.SameFile(ctx, a, b)
}

// On a disk that ignores case, `~/code/App` and a kept `~/Code/app` are one directory: it is read once, is the
// checkout kept, and is not a clone of its own record kept elsewhere, which would refuse it for sharing its first
// commit with itself when it has no remote.
func TestKeptPathsThatDifferOnlyInCaseAreOneDirectoryOnADiskThatIgnoresCase(t *testing.T) {
	m := newMergedSource(t)
	kept := foldingCase{&checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}}
	id := m.workspace.Roots("HEAD")[0]
	if err := kept.Keep(t.Context(), soloScope, id, m.workspace.Dir); err != nil {
		t.Fatal(err)
	}
	variant := strings.ToUpper(m.workspace.Dir)
	if got, held, err := keptElsewhere(kept, soloScope, variant)(t.Context(), id); err != nil || held {
		t.Errorf("the kept directory spelled in another case counts as kept elsewhere: %+v, %v (%v)", got, held, err)
	}
	if got := checkoutIdentity(kept, soloScope, variant)(t.Context()); got != id {
		t.Errorf("the kept directory spelled in another case has the identity %q, want %s", got, id)
	}
	sources, err := projectSources(t.Context(), soloOptions{sources: repeatedFlag{variant}}, kept, soloScope, soloScope, nil)
	if err != nil || len(sources) != 1 {
		t.Errorf("the project reads %d sources (%v), want the one directory once", len(sources), err)
	}
}

// A daemon source that shares its first commit with a clone this worker keeps at another path, with another
// remote or none, is not read under the record's ID: intake reports it as it reports a repository the project
// removed, makes no record or key for it, and no lane resolves to it.
func TestASourceThatSharesAFirstCommitWithAKeptCloneIsReportedAndNotRead(t *testing.T) {
	m := newMergedSource(t)
	m.legacyRecord(t, m.lowest)
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}
	if err := kept.Keep(t.Context(), soloScope, m.lowest, m.design.Dir); err != nil {
		t.Fatal(err)
	}
	var reported []string
	opts := soloOptions{
		intakeTick: time.Second, sources: repeatedFlag{m.workspace.Dir}, records: recordsIn(m.store, soloScope),
		report: func(line string) { reported = append(reported, line) },
	}
	ticks := soloTicks(m.state, soloScope, opts, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	// The refusal is reported when it begins, once, and not at each poll.
	for range 3 {
		if err := ticks[at].Run(t.Context()); err != nil {
			t.Errorf("a refused source fails the intake tick with %v", err)
		}
	}
	if len(reported) != 1 || !strings.Contains(reported[0], "shares its first commit `"+ledger.ShortCommit(m.lowest)+"`") || !strings.Contains(reported[0], m.design.Dir) {
		t.Errorf("intake reported %q, want one line naming the first commit and the kept clone", reported)
	}
	// Once the clone is no longer kept the source reads, and a refusal that begins again is reported again.
	if err := kept.Forget(t.Context(), soloScope, m.lowest); err != nil {
		t.Fatal(err)
	}
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := kept.Keep(t.Context(), soloScope, m.lowest, m.design.Dir); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ticks[at].Run(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(reported) != 2 {
		t.Errorf("intake reported %d lines after the refusal ended and began again, want 2: %q", len(reported), reported)
	}
	// The refusal made no record of its own under the source's first-parent root.
	var shared *repofiles.SharedFirstCommitError
	if ids, _ := m.records(t); !slices.Equal(ids, []string{m.lowest}) {
		t.Errorf("intake left records %v, want the one record %s", ids, m.lowest)
	}
	find := checkoutOf(opts, kept)
	if path, err := find(t.Context(), soloScope, m.lowest); err != nil || path != m.design.Dir {
		t.Errorf("the kept clone's record resolves to %q: %v", path, err)
	}
	if path, err := find(t.Context(), soloScope, m.own); !errors.Is(err, ports.ErrNotFound) || errors.As(err, &shared) {
		t.Errorf("the refused source resolves to %q (%v), and its refusal must not stop the lookup", path, err)
	}
}

// A daemon source that is a shallow clone is not read, as registration refuses it: intake says so once, makes no
// record under its grafted first commit, and the tick does not fail.
func TestAShallowSourceIsReportedAndNotRead(t *testing.T) {
	m := newMergedSource(t)
	shallow := m.workspace.ShallowClone("shallow")
	var reported []string
	opts := soloOptions{
		intakeTick: time.Second, sources: repeatedFlag{shallow.Dir}, records: recordsIn(m.store, soloScope),
		report: func(line string) { reported = append(reported, line) },
	}
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}
	ticks := soloTicks(m.state, soloScope, opts, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	for range 3 {
		if err := ticks[at].Run(t.Context()); err != nil {
			t.Errorf("a shallow source fails the intake tick with %v", err)
		}
	}
	if want := shallow.Dir + " " + ledger.ShallowClone; len(reported) != 1 || !strings.Contains(reported[0], want) {
		t.Errorf("intake reported %q, want one line with %q", reported, want)
	}
	if ids, _ := m.records(t); len(ids) != 0 {
		t.Errorf("intake left records %v for a shallow source", ids)
	}
	if path, err := checkoutOf(opts, kept)(t.Context(), soloScope, m.own); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a record resolves to the shallow source %q (%v), want none", path, err)
	}
}

// One daemon source that shares its first commit with a clone this worker keeps is not read, and the Documents
// view still lists the documents of the others: the refused source is out of service, as intake leaves it, and
// does not fail the response.
func TestTheDocumentsViewListsTheOtherSourcesWhenOneIsRefused(t *testing.T) {
	m := newMergedSource(t)
	m.legacyRecord(t, m.lowest)
	other := gittest.New(t)
	other.Commit("NOTES.md", "# Notes\n\nThe other repository.\n")
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}
	if err := kept.Keep(t.Context(), soloScope, m.lowest, m.design.Dir); err != nil {
		t.Fatal(err)
	}
	sources := soloKnowledge(soloOptions{sources: repeatedFlag{m.workspace.Dir, other.Dir}, records: recordsIn(m.store, soloScope)}, kept)
	d := apitest.StartWith(t, apitest.Options{Knowledge: sources})
	documents, err := d.Client.Documents(t.Context())
	if err != nil {
		t.Fatalf("the Documents view fails: %v", err)
	}
	var paths []string
	for _, document := range documents {
		paths = append(paths, document.Path)
	}
	if !slices.Contains(paths, "NOTES.md") || slices.Contains(paths, "contract.md") {
		t.Errorf("the view lists %v, want the other repository's NOTES.md and nothing of the refused source", paths)
	}
	var read api.Document
	if err := d.Client.Do(t.Context(), http.MethodGet, api.InProject(apitest.ProjectName, "/v1/documents/read?path=NOTES.md"), nil, &read); err != nil || read.Path != "NOTES.md" {
		t.Errorf("reading the other repository's document fails: %+v (%v)", read, err)
	}
}

// The documents of a daemon source that is also a kept checkout are keyed by the ID of its record, as intake
// keys its work, though the checkout is kept after the source is made.
func TestAKnowledgeSourceThatIsAKeptCheckoutUsesTheID(t *testing.T) {
	m := newMergedSource(t)
	kept := &checkouts.File{Path: filepath.Join(t.TempDir(), checkouts.FileName), Env: m.workspace.Env()}
	sources := soloKnowledge(soloOptions{sources: repeatedFlag{m.workspace.Dir}}, kept)
	if err := kept.Keep(t.Context(), soloScope, m.lowest, m.workspace.Dir); err != nil {
		t.Fatal(err)
	}
	description, err := sources[0].Describe(t.Context())
	if err != nil || description.Instance != m.lowest {
		t.Errorf("the knowledge source names %q (%v), want the kept checkout's record %s", description.Instance, err, m.lowest)
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
