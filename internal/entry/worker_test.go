package entry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/servertls"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

// joinRig is a server on a loopback port, the isolated state a worker keeps, and a way to read that state. A rig of
// TLS has its own authority, whose fingerprint a join string pins.
type joinRig struct {
	t *testing.T
	d *apitest.Daemon
	// server is the URL a worker reaches the server at, and pin the fingerprint of its authority, which is empty
	// when it serves plaintext.
	server string
	pin    string
	paths  api.Paths
}

func joinOn(t *testing.T) *joinRig {
	t.Helper()
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_JOIN", "")
	d := apitest.Start(t)
	network := d.ServeNetwork(t, nil, 0, nil)
	paths, err := installationPaths()
	if err != nil {
		t.Fatal(err)
	}
	return &joinRig{t: t, d: d, server: "http://" + network.Host, paths: paths}
}

// joinOnTLS is a rig whose server serves TLS in a certificate its own authority issued, as a server of mode (a) does.
func joinOnTLS(t *testing.T) *joinRig {
	t.Helper()
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_JOIN", "")
	made, err := servertls.OpenAuthority(filepath.Join(t.TempDir(), "tls"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	d := apitest.Start(t)
	network := d.ServeNetworkWith(t, apitest.NetworkOptions{TLS: made.TLSConfig()})
	paths, err := installationPaths()
	if err != nil {
		t.Fatal(err)
	}
	return &joinRig{t: t, d: d, server: "https://" + network.Host, pin: made.Fingerprint(), paths: paths}
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

// joinString is a join string of a new token for the server of the rig.
func (r *joinRig) joinString() string {
	r.t.Helper()
	return r.joinStringOf(r.token(), r.server, r.pin)
}

// joinStringOf makes a join string of a token, which is a server's URL and the fingerprint it pins.
func (r *joinRig) joinStringOf(token, url, pin string) string {
	r.t.Helper()
	made, err := api.JoinString{WorkerURL: url, CASHA256: pin, Token: token}.Encode()
	if err != nil {
		r.t.Fatal(err)
	}
	return made
}

// kept opens the journal the worker keeps, and reads what a join left in it.
func (r *joinRig) kept() (ledger.WorkerIdentity, string, error) {
	r.t.Helper()
	identity, url, _, err := r.keptLink()
	return identity, url, err
}

// keptLink reads the identity, the server's address, and the fingerprint a worker pinned.
func (r *joinRig) keptLink() (ledger.WorkerIdentity, string, string, error) {
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
	ca, _ := journal.ServerCA(r.t.Context())
	if identityErr != nil {
		return identity, url, ca, identityErr
	}
	return identity, url, ca, urlErr
}

// hasNone fails the test for every secret a program's output shows.
func hasNone(t *testing.T, what string, shown []string, secrets ...string) {
	t.Helper()
	for _, text := range shown {
		for _, secret := range secrets {
			if secret != "" && strings.Contains(text, secret) {
				t.Errorf("%s shows a secret: %q", what, text)
			}
		}
	}
}

// A worker joins with a join string a member made: it registers, and keeps the server's address and its
// credential in its journal, which only its user reads. It prints neither the join string nor the token in it.
func TestAWorkerJoinsWithAJoinStringAndKeepsWhatItNeedsToReturn(t *testing.T) {
	r := joinOn(t)
	token := r.token()
	joined := r.joinStringOf(token, r.server, "")
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", "--name", "laptop", joined)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	identity, url, ca, err := r.keptLink()
	if err != nil {
		t.Fatal(err)
	}
	if url != r.server || ca != "" || identity.WorkerID == "" || identity.Credential == "" {
		t.Fatalf("the journal holds %q, the fingerprint %q, and %+v", url, ca, identity)
	}
	if !strings.Contains(stdout, identity.WorkerID) || !strings.Contains(stdout, r.server) {
		t.Errorf("join says %q, which must name the worker and the server", stdout)
	}
	hasNone(t, "join", []string{stdout, stderr}, identity.Credential, token, joined)
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

// A join string that pins an authority joins over TLS, trusting that authority and no other, and the fingerprint is
// kept in the journal for every start after it. The fingerprint is a credential, so no output shows it.
func TestAWorkerThatPinsAnAuthorityJoinsOverTLSAndKeepsThePin(t *testing.T) {
	r := joinOnTLS(t)
	token := r.token()
	joined := r.joinStringOf(token, r.server, r.pin)
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", "--name", "pinned", joined)
	if code != exitOK || stderr != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	identity, url, ca, err := r.keptLink()
	if err != nil || url != r.server || ca != r.pin || identity.Credential == "" {
		t.Fatalf("the journal holds %q, the fingerprint %q, and %+v: %v", url, ca, identity, err)
	}
	hasNone(t, "join", []string{stdout, stderr}, r.pin, token, joined, identity.Credential)
	if workers, err := r.d.Client.Workers(t.Context()); err != nil || len(workers) != 1 {
		t.Errorf("the server lists %d workers: %v", len(workers), err)
	}
	// A worker that starts after it reads the same link from its journal.
	link, err := joinedServer(t.Context(), r.paths, os.Stderr)
	if err != nil || link.URL != r.server || link.CASHA256 != r.pin {
		t.Errorf("a worker that starts reads the link %+v: %v", link, err)
	}
}

// A server whose certificate does not chain to the authority the join string pins is refused, and the refusal says
// the pin did not hold. The worker has joined nothing, the join token is unspent, and no output shows the token or
// either fingerprint.
func TestAWorkerRefusesAServerThatIsNotTheAuthorityItPinned(t *testing.T) {
	r := joinOnTLS(t)
	other, err := servertls.OpenAuthority(filepath.Join(t.TempDir(), "tls"), servertls.Options{Names: []string{"127.0.0.1"}, Clock: clock.System{}})
	if err != nil {
		t.Fatal(err)
	}
	token := r.token()
	wrong := r.joinStringOf(token, r.server, other.Fingerprint())
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", wrong)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "pinned") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	hasNone(t, "the refusal", []string{stdout, stderr}, token, wrong, other.Fingerprint(), r.pin)
	identity, url, ca, err := r.keptLink()
	if identity.Credential != "" || url != "" || ca != "" || !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a refused join left %+v at %q pinned to %q: %v", identity, url, ca, err)
	}
	// The token it never presented joins the worker when the pin holds.
	if code, _, stderr := runMain(commandDaemon, "worker", "join", r.joinStringOf(token, r.server, r.pin)); code != exitOK {
		t.Errorf("the unspent token joined no worker: exit %d: %s", code, stderr)
	}
}

// A join string that pins nothing is verified through the system's roots, which a server's own authority is not in, so
// the worker refuses it and says the roots do not vouch for it, and that is the system roots and not a pin.
func TestAWorkerThatPinsNothingRefusesAServerTheSystemRootsDoNotVouchFor(t *testing.T) {
	r := joinOnTLS(t)
	token := r.token()
	unpinned := r.joinStringOf(token, r.server, "")
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", unpinned)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "system roots") || strings.Contains(stderr, "pinned") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	hasNone(t, "the refusal", []string{stdout, stderr}, token, unpinned, r.pin)
	if identity, url, _, err := r.keptLink(); identity.Credential != "" || url != "" || !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a refused join left %+v at %q: %v", identity, url, err)
	}
	if code, _, stderr := runMain(commandDaemon, "worker", "join", r.joinStringOf(token, r.server, r.pin)); code != exitOK {
		t.Errorf("the unspent token joined no worker: exit %d: %s", code, stderr)
	}
}

// A join string refused as no join string says what kind of wrong it is, and never repeats it: a string that is
// nearly right holds a token. The worker asks nothing of any server and writes nothing.
func TestAJoinStringThatIsNoJoinStringIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_JOIN", "")
	for _, given := range []string{
		"a-join-token-nobody-may-see",
		"ropeshift-join-2.eyJ3b3JrZXJfdXJsIjoiaHR0cDovLzEyNy4wLjAuMTo3NDAwIiwidG9rZW4iOiJub2JvZHktbWF5LXNlZSJ9",
		"ropeshift-join-1.!!!nobody-may-see",
		"ropeshift-join-1.eyJ3b3JrZXJfdXJsIjoiaHR0cDovL2V4YW1wbGUub3JnOjc0MDAiLCJ0b2tlbiI6Im5vYm9keS1tYXktc2VlIn0",
	} {
		code, stdout, stderr := runMain(commandDaemon, "worker", "join", given)
		if code != exitUsage || stdout != "" || stderr == "" {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", given, code, stdout, stderr)
		}
		hasNone(t, "the refusal", []string{stdout, stderr}, "nobody-may-see", given)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv(api.EnvStateDir), api.JournalFileName)); err == nil {
		t.Error("a refused join made a journal")
	}
}

// A worker that holds a credential has joined, and a second join does not pretend to register again: it
// refuses, says which server the worker joined, and leaves what the first left, the join token unspent.
func TestJoiningAgainWithTheSameServerIsRefused(t *testing.T) {
	r := joinOn(t)
	if code, _, stderr := runMain(commandDaemon, "worker", "join", r.joinString()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	first, url, err := r.kept()
	if err != nil {
		t.Fatal(err)
	}
	second := r.token()
	again := r.joinStringOf(second, r.server, "")
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", again)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "already joined") || !strings.Contains(stderr, url) {
		t.Fatalf("a second join: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	hasNone(t, "the refusal", []string{stderr}, second, again)
	kept, same, err := r.kept()
	if err != nil || kept != first || same != url {
		t.Errorf("a second join left %+v at %q after %+v at %q: %v", kept, same, first, url, err)
	}
	if workers, err := r.d.Client.Workers(t.Context()); err != nil || len(workers) != 1 {
		t.Errorf("the server lists %d workers: %v", len(workers), err)
	}
	// The token it did not spend still joins a worker that has not joined.
	t.Setenv(api.EnvStateDir, t.TempDir())
	t.Setenv(api.EnvRunDir, apitest.ShortDir(t))
	if code, _, stderr := runMain(commandDaemon, "worker", "join", again); code != exitOK {
		t.Errorf("the unspent token joined no other worker: exit %d: %s", code, stderr)
	}
}

// A worker holds one server. A join to another leaves the first as it was, and says so.
func TestAWorkerThatJoinedOneServerJoinsNoOther(t *testing.T) {
	r := joinOn(t)
	if code, _, stderr := runMain(commandDaemon, "worker", "join", r.joinString()); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	first, url, err := r.kept()
	if err != nil {
		t.Fatal(err)
	}
	other := r.joinStringOf("a-token", "http://127.0.0.1:1", "")
	code, _, stderr := runMain(commandDaemon, "worker", "join", other)
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
	bad := r.joinStringOf("not-a-join-token", r.server, "")
	code, _, stderr := runMain(commandDaemon, "worker", "join", bad)
	if code != exitError || stderr == "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	hasNone(t, "the refusal", []string{stderr}, "not-a-join-token", bad)
	identity, url, ca, err := r.keptLink()
	if identity.Credential != "" {
		t.Errorf("a refused join left the credential %q", identity.Credential)
	}
	if !errors.Is(err, ports.ErrNotFound) || url != "" || ca != "" {
		t.Errorf("a refused join left the server address %q: %v", url, err)
	}
	// The same worker joins with a good join string afterwards, as a person who mistyped one would.
	if code, _, stderr := runMain(commandDaemon, "worker", "join", r.joinString()); code != exitOK {
		t.Fatalf("a join after a refusal: exit %d: %s", code, stderr)
	}
	if identity, url, err := r.kept(); err != nil || identity.Credential == "" || url != r.server {
		t.Errorf("after a good join the journal holds %+v at %q: %v", identity, url, err)
	}
}

// A join string is single use: the server makes one credential for its token, and a second worker with it gets none,
// whichever machine it comes from, and is told no more than that the server refused the join.
func TestAJoinStringJoinsOneWorker(t *testing.T) {
	r := joinOn(t)
	joined := r.joinString()
	if code, _, stderr := runMain(commandDaemon, "worker", "join", joined); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// A second machine's worker: its own state, the same join string.
	t.Setenv(api.EnvStateDir, t.TempDir())
	t.Setenv(api.EnvRunDir, apitest.ShortDir(t))
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", joined)
	if code != exitError || stdout != "" || !strings.Contains(stderr, "refused") {
		t.Errorf("a used join string joined a second worker: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	hasNone(t, "the refusal", []string{stderr}, joined)
	if workers, err := r.d.Client.Workers(t.Context()); err != nil || len(workers) != 1 {
		t.Errorf("the server lists %d workers: %v", len(workers), err)
	}
}

// A join string is a secret for as long as its token is unspent, and an argument is visible to every process on the
// machine, so a container's way to give it, the environment, is the documented one, and an argument still works.
func TestAJoinStringComesFromTheEnvironmentToo(t *testing.T) {
	r := joinOn(t)
	joined := r.joinString()
	t.Setenv("ROPESHIFT_JOIN", joined)
	code, stdout, stderr := runMain(commandDaemon, "worker", "join")
	if code != exitOK || strings.Contains(stdout+stderr, joined) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	identity, url, err := r.kept()
	if err != nil || identity.Credential == "" || url != r.server {
		t.Errorf("the journal holds %+v at %q: %v", identity, url, err)
	}
}

// Two ways to give one join string leave no way to say which is meant, so the join stops before it asks the server.
func TestAJoinStringGivenTwiceIsRefused(t *testing.T) {
	r := joinOn(t)
	joined := r.joinString()
	t.Setenv("ROPESHIFT_JOIN", joined)
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", joined)
	if code != exitUsage || stdout != "" || strings.Contains(stderr, joined) || !strings.Contains(stderr, "ROPESHIFT_JOIN") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if workers, err := r.d.Client.Workers(t.Context()); err != nil || len(workers) != 0 {
		t.Errorf("the server lists %d workers: %v", len(workers), err)
	}
}

// The help says to give the join string in the environment, and no help or usage error shows one that is there.
func TestTheWorkerHelpNamesTheJoinVariableAndNeverItsValue(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_JOIN", "a-join-string-nobody-may-see")
	code, stdout, stderr := runMain(commandDaemon, "worker", "--help")
	if code != exitOK || !strings.Contains(stdout, "ROPESHIFT_JOIN") || !strings.Contains(stdout, "join string") || strings.Contains(stdout, "--server") {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	hasNone(t, "the help", []string{stdout, stderr}, "nobody-may-see")
	_, _, stderr = runMain(commandDaemon, "worker", "join", "--no-such-flag")
	hasNone(t, "a usage error", []string{stderr}, "nobody-may-see")
	_, _, stderr = runMain(commandDaemon, "worker", "join")
	hasNone(t, "a join of a string that is no join string", []string{stderr}, "nobody-may-see")
}

// The --server of the first version is gone: a server is named by the join string that carries its URL and its pin,
// and a join that names one another way is a usage error, which says to use a join string.
func TestJoinTakesNoServerFlag(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_JOIN", "")
	code, stdout, stderr := runMain(commandDaemon, "worker", "join", "--server", "http://127.0.0.1:7400", "a-token")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "server") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestJoinNeedsOneJoinString(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "preview")
	t.Setenv("ROPESHIFT_JOIN", "")
	for _, args := range [][]string{
		{"join"},
		{"join", "one", "two"},
		{"join", "--name"},
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
	if code != exitError || !strings.Contains(stderr, "worker join") || !strings.Contains(stderr, "join string") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

// The group worker commands refuse without the preview gate, as the server does.
func TestTheWorkerCommandsRefuseWithoutThePreviewGate(t *testing.T) {
	isolate(t)
	t.Setenv("ROPESHIFT_GROUP_MODE", "")
	for _, args := range [][]string{{"worker"}, {"worker", "join", "ropeshift-join-1.e30"}, {"worker", "--help"}} {
		code, stdout, stderr := runMain(append([]string{commandDaemon}, args...)...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, groupRefusal) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(os.Getenv(api.EnvStateDir), api.JournalFileName)); err == nil {
		t.Error("a refused worker command made a journal")
	}
}
