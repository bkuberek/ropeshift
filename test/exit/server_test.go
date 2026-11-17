//go:build postgres

package exit

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/pgtest"
	"ropeshift.com/ropeshift/test/procs"
)

// A group server and its workers run as separate processes (ADR-0031), each in a state directory of its own,
// with a private Postgres for the server and a kernel-chosen loopback port. These tests run in
// `make check-postgres`, and never touch the installation of the person who runs them.

const groupGate = "ROPESHIFT_GROUP_MODE=preview"

// aFewServerTicks is how long a test lets a server's ticks run, at the intervals it started them with.
const aFewServerTicks = 2 * time.Second

// exitBusy is the exit code of a daemon that finds its installation held.
const exitBusy = 3

// aDatabase starts a private Postgres for one test, and returns the connection string of an empty database
// on it. The server stops after every process the test started.
func aDatabase(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	postgres, err := pgtest.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := postgres.Stop(); err != nil {
			t.Error(err)
		}
	})
	name, err := postgres.NewDatabase(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return postgres.DSN(name)
}

// startGroup starts a group command of the daemon with the preview gate set, as the one process of an
// install.
func (in *install) startGroup(args ...string) *daemon {
	in.t.Helper()
	cmd := exec.Command(binaries.daemon, args...)
	cmd.Env = append(append(in.env(), groupGate), in.daemonEnv...)
	d := &daemon{cmd: cmd, stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, exited: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = d.stdout, d.stderr
	if err := procs.Start(in.t, cmd); err != nil {
		in.t.Fatal(err)
	}
	go func() { d.exited <- cmd.Wait() }()
	in.t.Cleanup(func() { d.kill() })
	in.daemons = append(in.daemons, d)
	return d
}

// said waits for a line a daemon prints, and returns what follows the prefix.
func (d *daemon) said(t *testing.T, prefix string) string {
	t.Helper()
	bounds := exitWaits
	bounds.poll = 10 * time.Millisecond
	var found string
	heard, why := bounds.await(func() bool {
		for line := range strings.Lines(d.stdout.String()) {
			if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
				found = rest
				return true
			}
		}
		return false
	})
	if !heard {
		t.Fatalf("the daemon never said %q: the wait %s%s%s", prefix, why, d.stdout.String(), d.stderr.String())
	}
	return found
}

// aServer is a group server on a private database, with the member signed in on its control socket.
type aServer struct {
	*install
	d       *daemon
	address string
	dsn     string
	client  *api.Client
}

// startServer starts a group server in a state directory of its own, and waits for it to answer.
func startServer(t *testing.T, dsn string, extra ...string) *aServer {
	t.Helper()
	in := newInstall(t)
	args := append([]string{"server", "--database", dsn, "--listen", "127.0.0.1:0",
		"--heartbeat", "300ms", "--lease-ttl", "3s", "--lease-tick", "300ms", "--wake-tick", "300ms", "--intake-tick", "300ms", "--batch-tick", "300ms"}, extra...)
	d := in.startGroup(args...)
	in.waitReady(d)
	client := api.NewUnixClient(in.paths().ControlSocket())
	client.UseSession(in.session)
	t.Cleanup(client.Close)
	return &aServer{install: in, d: d, address: d.said(t, "ropeshiftd: server "), dsn: dsn, client: client}
}

// join makes a worker of another state directory join the server with a join token a member made, and starts
// it.
func (s *aServer) join(t *testing.T) (*install, *daemon) {
	t.Helper()
	token, err := s.client.CreateJoinToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	machine := newInstall(t)
	cmd := exec.Command(binaries.daemon, "worker", "join", "--server", s.address, "--name", "the-worker", token)
	cmd.Env = append(machine.env(), groupGate)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("join: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if strings.Contains(stdout.String()+stderr.String(), token) {
		t.Errorf("join printed its token")
	}
	return machine, machine.startGroup("worker")
}

// floor reads the server's shop floor, which lists its workers with their drivers.
func (s *aServer) floor(t *testing.T) api.ShopFloor {
	t.Helper()
	var floor api.ShopFloor
	if err := s.client.Project(ledger.DefaultProjectName).ReadView(t.Context(), api.ViewShopFloor, "", &floor); err != nil {
		t.Fatal(err)
	}
	return floor
}

// lastBeat reads when the server last heard the worker beat, straight from the installation's table.
func lastBeat(t *testing.T, dsn string) int64 {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var beat int64
	if err := db.QueryRowContext(t.Context(), `SELECT coalesce(max(last_beat_at), 0) FROM installation.workers`).Scan(&beat); err != nil {
		t.Fatal(err)
	}
	return beat
}

// TestServerAndWorkerOnLoopback runs a server on a private Postgres and a worker in another state
// directory: the worker joins with a join token, the server lists it with its drivers in Workers and
// resources, its heartbeats renew, and a stopped worker shows offline.
func TestServerAndWorkerOnLoopback(t *testing.T) {
	s := startServer(t, aDatabase(t))
	_, worker := s.join(t)
	var line api.WorkerLine
	s.waitFor("the worker in the server's list, with its drivers", func() bool {
		for _, found := range s.floor(t).Workers {
			if found.Worker.Name == "the-worker" && len(found.Drivers) > 0 {
				line = found
				return true
			}
		}
		return false
	})
	if line.Drivers[0].Driver != "claude-code" || line.Worker.Protocol != ledger.ProtocolVersion {
		t.Errorf("the worker is listed as %+v", line)
	}
	first := lastBeat(t, s.dsn)
	s.waitFor("the worker's heartbeats to renew", func() bool { return lastBeat(t, s.dsn) > first })
	worker.stop()
	s.waitFor("the stopped worker to show offline", func() bool {
		for _, found := range s.floor(t).Workers {
			if found.Worker.Name == "the-worker" {
				return found.Freshness == ledger.FreshnessLastReported
			}
		}
		return false
	})
	stopped := lastBeat(t, s.dsn)
	time.Sleep(aFewServerTicks)
	if now := lastBeat(t, s.dsn); now != stopped {
		t.Errorf("a stopped worker's heartbeats went on: %d, then %d", stopped, now)
	}
}

// The server's listener takes a credential on every route: a request with none is refused, a member's
// session as a bearer token and the web UI's cookie open what a member may, and a worker's credential opens
// the plane and nothing else.
func TestServerListenerTakesACredentialOnEveryRoute(t *testing.T) {
	s := startServer(t, aDatabase(t))
	call := func(method, path string, headers map[string]string, body any) (*http.Response, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(raw)
		}
		request, err := http.NewRequestWithContext(t.Context(), method, s.address+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			request.Header.Set("Content-Type", "application/json")
		}
		for name, value := range headers {
			request.Header.Set(name, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(response.Body)
		return response, raw
	}
	for _, path := range []string{"/v1/health", "/v1/workers", "/v1/members", "/v1/passphrase", "/v1/projects"} {
		if response, raw := call(http.MethodGet, path, nil, nil); response.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s with no credential answered %d: %s", path, response.StatusCode, raw)
		}
	}
	// Nobody sets the first passphrase over the network.
	if response, raw := call(http.MethodPut, api.PassphrasePath, nil, api.SetPassphrase{Passphrase: "taken over"}); response.StatusCode != http.StatusUnauthorized {
		t.Errorf("setting the passphrase with no credential answered %d: %s", response.StatusCode, raw)
	}
	response, raw := call(http.MethodGet, "/v1/health", map[string]string{api.HeaderSession: s.session}, nil)
	var health api.Health
	if response.StatusCode != http.StatusOK || json.Unmarshal(raw, &health) != nil || health.Mode != api.ModeGroup {
		t.Errorf("a member's session reads health as %d %s", response.StatusCode, raw)
	}
	if response, raw := call(http.MethodPost, api.SignInPath, nil, api.SignIn{Passphrase: apitest.Passphrase}); response.StatusCode != http.StatusCreated {
		t.Errorf("signing in through the open route answered %d: %s", response.StatusCode, raw)
	} else if len(response.Cookies()) == 0 {
		t.Error("signing in set no cookie")
	}
	// A worker's credential reaches the plane and no route of a member.
	machine, _ := s.join(t)
	s.waitFor("the worker to register", func() bool { return len(s.floor(t).Workers) == 1 })
	credential := workerCredential(t, machine)
	for _, path := range []string{"/v1/workers", "/v1/members", "/v1/health", "/v1/projects"} {
		if response, raw := call(http.MethodGet, path, map[string]string{"Authorization": "Bearer " + credential}, nil); response.StatusCode != http.StatusForbidden {
			t.Errorf("GET %s with a worker credential answered %d: %s", path, response.StatusCode, raw)
		}
	}
	id := s.floor(t).Workers[0].Worker.ID
	if response, raw := call(http.MethodPost, "/v1/workers/"+id+"/sessions", map[string]string{"Authorization": "Bearer " + credential}, ledger.SessionRequest{Protocol: ledger.ProtocolVersion}); response.StatusCode != http.StatusOK {
		t.Errorf("the worker's own session call answered %d: %s", response.StatusCode, raw)
	}
	// The same server answers every control-plane tick with no error on Postgres.
	time.Sleep(aFewServerTicks)
	if bad := tickErrors(s.d.stderr.String()); bad != "" {
		t.Errorf("a tick failed on Postgres:\n%s", bad)
	}
}

// tickErrors lists the lines in which a tick of the daemon reported a failure.
func tickErrors(stderr string) string {
	var bad []string
	for line := range strings.Lines(stderr) {
		if strings.Contains(line, " tick: ") {
			bad = append(bad, strings.TrimSpace(line))
		}
	}
	return strings.Join(bad, "\n")
}

// The server needs no git, no tmux, and no agent CLI on its machine: it runs with an empty PATH, and a read
// it would make from a clone is refused with a code that names it.
func TestServerRunsWithNothingOfAWorkersMachine(t *testing.T) {
	dsn := aDatabase(t)
	in := newInstall(t)
	in.daemonEnv = []string{"PATH=" + t.TempDir()}
	d := in.startGroup("server", "--database", dsn, "--listen", "127.0.0.1:0", "--wake-tick", "300ms", "--lease-tick", "300ms", "--intake-tick", "300ms")
	in.waitReady(d)
	client := api.NewUnixClient(in.paths().ControlSocket())
	client.UseSession(in.session)
	t.Cleanup(client.Close)
	err := client.Project(ledger.DefaultProjectName).ReadView(t.Context(), api.ViewDocuments, "", new(api.Documents))
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Detail.Code != api.CodeRepositoryReadsUnavailable || refused.Status != http.StatusNotImplemented {
		t.Fatalf("documents on a server answered %v", err)
	}
	if !strings.Contains(refused.Detail.Message, "documents") {
		t.Errorf("the refusal names no read: %s", refused.Detail.Message)
	}
	time.Sleep(aFewServerTicks)
	if bad := tickErrors(d.stderr.String()); bad != "" {
		t.Errorf("a tick failed with no git, tmux, or agent CLI:\n%s", bad)
	}
}

// One server holds one installation: a second on the same state directory is refused as busy.
func TestServerHoldsItsInstallation(t *testing.T) {
	s := startServer(t, aDatabase(t))
	second := s.startGroup("server", "--database", s.dsn, "--listen", "127.0.0.1:0")
	select {
	case err := <-second.exited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != exitBusy {
			t.Fatalf("a second server on one installation ended with %v\n%s", err, second.stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a second server on one installation is still running")
	}
	if _, err := s.client.Health(t.Context()); err != nil {
		t.Errorf("the first server stopped answering: %v", err)
	}
}

// workerCredential reads the credential a worker keeps in its journal, as only its user may.
func workerCredential(t *testing.T, machine *install) string {
	t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(machine.paths().StateDir, api.JournalFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	identity, err := journal.Identity(t.Context())
	if err != nil || identity.Credential == "" {
		t.Fatalf("the worker's journal holds %+v: %v", identity, err)
	}
	return identity.Credential
}
