package server_test

import (
	"errors"
	"net/http"
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

// creationPlan is a plan whose repository manifest lists ghost, a repository the plan's version creates and whose contract
// gives it work, so approving the version leaves ghost created in part: its push is denied.
type creationPlan struct {
	*talkDaemon
	machine  *fake.Checkouts
	creator  *placingCreator
	approval authority.Approval
}

func approvedCreation(t *testing.T) creationPlan {
	t.Helper()
	machine := fake.NewCheckouts()
	machine.Place(ports.Clone{Path: "/work/plan", ID: "id-plan", DefaultBranch: "main"})
	creator := newPlacingCreator(machine)
	d := startTalkWith(t, apitest.Options{Checkouts: machine, Creator: creator})
	ctx := t.Context()
	if err := machine.Keep(ctx, apitest.Scope, d.repo, "/work/plan"); err != nil {
		t.Fatal(err)
	}
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte("+++\nkind = \"plan\"\nid = \"platform-v1\"\ntitle = \"The platform\"\ndocuments = [\"repositories:platform\"]\n+++\n\nHow.\n"))
	source.Write("repositories.md", []byte("+++\nkind = \"repositories\"\nid = \"platform\"\nplan = \"platform-v1\"\n\n[[repository]]\nname = \"ghost\"\npath = \"../ghost\"\ncreate = \"github:acme\"\n+++\n"))
	source.Write("core.md", []byte("+++\nkind = \"contract\"\nid = \"core\"\ntitle = \"The core\"\nplan = \"platform-v1\"\n\n[[criteria]]\nid = \"c1\"\ntext = \"It works\"\n\n[[work_orders]]\nid = \"core-1\"\ntitle = \"The core work\"\nrepo = \"ghost\"\n+++\n"))
	if _, err := control.NewIntake(d.Store).Poll(ctx, apitest.Scope, source); err != nil {
		t.Fatal(err)
	}
	plans, err := d.client.Plans(ctx)
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans %+v: %v", plans, err)
	}
	view, err := d.client.Plan(ctx, plans[0].ID)
	if err != nil || view.Approval == nil {
		t.Fatalf("the plan reads %+v: %v", view.Approval, err)
	}
	asked := view.Approval.Approval
	creator.approval = asked.ID
	creator.failsPush("ghost")
	decided, err := d.client.DecideApproval(ctx, asked.ID, api.DecideApproval{Digest: asked.SubjectDigest, Allow: true})
	if err != nil || len(decided.Repositories) != 1 || decided.Repositories[0].Outcome != ledger.CreateFailed {
		t.Fatalf("approving reads %+v: %v", decided, err)
	}
	return creationPlan{talkDaemon: d, machine: machine, creator: creator, approval: decided.Approval}
}

// creationItems reads the repository creations Needs you asks a member about.
func creationItems(t *testing.T, d *apitest.Daemon) []api.NeedItem {
	t.Helper()
	var out []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedRepositoryCreation {
			out = append(out, item)
		}
	}
	return out
}

// A creation the approval left failed is one item of Needs you for the entry, named by the approval and the entry, that
// says where it stands and offers Retry and Skip.
func TestAFailedCreationIsAnItemOfNeedsYou(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	items := creationItems(t, p.Daemon)
	if len(items) != 1 {
		t.Fatalf("Needs you holds %d creations: %+v", len(items), items)
	}
	item, need := items[0], items[0].RepositoryCreation
	if need == nil || item.ID != p.approval.ID+"/ghost" || need.Approval != p.approval.ID || need.Plan != p.approval.Plan || need.PlanVersion != p.approval.PlanVersion {
		t.Fatalf("the item reads %+v", item)
	}
	if need.Name != "ghost" || need.ManifestPath != "../ghost" || need.Path != "/work/ghost" || need.State != api.RepositoryCreationFailed || !strings.Contains(need.Reason, "Permission denied") || need.Creation == nil || len(need.Creation.Remaining) != 1 {
		t.Errorf("the need reads %+v", need)
	}
	if len(need.Actions) != 2 || need.Actions[0] != api.ActRepositoryRetry || need.Actions[1] != api.ActRepositorySkip {
		t.Errorf("the need offers %v", need.Actions)
	}
}

// Retry runs the entry again as one entry of a real run, and answers the approval with what its registration holds now:
// created in part while the forge denies the push, created and registered once it takes it, and then the item is gone.
func TestRetryingARepositoryCreationCreatesItOnceItsCheckPasses(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	ctx := t.Context()
	decided, err := p.client.RetryRepository(ctx, p.approval.ID, "ghost")
	if err != nil || decided.ID != p.approval.ID || len(decided.Repositories) != 1 || decided.Repositories[0].Outcome != ledger.CreateFailed {
		t.Fatalf("a retry while the push is denied reads %+v: %v", decided, err)
	}
	p.creator.passes("ghost")
	decided, err = p.client.RetryRepository(ctx, p.approval.ID, "ghost")
	if err != nil || len(decided.Repositories) != 1 || decided.Repositories[0].Outcome != ledger.Created || decided.Repositories[0].Repository == nil || decided.Repositories[0].Repository.Name != "ghost" {
		t.Fatalf("a retry once the push is taken reads %+v: %v", decided, err)
	}
	if items := creationItems(t, p.Daemon); len(items) != 0 {
		t.Errorf("Needs you still holds %+v", items)
	}
	if _, ok := repositoriesOf(t, p.client)["ghost"]; !ok {
		t.Error("the project holds no ghost")
	}
	_, err = p.client.RetryRepository(ctx, p.approval.ID, "ghost")
	refusedWith(t, err, http.StatusConflict, api.CodeIllegalMove)
}

// Skip records why, and answers with the entry skipped and the item gone, and wants a reason of 1 to 2000 characters.
func TestSkippingARepositoryCreationRecordsItsReason(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	ctx := t.Context()
	for _, reason := range []string{"", "   ", strings.Repeat("x", 2001), "bad\x1b[31m red", "two\nlines", "bell\x07"} {
		_, err := p.client.SkipRepository(ctx, p.approval.ID, "ghost", api.SkipRepository{Reason: reason})
		refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	}
	if items := creationItems(t, p.Daemon); len(items) != 1 {
		t.Fatalf("a refused skip left %+v", items)
	}
	decided, err := p.client.SkipRepository(ctx, p.approval.ID, "ghost", api.SkipRepository{Reason: "  built   by hand "})
	if err != nil || len(decided.Repositories) != 1 || decided.Repositories[0].Outcome != ledger.Skipped || decided.Repositories[0].Reason != "built by hand" {
		t.Fatalf("a skip reads %+v: %v", decided, err)
	}
	// A skipped creation is left as the member left it: a retry of it is refused, naming why.
	_, err = p.client.RetryRepository(ctx, p.approval.ID, "ghost")
	refusedWith(t, err, http.StatusConflict, api.CodeIllegalMove)
	if refused := new(api.Error); !errors.As(err, &refused) || !strings.Contains(refused.Detail.Message, "skipped; a later approved version that targets it creates it") {
		t.Errorf("a retry of a skipped creation answered %v", err)
	}
	if items := creationItems(t, p.Daemon); len(items) != 0 {
		t.Errorf("Needs you still holds %+v", items)
	}
}

// A name the approval's version does not create, and an approval nobody holds, are refused and change nothing.
func TestRetryAndSkipRefuseWhatTheApprovalDoesNotCreate(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	ctx := t.Context()
	_, err := p.client.RetryRepository(ctx, p.approval.ID, "elsewhere")
	refusedWith(t, err, http.StatusConflict, api.CodeIllegalMove)
	_, err = p.client.SkipRepository(ctx, p.approval.ID, "elsewhere", api.SkipRepository{Reason: "r"})
	refusedWith(t, err, http.StatusConflict, api.CodeIllegalMove)
	_, err = p.client.RetryRepository(ctx, "no-such-approval", "ghost")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	if items := creationItems(t, p.Daemon); len(items) != 1 {
		t.Errorf("a refused word changed Needs you: %+v", items)
	}
}

// Retrying and skipping are a member's alone: a delegate's call and the assistant's are refused, not carded, and change
// nothing.
func TestOnlyAMemberRetriesOrSkipsARepositoryCreation(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	ctx := t.Context()
	created := aDelegate(t, p.Client.Client, apitest.ProjectName)
	outside := asDelegate(t, p.socket, created.Token, apitest.ProjectName)
	_, assistant, _ := p.assistant(t)
	for name, client := range map[string]*api.ProjectClient{"a delegate": outside, "the assistant": assistant} {
		_, err := client.RetryRepository(ctx, p.approval.ID, "ghost")
		if refused := new(api.Error); !errors.As(err, &refused) || refused.Status != http.StatusForbidden || refused.Detail.Code != api.CodeMemberOnly {
			t.Errorf("%s's retry answered %v, want 403 member_only", name, err)
		}
		_, err = client.SkipRepository(ctx, p.approval.ID, "ghost", api.SkipRepository{Reason: "not mine"})
		if refused := new(api.Error); !errors.As(err, &refused) || refused.Status != http.StatusForbidden || refused.Detail.Code != api.CodeMemberOnly {
			t.Errorf("%s's skip answered %v, want 403 member_only", name, err)
		}
	}
	cards, err := p.client.Approvals(ctx, authority.ApprovalPending)
	if err != nil || len(cards) != 0 {
		t.Errorf("a delegate's call left cards %+v: %v", cards, err)
	}
	if items := creationItems(t, p.Daemon); len(items) != 1 || items[0].RepositoryCreation.State != api.RepositoryCreationFailed {
		t.Errorf("a refused word changed Needs you: %+v", items)
	}
}

// A create that timed out after the forge made the repository finds it there on every retry, and the item says what a member
// does about it: clone it where the entry's path leads and name it as the entry's remote, or skip the creation.
func TestACreationRefusedForAnExistingRepositoryCarriesItsRemedy(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	p.creator.SetCheck(p.creator.key("ghost"), ledger.CreationCheck{Refusal: &ledger.TemplateWriteReport{
		Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedRemoteExists, Detail: "acme/ghost already exists on GitHub",
	}})
	decided, err := p.client.RetryRepository(t.Context(), p.approval.ID, "ghost")
	if err != nil || len(decided.Repositories) != 1 || decided.Repositories[0].Outcome != ledger.RegistrationRefused {
		t.Fatalf("a retry that finds the repository on the forge reads %+v: %v", decided, err)
	}
	items := creationItems(t, p.Daemon)
	if len(items) != 1 {
		t.Fatalf("Needs you holds %+v", items)
	}
	need := items[0].RepositoryCreation
	const remedy = "clone acme/ghost to ../ghost, and write `remote` in place of `create` in the manifest's entry for ghost, or skip it"
	if need.State != api.RepositoryCreationRefused || !strings.Contains(need.Reason, "acme/ghost already exists on GitHub") || need.Remedy != remedy {
		t.Errorf("the need reads %+v, want it refused with the remedy %q", need, remedy)
	}
}

// TestGoldenRepositoryCreationNeed writes what Needs you reads of a creation that stopped at its push, from a real
// ledger: the item with its steps, the commands that remain, and the actions.
func TestGoldenRepositoryCreationNeed(t *testing.T) {
	t.Parallel()
	p := approvedCreation(t)
	view := readView[api.NeedsYou](t, p.Daemon, api.ViewNeedsYou, "")
	view.Items = creationItems(t, p.Daemon)
	if len(view.Items) != 1 {
		t.Fatalf("Needs you holds %d creations", len(view.Items))
	}
	holdsGolden(t, "repository-creation-needs-you", steadyTimes(steady(pretty(t, view))))
}
