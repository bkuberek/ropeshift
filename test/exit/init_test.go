package exit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
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
	// initFailedCheck is a CLI on PATH that cannot run, with how its version check failed in the middle.
	initFailedCheck = "claude-code: installed, and its version check failed: claude --version %s. Run claude --version to see why, repair or reinstall Claude Code, and run ropeshift init again."
	// doctorFailedCheck is the doctor's line for the same CLI.
	doctorFailedCheck = "claude-code\tunusable\tclaude --version %s. Run claude --version to see why, and repair or reinstall Claude Code.\n"
)

// TestInitRefusesACLIThatFailsItsVersionCheck is a `claude` on PATH that cannot run: one that exits with
// an error, one a signal ends, and one that prints no version. None is usable, so init names the failed
// check and the next step and exits 1, the doctor says the same, and nothing runs the CLI past its
// version check.
func TestInitRefusesACLIThatFailsItsVersionCheck(t *testing.T) {
	tests := []struct{ name, body, how string }{
		{"it exits with an error", "exit 70", "exited with status 70"},
		{"a signal ends it", "kill -KILL $$", "ended on signal SIGKILL"},
		{"it prints no version", "exit 0", "printed no version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := newInstall(t)
			in.member = false
			in.path = doubles.InstallBrokenClaude(t, tt.body)
			code, stdout, stderr := in.runWith(openTerminal(t, apitest.Passphrase, apitest.Passphrase), "", nil, "init")
			if want := fmt.Sprintf(initFailedCheck, tt.how); code != cli.ExitError || !strings.Contains(stdout, want) {
				t.Errorf("init: exit %d, want %d and %q\nstdout %s\nstderr %s", code, cli.ExitError, want, stdout, stderr)
			}
			if strings.Contains(stdout, "sign-in could not be read") || strings.Contains(stdout, "not installed") {
				t.Errorf("init reads a CLI that failed its version check as something else:\n%s", stdout)
			}
			code, stdout, stderr = in.run("worker", "doctor")
			if want := fmt.Sprintf(doctorFailedCheck, tt.how); code != cli.ExitError || stdout != want {
				t.Errorf("the doctor: exit %d, want %d and %q\nstdout %q\nstderr %s", code, cli.ExitError, want, stdout, stderr)
			}
			for _, run := range doubles.BrokenClaudeRuns(t, in.path) {
				if run != "--version" {
					t.Errorf("a CLI that failed its version check ran with %q", run)
				}
			}
		})
	}
}

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
