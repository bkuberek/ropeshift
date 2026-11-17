package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
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
