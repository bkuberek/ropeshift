package doubles

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api/shimlink"
	"ropeshift.com/ropeshift/internal/ports"
)

// A record is named for a process, and process IDs come round: a session that started later can hold
// the smaller number, and a name sorts as text in any case. Sessions read in the order they started.
func TestSessionsReadInTheOrderTheyStarted(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, SeenDir), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-time.Hour)
	for i, record := range []struct{ name, body string }{{"99990.json", `{"args":["first"]}`}, {"105.json", `{"args":["second"]}`}} {
		path := filepath.Join(dir, SeenDir, record.name)
		if err := os.WriteFile(path, []byte(record.body), 0o600); err != nil {
			t.Fatal(err)
		}
		at := start.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	seen := SeenBy(t, dir)
	if len(seen) != 2 || seen[0].Args[0] != "first" || seen[1].Args[0] != "second" {
		t.Errorf("seen %+v", seen)
	}
}

// A machine with no Go runs what a bundle holds: each binary by its name, and the canary from its own
// directory there. A name the bundle lacks fails, and nothing is built.
func TestAPrebuiltBundleNeedsNoGo(t *testing.T) {
	bundle := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundle, PrebuiltFakeClaude), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPrebuilt, bundle)
	t.Setenv("PATH", t.TempDir())
	out := t.TempDir()

	path, err := Binary(out, PrebuiltFakeClaude, "ropeshift.com/ropeshift/test/doubles/fakeclaude")
	if err != nil || path != filepath.Join(bundle, PrebuiltFakeClaude) {
		t.Errorf("the double is %q: %v", path, err)
	}
	if path, err := Binary(out, PrebuiltChaos, "ropeshift.com/ropeshift/cmd/ropeshift", "chaos"); err == nil {
		t.Errorf("a binary the bundle lacks is %q", path)
	}
	if entries, err := os.ReadDir(out); err != nil || len(entries) != 0 {
		t.Errorf("something was built: %v, %v", entries, err)
	}
	if dir, _, _ := strings.Cut(CanaryPath(t), string(os.PathListSeparator)); dir != filepath.Join(bundle, PrebuiltCanary) {
		t.Errorf("the canary is in %q", dir)
	}
}

// A pane that is still writing into the test's directory when the test ends must not make the
// directory's removal fail: the private tmux server and its panes end before the files go.
func TestPrivateTmuxEndsBeforeTheTestsFilesAreRemoved(t *testing.T) {
	RequireTmux(t)
	terminal := PrivateTmux(t)
	dir := t.TempDir()
	loop := `i=0; while :; do i=$((i+1)); : > f$i; done`
	spec := ports.PaneSpec{Name: "writer", Dir: dir, Width: 130, Height: 45, Command: []string{"/bin/sh", "-c", loop}}
	if _, err := terminal.Open(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "f1")); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pane wrote nothing")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// holder is a harmless process a test records as if a shim had started it, and a channel that closes
// when it has ended and been collected.
type holder struct {
	pid  int
	done chan struct{}
}

// hold starts a process that does nothing for a minute. A name and arguments the test chooses stand for
// what the process is, and the test ends it by its own handle.
func hold(t *testing.T, name string, args ...string) holder {
	t.Helper()
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := holder{pid: cmd.Process.Pid, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(h.done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-h.done
	})
	return h
}

func (h holder) ended(within time.Duration) bool {
	select {
	case <-h.done:
		return true
	case <-time.After(within):
		return false
	}
}

// record writes the state a shim leaves for a generation, with the IDs of the two processes a test holds.
func record(t *testing.T, configDir string, shim, child holder) {
	t.Helper()
	dir := filepath.Join(configDir, "shim-1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := shimlink.State{Version: shimlink.Version, ShimPID: shim.pid, ChildPID: child.pid}
	if err := shimlink.WriteState(dir, state); err != nil {
		t.Fatal(err)
	}
}

// An ID outlives its process: a recorded ID may name some other process by the time a test ends, and that
// process is not the test's to stop.
func TestReapShimsLeavesAProcessThatIsNotTheRecordedOne(t *testing.T) {
	configDir := t.TempDir()
	shim, child := hold(t, "sleep", "61"), hold(t, "sleep", "62")
	record(t, configDir, shim, child)

	t.Run("the test ends", func(t *testing.T) { ReapShims(t, configDir) })

	if shim.ended(500 * time.Millisecond) {
		t.Error("a process that is not the recorded shim was killed")
	}
	if child.ended(0) {
		t.Error("a process that is not the recorded session was killed")
	}
}

// The recorded processes are the ones whose command lines name the generation: the shim by its name and
// the directory it was given, and the session it runs by the files it was handed from that directory.
func TestReapShimsStopsTheProcessesItRecorded(t *testing.T) {
	configDir := t.TempDir()
	shim := hold(t, "sh", "-c", "sleep 63; :", "lane-shim", "--dir", filepath.Join(configDir, "shim-1"))
	child := hold(t, "sh", "-c", "sleep 64; :", "claude", "--settings", filepath.Join(configDir, "settings-1.json"))
	record(t, configDir, shim, child)

	t.Run("the test ends", func(t *testing.T) { ReapShims(t, configDir) })

	if !shim.ended(5 * time.Second) {
		t.Error("the recorded shim still runs")
	}
	if !child.ended(5 * time.Second) {
		t.Error("the recorded session still runs")
	}
}

// A shim's name alone is not enough: a shim of some other generation, or another run, holds a different
// directory, and stays.
func TestReapShimsLeavesAShimOfAnotherDirectory(t *testing.T) {
	configDir := t.TempDir()
	shim := hold(t, "sh", "-c", "sleep 65; :", "lane-shim", "--dir", filepath.Join(t.TempDir(), "shim-1"))
	child := hold(t, "sleep", "66")
	record(t, configDir, shim, child)

	t.Run("the test ends", func(t *testing.T) { ReapShims(t, configDir) })

	if shim.ended(500 * time.Millisecond) {
		t.Error("a shim of another directory was killed")
	}
}

// A process that is gone is nothing to stop and nothing to report.
func TestReapShimsIgnoresAProcessThatEnded(t *testing.T) {
	configDir := t.TempDir()
	gone := hold(t, "sleep", "67")
	_ = syscall.Kill(gone.pid, syscall.SIGKILL)
	gone.ended(5 * time.Second)
	record(t, configDir, gone, gone)

	t.Run("the test ends", func(t *testing.T) { ReapShims(t, configDir) })
}

// A pane's process ID is recorded while its server runs, and the server's end can leave that ID to
// another process. What the ID held when it was recorded is what may be stopped by it.
func TestEndingATmuxServerStopsOnlyWhatItRecorded(t *testing.T) {
	same, other := hold(t, "sleep", "81"), hold(t, "sleep", "82")
	recorded := recordProcesses([]int{same.pid, other.pid})
	for i := range recorded {
		if recorded[i].pid == other.pid {
			recorded[i].command = "sleep 99"
		}
	}

	var skipped []string
	killRecorded(recorded, func(format string, args ...any) { skipped = append(skipped, format) })

	if !same.ended(5 * time.Second) {
		t.Error("a process that still runs what was recorded was left")
	}
	if other.ended(500 * time.Millisecond) {
		t.Error("a process that runs something else was killed")
	}
	if len(skipped) != 1 {
		t.Errorf("logged %v", skipped)
	}
}

// A process that ended before it was recorded is not recorded: its ID names nothing.
func TestRecordingLeavesOutAProcessThatEnded(t *testing.T) {
	gone := hold(t, "sleep", "83")
	_ = syscall.Kill(gone.pid, syscall.SIGKILL)
	gone.ended(5 * time.Second)

	if recorded := recordProcesses([]int{gone.pid}); len(recorded) != 0 {
		t.Errorf("recorded %+v", recorded)
	}
}
