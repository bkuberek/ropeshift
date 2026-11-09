package exit

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/exit/ptypair"
	"ropeshift.com/ropeshift/test/gittest"
)

// heldTerminalFD is where a child finds its terminal: the first of cmd.ExtraFiles.
const heldTerminalFD = 3

// terminal is a person at a keyboard: it answers each prompt, and keeps what the screen
// showed.
type terminal struct {
	t      *testing.T
	typed  *os.File
	held   *os.File
	mu     sync.Mutex
	screen bytes.Buffer
	done   chan struct{}
}

func openTerminal(t *testing.T, answers ...string) *terminal {
	t.Helper()
	typed, held, err := ptypair.Open()
	if err != nil {
		t.Fatal(err)
	}
	term := &terminal{t: t, typed: typed, held: held, done: make(chan struct{})}
	t.Cleanup(func() { _ = typed.Close(); _ = held.Close() })
	go term.answer(answers)
	return term
}

// answer waits for each prompt before it types, as a person does, so nothing typed can
// arrive with echo still on.
func (term *terminal) answer(answers []string) {
	defer close(term.done)
	buffer := make([]byte, 256)
	asked := 0
	for {
		n, err := term.typed.Read(buffer)
		term.mu.Lock()
		term.screen.Write(buffer[:n])
		prompts := strings.Count(term.screen.String(), ": ")
		term.mu.Unlock()
		if err != nil {
			return
		}
		for asked < prompts && asked < len(answers) {
			_, _ = term.typed.WriteString(answers[asked] + "\n")
			asked++
		}
	}
}

func (term *terminal) shown() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.screen.String()
}

// runWith runs one command the way a shell's `$(...)` does: its stdout is a pipe, whatever
// its terminal is, and stdin holds what a test puts there.
func (in *install) runWith(term *terminal, stdin string, extraEnv []string, args ...string) (int, string, string) {
	in.t.Helper()
	cmd := exec.Command(binaries.cli, args...)
	cmd.Env = append(in.env(), extraEnv...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if term != nil {
		cmd.ExtraFiles = []*os.File{term.held}
		cmd.SysProcAttr.Setctty = true
		cmd.SysProcAttr.Ctty = heldTerminalFD
	}
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	in.t.Fatal(err)
	return 0, "", ""
}

var exportLine = regexp.MustCompile(`^export ` + authority.EnvMemberToken + `=[0-9a-f]+\n$`)

// TestThePassphraseComesOnlyFromTheTerminal holds D26: a pipe, a variable, and an argument
// can all be written by a lane, so none of them can sign anyone in.
func TestThePassphraseComesOnlyFromTheTerminal(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	offered := []string{"PASSPHRASE=" + apitest.Passphrase, "ROPESHIFT_PASSPHRASE=" + apitest.Passphrase}
	t.Run("with no terminal, stdin and the environment prove nothing", func(t *testing.T) {
		code, stdout, stderr := in.runWith(nil, apitest.Passphrase+"\n", offered, "login")
		if code != cli.ExitForbidden || stdout != "" {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		if !strings.Contains(stderr, "terminal") {
			t.Errorf("stderr %q", stderr)
		}
	})
	t.Run("with no terminal, a change is refused and leaves no record", func(t *testing.T) {
		code, _, stderr := in.runWith(nil, apitest.Passphrase+"\n", offered, "work", "create", "--id", "refused", "--title", "T", "--criterion", "C")
		if code != cli.ExitForbidden {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if code, _, _ := in.run("work", "show", "refused"); code != cli.ExitNotFound {
			t.Fatalf("the refused work order reads with exit %d", code)
		}
	})
	t.Run("no flag carries a passphrase", func(t *testing.T) {
		code, stdout, _ := in.runWith(nil, "", nil, "login", "--passphrase", apitest.Passphrase)
		if code != cli.ExitUsage || stdout != "" {
			t.Fatalf("exit %d, stdout %q", code, stdout)
		}
	})
	t.Run("the terminal signs in, past a wrong passphrase on stdin", func(t *testing.T) {
		term := openTerminal(t, apitest.Passphrase)
		code, stdout, stderr := in.runWith(term, "a wrong passphrase on a pipe\n", nil, "login")
		if code != 0 || !exportLine.MatchString(stdout) {
			t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
		}
		if shown := term.shown(); strings.Contains(shown, apitest.Passphrase) {
			t.Fatalf("the terminal echoed the passphrase: %q", shown)
		}
		token := strings.TrimSpace(strings.SplitN(stdout, "=", 2)[1])
		code, _, stderr = in.runWith(nil, "", []string{authority.EnvMemberToken + "=" + token}, "work", "create", "--id", "w1", "--title", "T", "--criterion", "C")
		if code != 0 {
			t.Fatalf("the printed session: exit %d, stderr %q", code, stderr)
		}
	})
	t.Run("a wrong passphrase on the terminal is refused", func(t *testing.T) {
		term := openTerminal(t, "not the passphrase at all")
		code, stdout, _ := in.runWith(term, "", nil, "login")
		if code != cli.ExitForbidden || stdout != "" {
			t.Fatalf("exit %d, stdout %q", code, stdout)
		}
	})
	t.Run("a change with no session asks once, and a read does not ask", func(t *testing.T) {
		term := openTerminal(t, apitest.Passphrase)
		code, _, stderr := in.runWith(term, "", nil, "work", "create", "--id", "w2", "--title", "T", "--criterion", "C")
		if code != 0 {
			t.Fatalf("exit %d, stderr %q", code, stderr)
		}
		if got := strings.Count(term.shown(), "Passphrase: "); got != 1 {
			t.Fatalf("asked %d times", got)
		}
		quiet := openTerminal(t)
		if code, _, stderr := in.runWith(quiet, "", nil, "work", "list"); code != 0 {
			t.Fatalf("a read: exit %d, stderr %q", code, stderr)
		}
		if shown := quiet.shown(); shown != "" {
			t.Fatalf("a read asked: %q", shown)
		}
	})
}

// TestSignOutTakesOnlyTheToken is a shell after eval "$(ropeshift login)": with no terminal, the
// token in the environment ends its session and a later command with it is refused. Signing out
// again finds no session to end and asks for nothing, even with a terminal that would answer.
func TestSignOutTakesOnlyTheToken(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	code, stdout, stderr := in.runWith(openTerminal(t, apitest.Passphrase), "", nil, "login")
	if code != 0 || !exportLine.MatchString(stdout) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	shell := []string{authority.EnvMemberToken + "=" + strings.TrimSpace(strings.SplitN(stdout, "=", 2)[1])}
	if code, stdout, stderr := in.runWith(nil, "", shell, "sign-out"); code != 0 || stdout != "signed out\n" {
		t.Fatalf("sign-out: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if code, _, stderr := in.runWith(nil, "", shell, "work", "create", "--id", "after", "--title", "T", "--criterion", "C"); code != cli.ExitForbidden {
		t.Fatalf("the ended session: exit %d, stderr %q", code, stderr)
	}
	term := openTerminal(t, apitest.Passphrase)
	code, _, stderr = in.runWith(term, "", shell, "sign-out")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "no session to end") {
		t.Errorf("signing out again: exit %d, stderr %q", code, stderr)
	}
	if shown := term.shown(); strings.Contains(shown, "Passphrase") {
		t.Errorf("sign-out asked for a passphrase: %q", shown)
	}
	code, _, stderr = in.runWith(nil, "", nil, "sign-out")
	if code != cli.ExitForbidden || !strings.Contains(stderr, "no session to end") {
		t.Errorf("no session: exit %d, stderr %q", code, stderr)
	}
}

func TestTheFirstPassphraseIsSetOnTheTerminal(t *testing.T) {
	in := newInstall(t)
	in.member = false
	d := in.start()
	in.waitReady(d)
	if code, _, stderr := in.runWith(openTerminal(t, "short", "short"), "", nil, "passphrase", "set"); code != cli.ExitUsage {
		t.Fatalf("a short passphrase: exit %d, stderr %q", code, stderr)
	}
	if code, _, _ := in.runWith(openTerminal(t, apitest.Passphrase, "something else"), "", nil, "passphrase", "set"); code != cli.ExitUsage {
		t.Fatalf("two that differ: exit %d", code)
	}
	if code, _, stderr := in.runWith(openTerminal(t, apitest.Passphrase), "", nil, "login"); code != cli.ExitForbidden || !strings.Contains(stderr, "no passphrase is set") {
		t.Fatalf("before one is set: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := in.runWith(openTerminal(t, apitest.Passphrase, apitest.Passphrase), "", nil, "passphrase", "set"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if code, stdout, stderr := in.runWith(openTerminal(t, apitest.Passphrase), "", nil, "login"); code != 0 || !exportLine.MatchString(stdout) {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	const changed = "a second passphrase altogether"
	if code, _, stderr := in.runWith(nil, "", nil, "passphrase", "set"); code != cli.ExitForbidden {
		t.Fatalf("no terminal: exit %d, stderr %q", code, stderr)
	}
	if code, _, stderr := in.runWith(openTerminal(t, changed, changed, apitest.Passphrase), "", nil, "passphrase", "set"); code != 0 {
		t.Fatalf("a change: exit %d, stderr %q", code, stderr)
	}
	if code, stdout, stderr := in.runWith(openTerminal(t, changed), "", nil, "login"); code != 0 || !exportLine.MatchString(stdout) {
		t.Fatalf("the new one: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

// TestNoSessionValueReachesAFile reads every file an installation holds, the ledger's
// included, for a session and a passphrase.
func TestNoSessionValueReachesAFile(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	in.must("work", "create", "--id", "w1", "--title", "T", "--criterion", "C")
	d.kill()
	secrets := [][]byte{[]byte(in.session), []byte(apitest.Passphrase)}
	seen := 0
	err := filepath.WalkDir(in.home, func(path string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		seen++
		for _, secret := range secrets {
			if bytes.Contains(raw, secret) {
				t.Errorf("%s holds a secret", filepath.Base(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("read no file, so the check proved nothing")
	}
}

// TestALaneNeverSeesTheMemberSession starts the daemon from a shell that has signed in,
// which is the path that could carry a session to a lane.
func TestALaneNeverSeesTheMemberSession(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-completed")
	in.daemonEnv = []string{authority.EnvMemberToken + "=a-session-from-the-shell"}
	d := in.start("--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", in.workers()[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the lane's outcome", func() bool { return in.show(id).State == workorders.Acceptance })
	if doubles.SeenEnv(t, in.path, "ROPESHIFT_LANE_TOKEN") == "" {
		t.Fatal("the session recorded no environment, so the check proved nothing")
	}
	if got := doubles.SeenEnv(t, in.path, authority.EnvMemberToken); got != "" {
		t.Fatalf("the lane saw %q", got)
	}
}
