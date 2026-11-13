package server_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

func outcomes(report api.RepositoryAddReport) map[string]ledger.RepositoryOutcome {
	out := map[string]ledger.RepositoryOutcome{}
	for _, added := range report.Repositories {
		out[added.Path] = added.Outcome
	}
	return out
}

func repositoriesOf(t *testing.T, client *api.ProjectClient) map[string]api.RepositoryView {
	t.Helper()
	list, err := client.Repositories(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]api.RepositoryView{}
	for _, view := range list.Repositories {
		out[view.Name] = view
	}
	return out
}

// ADR-0022: `repository add` registers each clone at or below the paths it names, in the project it
// resolves, with the safe defaults; a repository another project holds is refused, naming that project,
// and the others are still registered; a dry run says the same and records nothing.
func TestAddingRepositoriesRegistersAFolderOfClones(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	machine := in.Checkouts
	machine.Place(ports.Clone{Path: "/work/api", ID: "id-api", Remote: "git@example.invalid:acme/api.git", DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/work/web", ID: "id-web", Remote: "https://example.invalid/acme/web.git", DefaultBranch: "trunk"})
	machine.Place(ports.Clone{Path: "/other/api", ID: "id-api-2", Remote: "https://example.invalid/acme/payments-api.git", DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/held/shared", ID: "id-shared", Remote: "https://example.invalid/acme/shared.git", DefaultBranch: "main"})
	beta, _, betaClient := in.In(t, "beta")
	if _, err := betaClient.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/held/shared"}}); err != nil {
		t.Fatal(err)
	}
	home, store, client := in.In(t, apitest.ProjectName)

	dry, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work", "/other/api", "/held/shared", "/nowhere"}, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ledger.RepositoryOutcome{
		"/work/api": ledger.Registered, "/work/web": ledger.Registered, "/other/api": ledger.Registered,
		"/held/shared": ledger.RegistrationRefused, "/nowhere": ledger.RegistrationRefused,
	}
	if got := outcomes(dry); !dry.DryRun || len(got) != len(want) || dry.Refused() != 2 {
		t.Fatalf("the dry run says %+v", dry)
	}
	for path, outcome := range want {
		if outcomes(dry)[path] != outcome {
			t.Errorf("dry run: %s %s, want %s", path, outcomes(dry)[path], outcome)
		}
	}
	if len(repositoriesOf(t, client)) != 0 {
		t.Fatal("a dry run registered a repository")
	}
	if kept, _ := machine.Of(t.Context(), home.ID); len(kept) != 0 {
		t.Fatalf("a dry run kept checkouts %v", kept)
	}

	report, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work", "/other/api", "/held/shared", "/nowhere"}})
	if err != nil {
		t.Fatal(err)
	}
	if report.DryRun || report.Refused() != 2 {
		t.Errorf("the run says %+v", report)
	}
	for _, added := range report.Repositories {
		if added.Path == "/held/shared" && !strings.Contains(added.Reason, "beta") {
			t.Errorf("the refusal does not name the project that holds it: %q", added.Reason)
		}
	}
	named := repositoriesOf(t, client)
	first, web, payments := named["api"], named["web"], named["payments-api"]
	if first.ID != "id-api" || web.ID != "id-web" || payments.ID != "id-api-2" || len(named) != 3 {
		t.Fatalf("the project holds %+v", named)
	}
	if first.ReviewTarget != ledger.ReviewOnBranch || first.MergeMethod != ledger.MergeCommit || first.StartFrom != ledger.StartFromMerged ||
		first.Level() != ledger.PersonMerges || web.DefaultBranch != "trunk" {
		t.Errorf("the records do not hold the safe defaults: %+v, %+v", first, web)
	}
	kept, _ := machine.Of(t.Context(), home.ID)
	if kept["id-api"] != "/work/api" || kept["id-web"] != "/work/web" || kept["id-api-2"] != "/other/api" || len(kept) != 3 {
		t.Errorf("the worker keeps %v", kept)
	}
	if held, _ := machine.Of(t.Context(), beta.ID); held["id-shared"] != "/held/shared" {
		t.Errorf("beta's checkout moved: %v", held)
	}

	// Each registration is one audited event, by the member.
	for _, id := range []string{"id-api", "id-web", "id-api-2"} {
		var events []ledger.Event
		if err := store.Read(t.Context(), home.ID, func(tx ports.ReadTx) error {
			var err error
			events, err = tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectRepository, ID: id})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].Type != ledger.EventRepositoryRegistered || events[0].Actor.Kind != ledger.ActorMember {
			t.Errorf("%s's history is %+v", id, events)
		}
	}

	// Running it again registers nothing new, and a second clone of a repository keeps the first checkout.
	machine.Place(ports.Clone{Path: "/copy/api", ID: "id-api", Remote: "git@example.invalid:acme/api.git", DefaultBranch: "main"})
	again, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api", "/copy/api"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomes(again); got["/work/api"] != ledger.AlreadyRegistered || got["/copy/api"] != ledger.AlreadyRegistered {
		t.Errorf("again: %+v", again)
	}
	if kept, _ := machine.Of(t.Context(), home.ID); kept["id-api"] != "/work/api" {
		t.Errorf("a second clone moved the checkout to %q", kept["id-api"])
	}
}

func TestOnlyAMemberAddsRepositories(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t)
	in.Checkouts.Place(ports.Clone{Path: "/work/api", ID: "id-api", DefaultBranch: "main"})
	plain := api.NewUnixClient(in.Socket).Project(apitest.ProjectName)
	defer plain.Close()
	_, err := plain.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}})
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusUnauthorized {
		t.Errorf("no session: %v", err)
	}
	_, _, client := in.In(t, apitest.ProjectName)
	if _, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"work/api"}}); !errors.As(err, &refused) || refused.Status != http.StatusBadRequest {
		t.Errorf("a relative path: %v", err)
	}
}

// ADR-0021: a removed repository stays in its project as history, leaves the repository index and this
// worker's checkouts, and another project may then register it. The first project registering it again
// is then refused, naming the project that holds it.
func TestARemovedRepositoryMayJoinAnotherProject(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	in.Checkouts.Place(ports.Clone{Path: "/work/api", ID: "id-api", Remote: "git@example.invalid:acme/api.git", DefaultBranch: "main"})
	home, _, client := in.In(t, apitest.ProjectName)
	beta, _, betaClient := in.In(t, "beta")
	if _, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}}); err != nil {
		t.Fatal(err)
	}
	refused, err := betaClient.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}})
	if err != nil || refused.Refused() != 1 {
		t.Fatalf("beta registering a repository the first project holds: %+v, %v", refused, err)
	}
	removed, err := client.RemoveRepository(t.Context(), "api")
	if err != nil || removed.State != ledger.RepositoryRemoved || removed.Name != "api" {
		t.Fatalf("the removal answers %+v, %v", removed, err)
	}
	if kept, _ := in.Checkouts.Of(t.Context(), home.ID); len(kept) != 0 {
		t.Errorf("the worker still keeps %v", kept)
	}
	if view, err := client.Repository(t.Context(), "api"); err != nil || view.State != ledger.RepositoryRemoved {
		t.Errorf("the record as history: %+v, %v", view, err)
	}
	var detail *api.Error
	if _, err := client.RemoveRepository(t.Context(), "api"); !errors.As(err, &detail) || detail.Detail.Code != api.CodeRepositoryState {
		t.Errorf("removing it again: %v", err)
	}
	joined, err := betaClient.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}})
	if err != nil || joined.Refused() != 0 || outcomes(joined)["/work/api"] != ledger.Registered {
		t.Fatalf("beta registering the removed repository: %+v, %v", joined, err)
	}
	if kept, _ := in.Checkouts.Of(t.Context(), beta.ID); kept["id-api"] != "/work/api" {
		t.Errorf("beta's worker keeps %v", kept)
	}
	back, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}})
	if err != nil || back.Refused() != 1 || !strings.Contains(back.Repositories[0].Reason, "beta") {
		t.Errorf("the first project registering it again: %+v, %v", back, err)
	}
	if view, err := client.Repository(t.Context(), "api"); err != nil || view.State != ledger.RepositoryRemoved {
		t.Errorf("a refused registration changed the history: %+v, %v", view, err)
	}
}

// A project registering again a repository it removed brings its record back, with its name and settings.
func TestARepositoryRegisteredAgainComesBack(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t)
	in.Checkouts.Place(ports.Clone{Path: "/work/api", ID: "id-api", DefaultBranch: "main"})
	_, _, client := in.In(t, apitest.ProjectName)
	if _, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}}); err != nil {
		t.Fatal(err)
	}
	squash := ledger.MergeSquash
	if _, err := client.SetRepository(t.Context(), "api", api.RepositoryChange{MergeMethod: &squash}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveRepository(t.Context(), "api"); err != nil {
		t.Fatal(err)
	}
	again, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/api"}})
	if err != nil || outcomes(again)["/work/api"] != ledger.Registered {
		t.Fatalf("registering it again: %+v, %v", again, err)
	}
	if view, err := client.Repository(t.Context(), "api"); err != nil || view.Removed() || view.MergeMethod != ledger.MergeSquash {
		t.Errorf("the record came back as %+v, %v", view, err)
	}
}
