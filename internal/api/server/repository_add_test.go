package server_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
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

// FR-REPO-14, DG2: a repository with no remote is identified by its first commit, so a clone with no remote
// that shares its first commit with a repository another project holds is refused, and the reason names that
// commit and that project, not only an identity.
func TestACloneWithNoRemoteThatSharesAFirstCommitWithAnotherProjectIsRefusedNamingBoth(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t, "beta")
	root := "5ca1ab1e" + strings.Repeat("0", 32)
	in.Checkouts.Place(ports.Clone{Path: "/held/template", ID: root, DefaultBranch: "main"})
	in.Checkouts.Place(ports.Clone{Path: "/work/template", ID: root, DefaultBranch: "main"})
	_, _, betaClient := in.In(t, "beta")
	if _, err := betaClient.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/held/template"}}); err != nil {
		t.Fatal(err)
	}
	_, _, client := in.In(t, apitest.ProjectName)
	for _, dryRun := range []bool{true, false} {
		report, err := client.AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/template"}, DryRun: dryRun})
		if err != nil || report.Refused() != 1 {
			t.Fatalf("a clone sharing a first commit with another project's, dry run %v: %+v, %v", dryRun, report, err)
		}
		for _, want := range []string{"shares its first commit `5ca1ab1e`", "project beta", "give this clone a remote of its own or a history of its own"} {
			if reason := report.Repositories[0].Reason; !strings.Contains(reason, want) {
				t.Errorf("dry run %v: the refusal %q does not say %q", dryRun, reason, want)
			}
		}
	}
	if len(repositoriesOf(t, client)) != 0 {
		t.Error("a refused clone was registered")
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

// A delegate registers no clone from a path on this machine on its own word: its call becomes a card,
// which registers nothing until a member confirms it. A backup writes a file at a path its caller names,
// so a delegate makes none.
func TestADelegatesRegistrationIsACardAndItsBackupIsRefused(t *testing.T) {
	t.Parallel()
	in := apitest.StartInstallation(t)
	in.Checkouts.Place(ports.Clone{Path: "/work/api", ID: "id-api", Remote: "https://example.invalid/acme/api.git", DefaultBranch: "main"})
	_, _, client := in.In(t, apitest.ProjectName)
	ctx := t.Context()
	created := aDelegate(t, in.Client.Client, apitest.ProjectName)
	delegate := asDelegate(t, in.Socket, created.Token, apitest.ProjectName)
	_, err := delegate.AddRepositories(ctx, api.RepositoryAdd{Paths: []string{"/work/api"}})
	card := preparedCard(t, err)
	if len(repositoriesOf(t, client)) != 0 {
		t.Fatal("a delegate's call registered a repository")
	}
	if _, err := client.DecideApproval(ctx, card.ID, api.DecideApproval{Digest: card.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	if registered := repositoriesOf(t, client); len(registered) != 1 {
		t.Fatalf("the confirmed card registered %+v", registered)
	}
	copied := filepath.Join(t.TempDir(), "copy.db")
	_, err = delegate.Backup(ctx, api.BackupRequest{Path: copied})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	if _, err := os.Stat(copied); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a delegate's backup wrote %s: %v", copied, err)
	}
	err = delegate.Do(ctx, http.MethodPost, api.InProject(apitest.ProjectName, api.BackupDownloadPath), struct{}{}, nil)
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
}

// manifestText is a repository manifest that lists billing beside the planning repository's clone.
const manifestText = "+++\nkind = \"repositories\"\nid = \"platform\"\n\n[[repository]]\nname = \"billing\"\npath = \"../billing\"\n+++\n"

// ADR-0022: registering what a manifest lists reads paths a file an agent can write names, so it is a
// member's alone: an outside delegate's call and the assistant's are refused, not carded, and register
// nothing.
func TestOnlyAMemberRegistersFromAManifest(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: "id-plan", DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/work/billing", ID: "id-billing", DefaultBranch: "main"})
	d := startTalkWith(t, apitest.Options{Checkouts: machine})
	ctx := t.Context()
	if err := machine.Keep(ctx, apitest.Scope, d.repo, "/work/plan"); err != nil {
		t.Fatal(err)
	}
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	outside := asDelegate(t, d.socket, created.Token, apitest.ProjectName)
	_, err := outside.AddRepositories(ctx, api.RepositoryAdd{Manifest: manifestText})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	_, assistant, _ := d.assistant(t)
	_, err = assistant.AddRepositories(ctx, api.RepositoryAdd{Manifest: manifestText, DryRun: true})
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	if _, ok := repositoriesOf(t, d.client)["billing"]; ok {
		t.Fatal("a delegate's call registered billing")
	}
	cards, err := d.client.Approvals(ctx, authority.ApprovalPending)
	if err != nil || len(cards) != 0 {
		t.Fatalf("a delegate's call left cards %+v: %v", cards, err)
	}
	report, err := d.client.AddRepositories(ctx, api.RepositoryAdd{Manifest: manifestText})
	if err != nil || len(report.Repositories) != 1 || report.Repositories[0].Outcome != ledger.Registered {
		t.Fatalf("a member's registration from the manifest reads %+v: %v", report, err)
	}
}

// ADR-0022: a member reads what approving a plan version registers before approving it, from the clones on
// the daemon's worker's machine, and the approval keeps what it registered, which its own view and the
// plan's read later. A delegate's read previews nothing.
func TestAPlanApprovalShowsWhatItRegisters(t *testing.T) {
	t.Parallel()
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: "id-plan", DefaultBranch: "main"})
	machine.Place(ports.Clone{Path: "/work/billing", ID: "id-billing", Remote: "git@example.invalid:acme/billing.git", DefaultBranch: "main"})
	d := startTalkWith(t, apitest.Options{Checkouts: machine})
	ctx := t.Context()
	if err := machine.Keep(ctx, apitest.Scope, d.repo, "/work/plan"); err != nil {
		t.Fatal(err)
	}
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \"platform-v1\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nHow.\n"))
	source.Write("repositories.md", []byte("+++\nkind = \"repositories\"\nid = \"platform\"\nplan = \"platform-v1\"\n\n[[repository]]\nname = \"billing\"\nremote = \"git@example.invalid:acme/billing.git\"\npath = \"../billing\"\n\n[[repository]]\nname = \"ghost\"\npath = \"../ghost\"\n+++\n"))
	if _, err := control.NewIntake(d.Store).Poll(ctx, apitest.Scope, source); err != nil {
		t.Fatal(err)
	}
	plans, err := d.client.Plans(ctx)
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	view, err := d.client.Plan(ctx, plans[0].ID)
	if err != nil || view.Approval == nil || view.Approval.Registration == nil {
		t.Fatalf("the plan reads %+v: %v", view.Approval, err)
	}
	preview := view.Approval.Registration
	want := map[string]api.RepositoryAdded{
		"billing": {Name: "billing", ManifestPath: "../billing", Path: "/work/billing", Remote: "git@example.invalid:acme/billing.git", Outcome: ledger.Registered},
		"ghost":   {Name: "ghost", ManifestPath: "../ghost", Path: "/work/ghost", Outcome: ledger.RegistrationRefused},
	}
	if !preview.Preview || len(preview.Repositories) != 2 {
		t.Fatalf("the preview reads %+v", preview)
	}
	for _, got := range preview.Repositories {
		w := want[got.Name]
		if got.ManifestPath != w.ManifestPath || got.Path != w.Path || got.Remote != w.Remote || got.Outcome != w.Outcome {
			t.Errorf("the preview of %s reads %+v, want %+v", got.Name, got, w)
		}
	}
	if _, ok := repositoriesOf(t, d.client)["billing"]; ok {
		t.Fatal("reading the preview registered billing")
	}
	created := aDelegate(t, d.Client.Client, apitest.ProjectName)
	delegated, err := asDelegate(t, d.socket, created.Token, apitest.ProjectName).Plan(ctx, plans[0].ID)
	if err != nil || delegated.Approval == nil || delegated.Approval.Registration == nil || len(delegated.Approval.Registration.Repositories) != 0 ||
		!strings.Contains(delegated.Approval.Registration.Problem, "member") {
		t.Errorf("a delegate's read of the plan previews %+v: %v", delegated.Approval, err)
	}
	asked := view.Approval.Approval
	if _, err := d.client.DecideApproval(ctx, asked.ID, api.DecideApproval{Digest: asked.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	var detail api.ApprovalDetail
	err = d.client.Do(ctx, http.MethodGet, api.InProject(apitest.ProjectName, "/v1/approvals/"+asked.ID), nil, &detail)
	if err != nil || detail.Registration == nil || detail.Registration.Preview || len(detail.Registration.Repositories) != 2 {
		t.Fatalf("the approval reads %+v: %v", detail.Registration, err)
	}
	for _, got := range detail.Registration.Repositories {
		switch got.Name {
		case "billing":
			if got.Outcome != ledger.Registered || got.Repository == nil || got.Repository.ID != "id-billing" || got.Checkout != "/work/billing" {
				t.Errorf("the approval says billing %+v", got)
			}
		case "ghost":
			if got.Outcome != ledger.RegistrationRefused || !strings.Contains(got.Reason, "/work/ghost") {
				t.Errorf("the approval says ghost %+v", got)
			}
		}
	}
	after, err := d.client.Plan(ctx, plans[0].ID)
	if err != nil || after.Approval == nil || after.Approval.Registration == nil || after.Approval.Registration.Preview {
		t.Errorf("the plan after approval reads %+v: %v", after.Approval, err)
	}
}
