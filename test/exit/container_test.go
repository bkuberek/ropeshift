//go:build container

package exit

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/test/doubles"
)

// The container tests run the Linux build of ropeshift in a container with no network, as a new
// account on a machine with git and tmux, so they run wherever Docker runs Linux containers, from a
// macOS host too. `make exit-container` runs them; with no Docker they skip and say so. They never run
// the macOS build: macOS has no network namespace and traces no system calls with System Integrity
// Protection on, so the macOS build is held to loopback only by TestSoloPrivateSocket's check of its
// listeners.

// containerBase is the distribution under test. It gets git and tmux, which solo mode uses on Linux,
// and strace, which only TestNoOutboundConnections runs.
const (
	containerBase     = "debian:bookworm-slim"
	containerPackages = "git tmux strace"
)

// containerFixture is the session the double plays: one turn that reports the work completed.
const containerFixture = "lane-completed"

// traceCalls is what TestNoOutboundConnections asks strace to record: the calls that start a process,
// a thread, or a program, which strace names for each architecture, so a call can be traced to its
// program, and each call that carries an address.
const traceCalls = "%process,connect,bind,sendto,sendmsg,sendmmsg"

// firstRunResult is what test/exit/firstrun writes, read with the fields these tests judge.
type firstRunResult struct {
	Interfaces []string `json:"interfaces"`
	Devices    []string `json:"devices"`
	Docker     bool     `json:"docker"`
	PriorState bool     `json:"prior_state"`
	Steps      []struct {
		Name     string   `json:"name"`
		Args     []string `json:"args"`
		Exit     int      `json:"exit"`
		Stdout   string   `json:"stdout"`
		Stderr   string   `json:"stderr"`
		Terminal string   `json:"terminal"`
		Millis   int64    `json:"ms"`
	} `json:"steps"`
	WorkOrder   string   `json:"work_order"`
	States      []string `json:"states"`
	Final       string   `json:"final"`
	WebStatus   int      `json:"web_status"`
	OffLoopback []string `json:"off_loopback"`
	Left        []string `json:"left"`
	CanaryRan   bool     `json:"canary_ran"`
	Failure     string   `json:"failure"`
}

// TestNoOutboundConnections runs a new user's first work order on a build of this tree, in a container
// whose only interface is loopback, under strace. Every call that names an address, from the daemon and
// its worker, the CLI, the hooks, the MCP server, the shims, git, tmux, and the double that stands for
// the agent CLI, must name a local socket or a loopback address (FR-DEP-1). A connection that fails
// for want of a network still fails the test, because strace records the attempt.
func TestNoOutboundConnections(t *testing.T) {
	arch := requireDocker(t)
	image := containerImage(t)
	bin := firstRunBinaries(t, arch, true)
	result, out := runFirstWorkOrder(t, firstRun{image: image, bin: bin, trace: true})
	assertFirstWorkOrder(t, result, "dev")

	raw, err := os.ReadFile(filepath.Join(out, "trace.log"))
	if err != nil {
		t.Fatalf("strace wrote no log: %v", err)
	}
	report, err := readTrace(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range report.offLoopback {
		t.Errorf("off loopback: %s", call)
	}
	// A trace that saw nothing would pass, so it must have seen every part of the run, and a
	// connection of each kind it judges.
	for _, program := range []string{"ropeshiftd solo", "ropeshift init", "ropeshift login", "ropeshift work", "ropeshift lane-shim", "ropeshift hook", "ropeshift mcp", "claude", "git"} {
		if !report.programs[program] {
			t.Errorf("the trace never saw %q run", program)
		}
	}
	if report.unix == 0 || report.loopback == 0 {
		t.Errorf("the trace saw %d connections to local sockets and %d to loopback, want some of each", report.unix, report.loopback)
	}
	seen := make([]string, 0, len(report.programs))
	for program := range report.programs {
		seen = append(seen, program)
	}
	slices.Sort(seen)
	t.Logf("traced %d lines: %d connections to local sockets, %d to loopback, none off it; programs %v", bytes.Count(raw, []byte("\n")), report.unix, report.loopback, seen)
}

// TestExitE1FirstWorkOrder is E1 from a release archive: `make snapshot` builds it, install.sh installs
// it from that directory for a new account with nothing in its home, and that account follows
// onboarding with the double first on PATH and the canary behind it. ropeshift init, then the sample
// contract in a repository of its own, reviewed, claimed, run in a lane, and accepted, with no account,
// no Docker, and no network.
func TestExitE1FirstWorkOrder(t *testing.T) {
	snap := containerSnapshot(t)
	arch := requireDocker(t)
	archive := filepath.Join(snap.dir, "ropeshift_"+strings.TrimPrefix(snap.version, "v")+"_linux_"+arch+".tar.gz")
	if _, err := os.Stat(archive); err != nil {
		t.Fatalf("the snapshot holds no archive for linux/%s: %v", arch, err)
	}
	image := containerImage(t)
	bin := firstRunBinaries(t, arch, false)
	result, _ := runFirstWorkOrder(t, firstRun{image: image, bin: bin, dist: snap.dir, version: snap.version})
	assertFirstWorkOrder(t, result, snap.version)
}

// assertFirstWorkOrder holds what every first run must show, whatever built the binary.
func assertFirstWorkOrder(t *testing.T, result firstRunResult, version string) {
	t.Helper()
	for _, step := range result.Steps {
		t.Logf("%-12s exit %d in %4d ms: %s", step.Name, step.Exit, step.Millis, strings.Join(step.Args, " "))
	}
	t.Logf("states %v, web UI %d, ended after the run: %v", result.States, result.WebStatus, result.Left)
	if result.Failure != "" {
		t.Fatalf("the first run failed: %s", result.Failure)
	}
	if !slices.Equal(result.Interfaces, []string{"lo"}) {
		t.Errorf("the container's interfaces that are up or hold an address are %v, want loopback alone", result.Interfaces)
	}
	t.Logf("network devices %v, of which up or holding an address %v", result.Devices, result.Interfaces)
	if result.Docker {
		t.Error("the container holds a docker command or socket")
	}
	if result.PriorState {
		t.Error("the account had an installation before init")
	}
	step := func(name string) (int, string, string) {
		for _, s := range result.Steps {
			if s.Name == name {
				return s.Exit, s.Stdout, s.Terminal
			}
		}
		t.Fatalf("no %s step", name)
		return 0, "", ""
	}
	_, stdout, _ := step("version")
	var reported struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(stdout), &reported); err != nil || reported.Version != version {
		t.Errorf("the binary reports %q (%v), want %s", reported.Version, err, version)
	}
	_, stdout, terminal := step("init")
	for _, want := range []string{"Daemon: started for this run", "Passphrase: set", "Worker: registered as ", "signed in with an API key", initNoCredsRead, "Sample contract: wrote "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("init did not say %q:\n%s", want, stdout)
		}
	}
	if strings.Count(terminal, ": ") != 2 {
		t.Errorf("init's terminal showed %q, want its two prompts", terminal)
	}
	order := []string{"contract_review", "ready", "acceptance", "done"}
	if got := slices.DeleteFunc(slices.Clone(result.States), func(s string) bool { return !slices.Contains(order, s) }); !slices.Equal(got, order) {
		t.Errorf("the work order went through %v, want %v in that order", result.States, order)
	}
	if result.Final != "done" {
		t.Errorf("the work order ended %s", result.Final)
	}
	if result.WebStatus != 200 {
		t.Errorf("the web UI answered %d on its default loopback address", result.WebStatus)
	}
	for _, socket := range result.OffLoopback {
		t.Errorf("a socket off loopback while the daemon ran: %s", socket)
	}
	if result.CanaryRan {
		t.Error("the claude canary ran")
	}
}

// requireDocker returns the architecture the container engine runs, and skips with no engine.
func requireDocker(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("runs a container")
	}
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Arch}}").Output()
	if err != nil {
		t.Skipf("these tests run the Linux build in a container, and Docker is not available: %v", err)
	}
	switch arch := strings.TrimSpace(string(out)); arch {
	case "arm64", "aarch64":
		return "arm64"
	case "amd64", "x86_64":
		return "amd64"
	default:
		t.Skipf("no release targets the container engine's architecture %q", arch)
		return ""
	}
}

// containerImage builds the distribution with its packages, and removes the image by its ID when the
// test ends. Docker's build cache keeps the layers, so a second build needs no network.
func containerImage(t *testing.T) string {
	t.Helper()
	dockerfile := "FROM " + containerBase + "\n" +
		"RUN apt-get update && apt-get install --yes --no-install-recommends " + containerPackages + " && rm -rf /var/lib/apt/lists/*\n"
	build := exec.Command("docker", "build", "--quiet", "-")
	build.Stdin = strings.NewReader(dockerfile)
	var stderr bytes.Buffer
	build.Stderr = &stderr
	out, err := build.Output()
	if err != nil {
		t.Fatalf("build the test image from %s: %v\n%s", containerBase, err, stderr.String())
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "image", "rm", id).CombinedOutput(); err != nil {
			t.Errorf("remove image %s: %v: %s", id, err, out)
		}
	})
	return id
}

// firstRunBinaries builds what goes into the container for linux/arch: the run itself, the double, and
// the canary beside the double's fixture, and with withRopeshift a ropeshift built from this tree.
func firstRunBinaries(t *testing.T, arch string, withRopeshift bool) string {
	t.Helper()
	dir := t.TempDir()
	packages := map[string]string{
		"firstrun":   "ropeshift.dev/ropeshift/test/exit/firstrun",
		"fakeclaude": "ropeshift.dev/ropeshift/test/doubles/fakeclaude",
	}
	if withRopeshift {
		packages["ropeshift"] = "ropeshift.dev/ropeshift/cmd/ropeshift"
	}
	for name, pkg := range packages {
		build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, name), pkg)
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s for linux/%s: %v\n%s", pkg, arch, err, out)
		}
	}
	canary, err := os.ReadFile(filepath.Join("..", "doubles", doubles.PrebuiltCanary, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "canary"), canary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture.json"), doubles.Fixtures(t)[containerFixture], 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// containerSnapshot is the snapshot `make snapshot` wrote, or the one ROPESHIFT_DIST names.
type snapshotDir struct{ dir, version string }

func containerSnapshot(t *testing.T) snapshotDir {
	t.Helper()
	dir := os.Getenv("ROPESHIFT_DIST")
	if dir == "" {
		dir = filepath.Join("..", "..", "dist")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		t.Skipf("no release snapshot at %s, so there is no archive to install; make snapshot builds one: %v", dir, err)
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil || meta.Version == "" {
		t.Fatalf("%s names no version: %v", filepath.Join(dir, "metadata.json"), err)
	}
	return snapshotDir{dir: dir, version: "v" + meta.Version}
}

// firstRun is one container run of test/exit/firstrun.
type firstRun struct {
	image string
	// bin holds what firstRunBinaries built.
	bin string
	// dist and version name a snapshot install.sh installs from. With no dist, bin's ropeshift runs.
	dist, version string
	// trace runs the whole run under strace, into trace.log.
	trace bool
}

// The container's script, run as root. It makes a new account, puts the double and the canary where
// that account can run them, installs ropeshift as the account, and runs firstrun as it with a clean
// environment. The run is a foreground command, because a non-interactive shell starts a command with
// & with SIGINT and SIGQUIT ignored, and every process would inherit that. A watcher in the background
// stops the run 30s after firstrun wrote its result if anything it traced still runs. A process left
// at the end is listed, and the output directory is opened to the host's user before the script exits
// with firstrun's code.
const firstRunScript = `set -u
useradd --create-home --shell /bin/sh newuser
install -d -o newuser -g newuser /opt/agent /opt/canary
install -o newuser -g newuser -m 0755 /harness/fakeclaude /opt/agent/claude
install -o newuser -g newuser -m 0644 /harness/fixture.json /opt/agent/fixture.json
install -o newuser -g newuser -m 0755 /harness/canary /opt/canary/claude
if [ -n "${DIST:-}" ]; then
	runuser -u newuser -- env -i HOME=/home/newuser PATH=/usr/bin:/bin sh /src/install.sh --from /dist --version "$VERSION" || exit 10
	BIN=/home/newuser/.local/bin
else
	install -d /opt/ropeshift/bin
	install -m 0755 /harness/ropeshift /opt/ropeshift/bin/ropeshift
	ln -s ropeshift /opt/ropeshift/bin/ropeshiftd
	BIN=/opt/ropeshift/bin
fi
set -- runuser -u newuser -- env -i HOME=/home/newuser USER=newuser LOGNAME=newuser LANG=C.UTF-8 TERM=xterm-256color \
	PATH=/opt/agent:/opt/canary:$BIN:/usr/bin:/bin /harness/firstrun -out /out -canary /opt/canary
if [ -n "${TRACE:-}" ]; then
	set -- strace -f --seccomp-bpf -qq -s 512 -e signal=none -e trace="$TRACE" -o /out/trace.log "$@"
fi
watch_run() {
	until [ -e /run/firstrun.pid ] || [ -e /run/firstrun.done ]; do sleep 1; done
	[ -e /run/firstrun.pid ] || return 0
	run=$(cat /run/firstrun.pid)
	after=0
	while kill -0 "$run" 2>/dev/null; do
		if [ -e /out/result.json ]; then
			after=$((after + 1))
			if [ "$after" -gt 30 ]; then
				echo "LINGERING: processes ran 30s after the run ended, so the run was stopped"
				kill -TERM "$run"
				return 0
			fi
		fi
		sleep 1
	done
}
watch_run &
watcher=$!
sh -c 'echo $$ > /run/firstrun.pid && exec "$@"' run "$@"
code=$?
: > /run/firstrun.done
wait "$watcher"
self=$(tr '\0' ' ' < /proc/$$/cmdline)
for p in /proc/[0-9]*; do
	pid=${p#/proc/}
	[ "$pid" = 1 ] || [ "$pid" = "$$" ] && continue
	cmd=$(tr '\0' ' ' < "$p/cmdline" 2>/dev/null)
	case "$cmd" in "" | "$self" | "tr "*) continue ;; esac
	echo "LEFT $pid $cmd"
done
chmod -R a+rwX /out
exit "$code"
`

// runFirstWorkOrder runs one first run in a new container with no network, removed by its ID at the
// end, and returns what firstrun wrote and the directory that holds it.
func runFirstWorkOrder(t *testing.T, run firstRun) (firstRunResult, string) {
	t.Helper()
	out := t.TempDir()
	if err := os.Chmod(out, 0o777); err != nil {
		t.Fatal(err)
	}
	args := []string{"create", "--network", "none", "--init", "--volume", run.bin + ":/harness:ro", "--volume", out + ":/out"}
	if run.dist != "" {
		script := filepath.Join("..", "..", "install.sh")
		script, err := filepath.Abs(script)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args, "--volume", run.dist+":/dist:ro", "--volume", script+":/src/install.sh:ro", "--env", "DIST=1", "--env", "VERSION="+run.version)
	}
	if run.trace {
		args = append(args, "--env", "TRACE="+traceCalls)
	}
	args = append(args, "--entrypoint", "/bin/sh", run.image, "-c", firstRunScript)
	created, err := exec.Command("docker", args...).Output()
	if err != nil {
		t.Fatalf("docker create: %v", err)
	}
	id := strings.TrimSpace(string(created))
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "--force", id).CombinedOutput(); err != nil {
			t.Errorf("remove container %s: %v: %s", id, err, out)
		}
	})
	start := exec.Command("docker", "start", "--attach", id)
	var output bytes.Buffer
	start.Stdout, start.Stderr = &output, &output
	runErr := start.Run()
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if strings.HasPrefix(line, "LEFT ") || strings.HasPrefix(line, "LINGERING") {
			t.Errorf("the container: %s", line)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "result.json"))
	if err != nil {
		daemonLog, _ := os.ReadFile(filepath.Join(out, "daemon.log"))
		t.Fatalf("firstrun wrote no result (%v, container %v):\n%s\ndaemon:\n%s", err, runErr, output.String(), daemonLog)
	}
	var result firstRunResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("container output:\n%s", strings.TrimSpace(output.String()))
	if result.Failure != "" || runErr != nil {
		daemonLog, _ := os.ReadFile(filepath.Join(out, "daemon.log"))
		t.Logf("container %v, result:\n%s\ndaemon:\n%s", runErr, raw, daemonLog)
	}
	return result, out
}
