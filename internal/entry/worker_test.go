package entry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// joinRig is a server on a loopback port, the isolated state a worker keeps, and a way to read that state.
type joinRig struct {
	t      *testing.T
	d      *apitest.Daemon
	server string
	paths  api.Paths
}

func joinOn(t *testing.T) *joinRig {
	t.Helper()
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	d := apitest.Start(t)
	network := d.ServeNetwork(t, nil, 0, nil)
	paths, err := installationPaths()
	if err != nil {
		t.Fatal(err)
	}
	return &joinRig{t: t, d: d, server: "http://" + network.Host, paths: paths}
}

// token is a join token the server made, as `ropeshift worker token` makes one.
func (r *joinRig) token() string {
	r.t.Helper()
	plane := control.NewWorkers(r.d.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})
	token, err := plane.CreateJoinToken(r.t.Context(), ledger.Actor{Kind: ledger.ActorSystem, ID: "test"})
	if err != nil {
		r.t.Fatal(err)
	}
	return token
}

// kept opens the journal the worker keeps, and reads what a join left in it.
func (r *joinRig) kept() (ledger.WorkerIdentity, string, error) {
	r.t.Helper()
	journal, err := sqlite.OpenJournal(filepath.Join(r.paths.StateDir, api.JournalFileName))
	if err != nil {
		r.t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.Migrate(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	identity, identityErr := journal.Identity(r.t.Context())
	url, urlErr := journal.ServerURL(r.t.Context())
	if identityErr != nil {
		return identity, url, identityErr
	}
	return identity, url, urlErr
}

// A worker joins with a join token a member made: it registers, and keeps the server's address and its
// credential in its journal, which only its user reads.
func TestAWorkerJoinsWithAJoinTokenAndKeepsWhatItNeedsToReturn(t *testing.T) {
	r := joinOn(t)
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, "--name", "laptop", r.token())
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	identity, url, err := r.kept()
	if err != nil {
		t.Fatal(err)
	}
	if url != r.server || identity.WorkerID == "" || identity.Credential == "" {
		t.Fatalf("the journal holds %q and %+v", url, identity)
	}
	if !strings.Contains(stdout, identity.WorkerID) || strings.Contains(stdout, identity.Credential) {
		t.Errorf("join says %q, which must name the worker and never its credential", stdout)
	}
	workers, err := r.d.Client.Workers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].ID != identity.WorkerID || workers[0].Name != "laptop" {
		t.Errorf("the server lists %+v for worker %s", workers, identity.WorkerID)
	}
	for _, path := range []string{r.paths.StateDir, filepath.Join(r.paths.StateDir, api.JournalFileName)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s is readable by others: %o", path, mode)
		}
	}
}

// A worker that holds a credential has joined, and a second join does not pretend to register again: it
// refuses, says which server the worker joined, and leaves what the first left, the join token unspent.
func TestJoiningAgainWithTheSameServerIsRefused(t *testing.T) {
	r := joinOn(t)
	if code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, r.token()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	first, url, err := r.kept()
	if err != nil {
		t.Fatal(err)
	}
	second := r.token()
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, second)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "already joined") || !strings.Contains(stderr, url) {
		t.Fatalf("a second join: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if strings.Contains(stderr, second) {
		t.Errorf("the refusal repeats the token: %q", stderr)
	}
	kept, again, err := r.kept()
	if err != nil || kept != first || again != url {
		t.Errorf("a second join left %+v at %q after %+v at %q: %v", kept, again, first, url, err)
	}
	if workers, err := r.d.Client.Workers(t.Context()); err != nil || len(workers) != 1 {
		t.Errorf("the server lists %d workers: %v", len(workers), err)
	}
	// The token it did not spend still joins a worker that has not joined.
	t.Setenv(api.EnvStateDir, t.TempDir())
	t.Setenv(api.EnvRunDir, t.TempDir())
	if code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, second); code != exitOK {
		t.Errorf("the unspent token joined no other worker: exit %d: %s", code, stderr)
	}
}

// A worker holds one server. A join to another leaves the first as it was, and says so.
func TestAWorkerThatJoinedOneServerJoinsNoOther(t *testing.T) {
	r := joinOn(t)
	if code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, r.token()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	first, url, err := r.kept()
	if err != nil {
		t.Fatal(err)
	}
	other := "http://127.0.0.1:1"
	code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", other, "a-token")
	if code != exitError || !strings.Contains(stderr, url) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	second, again, err := r.kept()
	if err != nil || second != first || again != url {
		t.Errorf("the journal holds %+v at %q after %+v at %q: %v", second, again, first, url, err)
	}
}

// A token the server refuses leaves the worker unjoined: no credential and no server address, so a worker
// started after it says it has joined nothing.
func TestAJoinTheServerRefusesLeavesNothingBehind(t *testing.T) {
	r := joinOn(t)
	code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, "not-a-join-token")
	if code != exitError || stderr == "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if strings.Contains(stderr, "not-a-join-token") {
		t.Errorf("the refusal repeats the token: %q", stderr)
	}
	identity, url, err := r.kept()
	if identity.Credential != "" {
		t.Errorf("a refused join left the credential %q", identity.Credential)
	}
	if !errors.Is(err, ports.ErrNotFound) || url != "" {
		t.Errorf("a refused join left the server address %q: %v", url, err)
	}
	// The same worker joins with a good token afterwards, as a person who mistyped one would.
	if code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, r.token()); code != exitOK {
		t.Fatalf("a join after a refusal: exit %d: %s", code, stderr)
	}
	if identity, url, err := r.kept(); err != nil || identity.Credential == "" || url != r.server {
		t.Errorf("after a good join the journal holds %+v at %q: %v", identity, url, err)
	}
}

// A token is single use: the server makes one credential for it, and a second worker with it gets none.
func TestAJoinTokenJoinsOneWorker(t *testing.T) {
	r := joinOn(t)
	token := r.token()
	if code, _, stderr := runMain(commandDaemon, "worker", "join", "--server", r.server, token); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// A second machine's worker: its own state, the same token.
	t.Setenv(api.EnvStateDir, t.TempDir())
	t.Setenv(api.EnvRunDir, t.TempDir())
	if code, _, _ := runMain(commandDaemon, "worker", "join", "--server", r.server, token); code != exitError {
		t.Errorf("a used token joined a second worker: exit %d", code)
	}
}

func TestJoinTakesOnlyALoopbackHTTPServerInThisVersion(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	for _, server := range []string{"https://127.0.0.1:7400", "http://192.168.1.2:7400", "http://server.example:7400", "127.0.0.1:7400", "http://user:secret@127.0.0.1:7400"} {
		code, stdout, stderr := runMain(commandDaemon, "worker", "join", "--server", server, "a-token")
		if code != exitUsage || stdout != "" || strings.Contains(stderr, "secret") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", server, code, stdout, stderr)
		}
	}
}

func TestJoinNeedsAServerAndOneToken(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	for _, args := range [][]string{
		{"join"},
		{"join", "a-token"},
		{"join", "--server", "http://127.0.0.1:7400"},
		{"join", "--server", "http://127.0.0.1:7400", "one", "two"},
	} {
		if code, _, stderr := runMain(append([]string{commandDaemon, "worker"}, args...)...); code != exitUsage || stderr == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr)
		}
	}
}

// A worker that never joined has no server to dial, and says how to join one.
func TestAWorkerThatNeverJoinedSaysHowToJoin(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv(api.EnvTelemetry, api.WebOff)
	done := make(chan struct{})
	var code int
	var stderr string
	go func() {
		defer close(done)
		code, _, stderr = runMain(commandDaemon, "worker")
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("a worker that joined nothing is still running")
	}
	if code != exitError || !strings.Contains(stderr, "worker join") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// The group worker commands refuse without the preview gate, as the server does.
func TestTheWorkerCommandsRefuseWithoutThePreviewGate(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "")
	for _, args := range [][]string{{"worker"}, {"worker", "join", "--server", "http://127.0.0.1:7400", "a-token"}, {"worker", "--help"}} {
		code, stdout, stderr := runMain(append([]string{commandDaemon}, args...)...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, groupRefusal) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv(api.EnvStateDir), api.JournalFileName)); err == nil {
		t.Error("a refused worker command made a journal")
	}
}
