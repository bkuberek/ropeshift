package control_test

import (
	"errors"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

var projectMember = ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}

// ledgerOf is an empty ledger with one workspace, as a project's database holds.
func ledgerOf(t *testing.T, id ledger.WorkspaceID) ports.Store {
	t.Helper()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: id, Name: string(id)}, projectMember); err != nil {
		t.Fatal(err)
	}
	return store
}

// TestOnlyAMemberTakesAProjectThroughItsLifecycle holds ADR-0021's member step on every use case.
func TestOnlyAMemberTakesAProjectThroughItsLifecycle(t *testing.T) {
	registry := fake.NewProjects()
	registry.Add(ledger.Project{ID: "acme-id", Name: "acme", Database: "projects/acme-id"}, nil)
	projects := control.NewProjects(registry)
	lane := ledger.Actor{Kind: ledger.ActorLane, ID: "lane-1"}
	steps := map[string]func() error{
		"create":  func() error { _, err := projects.Create(t.Context(), "beta", "", lane); return err },
		"rename":  func() error { _, err := projects.Rename(t.Context(), "acme", "beta", lane); return err },
		"archive": func() error { _, err := projects.Archive(t.Context(), "acme", lane); return err },
		"restore": func() error { _, err := projects.Restore(t.Context(), "acme", lane); return err },
		"remove": func() error {
			return projects.Remove(t.Context(), "acme", control.Removal{Confirm: "acme", NoBackup: true}, lane)
		},
		"import": func() error { _, err := projects.Import(t.Context(), "/backup.db", "", lane); return err },
	}
	for name, step := range steps {
		if err := step(); !errors.Is(err, workorders.ErrForbidden) {
			t.Errorf("%s by a lane: %v", name, err)
		}
	}
	if events := registry.Events(); len(events) != 0 {
		t.Errorf("events %v", events)
	}
}

// TestARemovalBacksUpFirstAndAMoveKeepsTheID walks a project out of one registry and into another through
// its backup, with the guards remove keeps.
func TestARemovalBacksUpFirstAndAMoveKeepsTheID(t *testing.T) {
	from := fake.NewProjects()
	from.Add(ledger.Project{ID: "default", Name: "default", Database: "ledger.db"}, ledgerOf(t, "default"))
	from.Add(ledger.Project{ID: "acme-id", Name: "acme", Database: "projects/acme-id"}, ledgerOf(t, "acme-id"))
	from.Hold(ledger.IndexedRepository{Identity: "example.com/acme/invoices", Project: "acme-id", Repository: "r1"})
	projects := control.NewProjects(from)
	ctx := t.Context()
	if err := projects.Remove(ctx, "acme", control.Removal{Confirm: "acme", Backup: "/acme.db"}, projectMember); !errors.Is(err, ledger.ErrProjectState) {
		t.Errorf("an active project: %v", err)
	}
	if _, err := projects.Restore(ctx, "acme", projectMember); !errors.Is(err, ledger.ErrProjectState) {
		t.Errorf("restoring an active project: %v", err)
	}
	for _, name := range []string{"acme", "default"} {
		if _, err := projects.Archive(ctx, name, projectMember); err != nil {
			t.Fatal(err)
		}
	}
	if err := projects.Remove(ctx, "acme", control.Removal{Confirm: "acme", Backup: "/acme.db"}, projectMember); err != nil {
		t.Fatal(err)
	}
	// The first project is no different, because nothing depends on a project being first.
	if err := projects.Remove(ctx, "default", control.Removal{Confirm: "default", NoBackup: true}, projectMember); err != nil {
		t.Errorf("the first project: %v", err)
	}

	to := fake.NewProjects()
	to.Add(ledger.Project{ID: "default", Name: "default", Database: "ledger.db"}, nil)
	from.Carry("/acme.db", to)
	moved, err := control.NewProjects(to).Import(ctx, "/acme.db", "", projectMember)
	if err != nil {
		t.Fatal(err)
	}
	if moved.ID != "acme-id" || moved.Name != "acme" || moved.State != ledger.ProjectActive {
		t.Errorf("moved %+v", moved)
	}
	want := []string{
		ledger.EventProjectArchived + " acme-id", ledger.EventProjectArchived + " default", ledger.EventProjectRemoved + " acme-id", ledger.EventProjectRemoved + " default",
	}
	if got := from.Events(); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}
