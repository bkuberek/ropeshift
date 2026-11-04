package exit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

// The words a person must read on each run of ropeshift init. The vendor's own flow is the only
// sign-in, and nothing of ropeshift's touches it (NFR-INST-3, NFR-SEC-1).
const (
	initNoCLI       = "claude-code: not installed. Install Claude Code, sign in through Anthropic's own flow, and run ropeshift init again."
	initSignedOut   = "installed, not signed in. Run claude and sign in through Anthropic's own flow, or give the CLI an API key it reads itself, then run ropeshift init again."
	initNoCredsRead = "Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials. Each CLI keeps its own sign-in."
	initAttended    = "signed in with a subscription. This worker starts at attended"
	initRisk        = "ropeshift worker risk"
	initIsolation   = "same operating system user"
)

// TestInitOnEmptyMachine is the first run on a home with nothing in it, as a person meets it: no agent
// CLI at all, then one installed and signed out, then one signed in. ropeshift init sets the passphrase
// on the terminal once, registers the worker once, runs the doctor each time and says what is left,
// writes a sample contract intake reads, and leaves no daemon running.
func TestInitOnEmptyMachine(t *testing.T) {
	in := newInstall(t)
	in.member = false
	in.path = doubles.CanaryPathWithout(t)
	sample := filepath.Join(in.paths().StateDir, cli.SampleContractName)

	term := openTerminal(t, apitest.Passphrase, apitest.Passphrase)
	code, stdout, stderr := in.runWith(term, "", nil, "init")
	if code != cli.ExitError {
		t.Fatalf("with no agent CLI: exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	for _, want := range []string{initNoCLI, initNoCredsRead, initIsolation, "Passphrase: set", "Worker: registered as ", "Sample contract: wrote " + sample} {
		if !strings.Contains(stdout, want) {
			t.Errorf("with no agent CLI, the run did not say %q:\n%s", want, stdout)
		}
	}
	if shown := term.shown(); strings.Count(shown, ": ") != 2 {
		t.Errorf("the terminal showed %q", shown)
	}
	if _, err := os.Stat(sample); err != nil {
		t.Fatalf("no sample contract: %v", err)
	}
	if code, _, _ := in.run("health"); code != cli.ExitUnreachable {
		t.Fatalf("a daemon is still answering after init: exit %d", code)
	}

	in.path = doubles.InstallFakeClaude(t, "no-session")
	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "signed-out"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	quiet := openTerminal(t)
	code, stdout, stderr = in.runWith(quiet, "", nil, "init")
	if code != cli.ExitError {
		t.Fatalf("signed out: exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	for _, want := range []string{"claude-code 2.1.316: " + initSignedOut, initNoCredsRead, "Passphrase: already set", "Worker: registered as ", "Sample contract: kept " + sample} {
		if !strings.Contains(stdout, want) {
			t.Errorf("signed out, the run did not say %q:\n%s", want, stdout)
		}
	}
	if shown := quiet.shown(); shown != "" {
		t.Errorf("a second run asked for a passphrase: %q", shown)
	}

	in.path = doubles.InstallFakeClaude(t, "signed-in-subscription")
	code, stdout, stderr = in.runWith(openTerminal(t), "", nil, "init")
	if code != 0 {
		t.Fatalf("signed in: exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	for _, want := range []string{"claude-code 2.1.316: " + initAttended, initRisk, initNoCredsRead} {
		if !strings.Contains(stdout, want) {
			t.Errorf("signed in, the run did not say %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "not installed") || strings.Contains(stdout, "not signed in") {
		t.Errorf("signed in, the run still names a missing step:\n%s", stdout)
	}

	raw, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	repo := gittest.New(t)
	repo.Commit("sample-contract.md", string(raw))
	in.member = true
	d := in.start("--intake-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	deadline := time.Now().Add(30 * time.Second)
	for {
		code, stdout, _ := in.run("work", "list")
		if code == 0 && strings.Contains(stdout, "contract_review") && strings.Contains(stdout, "says how to run the tests") {
			break
		}
		if time.Now().After(deadline) {
			_, sources, _ := in.run("source", "list")
			t.Fatalf("intake took no work order from the sample:\n%s\nsources:\n%s", stdout, sources)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if workers := in.workers(); len(workers) != 1 {
		t.Errorf("%d workers after three runs and a start", len(workers))
	}
}
