package control_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

func githubCreate(owner string) *ledger.RemoteCreate {
	return &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: owner}
}

var remoteRequest = ledger.TemplateWriteRequest{
	Shape: "monorepo", Path: "/srv/code/harbor", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: githubCreate("acme")},
}

// A request whose remote no worker could make is refused before anything is recorded, with the reason a person acts on.
func TestARemoteNoWorkerCouldMakeIsRefusedBeforeItIsRecorded(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	url := func(u string) *ledger.ProjectRemote { return &ledger.ProjectRemote{URL: u} }
	cases := map[string]struct {
		in   ledger.TemplateWriteRequest
		says string
	}{
		"both a url and a create": {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "https://example.com/a.git", Create: githubCreate("acme")}}, "not both"},
		"neither":                 {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{}}, "names a url or a repository to create"},
		"an empty url":            {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: url("")}, "names a url or a repository to create"},
		"a url with whitespace":   {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: url("https://example.com/a b.git")}, "whitespace"},
		"a url that is an option": {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: url("--upload-pack=x")}, "dash"},
		"a url that runs":         {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: url("ext::sh%20-x")}, "remote helper"},
		"another forge": {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: "gitlab", Owner: "acme"}}},
			"creating a repository on gitlab is not supported yet; ropeshift creates repositories on GitHub"},
		"an owner with a leading hyphen": {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: githubCreate("-acme")}}, "no GitHub account name"},
		"an owner with a double hyphen":  {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: githubCreate("ac--me")}}, "no GitHub account name"},
		"an owner too long":              {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: githubCreate(strings.Repeat("a", 40))}}, "no GitHub account name"},
		"an empty owner":                 {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: githubCreate("")}}, "no GitHub account name"},
		"a visibility that is neither": {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: "github", Owner: "acme", Visibility: "internal"}}},
			"private or public"},
		"a remote with no first commit":  {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", Remote: url("https://example.com/a.git")}, "needs the first commit"},
		"a new repository on a monorepo": {ledger.TemplateWriteRequest{Shape: "monorepo", Path: "/srv/h", Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true}}}, "names no new code repositories"},
		"an existing repository that creates": {ledger.TemplateWriteRequest{Shape: "workspace", Path: "/srv/h", FirstCommit: true,
			Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:a/b.git", Create: githubCreate("acme")}}}, "creates nothing"},
		"a new repository with a remote": {ledger.TemplateWriteRequest{Shape: "workspace", Path: "/srv/h", FirstCommit: true,
			Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true, Remote: "git@example.com:a/b.git"}}}, "no remote"},
		"a new repository with both": {ledger.TemplateWriteRequest{Shape: "workspace", Path: "/srv/h", FirstCommit: true,
			Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true, URL: "https://example.com/a.git", Create: githubCreate("acme")}}}, "not both"},
		"a new repository's create with no first commit": {ledger.TemplateWriteRequest{Shape: "workspace", Path: "/srv/h",
			Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true, Create: githubCreate("acme")}}}, "needs the first commit"},
		"a new repository's url that runs": {ledger.TemplateWriteRequest{Shape: "workspace", Path: "/srv/h", FirstCommit: true,
			Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true, URL: "ext::sh%20-x"}}}, "remote helper"},
	}
	for name, c := range cases {
		_, err := r.ask(c.in)
		if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want an invalid request that says %q", name, err, c.says)
		}
	}
	for _, kind := range r.events() {
		if strings.HasPrefix(kind, "template_write.") {
			t.Errorf("a refused request left an event of %s", kind)
		}
	}
}

// A worker on the version before ProtocolProjectRemotes ignores the remote fields, so it is offered no request that holds
// one and a request for it is refused; it is still accepted, and still offered a plain write.
func TestAWorkerBeforeTheRemotesProtocolIsOfferedNoRequestWithARemote(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolProjectRemotes-1)
	for name, in := range map[string]ledger.TemplateWriteRequest{
		"a remote":        remoteRequest,
		"a new repo only": {Shape: "workspace", Path: "/srv/code/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true}}},
	} {
		_, err := r.ask(in)
		if !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "speaks protocol 5; upgrade it to create a project's remote") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := r.ask(monorepoRequest); err != nil {
		t.Errorf("a plain write for a version 5 worker: %v", err)
	}
	r.inserted(ledger.TemplateWriteRecord{ID: "remote", Worker: "worker-1", RequestedBy: "m", Request: remoteRequest, RequestedAt: 1, ExpiresAt: 1 << 60, State: ledger.TemplateWriteOffered})
	for _, offered := range r.desired().TemplateWrites {
		if offered.ID == "remote" {
			t.Errorf("a version 5 worker is offered a request with a remote: %+v", offered)
		}
	}
	if len(r.desired().TemplateWrites) != 1 {
		t.Errorf("the plain write is offered once: %+v", r.desired().TemplateWrites)
	}
	reads := control.NewForgeAccountReads(r.state.Installation, 0)
	if _, err := reads.Request(t.Context(), "worker-1", ledger.ForgeAccountRequest{Forge: "github"}, projectMember); !errors.Is(err, workorders.ErrInvalid) || !strings.Contains(err.Error(), "upgrade it to create a project's remote") {
		t.Errorf("a forge account read for a version 5 worker: %v", err)
	}
	if offered := r.desired().ForgeAccountReads; len(offered) != 0 {
		t.Errorf("a version 5 worker is offered %+v", offered)
	}
}

func TestARequestWithARemoteIsOfferedToItsWorkerAtTheRemotesProtocol(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	view := r.mustAsk(remoteRequest)
	if view.Remote == nil || view.Remote.Create == nil || view.Remote.Create.Owner != "acme" {
		t.Errorf("the view keeps the remote: %+v", view.Remote)
	}
	offered := r.desired().TemplateWrites
	if len(offered) != 1 || offered[0].Remote == nil || offered[0].Remote.Create.Owner != "acme" {
		t.Errorf("offered = %+v", offered)
	}
	got, err := r.writes.View(t.Context(), "worker-1", view.ID)
	if err != nil || got.Remote == nil || got.Remote.Create == nil || got.Remote.Create.Owner != "acme" {
		t.Errorf("the stored request reads %+v (%v)", got, err)
	}
}

// The request that creates a repository, and a forge account read, are a member's: a delegate is refused.
func TestADelegateIsRefusedARemoteAndAForgeAccountRead(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "delegate-1"}
	if _, err := r.writes.Request(t.Context(), "worker-1", remoteRequest, delegate); !errors.Is(err, authority.ErrMemberOnly) {
		t.Errorf("a delegate's request: %v", err)
	}
	reads := control.NewForgeAccountReads(r.state.Installation, 0)
	if _, err := reads.Request(t.Context(), "worker-1", ledger.ForgeAccountRequest{Forge: "github"}, delegate); !errors.Is(err, authority.ErrMemberOnly) {
		t.Errorf("a delegate's read: %v", err)
	}
	if _, err := reads.Request(t.Context(), "worker-1", ledger.ForgeAccountRequest{Forge: "gitlab"}, projectMember); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("another forge: %v", err)
	}
}

// Each created repository the report records appends one audit event, once, naming the member, the worker, the forge,
// the owner and name, and the visibility.
func TestAReportOfCreatedRepositoriesAppendsOneEventForEachOnce(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	request := ledger.TemplateWriteRequest{
		Shape: "workspace", Path: "/srv/code/harbor", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: githubCreate("acme")},
		Repositories: []ledger.TemplateWriteRepository{
			{Name: "api", New: true, Create: &ledger.RemoteCreate{Forge: "github", Owner: "acme", Visibility: "public"}},
			{Name: "web", New: true, URL: "https://example.com/web.git"},
		},
	}
	view := r.mustAsk(request)
	done := []ledger.RemoteStep{{Step: "create", Outcome: "done"}, {Step: "origin", Outcome: "done"}, {Step: "push", Outcome: "done"}}
	report := ledger.TemplateWriteReport{
		Auth: r.auth, Request: view.ID, Outcome: ledger.TemplateOutcomeWritten, Project: "harbor", Clone: true,
		Repositories: []ledger.RepositoryReport{
			{Name: "harbor", Path: ".", Steps: done},
			{Name: "api", Path: "repos/api", Steps: done},
			{Name: "web", Path: "repos/web", Steps: []ledger.RemoteStep{{Step: "origin", Outcome: "done"}}},
		},
	}
	for range 2 {
		if err := r.plane.ReportTemplateWrite(t.Context(), report); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for _, kind := range r.events() {
		if kind == "repository.remote_created" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("%d repository.remote_created events, want one for each of the two created repositories, once", count)
	}
}

// A forge account read is offered to its worker only, until it expires or is reported, and the first report is kept.
func TestAForgeAccountReadIsOfferedToItsWorkerUntilItIsReported(t *testing.T) {
	r := newTemplateRig(t, ledger.ProtocolVersion)
	other := r.secondWorker(ledger.ProtocolVersion)
	reads := control.NewForgeAccountReads(r.state.Installation, 0)
	view, err := reads.Request(t.Context(), "worker-1", ledger.ForgeAccountRequest{Forge: "github"}, projectMember)
	if err != nil || view.State != ledger.ForgeAccountReadOffered {
		t.Fatalf("view = %+v, err = %v", view, err)
	}
	offered := r.desired().ForgeAccountReads
	if len(offered) != 1 || offered[0].ID != view.ID || offered[0].Forge != "github" || offered[0].ExpiresAt != view.ExpiresAt {
		t.Fatalf("offered = %+v", offered)
	}
	if view.ExpiresAt-view.RequestedAt != ledger.ForgeAccountReadTTLMs {
		t.Errorf("it is offered for %d ms, want %d", view.ExpiresAt-view.RequestedAt, ledger.ForgeAccountReadTTLMs)
	}
	stranger, err := r.plane.Desired(t.Context(), ledger.DesiredRequest{Auth: other})
	if err != nil || len(stranger.ForgeAccountReads) != 0 {
		t.Errorf("another worker is offered %+v (%v)", stranger.ForgeAccountReads, err)
	}
	first := ledger.ForgeAccountReport{Auth: r.auth, Request: view.ID, State: ledger.ForgeAccountOK, Login: "test", GitProtocol: "https", Owners: []string{"test", "acme"}}
	if err := r.plane.ReportForgeAccount(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	later := first
	later.Login, later.State = "other", ledger.ForgeAccountSignedOut
	if err := r.plane.ReportForgeAccount(t.Context(), later); err != nil {
		t.Fatal(err)
	}
	got, err := reads.View(t.Context(), "worker-1", view.ID)
	if err != nil || got.State != ledger.ForgeAccountReadReported || got.Report == nil || got.Report.Login != "test" || !slices.Equal(got.Report.Owners, []string{"test", "acme"}) {
		t.Errorf("the read reports %+v (%v), want the first report", got, err)
	}
	if len(r.desired().ForgeAccountReads) != 0 {
		t.Error("a reported read is offered again")
	}
	events := 0
	for _, kind := range r.events() {
		if strings.HasPrefix(kind, "forge_account_read.") {
			events++
		}
	}
	if events != 2 {
		t.Errorf("%d forge_account_read events, want one for the request and one for the first report", events)
	}
	if _, err := reads.View(t.Context(), other.WorkerID, view.ID); err == nil {
		t.Error("another worker reads the request")
	}
	if err := r.plane.ReportForgeAccount(t.Context(), ledger.ForgeAccountReport{Auth: r.auth, Request: view.ID, State: "other"}); !errors.Is(err, workorders.ErrInvalid) {
		t.Errorf("an unknown state: %v", err)
	}
}
