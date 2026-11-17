//go:build postgres

package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

var _ ports.Projects = (*postgres.State)(nil)

// installationEvents lists the types of the installation's events about one project, in order.
func installationEvents(t *testing.T, state *postgres.State, id ledger.WorkspaceID) []string {
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

func createProject(t *testing.T, state *postgres.State, id ledger.WorkspaceID, name string) (ledger.Project, ports.Store) {
	t.Helper()
	project, err := state.Create(t.Context(), ledger.Project{ID: id, Name: name}, member)
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), project)
	if err != nil {
		t.Fatal(err)
	}
	return project, store
}

func newWorkOrder(t *testing.T, store ports.Store, scope ledger.WorkspaceID, id, title string) {
	t.Helper()
	_, err := control.NewWorkOrders(store).Create(t.Context(), scope, workorders.WorkOrder{
		ID: id, Kind: workorders.KindChange, Title: title, MaxAttempts: 3,
	}, member)
	if err != nil {
		t.Fatal(err)
	}
}

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

func schemaExists(t *testing.T, db *sql.DB, schema string) bool {
	t.Helper()
	return scalar(t, db, `SELECT count(*) FROM pg_namespace WHERE nspname = $1`, schema) == "1"
}

// TestEachProjectKeepsItsLedgerInASchemaOfItsOwn holds that creating a project makes and migrates its
// schema, registers it, and leaves the installation's and the public schema alone.
func TestEachProjectKeepsItsLedgerInASchemaOfItsOwn(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, store := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	if alpha.Database != "project_pa" || beta.Database != "project_pb" {
		t.Errorf("databases %q and %q", alpha.Database, beta.Database)
	}
	want, err := (postgres.Dialect{}).Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{alpha.Database, beta.Database} {
		if got := scalar(t, db, fmt.Sprintf(`SELECT count(*) FROM %s.schema_migrations`, schema)); got != fmt.Sprint(len(want)) {
			t.Errorf("%s applied %s migrations, want %d", schema, got, len(want))
		}
	}
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")
	newWorkOrder(t, betaStore, beta.ID, "wo-2", "Ship the invoice export")
	if got := workOrderIDs(t, store, alpha.ID); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("alpha's work orders: %v", got)
	}
	if got := scalar(t, db, `SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`); got != "0" {
		t.Errorf("the public schema holds %s tables", got)
	}
	if again, err := state.Open(t.Context(), alpha); err != nil || again != store {
		t.Errorf("a second open of a project returns another Store: %v", err)
	}
}

// TestProjectWritesNeverStarveTheInstallation holds that a project's transaction, which reads the shared
// records through a transaction of the installation's, cannot wait for a connection the other writers
// hold: with a pool of two connections and more writers than that, every write still finishes.
func TestProjectWritesNeverStarveTheInstallation(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{MaxConns: 2})
	alpha, store := createProject(t, state, "pa", "alpha")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := control.NewWorkOrders(store).Create(ctx, alpha.ID, workorders.WorkOrder{
				ID: fmt.Sprintf("wo-%d", i), Kind: workorders.KindChange, Title: "T", MaxAttempts: 3,
			}, member)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

// TestAProjectCreatedWithNoIDGetsOneOfItsOwn is what project create relies on: an ID no other installation
// gives.
func TestAProjectCreatedWithNoIDGetsOneOfItsOwn(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
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

func TestAProjectIDThatIsNoSchemaNameIsRefused(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	for _, id := range []ledger.WorkspaceID{"has space", `quo"te`, "dot.ted", ledger.WorkspaceID(strings.Repeat("a", 56))} {
		if _, err := state.Create(t.Context(), ledger.Project{ID: id, Name: "alpha"}, member); !errors.Is(err, ledger.ErrInvalidProject) {
			t.Errorf("ID %q: %v", id, err)
		}
	}
	if projects, err := state.List(t.Context()); err != nil || len(projects) != 0 {
		t.Errorf("projects %+v, %v", projects, err)
	}
}

func TestARenameKeepsEverythingButTheName(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	alpha, store := createProject(t, state, "pa", "alpha")
	createProject(t, state, "pb", "beta")
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")

	renamed, err := state.Rename(t.Context(), alpha.ID, "gamma", "", member)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ID != alpha.ID || renamed.Database != alpha.Database || renamed.Name != "gamma" {
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
	want := []string{ledger.EventProjectCreated + " by member-1", ledger.EventProjectRenamed + " by member-1"}
	if got := installationEvents(t, state, alpha.ID); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}

// A rename keeps the name the project's own schema holds the same as the one it is shown by, which the
// assistant reads: the display name, or else the name. A rename that changes neither writes nothing there.
func TestARenameKeepsTheLedgersOwnNameCurrent(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	alpha, store := createProject(t, state, "pa", "alpha")
	ledgerName := func() (string, []string) {
		t.Helper()
		var name string
		var types []string
		err := store.Read(t.Context(), alpha.ID, func(tx ports.ReadTx) error {
			workspace, err := tx.Workspace()
			if err != nil {
				return err
			}
			name = workspace.Name
			events, err := tx.EventsAbout(ledger.Subject{Kind: "workspace", ID: string(alpha.ID)})
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
	for _, step := range []struct{ name, displayName, want string }{
		{"gamma", "", "gamma"},
		{"gamma", "Gamma docs", "Gamma docs"},
		{"delta", "", "Gamma docs"},
	} {
		if _, err := state.Rename(t.Context(), alpha.ID, step.name, step.displayName, member); err != nil {
			t.Fatal(err)
		}
		if got, _ := ledgerName(); got != step.want {
			t.Errorf("after a rename to %q, %q, the ledger names the project %q, want %q", step.name, step.displayName, got, step.want)
		}
	}
	want := []string{"workspace.created", sqlstore.EventWorkspaceRenamed, sqlstore.EventWorkspaceRenamed}
	if _, got := ledgerName(); !slices.Equal(got, want) {
		t.Errorf("the ledger's events about the project %v, want %v", got, want)
	}
}

// TestArchivingKeepsTheSchema holds that archiving and restoring change the registry's state and
// nothing in the project's schema.
func TestArchivingKeepsTheSchema(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, store := createProject(t, state, "pa", "alpha")
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")
	archived, err := state.SetState(t.Context(), alpha.ID, ledger.ProjectArchived, member)
	if err != nil || archived.State != ledger.ProjectArchived {
		t.Fatalf("archived %+v, %v", archived, err)
	}
	if !schemaExists(t, db, alpha.Database) || scalar(t, db, `SELECT count(*) FROM project_pa.work_orders`) != "1" {
		t.Error("archiving changed the project's schema")
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

// TestRemovingAProjectDropsItsSchema holds remove: the registry and the repository index forget the
// project, its schema goes, the event stays, and another project is untouched.
func TestRemovingAProjectDropsItsSchema(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
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
	if schemaExists(t, db, alpha.Database) {
		t.Error("the schema is still there")
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
	if _, err := state.Create(t.Context(), ledger.Project{ID: "pa", Name: "alpha"}, member); err != nil {
		t.Errorf("the name and the ID are free again: %v", err)
	}
}

// TestRemoveNeverDropsASchemaThatIsNotTheProjects holds that a project record that names another schema
// cannot make remove drop it.
func TestRemoveNeverDropsASchemaThatIsNotTheProjects(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, _ := createProject(t, state, "pa", "alpha")
	for _, database := range []string{postgres.InstallationSchema, "public", "project_other"} {
		forged := alpha
		forged.Database = database
		if err := state.Remove(t.Context(), forged, member); !errors.Is(err, ledger.ErrInvalidProject) {
			t.Errorf("database %q: %v", database, err)
		}
	}
	if !schemaExists(t, db, postgres.InstallationSchema) || !schemaExists(t, db, alpha.Database) {
		t.Error("a schema went")
	}
	if _, err := state.Named(t.Context(), "alpha"); err != nil {
		t.Errorf("the registry forgot the project: %v", err)
	}
}

// registerLegacy registers a project the way an earlier build did, on a schema that keeps the capitals of
// its ID, and makes that schema.
func registerLegacy(t *testing.T, state *postgres.State, id ledger.WorkspaceID, name string) ledger.Project {
	t.Helper()
	db := state.SQLDB()
	defer db.Close()
	schema := "project_" + string(id)
	if _, err := db.ExecContext(t.Context(), `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	legacy, _, err := state.Installation.CreateProject(t.Context(),
		ledger.Project{ID: id, Name: name, Database: schema, State: ledger.ProjectActive}, member)
	if err != nil {
		t.Fatal(err)
	}
	return legacy
}

// TestRemoveChecksTheRecordAgainstTheRegistry holds that a stale or forged record, which names a schema its
// ID could give but not the one the registry keeps the project on, drops no project's schema, whether the
// registry still holds the project or has forgotten it.
func TestRemoveChecksTheRecordAgainstTheRegistry(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, store := createProject(t, state, "pa", "alpha")
	newWorkOrder(t, store, alpha.ID, "wo-1", "Add the retry budget")
	// A project an earlier build registered beside "pa", whose ID differs from it only in case.
	legacy := registerLegacy(t, state, "PA", "legacy")
	forged := legacy
	forged.Database = alpha.Database

	if err := state.Remove(t.Context(), forged, member); !errors.Is(err, ledger.ErrInvalidProject) {
		t.Errorf("a record that names another project's schema: %v", err)
	}
	for _, name := range []string{"alpha", "legacy"} {
		if _, err := state.Named(t.Context(), name); err != nil {
			t.Errorf("the registry forgot %s: %v", name, err)
		}
	}
	if !schemaExists(t, db, alpha.Database) || !schemaExists(t, db, legacy.Database) {
		t.Error("a schema went")
	}

	// With the legacy project gone from the registry, the same record names a schema the registry gives
	// to another project.
	if err := state.Remove(t.Context(), legacy, member); err != nil {
		t.Fatal(err)
	}
	if err := state.Remove(t.Context(), forged, member); !errors.Is(err, ledger.ErrInvalidProject) {
		t.Errorf("a record of a forgotten project that names another project's schema: %v", err)
	}
	if !schemaExists(t, db, alpha.Database) {
		t.Error("the schema of a registered project went")
	}
	if got := workOrderIDs(t, store, alpha.ID); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("alpha's work orders: %v", got)
	}
}

// TestCreateRepeatsAfterACrashAtAnyStep holds the design's lifecycle: a crash after each step of creating a
// project leaves nothing the next attempt cannot finish, with one registry row, one workspace, one
// event, and each migration applied once.
func TestCreateRepeatsAfterACrashAtAnyStep(t *testing.T) {
	want, err := (postgres.Dialect{}).Migrations()
	if err != nil {
		t.Fatal(err)
	}
	for done := range postgres.CreateSteps + 1 {
		t.Run(fmt.Sprintf("after %d steps", done), func(t *testing.T) {
			state := openState(t, freshDatabase(t), postgres.Options{})
			db := state.SQLDB()
			defer db.Close()
			project := ledger.Project{ID: "pa", Name: "alpha"}
			if err := state.CreateUpTo(t.Context(), project, member, done); err != nil {
				t.Fatal(err)
			}
			if projects, err := state.List(t.Context()); err != nil || (done < postgres.CreateSteps && len(projects) != 0) {
				t.Fatalf("a crash before the registry step left %+v, %v", projects, err)
			}
			created, err := state.Create(t.Context(), project, member)
			if err != nil {
				t.Fatal(err)
			}
			if created.Database != "project_pa" || created.State != ledger.ProjectActive {
				t.Errorf("created %+v", created)
			}
			if got := scalar(t, db, `SELECT count(*) FROM project_pa.schema_migrations`); got != fmt.Sprint(len(want)) {
				t.Errorf("%s migrations applied, want %d", got, len(want))
			}
			if got := scalar(t, db, `SELECT count(*) FROM project_pa.workspaces`); got != "1" {
				t.Errorf("%s workspaces", got)
			}
			if got := installationEvents(t, state, "pa"); !slices.Equal(got, []string{ledger.EventProjectCreated + " by member-1"}) {
				t.Errorf("events %v", got)
			}
			if projects, err := state.List(t.Context()); err != nil || len(projects) != 1 {
				t.Errorf("projects %+v, %v", projects, err)
			}
		})
	}
}

// refuseRegistrations makes the registry refuse every project, as a failed registry step does, and returns
// what lifts the refusal.
func refuseRegistrations(t *testing.T, db *sql.DB) (lift func()) {
	t.Helper()
	for _, statement := range []string{
		`CREATE FUNCTION installation.refuse_project() RETURNS trigger LANGUAGE plpgsql
  AS $$ BEGIN RAISE EXCEPTION 'the registry refuses the project'; END $$`,
		`CREATE TRIGGER refuse_project BEFORE INSERT ON installation.projects
  FOR EACH ROW EXECUTE FUNCTION installation.refuse_project()`,
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		if _, err := db.ExecContext(t.Context(), `DROP TRIGGER refuse_project ON installation.projects`); err != nil {
			t.Fatal(err)
		}
	}
}

// TestACreateThatFailsLeavesNoSchemaItMade holds that a Create that fails after it made the schema, at the
// registry's step, drops that schema before it returns, so the failure leaves nothing behind. A schema it
// did not make, one a crashed Create left for a retry, stays.
func TestACreateThatFailsLeavesNoSchemaItMade(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	lift := refuseRegistrations(t, db)

	if _, err := state.Create(t.Context(), ledger.Project{ID: "pa", Name: "alpha"}, member); err == nil {
		t.Fatal("a project was registered although the registry refused it")
	}
	if schemaExists(t, db, "project_pa") {
		t.Error("the failed Create left its schema")
	}

	// A crashed Create left this schema for a retry, and a failure of the retry does not take it away.
	if err := state.CreateUpTo(t.Context(), ledger.Project{ID: "pb", Name: "beta"}, member, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Create(t.Context(), ledger.Project{ID: "pb", Name: "beta"}, member); err == nil {
		t.Fatal("a project was registered although the registry refused it")
	}
	if !schemaExists(t, db, "project_pb") {
		t.Error("a Create dropped a schema an earlier one made")
	}

	lift()
	for _, id := range []ledger.WorkspaceID{"pa", "pb"} {
		if _, err := state.Create(t.Context(), ledger.Project{ID: id, Name: "project-" + string(id)}, member); err != nil {
			t.Errorf("Create of %s after the refusal ended: %v", id, err)
		}
	}
}

// TestRemoveRepeatsAfterACrashAtAnyStep holds the other direction: the registry decides, so a crash after
// the registry forgot a project leaves a schema no project names, and a second remove drops it.
func TestRemoveRepeatsAfterACrashAtAnyStep(t *testing.T) {
	for done := range postgres.RemoveSteps + 1 {
		t.Run(fmt.Sprintf("after %d steps", done), func(t *testing.T) {
			state := openState(t, freshDatabase(t), postgres.Options{})
			db := state.SQLDB()
			defer db.Close()
			alpha, _ := createProject(t, state, "pa", "alpha")
			if err := state.RemoveUpTo(t.Context(), alpha, member, done); err != nil {
				t.Fatal(err)
			}
			if done == 1 && !schemaExists(t, db, alpha.Database) {
				t.Fatal("the schema went with the registry step")
			}
			if _, err := state.Named(t.Context(), "alpha"); (err == nil) != (done == 0) {
				t.Fatalf("after %d steps the registry answers %v", done, err)
			}
			err := state.Remove(t.Context(), alpha, member)
			if done == postgres.RemoveSteps {
				if !errors.Is(err, ports.ErrNotFound) {
					t.Fatalf("removing a project that is already gone: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if schemaExists(t, db, alpha.Database) {
				t.Error("the schema is still there")
			}
			if got := installationEvents(t, state, alpha.ID); len(got) != 2 {
				t.Errorf("events %v", got)
			}
			if err := state.Remove(t.Context(), alpha, member); !errors.Is(err, ports.ErrNotFound) {
				t.Errorf("removing a project that is gone: %v", err)
			}
		})
	}
}

// TestOpenRefusesAProjectTheRegistryDoesNotHold holds that a record nobody registered, or that a removal has
// forgotten, opens no Store and makes no schema.
func TestOpenRefusesAProjectTheRegistryDoesNotHold(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	ghost := ledger.Project{ID: "pz", Name: "zeta", Database: "project_pz", State: ledger.ProjectActive}
	if _, err := state.Open(t.Context(), ghost); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Open of a project no registry holds: %v", err)
	}
	if schemaExists(t, db, ghost.Database) {
		t.Error("Open made the schema of a project no registry holds")
	}
}

// TestRemoveAndOpenNeverLeaveASchemaBehind holds the interleaving of a removal with an Open of the same
// project: the whole removal, the registry's step and the drop, waits for the migration lock, as every
// other change to a schema does, so nothing sees a project the registry forgot whose schema still stands,
// and an Open that runs after the removal is refused, so it never makes the schema again.
func TestRemoveAndOpenNeverLeaveASchemaBehind(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	alpha, _ := createProject(t, state, "pa", "alpha")
	// A second state is a second process, whose cache starts empty.
	other := openState(t, url, postgres.Options{})
	release := holdMigrationLock(t, url)

	removed := make(chan error, 1)
	go func() { removed <- state.Remove(t.Context(), alpha, member) }()
	waitForLockWaiter(t, state)
	opened := opening(t.Context(), other, alpha)
	select {
	case err := <-removed:
		t.Fatalf("Remove finished while another session held the migration lock: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := state.Named(t.Context(), "alpha"); err != nil {
		t.Errorf("the registry forgot the project before the removal held the migration lock: %v", err)
	}
	if !schemaExists(t, db, alpha.Database) {
		t.Error("the schema went before the removal held the migration lock")
	}
	release()
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	// Whichever of the two takes the lock first, the removal ends with no schema and no project.
	if err := <-opened; err != nil && !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Open that raced the removal: %v", err)
	}
	if schemaExists(t, db, alpha.Database) {
		t.Error("the schema is there after the removal")
	}
	if _, err := state.Open(t.Context(), alpha); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("Open of a project that was removed: %v", err)
	}
}

// TestADropThatCannotTakeItsLocksGivesUp holds that the drop of a schema waits for the locks of its tables
// for a bounded time. A session with a transaction open on a table of the project keeps the drop from
// taking its lock, and the drop runs under the migration lock, so without a bound every other caller waits
// behind it. The registry has forgotten the project by then, as after a crash, and a repeat of Remove
// finishes the drop once the session is gone.
func TestADropThatCannotTakeItsLocksGivesUp(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	state.SetDropWait(300 * time.Millisecond)
	db := state.SQLDB()
	defer db.Close()
	alpha, _ := createProject(t, state, "pa", "alpha")
	_, session := otherSession(t, url)
	for _, statement := range []string{`BEGIN`, `LOCK TABLE project_pa.work_orders IN ACCESS SHARE MODE`} {
		if _, err := session.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	start := time.Now()
	err := state.Remove(ctx, alpha, member)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("Remove with a table of the project in use ended with %v, want the failure of the drop", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("the drop waited %s for a lock", took)
	}
	if !schemaExists(t, db, alpha.Database) {
		t.Error("the schema went although a session held a lock on its table")
	}
	if _, err := state.Named(t.Context(), "alpha"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the registry still names the project: %v", err)
	}

	if _, err := session.ExecContext(t.Context(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	if err := state.Remove(t.Context(), alpha, member); err != nil {
		t.Fatalf("the repeat of Remove: %v", err)
	}
	if schemaExists(t, db, alpha.Database) {
		t.Error("the schema is still there after the repeat")
	}
}

func TestImportAndBackupAreNotThereYet(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	alpha, _ := createProject(t, state, "pa", "alpha")
	if _, err := state.Import(t.Context(), "backup.db", "alpha", member); !errors.Is(err, postgres.ErrImportUnavailable) {
		t.Errorf("import: %v", err)
	}
	if _, err := state.Backup(t.Context(), alpha, "out.db"); !errors.Is(err, ports.ErrNoBackup) {
		t.Errorf("backup: %v", err)
	}
	if _, err := state.BackupInstallation(t.Context(), "out.db"); !errors.Is(err, ports.ErrNoBackup) {
		t.Errorf("installation backup: %v", err)
	}
}
