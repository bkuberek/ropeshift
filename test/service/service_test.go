//go:build service

// Package service holds the tests that run the daemon under this machine's real service manager:
// launchd on macOS and a systemd user manager on Linux. Each installs a service under a test name of
// its own, in a temporary installation, and removes it at its end, so they run only through
// `make service-test` and never in the default suite.
package service

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/shimlink"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

var binaries struct {
	cli, daemon string
	err         error
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rsbin")
	if err == nil {
		binaries.daemon = filepath.Join(dir, "ropeshiftd")
		binaries.cli, err = doubles.Binary(dir, doubles.PrebuiltRopeshift, "ropeshift.com/ropeshift/cmd/ropeshift")
		if err == nil {
			err = os.Symlink(binaries.cli, binaries.daemon)
		}
	}
	binaries.err = err
	code := doubles.Main(m)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func exists(pid int) bool { return pid > 0 && !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// manager is this machine's service manager, as a person would drive it.
type manager interface {
	// stop stops the service the way the manager does, which on launchd is to unload it.
	stop(t *testing.T)
	// start starts the installed service again.
	start(t *testing.T)
	// pid is the process the manager runs for the service, or 0.
	pid(t *testing.T) int
	// loaded says the manager still holds the service.
	loaded(t *testing.T) bool
	// remove takes away whatever a failed test left, by the test's own name only.
	remove(t *testing.T)
}

// launchd drives one agent in the user's GUI domain.
type launchd struct {
	label, plist string
}

func (l launchd) target() string { return "gui/" + strconv.Itoa(os.Getuid()) + "/" + l.label }

// stop unloads the agent, and waits for launchd to let its label go, which comes a moment after
// bootout returns.
func (l launchd) stop(t *testing.T) {
	t.Helper()
	mustRun(t, "/bin/launchctl", "bootout", l.target())
	for deadline := time.Now().Add(30 * time.Second); l.loaded(t); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("launchd still holds %s", l.target())
		}
	}
}

func (l launchd) start(t *testing.T) {
	mustRun(t, "/bin/launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), l.plist)
}

func (l launchd) pid(t *testing.T) int {
	out, err := exec.Command("/bin/launchctl", "print", l.target()).Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "pid = "); ok {
			pid, _ := strconv.Atoi(value)
			return pid
		}
	}
	return 0
}

func (l launchd) loaded(t *testing.T) bool {
	return exec.Command("/bin/launchctl", "print", l.target()).Run() == nil
}

func (l launchd) remove(t *testing.T) {
	if l.loaded(t) {
		_ = exec.Command("/bin/launchctl", "bootout", l.target()).Run()
	}
	_ = os.Remove(l.plist)
}

// systemd drives one user unit.
type systemd struct {
	unit, file string
	env        []string
}

func (s systemd) systemctl(args ...string) *exec.Cmd {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Env = s.env
	return cmd
}

func (s systemd) stop(t *testing.T)  { mustDo(t, s.systemctl("stop", s.unit)) }
func (s systemd) start(t *testing.T) { mustDo(t, s.systemctl("start", s.unit)) }

func (s systemd) pid(t *testing.T) int {
	out, err := s.systemctl("show", "--property", "MainPID", "--value", s.unit).Output()
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return pid
}

func (s systemd) loaded(t *testing.T) bool {
	out, _ := s.systemctl("show", "--property", "LoadState", "--value", s.unit).Output()
	return strings.TrimSpace(string(out)) == "loaded"
}

func (s systemd) remove(t *testing.T) {
	_ = s.systemctl("disable", "--now", s.unit).Run()
	_ = os.Remove(s.file)
	_ = s.systemctl("daemon-reload").Run()
}

func mustRun(t *testing.T, path string, args ...string) {
	t.Helper()
	mustDo(t, exec.Command(path, args...))
}

func mustDo(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %v: %s", cmd.Args, err, out)
	}
}

// installation is one service under a test name, with its state, sockets, and agent in temporary
// directories.
type installation struct {
	t       *testing.T
	name    string
	home    string
	path    string
	env     []string
	session string
	manager manager
}

// EnvServicePrefix replaces the start of every service name a test installs, so a runner that gives a
// run a name of its own can find and remove what a killed run left.
const EnvServicePrefix = "ROPESHIFT_TEST_SERVICE_PREFIX"

// randomName names one service, after the run's prefix when it has one.
func randomName(t *testing.T, prefix string) string {
	t.Helper()
	if own := os.Getenv(EnvServicePrefix); own != "" {
		prefix = own
	}
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return prefix + hex.EncodeToString(raw)
}

// newInstallation names a service no other can hold. On macOS the agent's file goes in the
// installation's own home, which launchctl loads from any path. A user manager reads units only
// from its own directories, so on Linux the unit goes in the user's, under the test name.
func newInstallation(t *testing.T, fixture string) *installation {
	t.Helper()
	if binaries.err != nil {
		t.Fatal(binaries.err)
	}
	in := &installation{t: t, home: apitest.ShortDir(t), path: doubles.InstallFakeClaude(t, fixture)}
	in.env = []string{
		"PATH=" + in.path, api.EnvStateDir + "=" + filepath.Join(in.home, "state"), api.EnvRunDir + "=" + filepath.Join(in.home, "run"),
		api.EnvWeb + "=" + api.WebOff, api.EnvTelemetry + "=" + api.WebOff,
	}
	switch runtime.GOOS {
	case "darwin":
		in.name = randomName(t, "test.ropeshift.")
		in.env = append(in.env, "HOME="+in.home)
		in.manager = launchd{label: in.name, plist: filepath.Join(in.home, "Library", "LaunchAgents", in.name+".plist")}
	case "linux":
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if _, err := os.Stat(filepath.Join(runtimeDir, "systemd", "private")); runtimeDir == "" || err != nil {
			t.Skip("no systemd user manager answers here")
		}
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		in.name = randomName(t, "ropeshift-test-")
		in.env = append(in.env, "HOME="+home, "XDG_RUNTIME_DIR="+runtimeDir)
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(home, ".config")
		}
		in.manager = systemd{unit: in.name + ".service", file: filepath.Join(config, "systemd", "user", in.name+".service"), env: in.env}
	default:
		t.Skip("no service manager on " + runtime.GOOS)
	}
	t.Logf("service %s", in.name)
	// The service goes before the files it runs from, and the lanes it left before both.
	doubles.EndTmuxOn(t, in.paths().TerminalSocket())
	t.Cleanup(func() {
		in.manager.remove(t)
		if in.manager.loaded(t) {
			t.Errorf("%s is still loaded", in.name)
		}
	})
	return in
}

func (in *installation) paths() api.Paths {
	return api.Paths{StateDir: filepath.Join(in.home, "state"), RunDir: filepath.Join(in.home, "run")}
}

func (in *installation) run(args ...string) (int, string, string) {
	in.t.Helper()
	cmd := exec.Command(binaries.cli, args...)
	cmd.Env = in.env
	if in.session != "" {
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

func (in *installation) must(args ...string) string {
	in.t.Helper()
	code, stdout, stderr := in.run(args...)
	if code != 0 {
		in.t.Fatalf("%v: exit %d: %s%s", args, code, stderr, in.log())
	}
	return stdout
}

// log is what the daemon wrote, where a manager keeps it, for a failure's message.
func (in *installation) log() string {
	if runtime.GOOS == "darwin" {
		raw, _ := os.ReadFile(filepath.Join(in.paths().StateDir, in.name+".log"))
		return "\nthe daemon's log:\n" + string(raw)
	}
	cmd := exec.Command("journalctl", "--user", "--unit", in.name+".service", "--no-pager", "--lines", "40")
	cmd.Env = in.env
	out, _ := cmd.CombinedOutput()
	return "\nthe daemon's journal:\n" + string(out)
}

func (in *installation) waitFor(what string, done func() bool) {
	in.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	in.t.Fatalf("%s did not happen%s", what, in.log())
}

// waitReady waits for the daemon's own answer, and holds one member's session across restarts.
func (in *installation) waitReady() {
	in.t.Helper()
	client := api.NewUnixClient(in.paths().ControlSocket())
	defer client.Close()
	in.waitFor("the daemon's answer", func() bool {
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

// daemonPID is the process that holds the installation's lock, which is the daemon of record.
func (in *installation) daemonPID() int {
	in.t.Helper()
	file, err := os.Open(filepath.Join(in.paths().StateDir, "ropeshiftd.lock"))
	if err != nil {
		in.t.Fatal(err)
	}
	defer file.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: io.SeekStart}
	if err := syscall.FcntlFlock(file.Fd(), syscall.F_GETLK, &lock); err != nil {
		in.t.Fatal(err)
	}
	if lock.Type == syscall.F_UNLCK {
		return 0
	}
	return int(lock.Pid)
}

func (in *installation) onlyWorkOrder() string {
	in.t.Helper()
	var id string
	in.waitFor("intake", func() bool {
		var list api.WorkOrderList
		if err := json.Unmarshal([]byte(in.must("work", "list", "--json")), &list); err != nil {
			in.t.Fatal(err)
		}
		if len(list.WorkOrders) != 1 {
			return false
		}
		id = list.WorkOrders[0].ID
		return true
	})
	return id
}

func (in *installation) state(id string) workorders.State {
	in.t.Helper()
	var row workorders.WorkOrder
	if err := json.Unmarshal([]byte(in.must("work", "show", id, "--json")), &row); err != nil {
		in.t.Fatal(err)
	}
	return row.State
}

// bound says the ledger holds the generation's binding to its session, which a later daemon adopts by.
func (in *installation) bound(id string) func() bool {
	return func() bool {
		store, err := sqlite.Open(filepath.Join(in.paths().StateDir, api.LedgerFileName), sqlstore.Options{})
		if err != nil {
			in.t.Fatal(err)
		}
		defer store.Close()
		found := false
		err = store.Read(in.t.Context(), "default", func(tx ports.ReadTx) error {
			events, err := control.ReadAllEvents(tx.Events)
			for _, e := range events {
				found = found || (e.Subject.ID == id && e.Type == workorders.EventGenerationBound)
			}
			return err
		})
		if err != nil {
			in.t.Fatal(err)
		}
		return found
	}
}

// startLane installs the service with the daemon's flags, waits for it, and runs a lane to its first
// turn.
func (in *installation) startLane(repo string) (daemon int) {
	in.t.Helper()
	installed := in.must("service", "install", "--name", in.name, "--json", "--",
		"--intake-tick", "20ms", "--heartbeat", "30ms", "--lease-tick", "20ms", "--source", repo)
	var service struct{ Name, Path string }
	if err := json.Unmarshal([]byte(installed), &service); err != nil || service.Name != in.name {
		in.t.Fatalf("installed %q: %v", installed, err)
	}
	in.waitReady()
	daemon = in.daemonPID()
	if daemon == 0 || daemon != in.manager.pid(in.t) {
		in.t.Fatalf("the daemon of record is %d, and the manager runs %d", daemon, in.manager.pid(in.t))
	}
	var workers api.WorkerList
	in.waitFor("the worker's session", func() bool {
		if err := json.Unmarshal([]byte(in.must("worker", "list", "--json")), &workers); err != nil {
			in.t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	id := in.onlyWorkOrder()
	in.must("work", "review", id)
	in.must("work", "claim", id, "--worker", workers.Workers[0].ID, "--claim-id", "c1")
	in.must("lane", "start", id, "--lane", "lane-1")
	in.waitFor("the generation's binding", in.bound(id))
	return daemon
}

// stopService stops the service through its manager and waits for the daemon to be gone, because a
// lane that outlived a daemon still running proves nothing.
func (in *installation) stopService(daemon int) {
	in.t.Helper()
	in.manager.stop(in.t)
	in.waitFor("the daemon's end", func() bool { return !exists(daemon) })
}

// restart starts the service again, and the new daemon adopts the lane and takes its outcome.
func (in *installation) restart(daemon int) {
	in.t.Helper()
	in.manager.start(in.t)
	in.waitReady()
	if next := in.daemonPID(); next == 0 || next == daemon {
		in.t.Fatalf("the daemon after the restart is %d, and before it %d", next, daemon)
	}
}

func (in *installation) uninstall() {
	in.t.Helper()
	in.must("service", "uninstall", "--name", in.name)
	if in.manager.loaded(in.t) {
		in.t.Errorf("%s is loaded after its uninstall", in.name)
	}
}

// release lets the double's turn end.
func (in *installation) release() {
	in.t.Helper()
	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "release"), nil, 0o600); err != nil {
		in.t.Fatal(err)
	}
}

// sessions counts the agent sessions the double started, which must be one for one lane.
func (in *installation) sessions() int {
	starts := 0
	for _, seen := range doubles.SeenBy(in.t, doubles.BinDir(in.path)) {
		if len(seen.Args) == 0 || seen.Args[0] != "agents" {
			starts++
		}
	}
	return starts
}

// outside holds D20 for one process the daemon started: it is in neither the daemon's process group
// nor, on Linux, its control group, so the manager's stop reaches it by neither.
func outside(t *testing.T, what string, pid, daemon int) {
	t.Helper()
	group, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("%s %d: %v", what, pid, err)
	}
	daemonGroup, err := syscall.Getpgid(daemon)
	if err != nil {
		t.Fatal(err)
	}
	if group == daemonGroup {
		t.Errorf("%s %d is in the daemon's process group %d", what, pid, group)
	}
	if runtime.GOOS != "linux" {
		return
	}
	own, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := os.ReadFile("/proc/" + strconv.Itoa(daemon) + "/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	if string(own) == string(theirs) || !strings.HasSuffix(strings.TrimSpace(string(own)), ".scope") {
		t.Errorf("%s %d is in control group %q, and the daemon in %q", what, pid, own, theirs)
	}
}

// TestServiceStopKeepsLanes is FR-LANE-17 under a real service manager: launchctl on macOS and
// systemctl --user on Linux stop the daemon's service mid-turn, the session lives on with no daemon,
// and the service's next start adopts it and takes the lane's outcome, in both run modes (D20).
func TestServiceStopKeepsLanes(t *testing.T) {
	t.Run("headless", func(t *testing.T) {
		in := newInstallation(t, "lane-survives")
		repo := gittest.New(t)
		repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
		shimDir := filepath.Join(in.paths().StateDir, "lanes", "lane-1", "shim-1")
		doubles.ReapShims(t, filepath.Dir(shimDir))
		daemon := in.startLane(repo.Dir)
		state, err := shimlink.ReadState(shimDir)
		if err != nil || state.ChildPID == 0 {
			t.Fatalf("the shim's state %+v: %v", state, err)
		}
		outside(t, "the shim", state.ShimPID, daemon)
		outside(t, "the session", state.ChildPID, daemon)

		in.stopService(daemon)
		if !exists(state.ShimPID) || !exists(state.ChildPID) {
			t.Fatal("the session ended with its service")
		}
		in.release()
		in.waitFor("the turn's end in the journal, with no daemon", func() bool {
			records, err := shimlink.ReadJournal(shimDir, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, record := range records {
				if strings.Contains(record.Line, `"type":"result"`) {
					return true
				}
			}
			return false
		})

		in.restart(daemon)
		id := in.onlyWorkOrder()
		in.waitFor("the lane's outcome", func() bool { return in.state(id) == workorders.Acceptance })
		in.waitFor("the shim's end", func() bool { return !exists(state.ShimPID) && !exists(state.ChildPID) })
		in.uninstall()
		if starts := in.sessions(); starts != 1 {
			t.Errorf("%d sessions started for one lane", starts)
		}
	})
	t.Run("pane", func(t *testing.T) {
		doubles.RequireTmux(t)
		in := newInstallation(t, "pane-survives")
		repo := gittest.New(t)
		repo.Commit(".ropeshift/profile.toml", "run_mode = \"tmux\"\n")
		repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
		terminal := doubles.TmuxOn(t, in.paths().TerminalSocket())
		daemon := in.startLane(repo.Dir)
		pane, err := terminal.Pane(t.Context(), "lane-1-g1")
		if err != nil {
			t.Fatal(err)
		}
		server := tmuxServer(t, in.paths().TerminalSocket())
		outside(t, "the terminal server", server, daemon)
		outside(t, "the session", pane.PID, daemon)

		in.stopService(daemon)
		after, err := terminal.Pane(t.Context(), "lane-1-g1")
		if err != nil {
			t.Fatalf("the pane went with its service: %v", err)
		}
		if after.Dead || after.PID != pane.PID || !exists(pane.PID) || !exists(server) {
			t.Fatalf("the session did not survive: %+v, server alive %v", after, exists(server))
		}
		in.release()
		in.waitFor("the turn's end in the spool, with no daemon", func() bool {
			raw, err := os.ReadFile(in.paths().Spool())
			return err == nil && strings.Contains(string(raw), `"event":"Stop"`)
		})

		in.restart(daemon)
		id := in.onlyWorkOrder()
		in.waitFor("the lane's outcome", func() bool { return in.state(id) == workorders.Acceptance })
		in.waitFor("the pane's end", func() bool { return !exists(pane.PID) })
		in.uninstall()
		if starts := in.sessions(); starts != 1 {
			t.Errorf("%d sessions started for one lane", starts)
		}
	})
}

// tmuxServer reads the process of the private server on a socket, and never the default server.
func tmuxServer(t *testing.T, socket string) int {
	t.Helper()
	cmd := exec.Command("tmux", "-S", socket, "list-sessions", "-F", "#{pid}")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(socket)}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(first))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
