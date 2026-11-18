package server_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

const readsPath = "/v1/workers/" + aSubscriptionWorker + "/forge-account-reads"

func createOn(owner string) *ledger.ProjectRemote {
	return &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: owner}}
}

// The remote fields of a request are checked before anything is recorded, each with its own reason.
func TestARemoteIsRefusedBeforeItIsRecorded(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	cases := map[string]struct {
		body api.TemplateWrite
		says string
	}{
		"both":          {api.TemplateWrite{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "https://example.com/a.git", Create: createOn("acme").Create}}, "not both"},
		"neither":       {api.TemplateWrite{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{}}, "names a url or a repository to create"},
		"a dash url":    {api.TemplateWrite{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{URL: "-oProxyCommand=x"}}, "dash"},
		"another forge": {api.TemplateWrite{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: &ledger.ProjectRemote{Create: &ledger.RemoteCreate{Forge: "gitlab", Owner: "acme"}}}, "creating a repository on gitlab is not supported yet; ropeshift creates repositories on GitHub"},
		"a bad owner":   {api.TemplateWrite{Shape: "monorepo", Path: "/srv/h", FirstCommit: true, Remote: createOn("acme-")}, "no GitHub account name"},
		"no commit":     {api.TemplateWrite{Shape: "monorepo", Path: "/srv/h", Remote: createOn("acme")}, "needs the first commit"},
		"new on a repo": {api.TemplateWrite{Shape: "repository", Path: "/srv/h", Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true}}}, "names no new code repositories"},
	}
	for name, c := range cases {
		_, err := d.Client.RequestTemplateWrite(t.Context(), aSubscriptionWorker, c.body)
		var refused *api.Error
		if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest || refused.Detail.Code != api.CodeInvalid || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v, want a 400 invalid that says %q", name, err, c.says)
		}
	}
	if desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil); err != nil || len(desired.TemplateWrites) != 0 {
		t.Errorf("a refused request is offered: %+v (%v)", desired.TemplateWrites, err)
	}
}

// A request that creates a repository is answered 202 and offered with its remote, and a delegate's is refused as a
// member's alone under the operation that creates a remote.
func TestARequestToCreateARepositoryIsOfferedWithItsRemoteAndRefusedToADelegate(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	body := api.TemplateWrite{
		Shape: "workspace", Path: "/srv/code/harbor", FirstCommit: true, Remote: createOn("acme"),
		Repositories: []ledger.TemplateWriteRepository{{Name: "api", New: true, URL: "https://example.com/acme/api.git"}, {Name: "web", New: true}},
	}
	view, err := d.Client.RequestTemplateWrite(t.Context(), aSubscriptionWorker, body)
	if err != nil {
		t.Fatal(err)
	}
	if view.Remote == nil || view.Remote.Create == nil || view.Remote.Create.Owner != "acme" || len(view.Repositories) != 2 || !view.Repositories[0].New {
		t.Errorf("view = %+v", view)
	}
	desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil)
	if err != nil || len(desired.TemplateWrites) != 1 || desired.TemplateWrites[0].Remote == nil || desired.TemplateWrites[0].Repositories[0].URL != "https://example.com/acme/api.git" {
		t.Fatalf("offered %+v (%v)", desired.TemplateWrites, err)
	}
	delegate := asDelegate(t, d.Socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	err = delegate.Do(t.Context(), http.MethodPost, writesPath, body, nil)
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	if again, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil); err != nil || len(again.TemplateWrites) != 1 {
		t.Errorf("a delegate's call recorded a request: %+v (%v)", again.TemplateWrites, err)
	}
}

// A worker on the version before the remotes protocol is accepted and refused a request that holds a remote.
func TestAWorkerOnProtocol5IsAcceptedAndARequestWithARemoteForItIsRefused(t *testing.T) {
	d := apitest.Start(t)
	token, err := d.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: "older", Name: "older"})
	if err != nil {
		t.Fatal(err)
	}
	older := workerOf(t, d, credential.Credential)
	// A control plane accepts a worker one protocol behind its own, so a worker on protocol 5 is a session an earlier
	// build opened, which outlives the upgrade: it is opened on the current protocol and then recorded the way that
	// build recorded it.
	if _, err := older.OpenSession(t.Context(), "older", ledger.SessionRequest{Protocol: ledger.ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	err = d.Store.Installation(apitest.Scope).Write(t.Context(), "test.record_protocol", func(tx ports.SharedTx) error {
		record, err := tx.Workers().Find("older")
		if err != nil {
			return err
		}
		next := record.Worker
		next.Protocol = ledger.ProtocolProjectRemotes - 1
		if _, err = tx.Workers().OpenEpoch(next); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "worker.session_opened", Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}, Subject: ledger.Subject{Kind: "worker", ID: "older"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.RequestTemplateWrite(t.Context(), "older", api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor", FirstCommit: true, Remote: createOn("acme")})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if want := "worker older speaks protocol 5; upgrade it to create a project's remote"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal says %v, want %q", err, want)
	}
	if _, err := d.Client.RequestTemplateWrite(t.Context(), "older", api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor"}); err != nil {
		t.Errorf("a plain write for a worker on protocol 5: %v", err)
	}
	_, err = d.Client.RequestForgeAccountRead(t.Context(), "older", ledger.ForgeAccountRequest{Forge: "github"})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
}

// A member asks a worker to read its forge account, is answered 202, and reads the worker's report.
func TestAMemberAsksAWorkerToReadItsForgeAccountAndReadsTheReport(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	own := capsServe(t, d, fake.NewProfileSource(), aSubscriptionWorker)
	status, raw := rawCall(t, own, d.Session, http.MethodPost, readsPath, ledger.ForgeAccountRequest{Forge: "github"})
	if status != http.StatusAccepted {
		t.Fatalf("the request is answered %d: %s", status, raw)
	}
	var view ledger.ForgeAccountReadView
	if err := json.Unmarshal(raw, &view); err != nil || view.ID == "" || view.State != ledger.ForgeAccountReadOffered || view.Forge != "github" {
		t.Fatalf("view = %+v (%v)", view, err)
	}
	desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil)
	if err != nil || len(desired.ForgeAccountReads) != 1 || desired.ForgeAccountReads[0].ID != view.ID {
		t.Fatalf("offered %+v (%v)", desired.ForgeAccountReads, err)
	}
	report := ledger.ForgeAccountReport{Request: view.ID, State: ledger.ForgeAccountOK, Login: "test", GitProtocol: "ssh", Owners: []string{"test", "acme"}}
	if err := worker.ReportForgeAccount(t.Context(), aSubscriptionWorker, report); err != nil {
		t.Fatal(err)
	}
	status, raw = rawCall(t, own, d.Session, http.MethodGet, readsPath+"/"+view.ID, nil)
	var got ledger.ForgeAccountReadView
	if status != http.StatusOK || json.Unmarshal(raw, &got) != nil || got.State != ledger.ForgeAccountReadReported || got.Report == nil ||
		got.Report.GitProtocol != "ssh" || !slices.Equal(got.Report.Owners, []string{"test", "acme"}) {
		t.Fatalf("the read is answered %d: %s", status, raw)
	}
	if status, _ := rawCall(t, own, d.Session, http.MethodGet, readsPath+"/nothing", nil); status != http.StatusNotFound {
		t.Errorf("an unknown read is answered %d", status)
	}
	if status, _ := rawCall(t, own, d.Session, http.MethodPost, readsPath, ledger.ForgeAccountRequest{Forge: "gitlab"}); status != http.StatusBadRequest {
		t.Errorf("another forge is answered %d", status)
	}
}

// The forge account read is a member's alone, needs a session, and its report needs the worker's credential.
func TestTheForgeAccountRoutesNeedASessionAMemberAndAWorker(t *testing.T) {
	d := apitest.Start(t)
	joinWorker(t, d)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	err := anonymous.Do(t.Context(), http.MethodPost, readsPath, ledger.ForgeAccountRequest{Forge: "github"}, nil)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	err = anonymous.Do(t.Context(), http.MethodPost, "/v1/workers/"+aSubscriptionWorker+"/forge-account-reports", ledger.ForgeAccountReport{Request: "x", State: "ok"}, nil)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadCredential)
	delegate := asDelegate(t, d.Socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	err = delegate.Do(t.Context(), http.MethodPost, readsPath, ledger.ForgeAccountRequest{Forge: "github"}, nil)
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
}
