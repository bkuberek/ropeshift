package sqlite_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// installationEvents lists the types of the installation's events about one project, in order.
func installationEvents(t *testing.T, state *sqlite.State, id ledger.WorkspaceID) []string {
	t.Helper()
	events, err := state.Installation.Events(t.Context(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range events {
		if e.Subject.Kind == ledger.SubjectProject && e.Subject.ID == string(id) {
			types = append(types, e.Type+" by "+e.Actor.ID)
		}
	}
	return types
}

// TestAProjectCreatedWithNoIDGetsOneOfItsOwn is what project create relies on: an ID no other installation
// gives, so a backup imports anywhere.
func TestAProjectCreatedWithNoIDGetsOneOfItsOwn(t *testing.T) {
	state := openState(t, t.TempDir())
	first, err := state.Create(t.Context(), ledger.Project{Name: "alpha"}, member)
	if err != nil {
		t.Fatal(err)
	}
	second, err := state.Create(t.Context(), ledger.Project{Name: "beta", DisplayName: "Beta"}, member)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.ID == second.ID || first.Database == second.Database {
		t.Errorf("IDs %s and %s, databases %s and %s", first.ID, second.ID, first.Database, second.Database)
	}
	if got := installationEvents(t, state, first.ID); !slices.Equal(got, []string{ledger.EventProjectCreated + " by member-1"}) {
		t.Errorf("events %v", got)
	}
}

// TestARenameKeepsEverythingButTheName holds the lifecycle's rename: the ID, the database, and every
// record stay, and the old name names nothing.
func TestARenameKeepsEverythingButTheName(t *testing.T) {
	state := openState(t, t.TempDir())
	alpha, store := createProject(t, state, "pa", "alpha")
	createProject(t, state, "pb", "beta")
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")

	renamed, err := state.Rename(t.Context(), alpha.ID, "gamma", "", member)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != alpha.ID || renamed.Database != alpha.Database || renamed.Name != "gamma" || renamed.DisplayName != alpha.DisplayName {
		t.Errorf("renamed %+v", renamed)
	}
	if _, err := state.Named(t.Context(), "alpha"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the old name: %v", err)
	}
	opened, err := state.Open(t.Context(), renamed)
	if err != nil {
		t.Fatal(err)
	}
	if got := workOrderIDs(t, opened, renamed.ID); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("work orders after a rename: %v", got)
	}
	if _, err := state.Rename(t.Context(), alpha.ID, "beta", "", member); !errors.Is(err, ledger.ErrProjectExists) {
		t.Errorf("a name another project holds: %v", err)
	}
	if _, err := state.Rename(t.Context(), alpha.ID, "Not A Slug", "", member); !errors.Is(err, ledger.ErrInvalidProject) {
		t.Errorf("a name that is no slug: %v", err)
	}
	want := []string{ledger.EventProjectCreated + " by member-1", ledger.EventProjectRenamed + " by member-1"}
	if got := installationEvents(t, state, alpha.ID); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}

// A rename sets the display name it is given, and a rename to the same name with a new display name changes
// that alone, with its event. One that changes neither records nothing.
func TestARenameSetsTheDisplayNameItIsGiven(t *testing.T) {
	state := openState(t, t.TempDir())
	alpha, _ := createProject(t, state, "pa", "alpha")
	shown, err := state.Rename(t.Context(), alpha.ID, "alpha", "Alpha docs", member)
	if err != nil || shown.Name != "alpha" || shown.DisplayName != "Alpha docs" {
		t.Fatalf("renamed %+v: %v", shown, err)
	}
	if named, err := state.Named(t.Context(), "alpha"); err != nil || named.DisplayName != "Alpha docs" {
		t.Errorf("the registry holds %+v: %v", named, err)
	}
	if _, err := state.Rename(t.Context(), alpha.ID, "alpha", "Alpha docs", member); err != nil {
		t.Fatal(err)
	}
	want := []string{ledger.EventProjectCreated + " by member-1", ledger.EventProjectRenamed + " by member-1"}
	if got := installationEvents(t, state, alpha.ID); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}

// ownName reads the name a project's ledger keeps of it, and the types of the events about its workspace.
func ownName(t *testing.T, store ports.Store, id ledger.WorkspaceID) (string, []string) {
	t.Helper()
	var name string
	var types []string
	err := store.Read(t.Context(), id, func(tx ports.ReadTx) error {
		workspace, err := tx.Workspace()
		if err != nil {
			return err
		}
		name = workspace.Name
		events, err := tx.EventsAbout(ledger.Subject{Kind: "workspace", ID: string(id)})
		for _, e := range events {
			types = append(types, e.Type)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return name, types
}

// A rename keeps the name the project's own ledger holds the same as the one it is shown by, which a backup
// carries and the assistant reads: the display name, or else the name. A rename that changes neither writes
// nothing to the ledger.
func TestARenameKeepsTheLedgersOwnNameCurrent(t *testing.T) {
	state := openState(t, t.TempDir())
	alpha, store := createProject(t, state, "pa", "alpha")
	for _, step := range []struct{ name, displayName, want string }{
		{"gamma", "", "gamma"},
		{"gamma", "Gamma docs", "Gamma docs"},
		{"delta", "", "Gamma docs"},
		{"delta", "Gamma docs", "Gamma docs"},
	} {
		if _, err := state.Rename(t.Context(), alpha.ID, step.name, step.displayName, member); err != nil {
			t.Fatal(err)
		}
		if got, _ := ownName(t, store, alpha.ID); got != step.want {
			t.Errorf("after a rename to %q, %q, the ledger names the project %q, want %q", step.name, step.displayName, got, step.want)
		}
	}
	want := []string{"workspace.created", sqlstore.EventWorkspaceRenamed, sqlstore.EventWorkspaceRenamed}
	if _, got := ownName(t, store, alpha.ID); !slices.Equal(got, want) {
		t.Errorf("the ledger's events about the project %v, want %v", got, want)
	}
}

// A backup made after a rename restores the project by the name and the display name it had when it was backed
// up, and not by the ones it was created with.
func TestABackupMadeAfterARenameRestoresTheNewNames(t *testing.T) {
	from := openState(t, t.TempDir())
	acme, _ := createProject(t, from, "acme-id", "acme")
	backUp := func(p ledger.Project) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "acme.db")
		if _, err := from.Backup(t.Context(), p, path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	renamed, err := from.Rename(t.Context(), acme.ID, "harbor", "", member)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := openState(t, t.TempDir()).Import(t.Context(), backUp(renamed), "", member)
	if err != nil || restored.Name != "harbor" || restored.DisplayName != "" {
		t.Errorf("restored after a rename %+v: %v", restored, err)
	}
	shown, err := from.Rename(t.Context(), acme.ID, "harbor", "Harbor docs", member)
	if err != nil {
		t.Fatal(err)
	}
	restored, err = openState(t, t.TempDir()).Import(t.Context(), backUp(shown), "harbor", member)
	if err != nil || restored.Name != "harbor" || restored.DisplayName != "Harbor docs" {
		t.Errorf("restored after a new display name %+v: %v", restored, err)
	}
}

// A display name no list could show whole, or one with a control character, is refused on a create and on a
// rename, and the refused rename changes nothing.
func TestTheRegistryRefusesADisplayNameItCannotShow(t *testing.T) {
	state := openState(t, t.TempDir())
	alpha, _ := createProject(t, state, "pa", "alpha")
	for _, displayName := range []string{strings.Repeat("a", 101), "Alpha\x1b[2J", " Alpha"} {
		if _, err := state.Create(t.Context(), ledger.Project{Name: "beta", DisplayName: displayName}, member); !errors.Is(err, ledger.ErrInvalidProject) {
			t.Errorf("created with display name %q: %v", displayName, err)
		}
		if _, err := state.Rename(t.Context(), alpha.ID, "alpha", displayName, member); !errors.Is(err, ledger.ErrInvalidProject) {
			t.Errorf("renamed to display name %q: %v", displayName, err)
		}
	}
	if named, err := state.Named(t.Context(), "alpha"); err != nil || named.DisplayName != alpha.DisplayName {
		t.Errorf("the registry holds %+v: %v", named, err)
	}
	want := []string{ledger.EventProjectCreated + " by member-1"}
	if got := installationEvents(t, state, alpha.ID); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}

// TestArchivingAndRestoringRecordEachMove holds the state a project's lifecycle moves through.
func TestArchivingAndRestoringRecordEachMove(t *testing.T) {
	state := openState(t, t.TempDir())
	alpha, _ := createProject(t, state, "pa", "alpha")
	archived, err := state.SetState(t.Context(), alpha.ID, ledger.ProjectArchived, member)
	if err != nil || archived.State != ledger.ProjectArchived {
		t.Fatalf("archived %+v, %v", archived, err)
	}
	if again, err := state.SetState(t.Context(), alpha.ID, ledger.ProjectArchived, member); err != nil || again.State != ledger.ProjectArchived {
		t.Errorf("archiving twice: %+v, %v", again, err)
	}
	restored, err := state.SetState(t.Context(), alpha.ID, ledger.ProjectActive, member)
	if err != nil || restored.State != ledger.ProjectActive {
		t.Fatalf("restored %+v, %v", restored, err)
	}
	want := []string{
		ledger.EventProjectCreated + " by member-1", ledger.EventProjectArchived + " by member-1", ledger.EventProjectRestored + " by member-1",
	}
	if got := installationEvents(t, state, alpha.ID); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}

// TestRemovingAProjectDeletesItsDatabase holds remove: the registry and the repository index forget the
// project, its file goes, the event stays, and another project is untouched.
func TestRemovingAProjectDeletesItsDatabase(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, store := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")
	newWorkOrder(t, betaStore, beta.ID, "wo-2", "Ship the invoice export")
	_, err := state.Installation.IndexRepositories(t.Context(), []ledger.IndexedRepository{
		{Identity: "example.com/acme/invoices", Project: alpha.ID, Repository: "r1"},
		{Identity: "example.com/acme/billing", Project: beta.ID, Repository: "r2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Remove(t.Context(), alpha, member); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(alpha.Database))); !os.IsNotExist(err) {
		t.Errorf("the database is still there: %v", err)
	}
	if _, err := state.Named(t.Context(), "alpha"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the registry still names it: %v", err)
	}
	index, err := state.Index(t.Context())
	if err != nil || len(index) != 1 || index[0].Project != beta.ID {
		t.Errorf("index %+v, %v", index, err)
	}
	if got := workOrderIDs(t, betaStore, beta.ID); !slices.Equal(got, []string{"wo-2"}) {
		t.Errorf("beta's work orders: %v", got)
	}
	if got := installationEvents(t, state, alpha.ID); len(got) != 2 || got[1] != ledger.EventProjectRemoved+" by member-1" {
		t.Errorf("events %v", got)
	}
}

// TestAProjectsBackupImportsIntoAFreshInstallation holds FR-PROJ-9's move: a project's backup registers in
// another installation with its ID, its name, and its records, and the installation indexes its
// repositories.
func TestAProjectsBackupImportsIntoAFreshInstallation(t *testing.T) {
	from := openState(t, t.TempDir())
	acme, store := createProject(t, from, "acme-id", "acme")
	newWorkOrder(t, store, acme.ID, "wo-1", "Ship the invoice export")
	detect(t, store, acme.ID, "r1", "git@example.com:acme/invoices.git")
	backup := filepath.Join(t.TempDir(), "acme.db")
	if _, err := from.Backup(t.Context(), acme, backup); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	to, home, err := sqlite.Upgrade(t.Context(), dir, "ledger.db", ledger.Workspace{ID: "default", Name: "Default"}, member, nil)
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
	if imported.ID != acme.ID || imported.Name != "acme" || imported.State != ledger.ProjectActive || imported.Database != "projects/acme-id.db" {
		t.Errorf("imported %+v", imported)
	}
	opened, err := to.Open(t.Context(), imported)
	if err != nil {
		t.Fatal(err)
	}
	if got := workOrderIDs(t, opened, imported.ID); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("work orders after the move: %v", got)
	}
	index, err := to.Index(t.Context())
	if err != nil || len(index) != 1 || index[0].Identity != "example.com/acme/invoices" || index[0].Project != acme.ID {
		t.Errorf("index %+v, %v", index, err)
	}
	if got := installationEvents(t, to, acme.ID); !slices.Equal(got, []string{ledger.EventProjectImported + " by member-1"}) {
		t.Errorf("events %v", got)
	}
	if _, err := to.Import(t.Context(), backup, "other", member); !errors.Is(err, ledger.ErrProjectExists) {
		t.Errorf("the same ID again: %v", err)
	}
}

// TestAnImportIsRefusedWhereAnotherProjectStands holds import's refusals: a name another project holds,
// and a repository another project holds, which the refusal names.
func TestAnImportIsRefusedWhereAnotherProjectStands(t *testing.T) {
	from := openState(t, t.TempDir())
	acme, store := createProject(t, from, "acme-id", "acme")
	detect(t, store, acme.ID, "r1", "https://example.com/acme/invoices")
	backup := filepath.Join(t.TempDir(), "acme.db")
	if _, err := from.Backup(t.Context(), acme, backup); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	to := openState(t, dir)
	_, holder := createProject(t, to, "billing-id", "billing")
	detect(t, holder, "billing-id", "r9", "git@example.com:acme/invoices.git")
	createProject(t, to, "named-id", "acme")
	_, err := to.Import(t.Context(), backup, "", member)
	if !errors.Is(err, ledger.ErrProjectExists) {
		t.Errorf("a name another project holds: %v", err)
	}
	_, err = to.Import(t.Context(), backup, "acme-two", member)
	if !errors.Is(err, ledger.ErrRepositoryHeld) || !strings.Contains(err.Error(), "billing") {
		t.Errorf("a repository another project holds: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "acme-id.db")); !os.IsNotExist(err) {
		t.Errorf("a refused import left its file: %v", err)
	}
	if _, err := to.Import(t.Context(), filepath.Join(t.TempDir(), "absent.db"), "x", member); err == nil {
		t.Error("a file that is not there imported")
	}
}

// A backup an earlier build made may name its project in a way no display name may take now. It still imports,
// by the name it is given, with no display name.
func TestABackupWhoseNameNoListCanShowImportsWithNoDisplayName(t *testing.T) {
	from := openState(t, t.TempDir())
	acme, _ := createProject(t, from, "acme-id", "acme")
	backup := filepath.Join(t.TempDir(), "acme.db")
	if _, err := from.Backup(t.Context(), acme, backup); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.OpenDB(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspaces SET name = ?`, strings.Repeat("Acme ", 30)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	imported, err := openState(t, t.TempDir()).Import(t.Context(), backup, "acme", member)
	if err != nil || imported.Name != "acme" || imported.DisplayName != "" {
		t.Errorf("imported %+v: %v", imported, err)
	}
}

// detect records a repository in a project, as intake does.
func detect(t *testing.T, store ports.Store, scope ledger.WorkspaceID, id, remote string) {
	t.Helper()
	_, err := control.NewRepositories(store).Detect(t.Context(), scope, control.DetectedRepository{ID: id, Remote: remote, DefaultBranch: "main"}, member)
	if err != nil {
		t.Fatal(err)
	}
}
