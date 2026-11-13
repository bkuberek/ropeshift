package server_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

func createWork(t *testing.T, client *api.ProjectClient, id, title string) {
	t.Helper()
	_, err := client.CreateWorkOrder(t.Context(), api.CreateWorkOrder{ID: id, Title: title, Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}}})
	if err != nil {
		t.Fatal(err)
	}
}

func workIDs(t *testing.T, client *api.ProjectClient) []string {
	t.Helper()
	rows, err := client.WorkOrders(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	slices.Sort(ids)
	return ids
}

// TestTwoProjectsKeepTheirRecordsApart holds FR-PROJ-1 and FR-PROJ-5 through the API: each project's
// routes read and write its own database, an ID in one project names nothing in the other, and a
// ladder lookup in one project finds none of the other's decisions.
func TestTwoProjectsKeepTheirRecordsApart(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "acme")
	_, _, home := in.In(t, apitest.ProjectName)
	_, _, acme := in.In(t, "acme")
	createWork(t, home, "wo-home", "Add the retry budget")
	createWork(t, acme, "wo-acme", "Ship the invoice export")
	createWork(t, home, "wo-same", "One title")
	createWork(t, acme, "wo-same", "Another title")

	if got := workIDs(t, home); !slices.Equal(got, []string{"wo-home", "wo-same"}) {
		t.Errorf("the first project lists %v", got)
	}
	if got := workIDs(t, acme); !slices.Equal(got, []string{"wo-acme", "wo-same"}) {
		t.Errorf("acme lists %v", got)
	}
	_, err := acme.WorkOrder(t.Context(), "wo-home")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	same, err := acme.WorkOrder(t.Context(), "wo-same")
	if err != nil || same.Title != "Another title" {
		t.Errorf("acme's wo-same reads %+v, %v", same, err)
	}

	_, err = home.RecordDecision(t.Context(), api.RecordDecision{
		ID: "decision-retry", Status: questions.DecisionDecided, Statement: "Retry the invoice upload twice.",
		Rationale: "It covers the outage.", Scope: "retries", Categories: []string{questions.CategoryProduct}, Reversibility: questions.Reversible,
	})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := home.Candidates(t.Context(), "retry invoice upload", []string{"decision"}, 10); err != nil || len(found) == 0 {
		t.Fatalf("the first project's own lookup found %v, %v", found, err)
	}
	if found, err := acme.Candidates(t.Context(), "retry invoice upload", []string{"decision"}, 10); err != nil || len(found) != 0 {
		t.Errorf("acme's lookup found %v, %v", found, err)
	}
	if decisions, err := acme.Decisions(t.Context(), api.RecordFilter{}); err != nil || len(decisions) != 0 {
		t.Errorf("acme lists decisions %v, %v", decisions, err)
	}
	_, err = acme.Decision(t.Context(), "decision-retry")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// TestEachProjectHasADatabaseOfItsOwn holds FR-PROJ-1 on the disk: the registry names a file for each
// project, and the files are different.
func TestEachProjectHasADatabaseOfItsOwn(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "acme", "beta")
	projects, err := in.State.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range projects {
		if seen[p.Database] {
			t.Errorf("two projects share %s", p.Database)
		}
		seen[p.Database] = true
		if _, err := os.Stat(filepath.Join(in.State.Dir, filepath.FromSlash(p.Database))); err != nil {
			t.Errorf("project %s: %v", p.Name, err)
		}
	}
	if len(seen) != 3 {
		t.Errorf("databases %v", seen)
	}
}

// TestTheRegistryListsEveryProject is what a client reads to choose a project: each one with its state and
// the repositories it holds, and one project's record with its database.
func TestTheRegistryListsEveryProject(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "acme")
	_, err := in.State.Installation.IndexRepositories(t.Context(), []ledger.IndexedRepository{
		{Identity: "example.com/acme/invoices", Project: "acme-id", Repository: "r1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	projects, err := in.Client.Projects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].Name != ledger.DefaultProjectName || projects[1].Name != "acme" {
		t.Fatalf("projects %+v", projects)
	}
	if acme := projects[1]; acme.ID != "acme-id" || acme.State != ledger.ProjectActive || !slices.Equal(acme.Repositories, []string{"example.com/acme/invoices"}) {
		t.Errorf("acme reads %+v", acme)
	}
	detail, err := in.Client.ProjectRecord(t.Context(), "acme")
	if err != nil || detail.Database != "projects/acme-id.db" || detail.Name != "acme" {
		t.Errorf("acme's record %+v, %v", detail, err)
	}
	_, err = in.Client.ProjectRecord(t.Context(), "nowhere")
	refusedWith(t, err, http.StatusNotFound, api.CodeNoProject)
}

// TestAPathThatNamesNoProjectIsRefused says which name it did not find, apart from a record that is not
// there.
func TestAPathThatNamesNoProjectIsRefused(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t)
	_, err := in.Client.Project("nowhere").WorkOrders(t.Context(), "")
	refusedWith(t, err, http.StatusNotFound, api.CodeNoProject)
	_, err = in.Client.WorkOrder(t.Context(), "no such/work order")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// TestARouteWithNoProjectIsGone holds that every surface names a project: a control plane answers a
// route with no project as a route it does not have, and marks nothing deprecated.
func TestARouteWithNoProjectIsGone(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	err := d.Client.Do(t.Context(), http.MethodGet, "/v1/work-orders", nil, nil)
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusNotFound {
		t.Fatalf("got %v", err)
	}
	in := apitest.StartInstallation(t)
	createWork(t, d.Client, "wo-1", "Add the retry budget")
	for _, socket := range []string{d.Socket, in.Socket} {
		response, err := apitest.RawClient(socket).Get("http://ropeshift/v1/work-orders/wo-1")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNotFound || response.Header.Get("Deprecation") != "" {
			t.Errorf("status %d, Deprecation %q", response.StatusCode, response.Header.Get("Deprecation"))
		}
	}
}

// TestAWorkerOnProtocolOneIsRefused holds that the plane protocol before projects is no longer served: a
// worker that speaks it is refused at its session with 426 and the code `protocol`, and the message says
// to upgrade the worker.
func TestAWorkerOnProtocolOneIsRefused(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	token, err := d.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: "worker-old", Name: "old"})
	if err != nil {
		t.Fatal(err)
	}
	old := d.Client.WithCredential(credential.Credential).Client
	_, err = old.OpenSession(t.Context(), "worker-old", ledger.SessionRequest{Protocol: 1})
	refusedWith(t, err, http.StatusUpgradeRequired, api.CodeProtocol)
	var refused *api.Error
	if !errors.As(err, &refused) {
		t.Fatal(err)
	}
	if refused.Detail.Worker != 1 || refused.Detail.Oldest != ledger.ProtocolVersion || refused.Detail.Newest != ledger.ProtocolVersion {
		t.Errorf("the refusal names protocols %d, %d, %d", refused.Detail.Worker, refused.Detail.Oldest, refused.Detail.Newest)
	}
	for _, want := range []string{"no longer served", "upgrade the worker"} {
		if !strings.Contains(refused.Detail.Message, want) {
			t.Errorf("message %q does not say %q", refused.Detail.Message, want)
		}
	}
	if _, err := old.OpenSession(t.Context(), "worker-old", ledger.SessionRequest{Protocol: ledger.ProtocolVersion}); err != nil {
		t.Errorf("a worker on the current protocol: %v", err)
	}
}

// TestEachProjectBacksUpOnItsOwn holds FR-PROJ-9 through the API: a project's backup holds that project's
// records, the installation's holds the member, and each needs a member's session.
func TestEachProjectBacksUpOnItsOwn(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "acme")
	_, _, home := in.In(t, apitest.ProjectName)
	_, _, acme := in.In(t, "acme")
	createWork(t, home, "wo-home", "Add the retry budget")
	createWork(t, acme, "wo-acme", "Ship the invoice export")
	dir := t.TempDir()

	copied, err := acme.Backup(t.Context(), api.BackupRequest{Path: filepath.Join(dir, "acme.db")})
	if err != nil || copied.Bytes == 0 {
		t.Fatalf("acme's backup: %+v, %v", copied, err)
	}
	if rows := rowsIn(t, copied.Path, "work_orders"); !slices.Equal(rows, []string{"wo-acme"}) {
		t.Errorf("acme's backup holds work orders %v", rows)
	}
	installation, err := in.Client.BackupInstallation(t.Context(), api.BackupRequest{Path: filepath.Join(dir, "installation.db")})
	if err != nil {
		t.Fatal(err)
	}
	if rows := rowsIn(t, installation.Path, "projects"); !slices.Equal(rows, []string{"acme-id", string(apitest.Scope)}) {
		t.Errorf("the installation's backup holds projects %v", rows)
	}

	anonymous := api.NewUnixClient(in.Socket)
	defer anonymous.Close()
	_, err = anonymous.Project("acme").Backup(t.Context(), api.BackupRequest{Path: filepath.Join(dir, "again.db")})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	_, err = anonymous.BackupInstallation(t.Context(), api.BackupRequest{Path: filepath.Join(dir, "again.db")})
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
}

// rowsIn lists the IDs a table of a SQLite file holds.
func rowsIn(t *testing.T, file, table string) []string {
	t.Helper()
	db, err := sqlite.OpenDB(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT id FROM " + table + " ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestTheCombinedNeedsYouListsEveryActiveProject holds FR-PROJ-8 on the API: one read lists what waits in
// every active project, the longest wait first, each item labeled with its project, and an archived project
// lists nothing.
func TestTheCombinedNeedsYouListsEveryActiveProject(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	_, _, beta := in.In(t, "beta")
	createWork(t, in.Client, "wo-home", "Add the retry budget")
	createWork(t, beta, "wo-beta", "Ship the export")
	view, err := in.Client.NeedsYou(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, item := range view.Items {
		if item.Project == nil {
			t.Fatalf("an item with no project: %+v", item)
		}
		labels[item.ID] = item.Project.Name
	}
	if len(view.Items) != 2 || labels["wo-home"] != apitest.ProjectName || labels["wo-beta"] != "beta" {
		t.Errorf("items %v", labels)
	}
	if view.Items[0].Since > view.Items[1].Since {
		t.Errorf("the longest wait is not first: %+v", view.Items)
	}
	waiting := map[string]int{}
	for _, project := range view.Projects {
		waiting[project.Name] = project.Waiting
	}
	if waiting[apitest.ProjectName] != 1 || waiting["beta"] != 1 {
		t.Errorf("projects %+v", view.Projects)
	}
	if _, err := in.Client.ArchiveProject(t.Context(), "beta"); err != nil {
		t.Fatal(err)
	}
	if view, err = in.Client.NeedsYou(t.Context()); err != nil || len(view.Items) != 1 || view.Items[0].ID != "wo-home" {
		t.Errorf("with beta archived: %+v, %v", view.Items, err)
	}
}
