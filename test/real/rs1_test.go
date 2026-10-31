//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/sqlite"
	"ropeshift.dev/ropeshift/internal/adapters/sqlstore"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/doubles"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	turnLimit     = 120 * time.Second
	laneMaxTurns  = "6"
	laneTurnsPaid = 2
	probePrompt   = "Reply with the single word ok."
	contractText  = `+++
kind = "contract"
id = "rs1"
title = "Report a finished lane"

[[criteria]]
id = "c1"
text = "The lane reports its outcome"

[[work_orders]]
id = "rs1-order"
title = "Report a finished lane"
size = "small"
+++

This work order tests the path a lane reports through, and it has no other work. Change
no file and use no other tool. Call the lane_report tool once, with outcome completed.
Then end your turn with the single word done.
`
	profileText = `[agent]
model = "` + sessionModel + `"
permission_mode = "default"
`
)

// suite is one run of one suite and everything it must clean up.
type suite struct {
	t       *testing.T
	home    string
	scratch string
	binDir  string
	paths   api.Paths
	spend   *spendLedger
	repo    string
	stream  string
	before  [32]byte
	// worktree is where the lane's session ran.
	worktree string
	// standIn is the PATH of a dry run, which finds a scripted CLI first.
	standIn string
}

// start refuses to run without the explicit start, and takes the settings digest before
// anything else happens. A name keeps two suites' scratch paths apart.
func start(t *testing.T, name string) *suite {
	t.Helper()
	if os.Getenv(envStart) != "1" {
		t.Skipf("real sessions need %s=1", envStart)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	before, err := settingsDigest(home)
	if err != nil {
		t.Fatal(err)
	}
	// A short base, because a session's socket path has a 104-byte limit.
	scratch, err := os.MkdirTemp("/tmp", name)
	if err != nil {
		t.Fatal(err)
	}
	if scratch, err = filepath.EvalSymlinks(scratch); err != nil {
		t.Fatal(err)
	}
	s := &suite{
		t: t, home: home, scratch: scratch, binDir: filepath.Join(scratch, "bin"), spend: newSpendLedger(), before: before,
		paths:  api.Paths{StateDir: filepath.Join(scratch, "state"), RunDir: filepath.Join(scratch, "run")},
		stream: filepath.Join(scratch, "stream.log"),
	}
	t.Cleanup(s.finish)
	if os.Getenv(envStandIn) != "" {
		s.standIn = doubles.InstallFakeClaude(t, standInFixture)
	}
	return s
}

// finish removes what the run made and reports what the CLI left where a suite must
// not edit.
func (s *suite) finish() {
	s.t.Helper()
	after, err := settingsDigest(s.home)
	if err != nil {
		s.t.Error(err)
	} else if after != s.before {
		s.t.Error("STOP: the user-level settings file changed")
	}
	sessions, turns := s.spend.used()
	s.t.Logf("charged: sessions=%d turns=%d of %d and %d", sessions, turns, maxSessions, maxTurns)
	left, err := processesHolding(s.scratch)
	if err != nil {
		s.t.Error(err)
	}
	for _, process := range left {
		s.t.Errorf("a process remains: %s", process)
	}
	// The CLI names a project after the git root, so a lane's worktree lands on its
	// repository's name.
	for _, dir := range []string{filepath.Join(s.scratch, "probe"), s.repo, s.worktree} {
		if dir == "" {
			continue
		}
		if err := removeEmptyProject(s.home, s.scratch, dir); err != nil {
			s.t.Error(err)
		}
		if _, err := os.Stat(projectDirectory(s.home, dir)); err == nil {
			s.t.Errorf("a project directory remains for a scratch path")
		}
	}
	if err := os.RemoveAll(s.scratch); err != nil {
		s.t.Error(err)
	}
	s.t.Logf("scratch path, for the leftovers report: %s", s.scratch)
}

// environment is the whole environment of a process this suite starts.
func (s *suite) environment(tags ...string) []string {
	s.t.Helper()
	lookup := func(name string) (string, bool) {
		value, ok := os.LookupEnv(name)
		if name == "PATH" {
			if s.standIn != "" {
				value = s.standIn
			}
			return withoutCanary(value), ok
		}
		return value, ok
	}
	env, err := cleanEnvironment(lookup, tags...)
	if err != nil {
		s.t.Fatal(err)
	}
	return env
}

func (s *suite) daemonTags() []string {
	return []string{api.EnvStateDir + "=" + s.paths.StateDir, api.EnvRunDir + "=" + s.paths.RunDir}
}

func (s *suite) build() {
	s.t.Helper()
	if err := os.MkdirAll(s.binDir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	binary := filepath.Join(s.binDir, "ropeshift")
	build := exec.Command("go", "build", "-tags", "realsession", "-o", binary, "ropeshift.dev/ropeshift/cmd/ropeshift")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		s.t.Fatalf("build: %v\n%s", err, out)
	}
	if err := os.Symlink(binary, filepath.Join(s.binDir, "ropeshiftd")); err != nil {
		s.t.Fatal(err)
	}
}

func (s *suite) cli(args ...string) []byte {
	s.t.Helper()
	out, err := s.tryCLI(args...)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			s.t.Fatalf("%v: %v: %s", args, err, exit.Stderr)
		}
		s.t.Fatalf("%v: %v", args, err)
	}
	return out
}

// tryCLI is for a call that may come before the daemon listens.
func (s *suite) tryCLI(args ...string) ([]byte, error) {
	s.t.Helper()
	cmd := exec.Command(filepath.Join(s.binDir, "ropeshift"), args...)
	cmd.Env = s.environment(s.daemonTags()...)
	return cmd.Output()
}

func (s *suite) workOrder(id string) workorders.WorkOrder {
	s.t.Helper()
	var row workorders.WorkOrder
	if err := json.Unmarshal(s.cli("work", "show", id, "--json"), &row); err != nil {
		s.t.Fatal(err)
	}
	return row
}

// stopWith ends the run at the first sign of trouble, with no retry.
func (s *suite) stopWith(stop func(), format string, args ...any) {
	s.t.Helper()
	stop()
	s.t.Fatalf("STOP: "+format, args...)
}

// probe is the first session: no harness settings at all. It must report no hook event
// and no user configuration, or a user hook, plugin, or MCP server would load in the
// lane, so the suite stops before the lane starts.
func (s *suite) probe() {
	s.t.Helper()
	if err := s.spend.charge("probe", 1); err != nil {
		s.t.Fatal(err)
	}
	args := append([]string{
		"-p", "--verbose", "--output-format", "stream-json", "--include-hook-events", "--model", sessionModel,
		"--permission-mode", "default",
	}, guardFlags...)
	if err := checkArguments(args); err != nil {
		s.t.Fatal(err)
	}
	binary, err := claudeOn(s.environment())
	if err != nil {
		s.t.Fatal(err)
	}
	if strings.Contains(binary, filepath.Join("test", "doubles")) {
		s.t.Fatalf("claude resolves to a test double: %s", binary)
	}
	dir := filepath.Join(s.scratch, "probe")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = s.environment()
	cmd.Stdin = strings.NewReader(probePrompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(turnLimit):
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-done
		s.t.Fatal("STOP: the probe passed its limit")
	}
	signals := append(stopSignals(stdout.Bytes()), stderrSignals(stderr.Bytes())...)
	if len(signals) != 0 {
		s.t.Fatalf("STOP: the probe: %v", signals)
	}
	if err != nil {
		s.t.Fatalf("STOP: the probe exited: %v", err)
	}
	if hooks := hookEvents(stdout.Bytes()); hooks != 0 {
		s.t.Fatalf("STOP: %d hook events with no harness settings", hooks)
	}
	init, err := firstInit(stdout.Bytes())
	if err != nil {
		s.t.Fatal(err)
	}
	if err := checkInit(init, nil, nil); err != nil {
		s.t.Fatalf("STOP: the probe's init: %v", err)
	}
	s.t.Logf("probe: version=%s model=%s apiKeySource=%s permission=%s", init.Version, init.Model, init.KeySource, init.Permission)
}

// TestRS1 runs a real session through the production driver, in the walk the first
// milestone names: a contract file, a hand claim, one lane, and a typed outcome. Only a
// real session can show lane_report through `ropeshift mcp`, the lane token reaching a
// stdio server, and the driver's real flags.
func TestRS1(t *testing.T) {
	s := start(t, "rs1")
	s.build()
	s.probe()

	repo := gittest.NewIn(t, s.scratch)
	repo.Commit(".ropeshift/profile.toml", profileText)
	repo.Commit("contract.md", contractText)
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", "rs1-lane")

	if err := s.spend.charge("lane", laneTurnsPaid); err != nil {
		t.Fatal(err)
	}
	running := s.startDaemon()
	stop := running.stop
	t.Cleanup(stop)

	id := s.waitForWorkOrder(stop)
	s.cli("work", "review", id)
	var workers api.WorkerList
	s.poll(stop, "the worker's session", func() bool {
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			t.Fatal(err)
		}
		return len(workers.Workers) == 1 && workers.Workers[0].Epoch >= 1
	})
	s.cli("work", "claim", id, "--worker", workers.Workers[0].ID)
	s.cli("lane", "start", id, "--lane", "rs1-lane")

	settled := map[workorders.State]bool{
		workorders.Acceptance: true, workorders.Blocked: true, workorders.Failed: true, workorders.Ready: true,
	}
	var row workorders.WorkOrder
	s.poll(stop, "the lane's outcome", func() bool {
		s.checkStream(stop)
		row = s.workOrder(id)
		return settled[row.State]
	})
	s.checkStream(stop)
	stop()

	raw, err := os.ReadFile(s.stream)
	if err != nil {
		t.Fatal(err)
	}
	init, err := firstInit(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkInit(init, []string{"ropeshift"}, []string{"mcp__ropeshift__lane_report"}); err != nil {
		t.Fatalf("STOP: the lane's init: %v", err)
	}
	if row.State != workorders.Acceptance {
		t.Fatalf("the lane ended in %s\n%s", row.State, running.stderr.String())
	}
	s.report(id, init, raw)
}

// daemon is one `ropeshiftd solo` this suite started. Signals go to its recorded process
// and nowhere else.
type daemon struct {
	cmd     *exec.Cmd
	exited  chan error
	stderr  *bytes.Buffer
	stopped bool
}

// startDaemon runs the daemon on this suite's repository, with the guard flags reaching
// the production driver through the override that exists only under this build tag.
func (s *suite) startDaemon() *daemon {
	s.t.Helper()
	override, err := json.Marshal(map[string]any{
		"args": append(append([]string{}, guardFlags...), "--max-turns", laneMaxTurns), "stream_log": s.stream,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(s.binDir, "ropeshiftd"), "solo",
		"--source", s.repo, "--intake-tick", "200ms", "--heartbeat", "1s", "--lease-tick", "1s")
	cmd.Env = s.environment(append(s.daemonTags(), "ROPESHIFT_SESSION_OVERRIDE="+string(override))...)
	d := &daemon{cmd: cmd, exited: make(chan error, 1), stderr: &bytes.Buffer{}}
	cmd.Stderr = d.stderr
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	go func() { d.exited <- cmd.Wait() }()
	return d
}

// stop ends the daemon the way a service manager does.
func (d *daemon) stop() {
	if d.stopped {
		return
	}
	d.stopped = true
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.exited:
	case <-time.After(15 * time.Second):
		_ = d.cmd.Process.Signal(syscall.SIGKILL)
		<-d.exited
	}
}

// kill ends the daemon with no warning, by its exact process ID.
func (d *daemon) kill() {
	if d.stopped {
		return
	}
	d.stopped = true
	_ = d.cmd.Process.Signal(syscall.SIGKILL)
	<-d.exited
}

func (s *suite) waitForWorkOrder(stop func()) string {
	s.t.Helper()
	var id string
	s.poll(stop, "intake", func() bool {
		out, err := s.tryCLI("work", "list", "--json")
		if err != nil {
			return false
		}
		var list api.WorkOrderList
		if err := json.Unmarshal(out, &list); err != nil {
			s.t.Fatal(err)
		}
		if len(list.WorkOrders) == 0 {
			return false
		}
		id = list.WorkOrders[0].ID
		return true
	})
	return id
}

func (s *suite) poll(stop func(), what string, done func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(turnLimit)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.stopWith(stop, "%s did not happen within %v", what, turnLimit)
}

// checkStream applies the stop conditions to the lane's raw stream and stderr.
func (s *suite) checkStream(stop func()) {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.t.Fatal(err)
	}
	signals := stopSignals(raw)
	logs, err := filepath.Glob(filepath.Join(s.paths.StateDir, "lanes", "*", "stderr-*.log"))
	if err != nil {
		s.t.Fatal(err)
	}
	for _, path := range logs {
		text, err := os.ReadFile(path)
		if err == nil {
			signals = append(signals, stderrSignals(text)...)
		}
	}
	if len(signals) != 0 {
		s.stopWith(stop, "%v", signals)
	}
}

// report states the evidence by name, and holds no session ID, path, prompt, or reply.
func (s *suite) report(id string, init initLine, raw []byte) {
	s.t.Helper()
	store, err := sqlite.Open(filepath.Join(s.paths.StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		s.t.Fatal(err)
	}
	defer store.Close()
	var events []ledger.Event
	err = store.Read(s.t.Context(), "default", func(tx ports.ReadTx) error {
		all, err := control.ReadAllEvents(tx.Events)
		for _, e := range all {
			if e.Subject.Kind == workorders.SubjectKind && e.Subject.ID == id {
				events = append(events, e)
			}
		}
		return err
	})
	if err != nil {
		s.t.Fatal(err)
	}
	last := events[len(events)-1]
	var ended workorders.Change
	if err := json.Unmarshal(last.Data, &ended); err != nil {
		s.t.Fatal(err)
	}
	if ended.Outcome != workorders.OutcomeCompleted || last.Actor.Kind != ledger.ActorLane {
		s.t.Fatalf("the outcome on record: %+v from %+v", ended, last.Actor)
	}
	types := make([]string, 0, len(events))
	for _, e := range events {
		types = append(types, strings.TrimPrefix(e.Type, "work_order."))
	}
	turns := bytes.Count(raw, []byte(`"type":"result"`))
	s.t.Logf("lane: version=%s model=%s apiKeySource=%s permission=%s results=%d", init.Version, init.Model, init.KeySource, init.Permission, turns)
	s.t.Logf("lane: outcome=%s events=%s", ended.Outcome, strings.Join(types, ","))
}
