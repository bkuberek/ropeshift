//go:build postgres

package exit

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
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
	// The connection string holds a password, so it reaches the server through the environment, never an argument.
	in.daemonEnv = append(in.daemonEnv, "ROPESHIFT_DATABASE="+dsn)
	args := append([]string{"server", "--listen", "127.0.0.1:0",
		"--heartbeat", "300ms", "--lease-ttl", "3s", "--lease-tick", "300ms", "--wake-tick", "300ms", "--intake-tick", "300ms", "--batch-tick", "300ms"}, extra...)
	d := in.startGroup(args...)
	in.waitReady(d)
	if command, err := procs.Command(d.cmd.Process.Pid); err != nil || strings.Contains(command, dsn) {
		t.Fatalf("the server's command line shows its connection string, or cannot be read: %q: %v", command, err)
	}
	client := api.NewUnixClient(in.paths().ControlSocket())
	client.UseSession(in.session)
	t.Cleanup(client.Close)
	return &aServer{install: in, d: d, address: d.said(t, "ropeshiftd: server "), dsn: dsn, client: client}
}

// joinString makes a join string as a member does, with `ropeshift worker token` on the server's machine.
func (s *aServer) joinString(t *testing.T) api.JoinString {
	t.Helper()
	printed := strings.TrimSpace(s.must("worker", "token"))
	joined, err := api.ParseJoinString(printed)
	if err != nil {
		t.Fatalf("worker token printed %q: %v", printed, err)
	}
	return joined
}

// join makes a worker of another state directory join the server with a join string a member made, and starts it.
// Neither the join string nor the token or the fingerprint in it is what the worker prints.
func (s *aServer) join(t *testing.T) (*install, *daemon) {
	t.Helper()
	return s.joinWith(t, s.joinString(t))
}

// joinWith is join with the join string of a token the caller made, so the caller knows which token the worker used.
func (s *aServer) joinWith(t *testing.T, joined api.JoinString) (*install, *daemon) {
	t.Helper()
	text, err := joined.Encode()
	if err != nil {
		t.Fatal(err)
	}
	machine := newInstall(t)
	// The join string holds a secret until its token is spent, so it reaches the worker through the environment.
	cmd := exec.Command(binaries.daemon, "worker", "join", "--name", "the-worker")
	cmd.Env = append(machine.env(), groupGate, "ROPESHIFT_JOIN="+text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("join: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	for _, secret := range []string{text, joined.Token, joined.CASHA256} {
		if secret != "" && strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Errorf("join printed a secret of its join string")
		}
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

// TestServerAndWorkerOverTLS is the group server of mode (a) with a worker in a state directory of its own, across
// HTTPS: the server serves a certificate its own authority issued, a member makes a join string with `ropeshift
// worker token`, and the worker joins with it, pinning the authority the string names. The server lists the worker,
// its heartbeats renew over the pinned link, and the fingerprint is in the worker's journal and in nothing either
// process printed. A worker that pins another authority is refused and says so.
func TestServerAndWorkerOverTLS(t *testing.T) {
	s := startServer(t, aDatabase(t), "--name", "127.0.0.1")
	joined := s.joinString(t)
	if !strings.HasPrefix(joined.WorkerURL, "https://127.0.0.1:") || len(joined.CASHA256) != 64 {
		t.Fatalf("the join string names %q pinned to %q", joined.WorkerURL, joined.CASHA256)
	}
	// The worker joins with this very string, so its token is the one a worker used.
	machine, worker := s.joinWith(t, joined)
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
	if line.Worker.Protocol != ledger.ProtocolVersion {
		t.Errorf("the worker is listed as %+v", line)
	}
	first := lastBeat(t, s.dsn)
	s.waitFor("the worker's heartbeats to renew over the pinned link", func() bool { return lastBeat(t, s.dsn) > first })

	journal, err := sqlite.OpenJournal(filepath.Join(machine.paths().StateDir, api.JournalFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	url, urlErr := journal.ServerURL(t.Context())
	pinned, pinErr := journal.ServerCA(t.Context())
	if urlErr != nil || pinErr != nil || url != joined.WorkerURL || pinned != joined.CASHA256 {
		t.Errorf("the worker's journal holds %q pinned to %q: %v, %v", url, pinned, urlErr, pinErr)
	}
	for name, said := range map[string]string{
		"the server": s.d.stdout.String() + s.d.stderr.String(),
		"the worker": worker.stdout.String() + worker.stderr.String(),
	} {
		if strings.Contains(said, joined.Token) {
			t.Errorf("%s printed the token of a join string", name)
		}
	}

	// A worker given the join string of another authority does not trust this server, and the token stays unspent.
	other := s.joinString(t)
	other.CASHA256 = strings.Repeat("ab", 32)
	text, err := other.Encode()
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := newInstall(t)
	cmd := exec.Command(binaries.daemon, "worker", "join", "--name", "wrong-pin")
	cmd.Env = append(elsewhere.env(), groupGate, "ROPESHIFT_JOIN="+text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil || !strings.Contains(stderr.String(), "pinned") {
		t.Errorf("a worker that pins another authority joined, or did not say why: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	for _, secret := range []string{text, other.Token, other.CASHA256, joined.CASHA256} {
		if strings.Contains(stdout.String()+stderr.String(), secret) {
			t.Errorf("the refusal shows a secret of a join string")
		}
	}
	for _, found := range s.floor(t).Workers {
		if found.Worker.Name == "wrong-pin" {
			t.Errorf("the server lists the worker that pinned another authority: %+v", found)
		}
	}

	// A member revokes a token no worker used, with the real command, and a worker that joins with its string is
	// refused, saying the token was revoked. A token that was used is refused to the revocation.
	var made api.JoinTokenResponse
	if err := json.Unmarshal([]byte(s.must("worker", "token", "--expires-in", "2h", "--json")), &made); err != nil {
		t.Fatal(err)
	}
	if lives := time.UnixMilli(made.ExpiresAt).Sub(time.Now()); made.ID == "" || lives < time.Hour || lives > 2*time.Hour+time.Minute {
		t.Errorf("a token made for two hours lives %s: %+v", lives, made)
	}
	if out := s.must("worker", "token-revoke", made.ID); !strings.Contains(out, made.ID) {
		t.Errorf("worker token-revoke prints %q", out)
	}
	revokedHere := newInstall(t)
	cmd = exec.Command(binaries.daemon, "worker", "join", "--name", "revoked")
	cmd.Env = append(revokedHere.env(), groupGate, "ROPESHIFT_JOIN="+made.JoinString)
	stdout.Reset()
	stderr.Reset()
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err == nil || !strings.Contains(stderr.String(), "revoked") || strings.Contains(stdout.String()+stderr.String(), made.Token) {
		t.Errorf("a worker joined with a revoked token, or did not say why: %v\n%s%s", err, stdout.String(), stderr.String())
	}
	if code, _, stderr := s.run("worker", "token-revoke", joined.Token[:strings.Index(joined.Token, ".")]); code == 0 {
		t.Errorf("a token a worker used was revoked: %s", stderr)
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
	in.daemonEnv = []string{"PATH=" + t.TempDir(), "ROPESHIFT_DATABASE=" + dsn}
	d := in.startGroup("server", "--listen", "127.0.0.1:0", "--wake-tick", "300ms", "--lease-tick", "300ms", "--intake-tick", "300ms")
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

// A control socket something answers on is another daemon's, and a server on a working database, which has
// opened it and found its project, still leaves that socket as it is and stops.
func TestServerLeavesTheSocketOfALiveDaemonAlone(t *testing.T) {
	in := newInstall(t)
	in.daemonEnv = append(in.daemonEnv, "ROPESHIFT_DATABASE="+aDatabase(t))
	socket := in.paths().ControlSocket()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	live, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = live.Close() })
	before, err := os.Stat(socket)
	if err != nil {
		t.Fatal(err)
	}
	server := in.startGroup("server", "--listen", "127.0.0.1:0")
	select {
	case err := <-server.exited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(server.stderr.String(), "answers") {
			t.Fatalf("the server ended with %v\n%s", err, server.stderr.String())
		}
	case <-time.After(60 * time.Second):
		t.Fatal("a server that finds a live socket is still running")
	}
	after, err := os.Stat(socket)
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the live daemon's socket was replaced: %v", err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("the live daemon's socket does not answer: %v", err)
	}
	_ = conn.Close()
}

// One server holds one installation: a second on the same state directory is refused as busy.
func TestServerHoldsItsInstallation(t *testing.T) {
	s := startServer(t, aDatabase(t))
	second := s.startGroup("server", "--listen", "127.0.0.1:0")
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

// healthOver reads the server's health as a member over TLS, through a client with the config.
func (s *aServer) healthOver(t *testing.T, config *tls.Config) (api.Health, error) {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.address+"/v1/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(api.HeaderSession, s.session)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return api.Health{}, err
	}
	defer response.Body.Close()
	var health api.Health
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health answered %d", response.StatusCode)
	}
	return health, json.NewDecoder(response.Body).Decode(&health)
}

// A server given a name serves TLS in a certificate its own authority issued, keeps the authority's keys to its
// user, and prints the fingerprint a client pins, which is the one in the join strings it makes. A client that pins
// that fingerprint reaches it, and one that pins another does not. The join token in a string is no part of what the
// server prints.
func TestServerServesTLSWithItsOwnAuthority(t *testing.T) {
	s := startServer(t, aDatabase(t), "--name", "127.0.0.1")
	if !strings.HasPrefix(s.address, "https://127.0.0.1:") {
		t.Fatalf("the server says it serves %q", s.address)
	}
	joined := s.joinString(t)
	fingerprint := joined.CASHA256
	if len(fingerprint) != 64 || joined.WorkerURL != s.address {
		t.Fatalf("the join string names %q pinned to %q, and the server serves %q", joined.WorkerURL, fingerprint, s.address)
	}
	if said := s.d.said(t, "ropeshiftd: worker url "); said != joined.WorkerURL {
		t.Errorf("the server says its workers reach it at %q, and its join strings say %q", said, joined.WorkerURL)
	}
	if printed := s.d.said(t, "ropeshiftd: certificate authority sha256 "); printed != fingerprint {
		t.Errorf("the server prints the fingerprint %q, and its join strings pin %q", printed, fingerprint)
	}
	if said := s.d.stdout.String() + s.d.stderr.String(); strings.Contains(said, joined.Token) {
		t.Errorf("the server's output shows the token of a join string: %s", said)
	}
	for _, name := range []string{"ca.key", "server.key"} {
		info, err := os.Stat(filepath.Join(s.paths().StateDir, "tls", name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	health, err := s.healthOver(t, api.PinnedTLS(fingerprint, "127.0.0.1"))
	if err != nil || health.Mode != api.ModeGroup {
		t.Fatalf("a client that pins the fingerprint reads %+v: %v", health, err)
	}
	if _, err := s.healthOver(t, api.PinnedTLS(strings.Repeat("0", 64), "127.0.0.1")); err == nil {
		t.Error("a client that pins another fingerprint reached the server")
	}
	if said := s.d.stdout.String(); strings.Contains(said, "server http://") {
		t.Errorf("a server that serves TLS says it serves http: %s", said)
	}
}

// A server given a certificate and key serves them, and a client that verifies through roots that hold the
// authority which made them reaches it.
func TestServerServesASuppliedCertificate(t *testing.T) {
	dir := t.TempDir()
	made, err := servertls.OpenAuthority(filepath.Join(dir, "made"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	for from, to := range map[string]string{servertls.CertFile: "tls.crt", servertls.KeyFile: "tls.key"} {
		raw, err := os.ReadFile(filepath.Join(dir, "made", from))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, to), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := startServer(t, aDatabase(t), "--tls-cert", filepath.Join(dir, "tls.crt"), "--tls-key", filepath.Join(dir, "tls.key"))
	if !strings.HasPrefix(s.address, "https://127.0.0.1:") {
		t.Fatalf("the server says it serves %q", s.address)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(made.CertificatePEM())
	health, err := s.healthOver(t, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil || health.Mode != api.ModeGroup {
		t.Fatalf("a client that trusts the authority reads %+v: %v", health, err)
	}
	if _, err := s.healthOver(t, &tls.Config{MinVersion: tls.VersionTLS12}); err == nil {
		t.Error("a client on the system roots reached a certificate no public authority made")
	}
	if _, err := os.Stat(filepath.Join(s.paths().StateDir, "tls")); err == nil {
		t.Error("a server that was given a certificate made an authority of its own")
	}
}

// A server refuses a client address after the wrong passphrases its settings allow, with 429 and a Retry-After,
// and the right passphrase from that address is refused with the rest. The control socket is no client address,
// so the person at the server's own terminal is not locked out.
func TestServerRefusesAnAddressThatGuessesTooOften(t *testing.T) {
	s := startServer(t, aDatabase(t), "--signin-max-failures", "3", "--signin-lockout", "10m")
	signIn := func(passphrase string) (*http.Response, []byte) {
		t.Helper()
		raw, err := json.Marshal(api.SignIn{Passphrase: passphrase})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.address+api.SignInPath, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, body
	}
	for i := range 3 {
		if response, body := signIn("a passphrase nobody chose"); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d answered %d: %s", i+1, response.StatusCode, body)
		}
	}
	response, body := signIn("a passphrase nobody chose")
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") != "600" || !strings.Contains(string(body), api.CodeTooManyAttempts) {
		t.Fatalf("the fourth guess answered %d, Retry-After %q: %s", response.StatusCode, response.Header.Get("Retry-After"), body)
	}
	if response, body := signIn(apitest.Passphrase); response.StatusCode != http.StatusTooManyRequests {
		t.Errorf("the right passphrase from a refused address answered %d: %s", response.StatusCode, body)
	}
	if _, err := s.client.Health(t.Context()); err != nil {
		t.Errorf("the control socket is refused after the network's address was: %v", err)
	}
}

// A server behind a reverse proxy that terminates TLS (mode c) takes plaintext from the proxy it lists, says where
// members reach it, reads the client from the proxy's X-Forwarded-For, marks its cookies Secure under its public
// URL, and answers none of a request its proxy says came in over plain http. The test is the proxy: it connects from
// loopback, which it lists.
func TestServerBehindAProxyReadsItsClientsFromAListedPeer(t *testing.T) {
	s := startServer(t, aDatabase(t), "--tls", "off", "--trusted-proxies", "127.0.0.1", "--public-url", "https://ropeshift.example", "--signin-max-failures", "2")
	if !strings.HasPrefix(s.address, "http://127.0.0.1:") {
		t.Fatalf("the server says it serves %q", s.address)
	}
	if got := s.d.said(t, "ropeshiftd: public url "); got != "https://ropeshift.example" {
		t.Fatalf("the server says its public URL is %q", got)
	}
	signIn := func(client, passphrase, proto string) (*http.Response, []byte) {
		t.Helper()
		raw, err := json.Marshal(api.SignIn{Passphrase: passphrase})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.address+api.SignInPath, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "ropeshift.example"
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "https://ropeshift.example")
		request.Header.Set("X-Forwarded-For", client)
		request.Header.Set("X-Forwarded-Proto", proto)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, body
	}
	for i := range 2 {
		if response, body := signIn("203.0.113.7", "a passphrase nobody chose", "https"); response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("guess %d answered %d: %s", i+1, response.StatusCode, body)
		}
	}
	if response, body := signIn("203.0.113.7", apitest.Passphrase, "https"); response.StatusCode != http.StatusTooManyRequests {
		t.Errorf("a client that guessed twice answered %d: %s", response.StatusCode, body)
	}
	response, body := signIn("203.0.113.8", apitest.Passphrase, "https")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("another client through the same proxy answered %d: %s", response.StatusCode, body)
	}
	for _, cookie := range response.Cookies() {
		if !cookie.Secure {
			t.Errorf("the cookie %s is not Secure under an https public URL", cookie.Name)
		}
	}
	if response, body := signIn("203.0.113.9", apitest.Passphrase, "http"); response.StatusCode != http.StatusForbidden || !strings.Contains(string(body), api.CodeInsecureTransport) {
		t.Errorf("a request the proxy said was http answered %d: %s", response.StatusCode, body)
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
