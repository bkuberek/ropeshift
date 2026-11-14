//go:build screenshots

package screenshots

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/procs"
)

// install is one isolated installation: a home, state, runtime, and configuration directory of its own
// under one root, a daemon on a loopback port the kernel chose, and the fake agent CLI first on PATH. It
// never reads or writes the person's own installation.
type install struct {
	t *testing.T
	// root holds everything the install writes. Its path is short, because a socket's path is limited.
	root string
	home string
	// cli is the binary under capture, and daemonBin the link to it that starts as the daemon.
	cli, daemonBin string
	// path is the PATH of every process: the fake CLI, then the canary, then the system's directories.
	path    string
	session string
	daemon  *process
	web     string
}

// process is one daemon this install started, with what it said.
type process struct {
	cmd    *exec.Cmd
	stdout *lockedBuffer
	stderr *lockedBuffer
	exited chan error
}

type lockedBuffer struct {
	mu  sync.Mutex
	raw bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.raw.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.raw.String()
}

// maxRoot keeps a socket under the limit with the run directory and a lane's private directory below it.
const maxRoot = 24

// newInstall makes the root and the fake CLI. The root goes when the test ends, unless keep is set.
func newInstall(t *testing.T, binary string, keep bool) *install {
	t.Helper()
	base := os.TempDir()
	if len(base) > maxRoot-len("/rs-shots-0000000000") {
		base = "/tmp"
	}
	root, err := os.MkdirTemp(base, "rs-shots-")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if keep {
			t.Logf("kept the installation at %s", root)
			return
		}
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("removing the installation: %v", err)
		}
	})
	in := &install{t: t, root: root, home: filepath.Join(root, "home"), cli: binary}
	// Registered second, so it runs after every other cleanup and before the files go.
	t.Cleanup(in.sweep)
	for _, dir := range []string{in.home, filepath.Join(root, "tmp"), filepath.Join(root, "bin")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	in.daemonBin = filepath.Join(root, "bin", "ropeshiftd")
	if err := os.Symlink(binary, in.daemonBin); err != nil {
		t.Fatal(err)
	}
	in.path = doubles.InstallFakeClaude(t, "no-session") + string(os.PathListSeparator) + systemPath()
	// The terminal server a worker starts outlives the daemon by design, so it ends after the daemon
	// and before the files.
	doubles.EndTmuxOn(t, in.paths().TerminalSocket())
	if !strings.HasPrefix(in.paths().ControlSocket(), root) {
		t.Fatalf("the control socket %s is outside the installation", in.paths().ControlSocket())
	}
	return in
}

// systemPath is the PATH this process found, after any canary the Makefile put first, which the fake
// CLI's PATH already holds.
func systemPath() string {
	var kept []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if !strings.HasSuffix(dir, filepath.Join("test", "doubles", "canary")) {
			kept = append(kept, dir)
		}
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

func (in *install) env() []string {
	return []string{
		"HOME=" + in.home,
		"PATH=" + in.path,
		"TMPDIR=" + filepath.Join(in.root, "tmp"),
		"XDG_RUNTIME_DIR=" + filepath.Join(in.root, "run"),
		"XDG_STATE_HOME=" + filepath.Join(in.root, "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(in.home, ".config"),
		"LANG=C.UTF-8",
		"TZ=UTC",
		"GIT_CONFIG_NOSYSTEM=1",
		api.EnvWeb + "=127.0.0.1:0",
		api.EnvTelemetry + "=127.0.0.1:0",
	}
}

func (in *install) paths() api.Paths {
	in.t.Helper()
	env := map[string]string{"XDG_RUNTIME_DIR": filepath.Join(in.root, "run"), "XDG_STATE_HOME": filepath.Join(in.root, "state")}
	paths, err := api.ResolvePaths(runtime.GOOS, func(key string) string { return env[key] }, in.home)
	if err != nil {
		in.t.Fatal(err)
	}
	return paths
}

// start runs the daemon with its web UI on a port the kernel chooses, never the default one.
func (in *install) start(args ...string) {
	in.t.Helper()
	args = append([]string{"solo", "--web", "127.0.0.1:0", "--telemetry", "127.0.0.1:0"}, args...)
	cmd := exec.Command(in.daemonBin, args...)
	cmd.Env = in.env()
	cmd.Dir = in.home
	d := &process{cmd: cmd, stdout: &lockedBuffer{}, stderr: &lockedBuffer{}, exited: make(chan error, 1)}
	// What the daemon says also goes to a file in the installation, which a kept one holds for debugging.
	log, err := os.Create(filepath.Join(in.root, "daemon.log"))
	if err != nil {
		in.t.Fatal(err)
	}
	in.t.Cleanup(func() { _ = log.Close() })
	cmd.Stdout, cmd.Stderr = io.MultiWriter(d.stdout, log), io.MultiWriter(d.stderr, log)
	if err := cmd.Start(); err != nil {
		in.t.Fatal(err)
	}
	in.t.Logf("the daemon is process %d", cmd.Process.Pid)
	go func() { d.exited <- cmd.Wait() }()
	in.t.Cleanup(func() { in.stop(d) })
	in.daemon = d
	in.waitReady()
	in.web = in.webAddress()
	if !strings.HasPrefix(in.web, "http://127.0.0.1:") || strings.HasSuffix(in.web, ":7317") {
		in.t.Fatalf("the web UI is at %q", in.web)
	}
}

// stop ends the daemon the way a service manager does, and kills it if it does not end.
func (in *install) stop(d *process) {
	select {
	case err := <-d.exited:
		d.exited <- err
		return
	default:
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-d.exited:
		d.exited <- err
	case <-time.After(30 * time.Second):
		_ = d.cmd.Process.Signal(syscall.SIGKILL)
		err := <-d.exited
		d.exited <- err
	}
	if in.t.Failed() {
		in.t.Logf("the daemon said:\n%s", tail(d.stderr.String(), 80))
	}
}

func tail(text string, lines int) string {
	all := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n")
}

// waitReady waits for the daemon's own answer, and signs the install's member in.
func (in *install) waitReady() {
	in.t.Helper()
	client := api.NewUnixClient(in.paths().ControlSocket())
	defer client.Close()
	in.waitFor("the daemon's answer", func() bool {
		select {
		case err := <-in.daemon.exited:
			in.t.Fatalf("the daemon exited: %v\n%s", err, in.daemon.stderr.String())
		default:
		}
		_, err := client.Health(in.t.Context())
		var refused *api.Error
		return err == nil || errors.As(err, &refused)
	})
	if in.session != "" {
		return
	}
	token, err := apitest.SignIn(in.t.Context(), client)
	if err != nil {
		in.t.Fatal(err)
	}
	in.session = token
}

// webAddress reads the address the daemon says its web UI serves.
func (in *install) webAddress() string {
	in.t.Helper()
	var address string
	in.waitFor("the web address", func() bool {
		for line := range strings.Lines(in.daemon.stdout.String()) {
			if found, ok := strings.CutPrefix(strings.TrimSpace(line), "ropeshiftd: web "); ok {
				address = found
				return true
			}
		}
		return false
	})
	return address
}

// run runs one command of the binary as the member, or with env in place of the member's session.
func (in *install) runAs(env []string, args ...string) (int, string, string) {
	in.t.Helper()
	cmd := exec.Command(in.cli, args...)
	// A command names the sample's project unless it says another.
	cmd.Env = append(append(in.env(), api.EnvProject+"="+sampleProject), env...)
	cmd.Dir = in.home
	if len(env) == 0 {
		cmd.Env = append(cmd.Env, authority.EnvMemberToken+"="+in.session)
	}
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

func (in *install) run(args ...string) (int, string, string) {
	in.t.Helper()
	return in.runAs(nil, args...)
}

// must runs one command and fails the run when it fails.
func (in *install) must(args ...string) string {
	in.t.Helper()
	code, stdout, stderr := in.run(args...)
	if code != 0 {
		in.t.Fatalf("ropeshift %s: exit %d: %s%s%s", strings.Join(args, " "), code, stderr, in.state(), in.daemonTail())
	}
	return stdout
}

// read runs one command with --json and decodes what it printed.
func (in *install) read(out any, args ...string) {
	in.t.Helper()
	raw := in.must(append(args, "--json")...)
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		in.t.Fatalf("ropeshift %s: %v\n%s", strings.Join(args, " "), err, raw)
	}
}

func (in *install) daemonTail() string {
	if in.daemon == nil {
		return ""
	}
	return "\nthe daemon said:\n" + tail(in.daemon.stderr.String(), 40)
}

// The bounds of every wait. A step of the population takes well under a second on an idle machine.
const (
	waitLimit = 90 * time.Second
	waitPoll  = 100 * time.Millisecond
)

func (in *install) waitFor(what string, done func() bool) {
	in.t.Helper()
	if !in.waitUntil(done) {
		in.t.Fatalf("%s did not happen within %s%s%s", what, waitLimit, in.state(), in.daemonTail())
	}
}

// waitUntil is waitFor that reports a wait that ran out, for a caller that says more about it.
func (in *install) waitUntil(done func() bool) bool {
	deadline := time.Now().Add(waitLimit)
	for !done() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(waitPoll)
	}
	return true
}

// state is what the product holds, for a wait that ran out.
func (in *install) state() string {
	if in.session == "" {
		return ""
	}
	var out strings.Builder
	var orders api.WorkOrderList
	if code, raw, _ := in.run("work", "list", "--json"); code == 0 && json.Unmarshal([]byte(raw), &orders) == nil {
		for _, row := range orders.WorkOrders {
			fmt.Fprintf(&out, "\nwork order %s %q %s", row.ID, row.Title, row.State)
		}
	}
	var lanes api.LaneList
	if code, raw, _ := in.run("lane", "list", "--json"); code == 0 && json.Unmarshal([]byte(raw), &lanes) == nil {
		for _, lane := range lanes.Lanes {
			fmt.Fprintf(&out, "\nlane %s on %s: %s, %s", lane.ID, lane.WorkOrder, lane.ActualState, lane.Freshness)
		}
	}
	return out.String()
}

// workers lists the registered workers.
func (in *install) workers() []ledger.Worker {
	in.t.Helper()
	var list api.WorkerList
	in.read(&list, "worker", "list")
	return list.Workers
}

// reap ends what a lane's shims started when the run ends, by the IDs their state names.
func (in *install) reap(lane string) {
	in.t.Helper()
	doubles.ReapShims(in.t, filepath.Join(in.paths().StateDir, "lanes", lane))
}

// sweep ends whatever still runs from the installation once the daemon, the lanes' shims, and the terminal
// server have ended, such as a hook that waits on a person past its session's end. It signals only a
// process whose command line names the installation's root, and checks that again as it signals.
func (in *install) sweep() {
	out, err := exec.Command("ps", "-A", "-o", "pid=,args=").Output()
	if err != nil {
		in.t.Logf("listing processes: %v", err)
		return
	}
	var left []int
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.Contains(line, in.root+string(os.PathSeparator)) {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid == os.Getpid() {
			continue
		}
		if sent, _ := procs.Signal(pid, syscall.SIGKILL, in.root); sent {
			in.t.Logf("ended process %d, left running: %s", pid, strings.TrimSpace(strings.Join(fields[1:], " ")))
			left = append(left, pid)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !slices.ContainsFunc(left, procs.Alive) {
			return
		}
	}
}
