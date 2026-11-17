package cli_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

const aFingerprint = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// On a group server `ropeshift worker token` prints one join string and nothing else, which holds the server's URL, the
// fingerprint of its authority, and the token, so a member hands the whole of it to a worker.
func TestWorkerTokenOnAGroupServerPrintsOneJoinString(t *testing.T) {
	t.Parallel()
	s := newSessionWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400", CAFingerprint: aFingerprint})
	out := s.must("worker", "token")
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("worker token prints %q, which is not one line", out)
	}
	joined, err := api.ParseJoinString(strings.TrimSpace(out))
	if err != nil {
		t.Fatalf("worker token prints %q: %v", out, err)
	}
	if joined.WorkerURL != "https://ropeshift.lan:7400" || joined.CASHA256 != aFingerprint || joined.Token == "" {
		t.Errorf("the join string reads as %+v", joined)
	}
	if strings.Contains(out, joined.Token+"\n") && !strings.HasPrefix(out, api.JoinPrefix) {
		t.Errorf("worker token prints the bare token: %q", out)
	}
}

// With --json the answer carries the join string beside the token, which an automation reads.
func TestWorkerTokenAsJSONCarriesTheJoinString(t *testing.T) {
	t.Parallel()
	s := newSessionWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.example:443"})
	var made api.JoinTokenResponse
	if err := json.Unmarshal([]byte(s.must("worker", "token", "--json")), &made); err != nil {
		t.Fatal(err)
	}
	joined, err := api.ParseJoinString(made.JoinString)
	if err != nil || joined.Token != made.Token || joined.CASHA256 != "" {
		t.Errorf("the answer is %+v: %+v, %v", made, joined, err)
	}
}

// A solo daemon prints the token alone, as it did, because no worker on another machine joins it.
func TestWorkerTokenOnASoloDaemonPrintsTheTokenAlone(t *testing.T) {
	t.Parallel()
	s := newSession(t)
	out := strings.TrimSpace(s.must("worker", "token"))
	if out == "" || strings.HasPrefix(out, api.JoinPrefix) || strings.ContainsAny(out, " \t\n") {
		t.Errorf("a solo daemon prints %q", out)
	}
}

// A token lives a day unless --expires-in names another lifetime, up to thirty days. The command says when it ends and
// which token it is on its error stream, so a pipe of the join string carries nothing but the string, and --json carries
// all of it. A lifetime past the longest is refused as a usage error that names the longest.
func TestWorkerTokenTakesALifetimeAndSaysWhenItEnds(t *testing.T) {
	t.Parallel()
	s := newSessionWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400"})
	lives := func(args ...string) (time.Duration, api.JoinTokenResponse) {
		t.Helper()
		var made api.JoinTokenResponse
		before := time.Now()
		if err := json.Unmarshal([]byte(s.must(append([]string{"worker", "token", "--json"}, args...)...)), &made); err != nil {
			t.Fatal(err)
		}
		return time.UnixMilli(made.ExpiresAt).Sub(before), made
	}
	for name, test := range map[string]struct {
		args []string
		want time.Duration
	}{
		"the default":    {nil, 24 * time.Hour},
		"two hours":      {[]string{"--expires-in", "2h"}, 2 * time.Hour},
		"the longest":    {[]string{"--expires-in", "720h"}, 30 * 24 * time.Hour},
		"before a flag":  {[]string{"--expires-in=90m"}, 90 * time.Minute},
		"a few minutes ": {[]string{"--expires-in", "5m"}, 5 * time.Minute},
	} {
		got, made := lives(test.args...)
		if got < test.want-time.Minute || got > test.want+time.Minute || made.ID == "" || made.JoinString == "" {
			t.Errorf("%s: the token lives %s, and the answer is %+v", name, got, made)
		}
	}
	for _, args := range [][]string{{"--expires-in", "721h"}, {"--expires-in", "-1h"}, {"--expires-in", "8760h"}} {
		code, stdout, stderr := s.run(append([]string{"worker", "token"}, args...)...)
		if code != cli.ExitUsage || stdout != "" || !strings.Contains(stderr, "720h") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
	if code, _, stderr := s.run("worker", "token", "--expires-in", "tomorrow"); code != cli.ExitUsage {
		t.Errorf("a lifetime that is no duration: exit %d, stderr %q", code, stderr)
	}

	var stdout, stderr strings.Builder
	code := cli.Run([]string{"worker", "token", "--expires-in", "2h"}, &stdout, &stderr, cli.Options{Socket: s.daemon.Socket, Session: s.daemon.Session})
	if code != cli.ExitOK || strings.Count(stdout.String(), "\n") != 1 || !strings.HasPrefix(stdout.String(), api.JoinPrefix) {
		t.Fatalf("exit %d, stdout %q", code, stdout.String())
	}
	for _, want := range []string{"expires", "token-revoke"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q does not say %q", stderr.String(), want)
		}
	}
	joined, err := api.ParseJoinString(strings.TrimSpace(stdout.String()))
	if err != nil || strings.Contains(stderr.String(), joined.Token) {
		t.Errorf("stderr %q shows the token, or the string does not read: %v", stderr.String(), err)
	}
	if !strings.Contains(stderr.String(), strings.SplitN(joined.Token, ".", 2)[0]) {
		t.Errorf("stderr %q does not name the token to revoke", stderr.String())
	}
}

// `ropeshift worker token-revoke <id>` ends a token no worker used, which the server then refuses with its own code,
// says so again as no error, and refuses for a token a worker used or one that is not there.
func TestWorkerTokenRevokeEndsATokenNoWorkerUsed(t *testing.T) {
	t.Parallel()
	s := newSessionWith(t, apitest.Options{Mode: api.ModeGroup, WorkerURL: "https://ropeshift.lan:7400"})
	made := func() api.JoinTokenResponse {
		var made api.JoinTokenResponse
		if err := json.Unmarshal([]byte(s.must("worker", "token", "--json")), &made); err != nil {
			t.Fatal(err)
		}
		return made
	}
	plane := control.NewWorkers(s.daemon.Store, apitest.Scope, control.WorkerOptions{HeartbeatMs: apitest.HeartbeatMs, LeaseTTLMs: apitest.LeaseTTL})

	revoked := made()
	if out := s.must("worker", "token-revoke", revoked.ID); !strings.Contains(out, revoked.ID) {
		t.Errorf("worker token-revoke prints %q", out)
	}
	s.must("worker", "token-revoke", revoked.ID)
	if _, err := plane.Register(t.Context(), ledger.Registration{JoinToken: revoked.Token, WorkerID: "a", Name: "a"}); !errors.Is(err, ledger.ErrJoinTokenRevoked) {
		t.Errorf("a revoked token registers: %v", err)
	}

	used := made()
	if _, err := plane.Register(t.Context(), ledger.Registration{JoinToken: used.Token, WorkerID: "b", Name: "b"}); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := s.run("worker", "token-revoke", used.ID); code != cli.ExitConflict || !strings.Contains(stderr, "used") {
		t.Errorf("a used token: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := s.run("worker", "token-revoke", "no-such-token"); code != cli.ExitNotFound {
		t.Errorf("a token that is not there: exit %d", code)
	}
	for _, args := range [][]string{{"worker", "token-revoke"}, {"worker", "token-revoke", "one", "two"}} {
		if code, _, _ := s.run(args...); code != cli.ExitUsage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

// A group server that does not know the URL its workers reach it at makes no token, and the command says what to set
// on the server, in a sentence that holds no join token.
func TestWorkerTokenOnAGroupServerWithNoWorkerURLSaysWhatToSet(t *testing.T) {
	t.Parallel()
	s := newSessionWith(t, apitest.Options{Mode: api.ModeGroup})
	code, stdout, stderr := s.run("worker", "token")
	if code != cli.ExitConflict || stdout != "" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	for _, want := range []string{"--worker-url", "ROPESHIFT_WORKER_URL"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr %q does not say %s", stderr, want)
		}
	}
}
