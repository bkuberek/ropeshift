// Package exit holds the tests that run a whole path through the real binary. They
// run in the default suite and start no agent session.
package exit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/contract"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

var binaries struct {
	cli, daemon string
	err         error
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rsbin")
	if err == nil {
		binaries.cli = filepath.Join(dir, "ropeshift")
		binaries.daemon = filepath.Join(dir, "ropeshiftd")
		build := exec.Command("go", "build", "-o", binaries.cli, "ropeshift.dev/ropeshift/cmd/ropeshift")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, buildErr := build.CombinedOutput(); buildErr != nil {
			err = fmt.Errorf("build: %v\n%s", buildErr, out)
		} else {
			err = os.Symlink(binaries.cli, binaries.daemon)
		}
	}
	binaries.err = err
	code := doubles.Main(m)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// install is one home directory with no configuration in it.
type install struct {
	t    *testing.T
	home string
	// path is the PATH of every process, with the canary or fakeclaude first.
	path string
}

func newInstall(t *testing.T) *install {
	t.Helper()
	if testing.Short() {
		t.Skip("starts the real daemon")
	}
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	return &install{t: t, home: apitest.ShortDir(t), path: doubles.CanaryPath(t)}
}

func (in *install) env() []string {
	return []string{"HOME=" + in.home, "PATH=" + in.path, "XDG_RUNTIME_DIR=" + filepath.Join(in.home, "xdg-run"), "XDG_STATE_HOME=" + filepath.Join(in.home, "xdg-state")}
}

func (in *install) paths() api.Paths {
	in.t.Helper()
	env := map[string]string{"XDG_RUNTIME_DIR": filepath.Join(in.home, "xdg-run"), "XDG_STATE_HOME": filepath.Join(in.home, "xdg-state")}
	paths, err := api.ResolvePaths(runtime.GOOS, func(key string) string { return env[key] }, in.home)
	if err != nil {
		in.t.Fatal(err)
	}
	return paths
}

// daemon is one running ropeshiftd.
type daemon struct {
	cmd    *exec.Cmd
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	exited chan error
}

func (in *install) start(args ...string) *daemon {
	in.t.Helper()
	cmd := exec.Command(binaries.daemon, append([]string{"solo"}, args...)...)
	cmd.Env = in.env()
	d := &daemon{cmd: cmd, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, exited: make(chan error, 1)}
	cmd.Stdout, cmd.Stderr = d.stdout, d.stderr
	if err := cmd.Start(); err != nil {
		in.t.Fatal(err)
	}
	go func() { d.exited <- cmd.Wait() }()
	in.t.Cleanup(func() { d.kill() })
	return d
}

func (d *daemon) kill() {
	_ = d.cmd.Process.Signal(syscall.SIGKILL)
	select {
	case err := <-d.exited:
		d.exited <- err
	case <-time.After(30 * time.Second):
	}
}

// waitReady waits for the daemon's own answer. A connection proves only that something
// is bound, and the kernel takes one before the socket has its mode, so only a reply
// shows the daemon finished setting the socket up.
func (in *install) waitReady(d *daemon) {
	in.t.Helper()
	socket := in.paths().ControlSocket()
	client := api.NewUnixClient(socket)
	defer client.Close()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-d.exited:
			in.t.Fatalf("the daemon exited: %v\n%s", err, d.stderr.String())
		default:
		}
		_, err := client.Health(in.t.Context())
		var refused *api.Error
		if err == nil || errors.As(err, &refused) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	in.t.Fatalf("no answer at %s\n%s", socket, d.stderr.String())
}

func (in *install) run(args ...string) (int, string, string) {
	in.t.Helper()
	cmd := exec.Command(binaries.cli, args...)
	cmd.Env = in.env()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
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

func (in *install) must(args ...string) string {
	in.t.Helper()
	code, stdout, stderr := in.run(args...)
	if code != 0 {
		in.t.Fatalf("%v: exit %d: %s", args, code, stderr)
	}
	return stdout
}

func (in *install) show(id string) workorders.WorkOrder {
	in.t.Helper()
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(in.must("work", "show", id, "--json")), &row); err != nil {
		in.t.Fatal(err)
	}
	return row
}

func (in *install) waitFor(what string, done func() bool) {
	in.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	in.t.Fatalf("%s did not happen", what)
}

// TestSoloPrivateSocket checks the socket at the path D19 names, and that this
// binary opened no TCP port.
func TestSoloPrivateSocket(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	paths := in.paths()
	dir, err := os.Stat(paths.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := dir.Mode().Perm(); mode != 0o700 {
		t.Errorf("run directory mode %o", mode)
	}
	socket, err := os.Stat(paths.ControlSocket())
	if err != nil {
		t.Fatal(err)
	}
	if mode := socket.Mode().Perm(); mode != 0o600 {
		t.Errorf("socket mode %o", mode)
	}
	if socket.Mode()&os.ModeSocket == 0 {
		t.Errorf("not a socket: %v", socket.Mode())
	}
	state, err := os.Stat(paths.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := state.Mode().Perm(); mode != 0o700 {
		t.Errorf("state directory mode %o", mode)
	}
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		t.Log("no lsof, so only the source rule checks for a TCP listener")
		return
	}
	out, _ := exec.Command(lsof, "-a", "-nP", "-iTCP", "-p", fmt.Sprint(d.cmd.Process.Pid)).Output()
	if len(bytes.TrimSpace(out)) != 0 {
		t.Errorf("a TCP socket is open:\n%s", out)
	}
}

// TestLedgerAloneThroughTheBinary is FR-LEDGER-3 on the real binary, across a
// SIGKILL of the daemon.
func TestLedgerAloneThroughTheBinary(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	in.must("work", "create", "--id", "wo", "--title", "Add the retry budget", "--criterion", "It retries")
	in.must("work", "review", "wo")
	in.must("work", "claim", "wo", "--worker", "worker-a", "--claim-id", "c1")
	if row := in.show("wo"); row.State != workorders.Claimed || row.Fence != 1 {
		t.Fatalf("row %+v", row)
	}
	d.kill()
	if code, _, _ := in.run("work", "list"); code != cli.ExitUnreachable {
		t.Errorf("a dead daemon: exit %d", code)
	}
	again := in.start()
	in.waitReady(again)
	row := in.show("wo")
	if row.State != workorders.Claimed || row.ClaimID != "c1" {
		t.Fatalf("after a restart %+v", row)
	}
	in.must("work", "release", "wo", "--claim-id", "c1", "--fence", "1")
	if row := in.show("wo"); row.State != workorders.Ready || row.Fence != 2 {
		t.Fatalf("after a release %+v", row)
	}
}

func TestOneDaemonPerInstallation(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	second := in.start()
	select {
	case err := <-second.exited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() == 0 {
			t.Errorf("the second daemon exited with %v", err)
		}
		if !strings.Contains(second.stderr.String(), "already running") {
			t.Errorf("stderr %q", second.stderr.String())
		}
		second.exited <- err
	case <-time.After(30 * time.Second):
		t.Fatal("the second daemon kept running")
	}
	in.must("work", "list")
}

func TestLeaseTickLapsesAClaim(t *testing.T) {
	in := newInstall(t)
	d := in.start("--lease-tick", "20ms")
	in.waitReady(d)
	in.must("work", "create", "--id", "wo", "--title", "T", "--criterion", "C")
	in.must("work", "review", "wo")
	in.must("work", "claim", "wo", "--worker", "w", "--claim-id", "c1", "--ttl", "50ms")
	in.waitFor("the lapse", func() bool { return in.show("wo").State == workorders.Lapsed })
	in.must("work", "reassign", "wo", "--reason", "a closed laptop")
	if row := in.show("wo"); row.State != workorders.Ready {
		t.Errorf("row %+v", row)
	}
}

func TestIntakeTickReadsARepository(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "One."))
	d := in.start("--intake-tick", "20ms", "--source", repo.Dir)
	in.waitReady(d)
	var row workorders.WorkOrder
	in.waitFor("intake", func() bool {
		var list api.WorkOrderList
		if err := json.Unmarshal([]byte(in.must("work", "list", "--json")), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.WorkOrders) == 0 {
			return false
		}
		row = list.WorkOrders[0]
		return true
	})
	if row.State != workorders.ContractReview || row.ContractVersion != 1 {
		t.Fatalf("row %+v", row)
	}
	in.must("work", "review", row.ID)
	if got := in.show(row.ID); got.State != workorders.Ready {
		t.Fatalf("row %+v", got)
	}
	var sources api.SourceList
	if err := json.Unmarshal([]byte(in.must("source", "list", "--json")), &sources); err != nil {
		t.Fatal(err)
	}
	if len(sources.Sources) != 1 || sources.Sources[0].Health != "ok" {
		t.Errorf("sources %+v", sources)
	}
}

func (in *install) workers() []ledger.Worker {
	in.t.Helper()
	var list api.WorkerList
	if err := json.Unmarshal([]byte(in.must("worker", "list", "--json")), &list); err != nil {
		in.t.Fatal(err)
	}
	return list.Workers
}

// TestWorkerRegistersOnceAndRenews is the worker plane on the real binary: one
// registration across restarts, a new epoch each start, and heartbeats that hold a
// lease far past its first deadline.
func TestWorkerRegistersOnceAndRenews(t *testing.T) {
	in := newInstall(t)
	d := in.start("--heartbeat", "30ms", "--lease-tick", "20ms")
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	first := in.workers()[0]
	in.must("work", "create", "--id", "wo", "--title", "T", "--criterion", "C")
	in.must("work", "review", "wo")
	in.must("work", "claim", "wo", "--worker", first.ID, "--claim-id", "c1", "--ttl", "300ms")
	time.Sleep(time.Second)
	if row := in.show("wo"); row.State != workorders.Claimed || row.Fence != 1 {
		t.Fatalf("heartbeats did not hold the lease: %+v", row)
	}
	d.kill()
	again := in.start("--heartbeat", "30ms", "--lease-tick", "20ms")
	in.waitReady(again)
	in.waitFor("a new epoch", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch > first.Epoch
	})
	if got := in.workers()[0]; got.ID != first.ID {
		t.Errorf("a restart registered again: %s after %s", got.ID, first.ID)
	}
	in.waitFor("the return", func() bool { return in.show("wo").State == workorders.Claimed })
}

func TestJoinTokenFromTheCLI(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	var token api.JoinTokenResponse
	if err := json.Unmarshal([]byte(in.must("worker", "token", "--json")), &token); err != nil {
		t.Fatal(err)
	}
	if token.Token == "" {
		t.Error("no token")
	}
}

func TestUsageOfTheDaemon(t *testing.T) {
	in := newInstall(t)
	d := in.start("--nonsense")
	select {
	case err := <-d.exited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != cli.ExitUsage {
			t.Errorf("exit %v", err)
		}
		d.exited <- err
	case <-time.After(30 * time.Second):
		t.Fatal("a bad flag kept running")
	}
}
