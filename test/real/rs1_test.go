//go:build realsession

package real

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
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
	// session is a member's session, once the daemon has answered.
	session string
	spend   *spendLedger
	repo    string
	stream  string
	before  [32]byte
	// worktree is where the lane's session ran.
	worktree string
	// others are further directories a suite's sessions ran in.
	others []string
	// standIn is the PATH of a dry run, which finds a scripted CLI first.
	standIn string
	// watched names the lanes a run reports its evidence on, and daemons what it started.
	watched []string
	daemons []*daemon
	// limits are the readings this run has printed.
	limits []string
	// keep runs first in cleanup, while everything a run left still stands.
	keep func()
	// daemonEnv is what a suite adds to its daemons' environment beyond the allowlist.
	daemonEnv []string
	// logs are the lines this run has logged through logf, which a keyed run scans before it ends.
	logs []string
	// key is set only on the run of the API-key lane, and nothing but that lane's daemon receives it.
	key secretKey
	// entries are what the CLI's own files in the home directory were before the run.
	entries map[string]entryState
}

// logf logs one line of a run's evidence and keeps it for the scan a keyed run ends with.
func (s *suite) logf(format string, args ...any) {
	s.t.Helper()
	line := fmt.Sprintf(format, args...)
	s.logs = append(s.logs, line)
	s.t.Log(line)
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
		stream: filepath.Join(scratch, "stream.log"), entries: readEntries(home),
	}
	t.Cleanup(s.finish)
	if os.Getenv(envStandIn) != "" {
		s.standIn = doubles.InstallFakeClaude(t, standInFixtures[name])
	}
	return s
}

// finish removes what the run made and reports what the CLI left where a suite must
// not edit.
func (s *suite) finish() {
	s.t.Helper()
	if s.keep != nil {
		s.keep()
	}
	s.reportEvidence()
	after, err := settingsDigest(s.home)
	if err != nil {
		s.t.Error(err)
	} else if after != s.before {
		s.t.Error("STOP: the user-level settings file changed")
	}
	sessions, turns := s.spend.used()
	sessionCeiling, turnCeiling := s.spend.ceilings()
	s.logf("charged: sessions=%d turns=%d of %d and %d", sessions, turns, sessionCeiling, turnCeiling)
	left := s.processesLeft()
	for _, process := range left {
		s.t.Errorf("a process remains: %s", process)
	}
	// The CLI names a project after the git root, so a lane's worktree lands on its
	// repository's name. A probe's sessions are in no ledger, so the stream names them.
	transcripts := s.ledgerSessions()
	for _, session := range s.streamSessions() {
		if !slices.Contains(transcripts, session) {
			transcripts = append(transcripts, session)
		}
	}
	for _, dir := range append([]string{filepath.Join(s.scratch, "probe"), s.repo, s.worktree}, s.others...) {
		if dir == "" {
			continue
		}
		for _, session := range transcripts {
			if err := removeTranscript(s.home, s.scratch, dir, session); err != nil {
				s.t.Error(err)
			}
		}
		if err := removeEmptyProject(s.home, s.scratch, dir); err != nil {
			s.t.Error(err)
		}
		if _, err := os.Stat(projectDirectory(s.home, dir)); err == nil {
			s.t.Errorf("a project directory remains for a scratch path")
		}
	}
	s.logf("leftovers: the CLI's own files, which the harness never edits: %s", strings.Join(entriesReport(s.entries, readEntries(s.home)), "; "))
	s.logf("scratch path, for the leftovers report: %s", s.scratch)
	s.scanForKey()
	if err := os.RemoveAll(s.scratch); err != nil {
		s.t.Error(err)
	}
}

// scanForKey fails a keyed run whose output holds its key anywhere: a line it logged, what a daemon
// wrote, or a file under its scratch path. It runs last, while that path still stands, and names
// where it found the key and never the key.
func (s *suite) scanForKey() {
	s.t.Helper()
	if !s.key.set() {
		return
	}
	buffers := map[string][]byte{}
	var found []string
	for i, d := range s.daemons {
		name := fmt.Sprintf("daemon %d's output", i+1)
		if !d.stopped {
			found = append(found, name+", which was still being written")
			continue
		}
		buffers[name] = []byte(d.reported())
	}
	found = append(found, keyFindings(s.key, s.scratch, s.logs, buffers)...)
	for _, where := range found {
		s.t.Errorf("STOP: the API key appears in %s", where)
	}
	s.t.Logf("key scan: lines=%d daemons=%d findings=%d", len(s.logs), len(buffers), len(found))
}

// streamSessions names every session the raw stream holds, a probe's included.
func (s *suite) streamSessions() []string {
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		return nil
	}
	var sessions []string
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		var event timelineLine
		if json.Unmarshal(line, &event) == nil && sessionName.MatchString(event.SessionID) && !slices.Contains(sessions, event.SessionID) {
			sessions = append(sessions, event.SessionID)
		}
	}
	return sessions
}

// processesLeft gives a pane a killed terminal server just ended time to exit: a run with a stopped
// pane read a session and its MCP server here that were gone a moment later.
func (s *suite) processesLeft() []string {
	s.t.Helper()
	for deadline := time.Now().Add(rs5EndLimit); ; time.Sleep(rs5EndPoll) {
		left, err := processesHolding(s.scratch)
		if err != nil {
			s.t.Error(err)
		}
		if len(left) == 0 || !time.Now().Before(deadline) {
			return left
		}
	}
}

// ledgerSessions names every session this run's ledger holds, because a suite that resumes keeps
// its transcripts until here. A run that never reached a ledger has none.
func (s *suite) ledgerSessions() []string {
	path := filepath.Join(s.paths.StateDir, api.LedgerFileName)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	store, err := sqlite.Open(path, sqlstore.Options{})
	if err != nil {
		s.t.Error(err)
		return nil
	}
	defer store.Close()
	var sessions []string
	err = store.Read(context.Background(), "default", func(tx ports.ReadTx) error {
		rows, err := tx.Lanes().Rows()
		if err != nil {
			return err
		}
		for _, row := range rows {
			generations, err := tx.Lanes().Generations(row.ID)
			if err != nil {
				return err
			}
			for _, generation := range generations {
				if generation.SessionID != "" && !slices.Contains(sessions, generation.SessionID) {
					sessions = append(sessions, generation.SessionID)
				}
			}
		}
		return nil
	})
	if err != nil {
		s.t.Error(err)
	}
	return sessions
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
	build := exec.Command("go", "build", "-tags", "realsession", "-o", binary, "ropeshift.com/ropeshift/cmd/ropeshift")
	build.Env = append(withoutKeys(os.Environ()), "CGO_ENABLED=0")
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
	cmd.Env = s.environment(append(s.daemonTags(), s.memberSession()...)...)
	return cmd.Output()
}

// memberSession signs in once the daemon answers, and is empty until then. The token goes
// only to a member's command, never to the daemon, and so never toward a lane.
func (s *suite) memberSession() []string {
	s.t.Helper()
	if s.session == "" {
		client := api.NewUnixClient(s.paths.ControlSocket())
		defer client.Close()
		token, err := apitest.SignIn(s.t.Context(), client)
		if err != nil {
			return nil
		}
		s.session = token
	}
	return []string{authority.EnvMemberToken + "=" + s.session}
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
		s.t.Fatalf("STOP: the probe passed its limit, and it exited: %v", <-done)
	}
	s.noteLimits(stdout.Bytes())
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
	s.logf("probe: version=%s model=%s apiKeySource=%s permission=%s", init.Version, init.Model, init.KeySource, init.Permission)
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
	if err := checkInit(init, []string{"ropeshift"}, laneTools); err != nil {
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
	// exit is how its process ended, once it has.
	exit error
}

// startDaemon runs the daemon on this suite's repository, with the guard flags reaching
// the production driver through the override that exists only under this build tag.
func (s *suite) startDaemon() *daemon {
	s.t.Helper()
	return s.startDaemonWith(map[string]any{
		"args": append(append([]string{}, guardFlags...), "--max-turns", laneMaxTurns), "stream_log": s.stream,
	})
}

// startDaemonWith runs the daemon with one suite's override.
func (s *suite) startDaemonWith(overrides map[string]any) *daemon {
	s.t.Helper()
	return s.launchDaemon(s.daemonEnvironment(overrides))
}

// daemonEnvironment is a daemon's whole environment: the allowlist, its paths, and one suite's
// override.
func (s *suite) daemonEnvironment(overrides map[string]any) []string {
	s.t.Helper()
	override, err := json.Marshal(overrides)
	if err != nil {
		s.t.Fatal(err)
	}
	return s.environment(append(append(s.daemonTags(), s.daemonEnv...), "ROPESHIFT_SESSION_OVERRIDE="+string(override), api.EnvWeb+"="+api.WebOff)...)
}

// launchDaemon runs the daemon on this suite's repository with exactly this environment.
func (s *suite) launchDaemon(env []string) *daemon {
	s.t.Helper()
	cmd := exec.Command(filepath.Join(s.binDir, "ropeshiftd"), "solo",
		"--source", s.repo, "--intake-tick", "200ms", "--heartbeat", "1s", "--lease-tick", "1s")
	cmd.Env = env
	d := &daemon{cmd: cmd, exited: make(chan error, 1), stderr: &bytes.Buffer{}}
	cmd.Stderr = d.stderr
	if err := cmd.Start(); err != nil {
		s.t.Fatal(err)
	}
	go func() { d.exited <- cmd.Wait() }()
	s.daemons = append(s.daemons, d)
	return d
}

// reported reads what a daemon wrote, and only once it has stopped, because its process writes
// to that buffer until then.
func (d *daemon) reported() string {
	if !d.stopped {
		return ""
	}
	return fmt.Sprintf("exit: %v\n%s", d.exit, d.stderr)
}

// stop ends the daemon the way a service manager does.
func (d *daemon) stop() {
	if d.stopped {
		return
	}
	d.stopped = true
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case d.exit = <-d.exited:
	case <-time.After(15 * time.Second):
		_ = d.cmd.Process.Signal(syscall.SIGKILL)
		d.exit = <-d.exited
	}
}

// kill ends the daemon with no warning, by its exact process ID.
func (d *daemon) kill() {
	if d.stopped {
		return
	}
	d.stopped = true
	_ = d.cmd.Process.Signal(syscall.SIGKILL)
	d.exit = <-d.exited
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
	s.pollFor(turnLimit, stop, what, done)
}

// pollFor is a poll with a limit of its own.
func (s *suite) pollFor(limit time.Duration, stop func(), what string, done func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	s.stopWith(stop, "%s did not happen within %v", what, limit)
}

// checkStream applies the stop conditions to the lane's raw stream and stderr.
func (s *suite) checkStream(stop func()) {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.t.Fatal(err)
	}
	s.noteLimits(raw)
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

// noteLimits prints each new thing a stream says of a sign-in's limits, so what a run went on past
// stays on its record. A poll reads the whole stream each time, so each reading prints once.
func (s *suite) noteLimits(stream []byte) {
	s.t.Helper()
	for _, reading := range limitReadings(stream) {
		if !slices.Contains(s.limits, reading) {
			s.limits = append(s.limits, reading)
			s.logf("rate limit: %s", reading)
		}
	}
}

// eventsOf reads a work order's events from the ledger, which a stopped daemon has left.
func (s *suite) eventsOf(id string) []ledger.Event {
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
	return events
}

// outcomeOf says how a work order's lane ended. A reason the worker wrote is a code. A reason a
// session wrote is a reply, so a log keeps only that there was one.
func (s *suite) outcomeOf(id string) (workorders.Outcome, string) {
	s.t.Helper()
	var ended workorders.Change
	for _, event := range s.eventsOf(id) {
		var change workorders.Change
		if json.Unmarshal(event.Data, &change) == nil && change.Outcome != "" {
			ended = change
		}
	}
	return ended.Outcome, reasonClass(ended.Reason)
}

// reasonClass keeps a worker's code and hides anything a session wrote.
func reasonClass(reason string) string {
	if reason == "" || slices.Contains(workerReasons, reason) {
		return reason
	}
	return "the session's own"
}

// workerReasons are the reasons a worker records for a lane that did not report for itself.
var workerReasons = []string{
	workorders.ReasonNoOutcome, workorders.ReasonCrashed, workorders.ReasonSpawnFailed, workorders.ReasonNoRepository,
	workorders.ReasonWorkerRestarted, workorders.ReasonFenced, workorders.ReasonUnknownScreen, workorders.ReasonHookFailed,
	workorders.ReasonHookUnknown,
}

// report states the evidence by name, and holds no session ID, path, prompt, or reply.
func (s *suite) report(id string, init initLine, raw []byte) {
	s.t.Helper()
	events := s.eventsOf(id)
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
	s.logf("lane: version=%s model=%s apiKeySource=%s permission=%s results=%d", init.Version, init.Model, init.KeySource, init.Permission, turns)
	s.logf("lane: outcome=%s events=%s", ended.Outcome, strings.Join(types, ","))
}
