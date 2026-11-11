package sqlite_test

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

var testActor = ledger.Actor{Kind: ledger.ActorSystem, ID: "test"}

func openInstallation(t *testing.T, dir string) *sqlstore.Installation {
	t.Helper()
	installation, err := sqlite.OpenInstallation(filepath.Join(dir, sqlite.InstallationFileName), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = installation.Close() })
	if err := installation.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return installation
}

func TestTheInstallationRegistersEachProjectOnce(t *testing.T) {
	dir := t.TempDir()
	installation := openInstallation(t, dir)
	ctx := t.Context()
	if err := installation.Migrate(ctx); err != nil {
		t.Fatalf("a second migration: %v", err)
	}
	first := ledger.Project{ID: "default", Name: "default", DisplayName: "Default", Database: "ledger.db"}
	created, made, err := installation.CreateProject(ctx, first, testActor)
	if err != nil || !made {
		t.Fatalf("the first project: made %v: %v", made, err)
	}
	if created.State != ledger.ProjectActive || created.CreatedAt == 0 {
		t.Errorf("a new project reads %+v", created)
	}
	again, made, err := installation.CreateProject(ctx, first, testActor)
	if err != nil || made || again != created {
		t.Fatalf("a retry: %+v, made %v: %v", again, made, err)
	}
	for _, clash := range []ledger.Project{
		{ID: "p2", Name: "default", Database: "projects/p2.db"},
		{ID: "p2", Name: "acme", Database: "ledger.db"},
		{ID: "default", Name: "acme", Database: "projects/default.db"},
	} {
		if _, _, err := installation.CreateProject(ctx, clash, testActor); !errors.Is(err, ledger.ErrProjectExists) {
			t.Errorf("%+v: %v", clash, err)
		}
	}
	if _, _, err := installation.CreateProject(ctx, ledger.Project{ID: "p3", Name: "Not A Slug", Database: "projects/p3.db"}, testActor); !errors.Is(err, ledger.ErrInvalidProject) {
		t.Errorf("a name that is no slug: %v", err)
	}
	second, made, err := installation.CreateProject(ctx, ledger.Project{ID: "p2", Name: "acme", Database: "projects/p2.db"}, testActor)
	if err != nil || !made {
		t.Fatalf("a second project: made %v: %v", made, err)
	}
	projects, err := installation.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(projects, []ledger.Project{created, second}) {
		t.Errorf("the registry holds %+v", projects)
	}
	if got, err := installation.Project(ctx, "acme"); err != nil || got != second {
		t.Errorf("acme reads %+v: %v", got, err)
	}
	if _, err := installation.Project(ctx, "missing"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a project nobody created: %v", err)
	}
	events, err := installation.Events(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("the installation's log holds %d events: %+v", len(events), events)
	}
	for i, e := range events {
		if e.Rev != int64(i+1) || e.Type != ledger.EventProjectCreated || e.Subject.Kind != ledger.SubjectProject || e.Actor != testActor {
			t.Errorf("event %d reads %+v", i, e)
		}
	}
	if events[0].Subject.ID != "default" || events[1].Subject.ID != "p2" {
		t.Errorf("the events are about %s and %s", events[0].Subject.ID, events[1].Subject.ID)
	}
}

func TestTheRepositoryIndexKeepsTheFirstProjectThatHoldsARepository(t *testing.T) {
	installation := openInstallation(t, t.TempDir())
	ctx := t.Context()
	entries := []ledger.IndexedRepository{
		{Identity: "github.com/acme/app", Project: "default", Repository: "app"},
		{Identity: "github.com/acme/site", Project: "p2", Repository: "site"},
		{Identity: "github.com/acme/app", Project: "p2", Repository: "app-copy"},
	}
	conflicts, err := installation.IndexRepositories(ctx, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(conflicts, entries[2:]) {
		t.Errorf("the conflicts are %+v", conflicts)
	}
	index, err := installation.RepositoryIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(index, entries[:2]) {
		t.Errorf("the index holds %+v", index)
	}
	if _, err := installation.IndexRepositories(ctx, entries[1:2]); err != nil {
		t.Fatal(err)
	}
	if index, err = installation.RepositoryIndex(ctx); err != nil || !slices.Equal(index, entries[1:2]) {
		t.Errorf("a rebuild left %+v: %v", index, err)
	}
	events, err := installation.Events(ctx, 0, 100)
	if err != nil || len(events) != 0 {
		t.Errorf("a rebuild of routing data logged %+v: %v", events, err)
	}
}
