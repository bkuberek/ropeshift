package entry

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// heldOutputShell is a daemon that ends on SIGTERM and leaves a child behind that holds its output open,
// as a process the daemon started and did not wait for does. It writes the child's process ID to file.
func heldOutputShell(file string) string {
	return "sleep 30 & echo $! > " + file + "; wait"
}

// killLeftover ends the child heldOutputShell leaves, when the test is over.
func killLeftover(t *testing.T, file string) {
	t.Helper()
	t.Cleanup(func() {
		raw, err := os.ReadFile(file)
		if err != nil {
			return
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
}

// TestInitStopsADaemonWhoseChildHoldsItsOutput holds init's wait for its daemon to a bound. The daemon's
// output is read through a pipe, and a wait on a daemon that left a child holding that pipe returns only
// when the child lets go, so a stop that waits for the end of the daemon waits for the child.
func TestInitStopsADaemonWhoseChildHoldsItsOutput(t *testing.T) {
	t.Parallel()
	pidFile := filepath.Join(t.TempDir(), "child")
	killLeftover(t, pidFile)
	cmd := initDaemonCommand("/bin/sh")
	cmd.Args = []string{"sh", "-c", heldOutputShell(pidFile)}
	cmd.Stdout, cmd.Stderr = &bytes.Buffer{}, &bytes.Buffer{}
	daemon, err := launchInitDaemon(cmd, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// The child exists before the daemon is stopped, or the stop would end a daemon that held nothing.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if raw, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(raw), "\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon never started its child")
		}
	}
	stopped := make(chan error, 1)
	go func() { stopped <- daemon.stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Errorf("the stop of a daemon that ended on SIGTERM failed: %v", err)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("init's stop never returned for a daemon whose child held its output")
	}
}
