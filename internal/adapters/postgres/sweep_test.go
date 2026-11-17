//go:build postgres

package postgres_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/postgres"
	"ropeshift.com/ropeshift/internal/ledger"
)

// projectSchemas lists the schemas that carry the prefix of a project's, in name order.
func projectSchemas(t testing.TB, state *postgres.State) []string {
	t.Helper()
	db := state.SQLDB()
	defer db.Close()
	rows, err := db.QueryContext(t.Context(), `SELECT nspname FROM pg_namespace WHERE left(nspname, 8) = 'project_' ORDER BY nspname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestOpeningAStateDropsTheSchemasNoProjectNames holds the answer to a crash in the middle of Create with
// an ID the state made: the next process to open the installation drops the schema, and leaves every
// registered project's schema and every schema that is not a project's.
func TestOpeningAStateDropsTheSchemasNoProjectNames(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	createProject(t, state, "pa", "alpha")
	// A crash after the schema is made and migrated, in a Create that gave itself an ID.
	if err := state.CreateUpTo(t.Context(), ledger.Project{Name: "ghost"}, member, 2); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mine", "project", "projects_old"} {
		if _, err := db.ExecContext(t.Context(), `CREATE SCHEMA `+name); err != nil {
			t.Fatal(err)
		}
	}
	if got := projectSchemas(t, state); len(got) != 2 {
		t.Fatalf("schemas with a project's prefix before the sweep: %v", got)
	}

	next := openState(t, url, postgres.Options{})
	if got := projectSchemas(t, next); !slices.Equal(got, []string{"project_pa"}) {
		t.Errorf("schemas with a project's prefix after the sweep: %v", got)
	}
	for _, name := range []string{"mine", "project", "projects_old", postgres.InstallationSchema} {
		if !schemaExists(t, db, name) {
			t.Errorf("the sweep dropped %s", name)
		}
	}
	if projects, err := next.List(t.Context()); err != nil || len(projects) != 1 {
		t.Errorf("projects %+v, %v", projects, err)
	}
}

// TestConcurrentCreatesOfOneNameLeaveOneSchema holds that the creators that lose a race on a name leave no
// schema behind, although each gave itself an ID.
func TestConcurrentCreatesOfOneNameLeaveOneSchema(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	const callers = 8
	var wg sync.WaitGroup
	made := make([]ledger.Project, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			made[i], errs[i] = state.Create(context.Background(), ledger.Project{Name: "alpha"}, member)
		}()
	}
	wg.Wait()
	var winners []ledger.Project
	for i, err := range errs {
		switch {
		case err == nil:
			winners = append(winners, made[i])
		case !errors.Is(err, ledger.ErrProjectExists):
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("%d creators won", len(winners))
	}
	if got := projectSchemas(t, state); !slices.Equal(got, []string{winners[0].Database}) {
		t.Errorf("schemas %v, want only %s", got, winners[0].Database)
	}
}

// TestSchemaNamesAreLowercase holds that a project whose ID has capitals, as a generated one does, gets a
// schema whose name needs no quoting.
func TestSchemaNamesAreLowercase(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	created, err := state.Create(t.Context(), ledger.Project{ID: "01JABCDEFGHJKMNPQRSTVWXYZ0", Name: "alpha"}, member)
	if err != nil {
		t.Fatal(err)
	}
	if created.Database != "project_01jabcdefghjkmnpqrstvwxyz0" {
		t.Errorf("database %q", created.Database)
	}
	if got := projectSchemas(t, state); !slices.Equal(got, []string{created.Database}) {
		t.Errorf("schemas %v", got)
	}
	generated, err := state.Create(t.Context(), ledger.Project{Name: "beta"}, member)
	if err != nil {
		t.Fatal(err)
	}
	if want := "project_" + strings.ToLower(string(generated.ID)); generated.Database != want {
		t.Errorf("database %q, want %q", generated.Database, want)
	}
}

// TestTwoIDsThatDifferInCaseShareNoSchema holds that an ID whose lowercase name another project holds is
// refused before the schema is touched.
func TestTwoIDsThatDifferInCaseShareNoSchema(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	createProject(t, state, "pa", "alpha")
	if _, err := state.Create(t.Context(), ledger.Project{ID: "PA", Name: "beta"}, member); !errors.Is(err, ledger.ErrProjectExists) {
		t.Errorf("an ID that differs in case: %v", err)
	}
	if got := scalar(t, db, `SELECT count(*) FROM project_pa.workspaces`); got != "1" {
		t.Errorf("project_pa holds %s workspaces", got)
	}
}

// TestAnIDThatDiffersInCaseFromALegacyProjectIsRefused holds that a new ID that differs only in case from
// the ID of a project an earlier build registered on a schema with its capitals is refused before any
// schema is made, although the two schema names differ.
func TestAnIDThatDiffersInCaseFromALegacyProjectIsRefused(t *testing.T) {
	state := openState(t, freshDatabase(t), postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	registerLegacy(t, state, "PA", "legacy")
	if _, err := state.Create(t.Context(), ledger.Project{ID: "pa", Name: "alpha"}, member); !errors.Is(err, ledger.ErrProjectExists) {
		t.Errorf("an ID that differs in case from a legacy project's: %v", err)
	}
	if schemaExists(t, db, "project_pa") {
		t.Error("the refused Create made a schema")
	}
	if projects, err := state.List(t.Context()); err != nil || len(projects) != 1 {
		t.Errorf("projects %+v, %v", projects, err)
	}
}

// TestAProjectOnAnUppercaseSchemaStillOpens holds that a project an earlier build registered, whose schema
// keeps the capitals of its ID, opens, survives the sweep, and is removed with its schema.
func TestAProjectOnAnUppercaseSchemaStillOpens(t *testing.T) {
	url := freshDatabase(t)
	state := openState(t, url, postgres.Options{})
	db := state.SQLDB()
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `CREATE SCHEMA "project_PA"`); err != nil {
		t.Fatal(err)
	}
	legacy, _, err := state.Installation.CreateProject(t.Context(),
		ledger.Project{ID: "PA", Name: "legacy", Database: "project_PA", State: ledger.ProjectActive}, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Open(t.Context(), legacy); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, db, `SELECT count(*) > 0 FROM "project_PA".schema_migrations`); got != "true" {
		t.Error("the project's schema was not migrated")
	}

	next := openState(t, url, postgres.Options{})
	if !schemaExists(t, db, "project_PA") {
		t.Fatal("the sweep dropped a registered project's schema")
	}
	if err := next.Remove(t.Context(), legacy, member); err != nil {
		t.Fatal(err)
	}
	if schemaExists(t, db, "project_PA") {
		t.Error("the schema is still there")
	}
}
