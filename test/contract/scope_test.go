package contract

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/systemd"
	"ropeshift.dev/ropeshift/internal/ports"
)

// TestAnOutlivingProcessLeavesTheControlGroup is D20 on a real user manager: a process that must
// outlive the worker's service runs in a transient scope, which a stop of the worker's unit does not
// reach, in a session of its own, with exactly its own environment. It runs only where a systemd
// user manager answers, which `make service-test` needs on Linux.
func TestAnOutlivingProcessLeavesTheControlGroup(t *testing.T) {
	scope, ok := systemd.UserScope(osexec.Runner{}, os.Getenv)
	if !ok {
		t.Skip("no systemd user manager answers here")
	}
	spec := ports.ProcessSpec{Path: "/bin/sleep", Args: []string{"30"}, Env: []string{"PATH=/usr/bin:/bin", "ROPESHIFT_LANE_ID=lane-1"}, Outlive: true}
	process, err := scope.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = process.Signal(ports.SignalKill)
		_, _ = process.Wait()
	}()
	proc := "/proc/" + strconv.Itoa(process.PID())
	// systemd-run execs the process only once its scope exists, and env in turn.
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(proc + "/cmdline")
		if err == nil && string(raw) == "/bin/sleep\x0030\x00" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the process never ran: %q, %v", raw, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	group, err := os.ReadFile(proc + "/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	own, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	if string(group) == string(own) || !strings.HasSuffix(strings.TrimSpace(string(group)), ".scope") {
		t.Errorf("control group %q, and this process's is %q", group, own)
	}
	environ, err := os.ReadFile(proc + "/environ")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(environ), "\x00"), "\x00"); !slices.Equal(got, spec.Env) {
		t.Errorf("environment %q", got)
	}
	if session, err := syscall.Getpgid(process.PID()); err != nil || session != process.PID() {
		t.Errorf("process group %d of process %d: %v", session, process.PID(), err)
	}
}
