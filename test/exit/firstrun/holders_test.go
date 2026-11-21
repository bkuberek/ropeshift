package main

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/test/exit/ptypair"
)

// requireProc skips a test that reads /proc on a machine with none, such as a macOS host. The container the first run
// runs in is where these hold.
func requireProc(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(procRoot + "/self/fd"); err != nil {
		t.Skipf("this machine lists no processes in %s: %v", procRoot, err)
	}
}

// leftBehind is the ID of the process a step's script started to hold its terminal, which writes it to pidFile once it
// runs, and which the test ends when it is over.
func leftBehind(t *testing.T, pidFile string) int {
	t.Helper()
	var raw []byte
	deadline := time.Now().Add(5 * time.Second)
	for len(strings.TrimSpace(string(raw))) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the step's script started no process that holds its terminal")
		}
		time.Sleep(10 * time.Millisecond)
		raw, _ = os.ReadFile(pidFile)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

// A terminal that a process still holds open names that process, with its ID, its command line, and the descriptor it
// holds the terminal on, and names it no more once it ended.
func TestTheHoldersOfATerminalAreNamedWithTheirCommandLineAndDescriptors(t *testing.T) {
	requireProc(t)
	typed, held, err := ptypair.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = typed.Close(); _ = held.Close() })
	slave := held.Name()
	sleeper := exec.Command("sleep", "30")
	sleeper.Stdin = held
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() })
	_ = held.Close()
	want := fmt.Sprintf("pid %d holds %s on descriptor 0: sleep 30", sleeper.Process.Pid, slave)
	if lines := terminalHolders(slave); !slices.ContainsFunc(lines, func(line string) bool { return strings.HasPrefix(line, want) }) {
		t.Errorf("the holders were %q, want a line that starts %q", lines, want)
	}
	_ = sleeper.Process.Kill()
	_ = sleeper.Wait()
	for _, line := range terminalHolders(slave) {
		if strings.HasPrefix(line, fmt.Sprintf("pid %d ", sleeper.Process.Pid)) {
			t.Errorf("a process that ended is still named: %s", line)
		}
	}
}

// A step whose terminal stays open after it ended, because a process it left behind holds the terminal, does not
// wait for it for good: it reads as terminal open, with the holder named, so a run that hangs there says what
// holds the terminal.
func TestAStepWhoseTerminalStaysOpenNamesWhatHoldsIt(t *testing.T) {
	requireProc(t)
	defer func(grace time.Duration) { terminalGrace = grace }(terminalGrace)
	terminalGrace = 500 * time.Millisecond
	pidFile := t.TempDir() + "/holder"
	// The step ends once the holder runs. The holder holds no output of the step, so the step's own end is not held
	// back, and it ignores the hangup that the end of the step's session sends.
	script := `( trap '' HUP; exec 9</dev/tty >/dev/null 2>&1; exec sh -c 'echo $$ > ` + pidFile + `; exec sleep 30' ) & until [ -s ` + pidFile + ` ]; do sleep 0.05; done`
	began := time.Now()
	step := (&run{env: os.Environ()}).onTerminal("terminal", 30*time.Second, []string{"/bin/sh", "-c", script})
	pid := leftBehind(t, pidFile)
	if took := time.Since(began); took > 5*time.Second {
		t.Errorf("a step whose terminal stayed open held the run for %v", took)
	}
	if step.TimedOut || step.Exit != 0 {
		t.Errorf("the step itself ran as a step does: %+v", step)
	}
	if !step.TerminalOpen {
		t.Fatalf("the step's terminal was held open by pid %d, and the step does not say so: %+v", pid, step)
	}
	want := fmt.Sprintf("pid %d holds ", pid)
	if !slices.ContainsFunc(step.Holders, func(line string) bool {
		return strings.HasPrefix(line, want) && strings.Contains(line, "on descriptor 9 (opened as /dev/tty, so perhaps another terminal): sleep 30")
	}) {
		t.Errorf("the holders were %q, want a line that starts %q and names descriptor 9 and `sleep 30`", step.Holders, want)
	}
	_, err := (&run{}).must(step)
	if err == nil || !strings.Contains(err.Error(), "its terminal was still open") || !strings.Contains(err.Error(), want) {
		t.Errorf("the failure is %v, want it to say the terminal was open and name pid %d", err, pid)
	}
}

// A step that ran past its limit names what holds its terminal too.
func TestAStepPastItsLimitOnATerminalNamesWhatHoldsIt(t *testing.T) {
	requireProc(t)
	defer func(grace time.Duration) { terminalGrace = grace }(terminalGrace)
	terminalGrace = 500 * time.Millisecond
	pidFile := t.TempDir() + "/holder"
	// Job control puts the holder in a group of its own, so the kill of the step's group does not reach it, as it does
	// not reach a terminal server. It stays in the step's session, so the terminal is still its controlling terminal.
	script := `set -m; ( trap '' HUP; exec 9</dev/tty; exec sh -c 'echo $$ > ` + pidFile + `; exec sleep 30' ) & wait`
	step := (&run{env: os.Environ()}).onTerminal("terminal", 500*time.Millisecond, []string{"/bin/sh", "-c", script})
	pid := leftBehind(t, pidFile)
	if !step.TimedOut {
		t.Fatalf("the step did not run past its limit: %+v", step)
	}
	if want := fmt.Sprintf("pid %d holds ", pid); !slices.ContainsFunc(step.Holders, func(line string) bool { return strings.HasPrefix(line, want) }) {
		t.Errorf("the holders were %q, want a line that starts %q", step.Holders, want)
	}
}
