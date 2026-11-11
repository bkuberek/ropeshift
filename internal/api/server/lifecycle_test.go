package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// lifecycleEvents lists the installation's events about one project, as type and actor kind.
func lifecycleEvents(t *testing.T, in *apitest.Installation, id ledger.WorkspaceID) []string {
	t.Helper()
	events, err := in.State.Installation.Events(t.Context(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range events {
		if e.Subject.Kind == ledger.SubjectProject && e.Subject.ID == string(id) {
			out = append(out, e.Type+" by "+string(e.Actor.Kind))
		}
	}
	return out
}

// TestTheProjectLifecycle holds ADR-0021's lifecycle through the API: create, rename, archive, restore,
// and remove, each a member's step that records an installation event.
func TestTheProjectLifecycle(t *testing.T) {
	in := apitest.StartInstallation(t)
	created, err := in.Client.CreateProject(t.Context(), api.CreateProject{Name: "beta", DisplayName: "Beta"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "beta" || created.DisplayName != "Beta" || created.State != ledger.ProjectActive || created.ID == "" {
		t.Errorf("created %+v", created)
	}
	beta := in.Client.Project("beta")
	createWork(t, beta, "wo-1", "Ship the invoice export")
	if _, err := in.Client.RenameProject(t.Context(), "beta", api.RenameProject{Name: "gamma"}); err != nil {
		t.Fatal(err)
	}
	gamma := in.Client.Project("gamma")
	if got := workIDs(t, gamma); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("gamma lists %v", got)
	}
	_, err = beta.WorkOrders(t.Context(), "")
	refusedWith(t, err, http.StatusNotFound, api.CodeNoProject)
	if p, err := in.Client.ArchiveProject(t.Context(), "gamma"); err != nil || p.State != ledger.ProjectArchived {
		t.Fatalf("archived %+v, %v", p, err)
	}
	if p, err := in.Client.RestoreProject(t.Context(), "gamma"); err != nil || p.State != ledger.ProjectActive {
		t.Fatalf("restored %+v, %v", p, err)
	}
	if _, err := in.Client.ArchiveProject(t.Context(), "gamma"); err != nil {
		t.Fatal(err)
	}
	if err := in.Client.RemoveProject(t.Context(), "gamma", api.RemoveProject{Confirm: "gamma", NoBackup: true}); err != nil {
		t.Fatal(err)
	}
	if projects, err := in.Client.Projects(t.Context()); err != nil || len(projects) != 1 {
		t.Errorf("projects after the removal %+v, %v", projects, err)
	}
	want := []string{
		ledger.EventProjectCreated + " by member", ledger.EventProjectRenamed + " by member", ledger.EventProjectArchived + " by member",
		ledger.EventProjectRestored + " by member", ledger.EventProjectArchived + " by member", ledger.EventProjectRemoved + " by member",
	}
	if got := lifecycleEvents(t, in, created.ID); !slices.Equal(got, want) {
		t.Errorf("events %v", got)
	}
}

// TestEveryLifecycleStepNeedsAMember holds that no step of a project's lifecycle runs with no session.
func TestEveryLifecycleStepNeedsAMember(t *testing.T) {
	in := apitest.StartInstallation(t, "acme")
	anonymous := api.NewUnixClient(in.Socket)
	defer anonymous.Close()
	backup := filepath.Join(t.TempDir(), "acme.db")
	steps := map[string]func() error{
		"create": func() error {
			_, err := anonymous.CreateProject(t.Context(), api.CreateProject{Name: "beta"})
			return err
		},
		"rename": func() error {
			_, err := anonymous.RenameProject(t.Context(), "acme", api.RenameProject{Name: "beta"})
			return err
		},
		"archive": func() error { _, err := anonymous.ArchiveProject(t.Context(), "acme"); return err },
		"restore": func() error { _, err := anonymous.RestoreProject(t.Context(), "acme"); return err },
		"remove": func() error {
			return anonymous.RemoveProject(t.Context(), "acme", api.RemoveProject{Confirm: "acme", NoBackup: true})
		},
		"import": func() error {
			_, err := anonymous.ImportProject(t.Context(), api.ImportProject{Path: backup})
			return err
		},
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			refusedWith(t, step(), http.StatusUnauthorized, api.CodeSessionRequired)
		})
	}
	if got := lifecycleEvents(t, in, "acme-id"); len(got) != 1 {
		t.Errorf("events %v", got)
	}
}

// TestAnArchivedProjectIsReadOnly holds that an archived project takes no change in any route, claims
// included, until it is restored, and still reads and backs up.
func TestAnArchivedProjectIsReadOnly(t *testing.T) {
	in := apitest.StartInstallation(t, "acme")
	_, _, acme := in.In(t, "acme")
	createWork(t, acme, "wo-1", "Ship the invoice export")
	if _, err := acme.Review(t.Context(), "wo-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Client.ArchiveProject(t.Context(), "acme"); err != nil {
		t.Fatal(err)
	}
	if got := workIDs(t, acme); !slices.Equal(got, []string{"wo-1"}) {
		t.Errorf("an archived project reads %v", got)
	}
	_, err := acme.CreateWorkOrder(t.Context(), api.CreateWorkOrder{ID: "wo-2", Title: "More"})
	refusedWith(t, err, http.StatusConflict, api.CodeProjectArchived)
	_, err = acme.Claim(t.Context(), "wo-1", api.ClaimRequest{ClaimID: "c1", WorkerID: "worker-a"})
	refusedWith(t, err, http.StatusConflict, api.CodeProjectArchived)
	if _, err := acme.Backup(t.Context(), api.BackupRequest{Path: filepath.Join(t.TempDir(), "acme.db")}); err != nil {
		t.Errorf("an archived project's backup: %v", err)
	}
	if _, err := in.Client.RestoreProject(t.Context(), "acme"); err != nil {
		t.Fatal(err)
	}
	createWork(t, acme, "wo-2", "More")
}

// TestArchivingWaitsForEveryLane holds that a project is archived only once every lane of it ended.
func TestArchivingWaitsForEveryLane(t *testing.T) {
	in := apitest.StartInstallation(t, "acme")
	project, store, acme := in.In(t, "acme")
	createWork(t, acme, "wo-1", "Ship the invoice export")
	if _, err := acme.Review(t.Context(), "wo-1"); err != nil {
		t.Fatal(err)
	}
	worker := ledger.Actor{Kind: ledger.ActorWorker, ID: "worker-a"}
	leases := control.NewLeases(store)
	grant, err := leases.Claim(t.Context(), project.ID, workorders.ClaimRequest{WorkOrder: "wo-1", ClaimID: "c1", Worker: worker.ID, TTLMs: 60_000}, worker)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := leases.StartLane(t.Context(), project.ID, grant.Lease, "lane-1", worker)
	if err != nil {
		t.Fatal(err)
	}
	_, err = in.Client.ArchiveProject(t.Context(), "acme")
	refusedWith(t, err, http.StatusConflict, api.CodeProjectState)
	if !strings.Contains(err.Error(), "lane-1") || !strings.Contains(err.Error(), "fleet drain") {
		t.Errorf("the refusal says %v", err)
	}
	holder := workorders.Holder{Lease: grant.Lease, Lane: "lane-1", Generation: generation}
	if err := leases.EndLane(t.Context(), project.ID, holder, workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}}, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Client.ArchiveProject(t.Context(), "acme"); err != nil {
		t.Errorf("with every lane ended: %v", err)
	}
}

// TestRemovingAProjectNeedsAllItAsks holds remove's guards: an archived project, its name as confirmation,
// and one choice about a backup, which it writes before the database goes.
func TestRemovingAProjectNeedsAllItAsks(t *testing.T) {
	in := apitest.StartInstallation(t, "acme")
	project, _, acme := in.In(t, "acme")
	createWork(t, acme, "wo-1", "Ship the invoice export")
	err := in.Client.RemoveProject(t.Context(), "acme", api.RemoveProject{Confirm: "acme", NoBackup: true})
	refusedWith(t, err, http.StatusConflict, api.CodeProjectState)
	if _, err := in.Client.ArchiveProject(t.Context(), "acme"); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "acme.db")
	for name, removal := range map[string]api.RemoveProject{
		"the wrong name":      {Confirm: "acne", NoBackup: true},
		"no backup choice":    {Confirm: "acme"},
		"both backup choices": {Confirm: "acme", Backup: backup, NoBackup: true},
	} {
		t.Run(name, func(t *testing.T) {
			refusedWith(t, in.Client.RemoveProject(t.Context(), "acme", removal), http.StatusBadRequest, api.CodeInvalid)
		})
	}
	if err := in.Client.RemoveProject(t.Context(), "acme", api.RemoveProject{Confirm: "acme", Backup: backup}); err != nil {
		t.Fatal(err)
	}
	if rows := rowsIn(t, backup, "work_orders"); !slices.Equal(rows, []string{"wo-1"}) {
		t.Errorf("the backup holds %v", rows)
	}
	if _, err := os.Stat(filepath.Join(in.State.Dir, filepath.FromSlash(project.Database))); !os.IsNotExist(err) {
		t.Errorf("the database stayed: %v", err)
	}
	// The project the daemon's worker serves stays until the worker serves every project.
	if _, err := in.Client.ArchiveProject(t.Context(), apitest.ProjectName); err != nil {
		t.Fatal(err)
	}
	err = in.Client.RemoveProject(t.Context(), apitest.ProjectName, api.RemoveProject{Confirm: apitest.ProjectName, NoBackup: true})
	refusedWith(t, err, http.StatusConflict, api.CodeProjectState)
}
