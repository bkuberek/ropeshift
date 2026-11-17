package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// rawCall sends one request over a socket as a member's session would, and returns the status and the body, which
// the client the tests use would swallow.
func rawCall(t *testing.T, socket, session, method, path string, body any) (int, []byte) {
	t.Helper()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), method, "http://ropeshift"+path, &payload)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(api.HeaderSession, session)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, out.Bytes()
}

// workerOf is the client a joined worker calls with.
func workerOf(t *testing.T, d *apitest.Daemon, credential string) *api.ProjectClient {
	t.Helper()
	worker := api.NewUnixClient(d.Socket).Project(apitest.ProjectName).WithCredential(credential)
	t.Cleanup(worker.Close)
	return worker
}

const writesPath = "/v1/workers/" + aSubscriptionWorker + "/template-writes"

// A member asks a worker to write a shape's files and is answered 202 with the request, which the worker is
// offered and then settles with its report. Registrable is true only for the worker this daemon runs, and only
// for a folder the report says is a clone.
func TestAMemberAsksAWorkerToWriteAShapesFilesAndReadsHowItStands(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	own := capsServe(t, d, fake.NewProfileSource(), aSubscriptionWorker)
	elsewhere := capsServe(t, d, fake.NewProfileSource(), "another-worker")
	body := api.TemplateWrite{
		Shape: "workspace", Path: "/srv/code/harbor", FirstCommit: true,
		Repositories: []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:acme/api.git"}},
	}
	status, raw := rawCall(t, own, d.Session, http.MethodPost, writesPath, body)
	if status != http.StatusAccepted {
		t.Fatalf("the request is answered %d: %s", status, raw)
	}
	var view ledger.TemplateWriteView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if view.ID == "" || view.State != ledger.TemplateWriteOffered || view.Worker != aSubscriptionWorker || view.Shape != "workspace" || view.Path != "/srv/code/harbor" || !view.FirstCommit || view.ExpiresAt <= view.RequestedAt {
		t.Fatalf("the view is %+v", view)
	}
	desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(desired.TemplateWrites) != 1 || desired.TemplateWrites[0].ID != view.ID || desired.TemplateWrites[0].Path != "/srv/code/harbor" {
		t.Fatalf("the worker is offered %+v", desired.TemplateWrites)
	}
	read := func(socket string) ledger.TemplateWriteView {
		t.Helper()
		status, raw := rawCall(t, socket, d.Session, http.MethodGet, writesPath+"/"+view.ID, nil)
		if status != http.StatusOK {
			t.Fatalf("the read is answered %d: %s", status, raw)
		}
		var out ledger.TemplateWriteView
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := read(own); got.State != ledger.TemplateWriteOffered || got.Registrable || got.Report != nil {
		t.Errorf("an offered request reads %+v", got)
	}
	report := ledger.TemplateWriteReport{
		Request: view.ID, Outcome: ledger.TemplateOutcomeWritten, Project: "harbor", Files: []string{"AGENTS.md", "Makefile"},
		Commit: "abc123", Clone: true, Commands: []string{"ropeshift project create harbor"},
	}
	if err := worker.ReportTemplateWrite(t.Context(), aSubscriptionWorker, report); err != nil {
		t.Fatal(err)
	}
	if err := worker.ReportTemplateWrite(t.Context(), aSubscriptionWorker, ledger.TemplateWriteReport{Request: view.ID, Outcome: ledger.TemplateOutcomeRefused, Reason: "path_not_empty"}); err != nil {
		t.Fatalf("a second report is refused: %v", err)
	}
	written := read(own)
	if written.State != ledger.TemplateWriteWritten || written.Report == nil || written.Report.Project != "harbor" || written.Report.Commit != "abc123" ||
		!slices.Equal(written.Report.Files, report.Files) || !slices.Equal(written.Report.Commands, report.Commands) || !written.Registrable {
		t.Errorf("a written request reads %+v", written)
	}
	if got := read(elsewhere); !got.Report.Clone || got.Registrable {
		t.Errorf("a clone on a worker this daemon does not run reads %+v, and the register route cannot reach it", got)
	}
	if desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil); err != nil || len(desired.TemplateWrites) != 0 {
		t.Errorf("a reported request is still offered: %+v (%v)", desired.TemplateWrites, err)
	}
}

// Each state of a request reads as its own: refused with the worker's reason, written with no clone, and a request
// no worker took before it expired.
func TestAReadOfATemplateWriteSaysRefusedWrittenWithNoCloneAndNotFound(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	own := capsServe(t, d, fake.NewProfileSource(), aSubscriptionWorker)
	ask := func() ledger.TemplateWriteView {
		t.Helper()
		view, err := d.Client.RequestTemplateWrite(t.Context(), aSubscriptionWorker, api.TemplateWrite{Shape: "monorepo", Path: "/srv/code/harbor"})
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	refused := ask()
	if err := worker.ReportTemplateWrite(t.Context(), aSubscriptionWorker, ledger.TemplateWriteReport{
		Request: refused.ID, Outcome: ledger.TemplateOutcomeRefused, Reason: ledger.TemplateRefusedInsideClone, Detail: "inside /srv/code",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := d.Client.TemplateWriteView(t.Context(), aSubscriptionWorker, refused.ID)
	if err != nil || got.State != ledger.TemplateWriteRefused || got.Report.Reason != ledger.TemplateRefusedInsideClone || got.Report.Detail != "inside /srv/code" || got.Registrable {
		t.Errorf("a refused request reads %+v (%v)", got, err)
	}
	plain := ask()
	if err := worker.ReportTemplateWrite(t.Context(), aSubscriptionWorker, ledger.TemplateWriteReport{
		Request: plain.ID, Outcome: ledger.TemplateOutcomeWritten, Project: "harbor", Clone: false,
	}); err != nil {
		t.Fatal(err)
	}
	status, raw := rawCall(t, own, d.Session, http.MethodGet, writesPath+"/"+plain.ID, nil)
	var written ledger.TemplateWriteView
	if err := json.Unmarshal(raw, &written); err != nil || status != http.StatusOK || written.State != ledger.TemplateWriteWritten || written.Registrable {
		t.Errorf("a folder that is no clone is registrable: %d %+v (%v)", status, written, err)
	}
	_, err = d.Client.TemplateWriteView(t.Context(), aSubscriptionWorker, "no-such-request")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	_, err = d.Client.TemplateWriteView(t.Context(), "another-worker", refused.ID)
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// The POST refuses, as invalid and before it records anything, a request no worker could carry out, and a
// worker that is not connected or speaks a protocol that would never report it.
func TestARequestToWriteFilesIsRefusedBeforeItIsRecorded(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	pair := []ledger.TemplateWriteRepository{{Name: "api", Remote: "git@example.com:a/b.git"}, {Name: "api", Remote: "git@example.com:a/c.git"}}
	cases := map[string]api.TemplateWrite{
		"another shape":           {Shape: "service", Path: "/srv/harbor"},
		"a relative path":         {Shape: "monorepo", Path: "srv/harbor"},
		"a parent element":        {Shape: "monorepo", Path: "/srv/../harbor"},
		"repositories on a repo":  {Shape: "repository", Path: "/srv/harbor", Repositories: pair[:1]},
		"a name refused":          {Shape: "workspace", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "a b", Remote: "git@example.com:a/b.git"}}},
		"a name twice":            {Shape: "workspace", Path: "/srv/harbor", Repositories: pair},
		"an empty remote":         {Shape: "workspace", Path: "/srv/harbor", Repositories: []ledger.TemplateWriteRepository{{Name: "api"}}},
		"a project on a monorepo": {Shape: "monorepo", Path: "/srv/harbor", Project: "harbor"},
		"a project that is none":  {Shape: "repository", Path: "/srv/harbor", Project: "Not A Project"},
	}
	for name, body := range cases {
		_, err := d.Client.RequestTemplateWrite(t.Context(), aSubscriptionWorker, body)
		var refused *api.Error
		if !errors.As(err, &refused) || refused.Status != http.StatusBadRequest || refused.Detail.Code != api.CodeInvalid {
			t.Errorf("%s: %v, want a 400 invalid", name, err)
		}
	}
	_, err := d.Client.RequestTemplateWrite(t.Context(), "nobody", api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor"})
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	if desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil); err != nil || len(desired.TemplateWrites) != 0 {
		t.Errorf("a refused request is offered: %+v (%v)", desired.TemplateWrites, err)
	}
	// A worker that registered and never opened a session has not reached the control plane.
	token, err := d.Client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.Register(t.Context(), ledger.Registration{JoinToken: token, WorkerID: "silent", Name: "silent"}); err != nil {
		t.Fatal(err)
	}
	_, err = d.Client.RequestTemplateWrite(t.Context(), "silent", api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor"})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if !strings.Contains(err.Error(), "not connected") {
		t.Errorf("the refusal says %v", err)
	}
}

// A worker on protocol 4 is accepted, as NFR-COMPAT-2 says, and is offered no template write: the request for it
// is refused with the upgrade it needs.
func TestAWorkerOnProtocol4IsAcceptedAndTheRequestForItIsRefused(t *testing.T) {
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
	session, err := older.OpenSession(t.Context(), "older", ledger.SessionRequest{Protocol: ledger.ProtocolTemplateWrites - 1})
	if err != nil || session.Protocol != ledger.ProtocolTemplateWrites-1 {
		t.Fatalf("a worker on protocol 4 is answered %+v (%v)", session, err)
	}
	_, err = d.Client.RequestTemplateWrite(t.Context(), "older", api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor"})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if want := "worker older speaks protocol 4; upgrade it to write files from the web UI"; !strings.Contains(err.Error(), want) {
		t.Errorf("the refusal says %v, want %q", err, want)
	}
	if desired, err := older.Desired(t.Context(), "older", 0, nil); err != nil || len(desired.TemplateWrites) != 0 {
		t.Errorf("a worker on protocol 4 is offered %+v (%v)", desired.TemplateWrites, err)
	}
}

// A delegate is refused as a member's alone, and nothing is recorded, because writing files at a path on a machine is
// no act a card could bind to.
func TestADelegateCannotAskForATemplateWrite(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	delegate := asDelegate(t, d.Socket, aDelegate(t, d.Client.Client, apitest.ProjectName).Token, apitest.ProjectName)
	err := delegate.Do(t.Context(), http.MethodPost, writesPath, api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor"}, nil)
	refusedWith(t, err, http.StatusForbidden, api.CodeMemberOnly)
	if desired, err := worker.Desired(t.Context(), aSubscriptionWorker, 0, nil); err != nil || len(desired.TemplateWrites) != 0 {
		t.Errorf("a delegate's call recorded %+v (%v)", desired.TemplateWrites, err)
	}
}

// With no session, the member's two routes refuse, and a request that names a report is a worker's, not a member's.
func TestTheTemplateWriteRoutesNeedASessionAndTheReportsNeedAWorker(t *testing.T) {
	d := apitest.Start(t)
	joinWorker(t, d)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	err := anonymous.Do(t.Context(), http.MethodPost, writesPath, api.TemplateWrite{Shape: "monorepo", Path: "/srv/harbor"}, nil)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	err = anonymous.Do(t.Context(), http.MethodPost, "/v1/workers/"+aSubscriptionWorker+"/template-reports", ledger.TemplateWriteReport{Request: "x", Outcome: "written"}, nil)
	refusedWith(t, err, http.StatusUnauthorized, api.CodeBadCredential)
}
