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

// TestInitStopsADaemonWhoseChildHoldsItsOutput holds init's wait for its daemon to a bound, whatever its output
// goes to. Output that goes to a pipe is copied by a goroutine, and a wait on a daemon that left a child
// holding that pipe returns only when the child lets go, so a stop that waits for the end of the daemon waits
// for the child. Init writes the output to a file, which no child can hold up, and this holds the bound apart
// from that.
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

// An init whose daemon never answers keeps the daemon's log and says where it is, so a start that was slow or
// failed can be read afterwards. The log holds both of the daemon's streams.
func TestInitKeepsTheLogOfADaemonThatNeverAnswered(t *testing.T) {
	t.Parallel()
	root := apitest.ShortDir(t)
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run")}
	cmd := initDaemonCommand("/bin/sh")
	cmd.Args = []string{"sh", "-c", "echo said on stdout; echo said on stderr >&2; exit 3"}
	_, err := startInitDaemon(t.Context(), paths, cmd)
	if err == nil {
		t.Fatal("a daemon that exited before it answered was taken for one that runs")
	}
	log := paths.InitDaemonLog()
	for _, want := range []string{"exited before it answered", "said on stdout", "said on stderr", log} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure lacks %q:\n%v", want, err)
		}
	}
	raw, readErr := os.ReadFile(log)
	if readErr != nil || !strings.Contains(string(raw), "said on stdout") || !strings.Contains(string(raw), "said on stderr") {
		t.Errorf("the kept log holds %q (%v)", raw, readErr)
	}
	if info, statErr := os.Stat(log); statErr == nil && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("the log of a daemon is readable by others: %v", info.Mode())
	}
}

// The command line learns where the daemon it starts for init writes its log, because the log is the
// installation's own file in its state directory.
func TestTheCommandLineNamesTheLogOfTheDaemonInitStarts(t *testing.T) {
	state := apitest.ShortDir(t)
	t.Setenv(api.EnvStateDir, state)
	t.Setenv(api.EnvRunDir, apitest.ShortDir(t))
	opts := cliOptions()
	if want := filepath.Join(state, api.InitDaemonLogName); opts.DaemonLog != want {
		t.Errorf("the log is %q, want %q", opts.DaemonLog, want)
	}
}
