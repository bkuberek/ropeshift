package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/test/apitest"
)

const practicesReportsPath = "/v1/workers/" + aSubscriptionWorker + "/practices-reports"

// A request the daemon offered a worker, as it stood when the worker took it.
func offeredPracticesRequest(t *testing.T, d *apitest.Daemon, id string) {
	t.Helper()
	now := time.Now().UnixMilli()
	err := d.Store.Write(t.Context(), apitest.Scope, "test.practices", func(tx ports.WriteTx) error {
		err := tx.Practices().Insert(ledger.PracticesRequest{
			ID: id, Repository: "repo-a", Worker: aSubscriptionWorker, Cause: ledger.PracticesMember, RequestedBy: "member-1",
			RequestedAt: now, ExpiresAt: now + ledger.PracticesRequestTTLMs, State: ledger.PracticesOffered,
		})
		if err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: ledger.EventPracticesRequested, Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}, Subject: ledger.Subject{Kind: ledger.SubjectPracticesRequest, ID: id}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A worker reports a practices check on its own route, with its credential, and the first report is recorded while a
// later one is accepted and changes nothing.
func TestAWorkerReportsAPracticesCheckOnItsOwnRoute(t *testing.T) {
	d := apitest.Start(t)
	credential, _ := joinWorker(t, d)
	worker := workerOf(t, d, credential)
	offeredPracticesRequest(t, d, "req-1")
	result := practices.Result{
		Repository: "repo-a", Commit: "1111111111111111111111111111111111111111", BaseBranch: "main", Worker: "plan", CheckedAt: time.Now().UTC().Truncate(time.Millisecond),
		TableVersion: practices.TableVersion, RulesVersion: 1, Practices: []practices.PracticeStatus{{ID: practices.Secrets, Status: practices.StatusMet}},
	}
	report := ledger.PracticesReport{Request: "req-1", Outcome: ledger.PracticesOutcomeChecked, Result: &result}
	if err := worker.ReportPracticesCheck(t.Context(), aSubscriptionWorker, report); err != nil {
		t.Fatal(err)
	}
	if err := worker.ReportPracticesCheck(t.Context(), aSubscriptionWorker, ledger.PracticesReport{Request: "req-1", Outcome: ledger.PracticesOutcomeFailed, Detail: "later"}); err != nil {
		t.Errorf("a later report answered %v, want it accepted", err)
	}
	err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		request, err := tx.Practices().Request("req-1")
		if err != nil || request.State != ledger.PracticesReported {
			t.Errorf("the request is %+v (%v)", request, err)
		}
		record, err := tx.Practices().Result("repo-a")
		if err != nil || record.Commit != result.Commit {
			t.Errorf("the result is %+v (%v)", record, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := worker.ReportPracticesCheck(t.Context(), aSubscriptionWorker, ledger.PracticesReport{Request: "missing", Outcome: ledger.PracticesOutcomeFailed, Detail: "x"}); err == nil {
		t.Error("a report of a request nobody made was accepted")
	}
}

// The report needs the worker's credential, and the route is the plane's: the network listener lists it and the web
// listener does not serve it.
func TestAPracticesReportNeedsAWorkersCredentialAndIsNotOnTheWebListener(t *testing.T) {
	d := apitest.Start(t)
	joinWorker(t, d)
	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	err := anonymous.Do(t.Context(), "POST", practicesReportsPath, ledger.PracticesReport{Request: "x", Outcome: ledger.PracticesOutcomeFailed, Detail: "x"}, nil)
	refusedWith(t, err, 401, api.CodeBadCredential)
	if !slices.ContainsFunc(server.PlaneRoutes(), func(r server.PlaneRoute) bool {
		return r.Method == "POST" && r.Pattern == "/v1/workers/{id}/practices-reports" && !r.JoinToken
	}) {
		t.Error("the plane's routes do not list the practices report")
	}
	if slices.ContainsFunc(api.WebRoutes(), func(r api.WebRoute) bool { return r.Pattern == "/v1/workers/{id}/practices-reports" }) {
		t.Error("the web listener serves the practices report")
	}
}

// A registration whose check cannot be requested still registers, and the server's log says what failed.
func TestARegistrationWhoseCheckCannotBeRequestedStillRegistersAndIsLogged(t *testing.T) {
	installation := apitest.StartInstallation(t)
	var mu sync.Mutex
	var logged []string
	config := installation.Config
	config.AssistantHosts = func(context.Context, ledger.WorkspaceID) (map[string]control.AssistantHost, error) {
		return nil, errors.New("the workers' checkouts cannot be read")
	}
	config.Warn = func(line string) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, line)
	}
	socket := filepath.Join(apitest.ShortDir(t), "register.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	member := api.NewUnixClient(socket)
	t.Cleanup(member.Close)
	member.UseSession(installation.Session)
	installation.Checkouts.Place(ports.Clone{Path: "/work/widgets", ID: "repo-widgets", Remote: "git@github.com:acme/widgets.git", DefaultBranch: "main", Directory: "widgets"})
	report, err := member.Project(apitest.ProjectName).AddRepositories(t.Context(), api.RepositoryAdd{Paths: []string{"/work/widgets"}})
	if err != nil || len(report.Repositories) != 1 || report.Repositories[0].Outcome != ledger.Registered {
		t.Fatalf("registering the clone reads %+v (%v)", report, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logged) != 1 || !strings.Contains(logged[0], "repo-widgets") || !strings.Contains(logged[0], "the workers' checkouts cannot be read") {
		t.Errorf("the log holds %q, want one line naming the repository and what failed", logged)
	}
}
