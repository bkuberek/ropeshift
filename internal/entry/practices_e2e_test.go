package entry

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/adapters/workerlink"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/practices"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestARegisteredRepositoryIsCheckedByTheDaemonsWorkerAndItsResultIsRecorded is FR-PRAC-1 end to end, as a solo daemon
// and its worker run it. Registering a clone asks the daemon's own worker, which keeps its checkout, for a check. The
// worker takes the request through desired state, runs the check on the clone with this machine's git, and reports,
// and the ledger holds the result: the repository has exactly one `env-ignored` finding, its secrets are met, and no
// path of the worker's machine is in what it recorded. Registering the clone again asks for nothing more.
func TestARegisteredRepositoryIsCheckedByTheDaemonsWorkerAndItsResultIsRecorded(t *testing.T) {
	gittest.Require(t)
	repo := gittest.New(t)
	repo.Commit("go.mod", "module acme.test/widgets\n\ngo 1.24\n")
	repo.Commit("main.go", "package main\n\nfunc main() {}\n")
	head := repo.Git("rev-parse", "HEAD")
	installation := apitest.StartInstallation(t)
	ctx := t.Context()

	// The worker: its journal, the checkouts it keeps, and the practices runtime a solo daemon gives it, on this
	// test's git.
	state := t.TempDir()
	kept := &checkouts.File{Path: filepath.Join(state, checkouts.FileName), Env: repo.Env(), Reserved: []string{state}}
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	if err := journal.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	runtime := soloPractices(state, laneCheckout(checkoutOf(soloOptions{}, kept)))
	runtime.Sources.Snapshots = practicegit.Snapshots{Env: repo.Env()}
	runtime.Sources.Secrets = practicegit.SecretScanner{Env: repo.Env()}
	socket := api.NewUnixClient(installation.Socket)
	t.Cleanup(socket.Close)
	subject := worker.New(workerlink.New(socket), journal, clock.System{}, worker.Config{
		Name:      "local",
		JoinToken: installation.Client.CreateJoinToken,
		Practices: runtime,
		Report:    func(err error) { t.Errorf("the worker reported %v", err) },
	})
	if err := subject.Start(ctx); err != nil {
		t.Fatal(err)
	}
	local := subject.Auth().WorkerID

	// The daemon names that worker as the one it runs, and reads what it keeps, as a solo daemon does.
	hosts := &soloHosts{kept: kept}
	hosts.serve(subject)
	config := installation.Config
	config.Checkouts = kept
	config.LocalWorker = func(context.Context) (string, error) { return local, nil }
	config.AssistantHosts = hosts.read
	listener := filepath.Join(apitest.ShortDir(t), "web.sock")
	bound, err := net.Listen("unix", listener)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.New(config), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpServer.Serve(bound) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	member := api.NewUnixClient(listener)
	t.Cleanup(member.Close)
	member.UseSession(installation.Session)
	project := member.Project(apitest.ProjectName)

	report, err := project.AddRepositories(ctx, api.RepositoryAdd{Paths: []string{repo.Dir}})
	if err != nil || len(report.Repositories) != 1 || report.Repositories[0].Outcome != ledger.Registered {
		t.Fatalf("registering the clone reads %+v (%v)", report, err)
	}
	id := report.Repositories[0].Repository.ID
	if err := subject.Converge(ctx); err != nil {
		t.Fatal(err)
	}

	var record ledger.PracticesRecord
	var request ledger.PracticesRequest
	err = installation.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		var err error
		if record, err = tx.Practices().Result(id); err != nil {
			return err
		}
		request, err = tx.Practices().Request(record.Request)
		return err
	})
	if err != nil {
		t.Fatalf("reading the recorded result: %v", err)
	}
	if request.Cause != ledger.PracticesRegistration || request.State != ledger.PracticesReported || request.Worker != local {
		t.Errorf("the request is %+v, want a registration's, reported, of the daemon's worker", request)
	}
	result := record.Result
	if record.Commit != head || result.Commit != head || result.Worker != "local" || result.Repository != id {
		t.Errorf("the result reads %s by %q of %q, want %s by local of %s", result.Commit, result.Worker, result.Repository, head, id)
	}
	var env []string
	for _, finding := range result.Findings {
		if finding.Practice == practices.EnvIgnored {
			env = append(env, finding.ID)
		}
	}
	if !slices.Equal(env, []string{"env-ignored:ignore:."}) {
		t.Errorf("the env-ignored findings are %v, want only the root's", env)
	}
	for _, status := range result.Practices {
		if status.ID == practices.Secrets && status.Status != practices.StatusMet {
			t.Errorf("secrets are %+v", status)
		}
	}
	if raw, _ := json.Marshal(record); strings.Contains(string(raw), repo.Dir) {
		t.Errorf("the recorded result names the checkout: %.300s", raw)
	}
	err = installation.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		counts := map[string]int{}
		for _, event := range events {
			counts[event.Type]++
		}
		if counts[ledger.EventPracticesRequested] != 1 || counts[ledger.EventPracticesChecked] != 1 || counts[ledger.EventPracticesFailed] != 0 {
			t.Errorf("the log holds %v, want one request and one check", counts)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	// The clone is registered already, so registering it again asks for nothing.
	if again, err := project.AddRepositories(ctx, api.RepositoryAdd{Paths: []string{repo.Dir}}); err != nil || again.Repositories[0].Outcome != ledger.AlreadyRegistered {
		t.Fatalf("registering again reads %+v (%v)", again, err)
	}
	if err := subject.Converge(ctx); err != nil {
		t.Fatal(err)
	}
	err = installation.Store.Read(ctx, apitest.Scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		n := len(slices.DeleteFunc(events, func(e ledger.Event) bool { return e.Type != ledger.EventPracticesRequested }))
		if n != 1 {
			t.Errorf("registering again left %d requests", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
