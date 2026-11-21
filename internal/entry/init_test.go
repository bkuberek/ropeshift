package entry

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
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

// The daemon ropeshift init starts runs for a few seconds, and its first start registers the worker. A
// probe of the agent CLI starts real sessions that spend turns, and this daemon's stop always cancels it
// within a second, so the daemon is told not to start one.
func TestTheDaemonInitStartsIsToldNotToProbe(t *testing.T) {
	t.Parallel()
	args := initDaemonCommand("/ropeshiftd").Args
	opts, ok := parseSolo(args[2:], io.Discard)
	if !ok {
		t.Fatalf("the daemon init starts refuses its own flags: %v", args)
	}
	if !opts.noProbe {
		t.Errorf("the daemon init starts probes the agent CLI: %v", args)
	}
	if opts.web != api.WebOff || opts.telemetry != api.WebOff {
		t.Errorf("the daemon init starts listens on web %q and telemetry %q", opts.web, opts.telemetry)
	}
}

// A daemon probes the agent CLI unless it is told not to, and the lane runtime it builds carries what it was told.
func TestADaemonProbesUnlessToldNotTo(t *testing.T) {
	t.Parallel()
	if opts, ok := parseSolo(nil, io.Discard); !ok || opts.noProbe {
		t.Errorf("a daemon given no flags reads noProbe %v (%v)", opts.noProbe, ok)
	}
	opts, ok := parseSolo([]string{"--no-probe"}, io.Discard)
	if !ok || !opts.noProbe {
		t.Fatalf("--no-probe reads noProbe %v (%v)", opts.noProbe, ok)
	}
	root := apitest.ShortDir(t)
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "worktrees")}
	for _, told := range []bool{false, true} {
		runtime, _, err := soloLanes(paths, soloOptions{noProbe: told}, soloCheckouts(paths), io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.NoProbe != told {
			t.Errorf("a daemon told noProbe=%v builds a lane runtime with NoProbe=%v", told, runtime.NoProbe)
		}
	}
}

// A daemon that init stops ends within the limit init gives it, or init kills it. The daemon's stop waits for its
// server to drain and then for what its worker started, so the two waits fit in the limit together, with room
// for the rest of what a stop does.
func TestADaemonInitStopsEndsWithinTheLimitInitGivesIt(t *testing.T) {
	t.Parallel()
	if got := soloShutdownGrace + workerStopGrace; got >= initDaemonStop {
		t.Errorf("a stopping daemon waits %v for its server and its worker, and init kills it after %v", got, initDaemonStop)
	}
}
