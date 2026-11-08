//go:build realsession

package real

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/internal/workorders"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/exit/ptypair"
	"ropeshift.dev/ropeshift/test/gittest"
)

const (
	rs15E1Lane       = "rs15-e1"
	rs15E4Lane       = "rs15-e4"
	rs15HeadlessLane = "rs15-e5h"
	rs15PaneLane     = "rs15-e5t"
	rs15KeyLane      = "rs15-key"

	// rs15Sessions and rs15Turns are the ceiling of one run, the largest spike batch. Each part's own
	// charges keep it well under, and one run holds one part.
	rs15Sessions = 10
	rs15Turns    = 80

	// rs15FirstWait bounds a part's first step, which waits while a fresh worker's probe qualifies the
	// installed version, as RS14's lane does.
	rs15FirstWait = 2*turnLimit + 5*time.Minute
	// rs15InitLimit bounds `ropeshift init`, which waits up to a minute for its worker to register.
	rs15InitLimit = 3 * time.Minute
	// rs15TerminalFD is where init finds its terminal: the first of its extra files.
	rs15TerminalFD = 3
	// rs15ProbePane is the pane the qualification probe opens, whose screens the guards read.
	rs15ProbePane = "probe-screens"
	// rs15ToolLimit ends a pane that keeps calling tools past what its charge holds.
	rs15ToolLimit = 6

	// E1's lane reads the repository to find its test command and writes one file. A first real run
	// spent six turns reading before it reported, so each of its inputs takes up to twelve: its brief,
	// and one nudge's.
	rs15E1MaxTurns = "12"
	rs15E1Charge   = 24
	// E4's lane takes its brief and the instruction that asks for its narrative, then the resume.
	rs15E4MaxTurns     = "3"
	rs15E4ParkedCharge = 6
	rs15E4ResumeCharge = 3
	// E5's headless lane takes RS10's charge: its brief, the compaction, the turn after it, and the
	// handoff, at six turns an input and one for the compaction, then its next generation.
	rs15ContextMaxTurns = "6"
	rs15HeadlessCharge  = 19
	rs15NextCharge      = 6
	// E5's pane takes its brief, the compaction, the turn after it, and the handoff, under its tool
	// limit, then its next generation's brief.
	rs15PaneCharge     = 8
	rs15PaneNextCharge = 3
	// The API-key lane takes one input.
	rs15KeyMaxTurns = "3"
	rs15KeyCharge   = 3

	rs15Question = "rs15 question"
	// rs15Answer is a word the question never holds, so a report that names it heard the answer.
	rs15Answer = "blue"
	// rs15Handoff is a stand-in's mark that its first generation has handed off.
	rs15Handoff = "rs15-handed-off"
)

// rs15Parts is how each part runs.
const rs15Parts = `  make real SUITE=RS15/E1
  make real SUITE=RS15/E4
  make real SUITE=RS15/E5/headless
  make real SUITE=RS15/E5/tmux
  make real SUITE=RS15/APIKey, with ` + envTestKey + ` set`

// rs15Charge is what one session may spend.
type rs15Charge struct {
	session string
	turns   int
}

// rs15ProbeCharges are the qualification probe's sessions, which each part's fresh worker runs before
// its first lane, charged as RS14 charges them. The guard's own probe charges itself.
var rs15ProbeCharges = []rs15Charge{{"probe-accept", 2}, {"probe-refuse", 1}, {"probe-hold", 1}, {"probe-screens", 0}}

// rs15Ended are the states a lane cannot leave on its own.
var rs15Ended = map[workorders.State]bool{workorders.Blocked: true, workorders.Failed: true, workorders.Acceptance: true, workorders.Done: true}

// rs15InitLines are what `ropeshift init` must print, by the name the evidence gives each.
var rs15InitLines = map[string]string{
	"daemon_started":      "Daemon: started for this run",
	"passphrase_set":      "Passphrase: set",
	"worker_registered":   "Worker: registered as ",
	"sample_written":      "Sample contract: wrote ",
	"no_credentials_read": "Ropeshift never reads, stores, relays, or proxies an agent CLI's credentials.",
}

// rs15SignIns name what init said of the CLI's sign-in, by the words of its line.
var rs15SignIns = []struct{ words, kind string }{
	{"version check failed", "failed_check"}, {"not installed", "not_installed"}, {"not signed in", "signed_out"},
	{"signed in with an API key", "api_key"}, {"signed in with a subscription", "subscription"}, {"sign-in could not be read", "unread"},
}

func rs15Flags(maxTurns string) []string {
	return append(slices.Clone(guardFlags), "--max-turns", maxTurns)
}

// rs15E1Flags are the guard flags with the tools a first work order writes a file with.
var rs15E1Flags = []string{
	"--setting-sources", "project", "--strict-mcp-config", "--tools", "Read,Write,Edit",
	"--max-budget-usd", budgetUSD, "--no-session-persistence", "--max-turns", rs15E1MaxTurns,
}

// rs15ResumableFlags keep a transcript, because a native resume needs one, and finish takes away only
// the transcripts of sessions this run's ledger and stream name.
var rs15ResumableFlags = []string{
	"--setting-sources", "project", "--strict-mcp-config", "--tools", "",
	"--max-budget-usd", budgetUSD, "--max-turns", rs15E4MaxTurns,
}

// rs15E1UserProfile is the new user's own layer, which alone may let edits run without a prompt, so
// the lane's one file needs no person to approve it.
const rs15E1UserProfile = `[agent]
permission_mode = "acceptEdits"
`

// rs15E1Makefile is the test command of the new user's repository, which the sample contract asks
// README.md to name. A repository with none gives the criterion nothing to name, and a first real run
// spent its turns looking for one.
const rs15E1Makefile = `.PHONY: test

# Runs this repository's tests.
test:
	sh -c 'echo "no tests yet"'
`

// rs15E1Profile is all the repository adds to the sample contract: the model and limits every real
// session runs under.
const rs15E1Profile = `run_mode = "headless"

[agent]
model = "` + sessionModel + `"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`

const rs15E4Profile = `run_mode = "headless"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
handoff_timeout = "90s"
ask_wait = "1s"
max_restarts = 0
`

func rs15Contract(id, title, criterion, steps string) string {
	return `+++
kind = "contract"
id = "` + id + `"
title = "` + title + `"

[[criteria]]
id = "c1"
text = "` + criterion + `"

[[work_orders]]
id = "` + id + `-order"
title = "` + title + `"
size = "small"
+++

This work order tests the lane it runs in, and it has no other work. Change no file, and call no tool
these steps do not name. Read only the latest message you received, and follow the first of these
that applies to it. An earlier message no longer applies.

` + steps + `
`
}

const rs15E4Steps = `1. If a message says this lane parked: call the lane_report tool once, with outcome completed and reason set to the answer your question was given, as it was given. End your turn with the single word done.
2. If a message asks you to hand off: call the lane_handoff tool once, with done set to ["asked the question"], next_steps set to ["report the answer"], and carries_next_step set to false. End your turn with the single word waiting.
3. Otherwise call the ask tool once, with blocking set to true and this exact question: ` + rs15Question + `. End your turn with the single word waiting.`

// rs15ContextSteps carry a lane through a compaction that cannot pass, its handoff, and the next
// generation, with nothing typed and nothing answered. A search after the compaction is a call that
// returns at once, so a lane told to hand off between two calls and a lane told at rest both get there.
const rs15ContextSteps = `1. If you are in generation 2 or later of this lane, call the lane_report tool once, with outcome completed and reason set to the word generation, one space, and your generation's number. End your turn with the single word done.
2. If the message, or a tool's answer, tells you to hand off or to write a handoff, do what it says, and end your turn.
3. If the message says your context was compacted, call the decision_search tool once with the query rs15. End your turn with the single word waiting. Do not call lane_report in this step, whatever the message says.
4. Otherwise call the question_search tool once with the query rs15. End your turn with the single word waiting. Do not call lane_report here: a later step of this lane does that.`

// rs15ContextProfile lowers a lane's compaction threshold under anything a call can report, so the
// worker compacts the lane at its first rest, the next call fails that compaction, and the lane hands
// off, as RS10's headless lane did.
func rs15ContextProfile(mode string) string {
	return fmt.Sprintf(`run_mode = %q

[agent]
model = %q
permission_mode = "default"

[lifecycle]
ready_timeout = "60s"
ask_wait = "10m"
max_restarts = 0

[context.roles.writer]
compact_tokens = 1
handoff_tokens = 180000
`, mode, sessionModel)
}

// rs15KeyProfile names the key by reference, so the driver passes that one variable to this lane, and
// no ropeshift code reads its value.
const rs15KeyProfile = `run_mode = "headless"

[agent]
model = "` + sessionModel + `"
permission_mode = "default"

[agent.credentials]
kind = "api_key"
secret_ref = "env:` + envSessionKey + `"

[lifecycle]
ready_timeout = "60s"
max_restarts = 0
`

const rs15ReportSteps = `1. Call the lane_report tool once, with outcome completed. Read no file and run no command. Then end your turn with the single word done.`

// RS15's parts, by level: E5 has one in each run mode.
var (
	rs15Tops  = []string{"E1", "E4", "E5", "APIKey"}
	rs15Modes = []string{"headless", "tmux"}
)

// onePartNamed says a -run pattern selects exactly one part of RS15, read as go test reads it: an
// unbracketed bar separates alternatives, an unbracketed slash separates levels, and each level is a
// regular expression a name must match. An alternative that names no part, or a level that matches
// more than one, selects more than one part, and so does a level that does not compile.
func onePartNamed(pattern string) bool {
	parts := map[string]bool{}
	for _, alternative := range splitTop(pattern, '|') {
		levels := splitTop(alternative, '/')
		if len(levels) < 2 {
			return false
		}
		tops, ok := matching(levels[1], rs15Tops)
		if !ok || len(tops) != 1 {
			return false
		}
		part := tops[0]
		if part == "E5" {
			if len(levels) < 3 {
				return false
			}
			modes, ok := matching(levels[2], rs15Modes)
			if !ok || len(modes) != 1 {
				return false
			}
			part += "/" + modes[0]
		}
		parts[part] = true
	}
	return len(parts) == 1
}

// matching keeps the names a level selects, and says whether it compiles.
func matching(level string, names []string) ([]string, bool) {
	re, err := regexp.Compile(level)
	if err != nil {
		return nil, false
	}
	return slices.DeleteFunc(slices.Clone(names), func(name string) bool { return !re.MatchString(name) }), true
}

// splitTop splits a pattern at each separator outside brackets and parentheses, and past an escape.
func splitTop(pattern string, separator byte) []string {
	var parts []string
	brackets, parens, from := 0, 0, 0
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '\\':
			i++
		case c == '[':
			brackets++
		case c == ']':
			brackets = max(brackets-1, 0)
		case c == '(' && brackets == 0:
			parens++
		case c == ')' && brackets == 0:
			parens--
		case c == separator && brackets == 0 && parens == 0:
			parts = append(parts, pattern[from:i])
			from = i + 1
		}
	}
	return append(parts, pattern[from:])
}

// runPattern is this run's -run pattern.
func runPattern() string {
	if f := flag.Lookup("test.run"); f != nil {
		return f.Value.String()
	}
	return ""
}

// rs15Run allows one part per run, because each part opens its own state directory, and its probe and
// its lanes together are what the ceiling of one run holds.
type rs15Run struct{ first string }

func (r *rs15Run) claim(part string) error {
	if r.first != "" {
		return fmt.Errorf("refused: %s already ran, and RS15 runs one part per run, so name one:\n%s", r.first, rs15Parts)
	}
	r.first = part
	return nil
}

// rs15Open opens one part's run on its own scratch path and fresh state directory: the ceiling, the
// guard's probe, and the charges of the qualification probe and the part's sessions, all before any
// lane. A part that needs a qualified version stops before its first session on any other.
func rs15Open(t *testing.T, name, lane string, qualified bool, charges ...rs15Charge) *suite {
	t.Helper()
	s := start(t, name)
	s.watched = []string{lane}
	s.spend.sessionCeiling, s.spend.turnCeiling = rs15Sessions, rs15Turns
	t.Cleanup(s.logInits)
	t.Cleanup(s.killTerminal)
	s.reapGenerations(lane)
	if qualified {
		version := s.installedVersion()
		if !claudecode.Qualified(version) {
			t.Fatalf("STOP: version %s is not qualified by the matrix, so %s does not run on it. RS14 qualifies a version", version, t.Name())
		}
		s.logf("rs15: version=%s qualified_by_matrix=true", version)
	}
	s.build()
	s.probe()
	probeDir := filepath.Join(s.paths.StateDir, "probe")
	for _, dir := range []string{"accept", "refuse", "hold", "screens"} {
		s.others = append(s.others, filepath.Join(probeDir, dir))
	}
	for _, charge := range append(slices.Clone(rs15ProbeCharges), charges...) {
		if err := s.spend.charge(charge.session, charge.turns); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// rs15Repo commits a part's files to a scratch repository its daemon reads.
func (s *suite) rs15Repo(lane string, files map[string]string) {
	s.t.Helper()
	repo := gittest.NewIn(s.t, s.scratch)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		repo.Commit(name, files[name])
	}
	s.repo = repo.Dir
	s.worktree = filepath.Join(filepath.Dir(repo.Dir), filepath.Base(repo.Dir)+"-lanes", lane)
}

// rs15Override carries a part's flags to the production driver: its own to each headless session, the
// qualification probe's included, the guard flags of a pane to each pane, and a copy of every stream.
func (s *suite) rs15Override(args []string) map[string]any {
	s.t.Helper()
	if err := checkArguments(append(slices.Clone(args), paneGuardFlags...)); err != nil {
		s.t.Fatal(err)
	}
	return map[string]any{"args": args, "pane_args": paneGuardFlags, "env": paneEnv, "stream_log": s.stream}
}

// rs15Watch applies the stop conditions to all a part's run shows: the streams and the lanes' stderr,
// each session's init, and the probe's pane.
func (s *suite) rs15Watch(stop func(), screens *screenLog) func() {
	return func() {
		s.checkStream(stop)
		raw, err := os.ReadFile(s.stream)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			s.t.Fatal(err)
		}
		for _, failure := range s.initFailures(everyInit(raw)) {
			s.stopWith(stop, "an init: %s", failure)
		}
		s.watchPane(stop, rs15ProbePane, screens)
	}
}

// initFailures names what a run's inits fail to show: no user configuration in any session, and every
// session on the subscription, except that a keyed run's lane runs on its key.
func (s *suite) initFailures(inits []initLine) []string {
	var found []string
	for _, init := range inits {
		servers := []string{}
		if laneInit(init) {
			servers = []string{"ropeshift"}
		}
		if s.key.set() {
			// keyedInitFailures reads where each session's key came from.
			init.KeySource = subscriptionSource
		}
		if err := checkInit(init, servers, laneTools); err != nil {
			found = append(found, err.Error())
		}
	}
	if s.key.set() {
		found = append(found, keyedInitFailures(inits, s.standIn == "")...)
	}
	return found
}

// rs15Expect polls for one step of a part, and stops the run as soon as its work order settles where
// the step cannot happen, with the lane's outcome on the record.
func (s *suite) rs15Expect(d *daemon, id, what string, limit time.Duration, watch func(), done func() bool) {
	s.t.Helper()
	s.pollFor(limit, d.stop, what, func() bool {
		watch()
		if done() {
			return true
		}
		if state := s.workOrder(id).State; rs15Ended[state] {
			outcome, reason := s.outcomeOf(id)
			s.stopWith(d.stop, "%s did not happen: the work order settled %s, with %s %s", what, state, outcome, reason)
		}
		return false
	})
}

// rs15Worker is the one worker of a daemon, once it holds a session.
func (s *suite) rs15Worker(d *daemon) (string, int) {
	s.t.Helper()
	var workers api.WorkerList
	s.poll(d.stop, "the worker's session", func() bool {
		if err := json.Unmarshal(s.cli("worker", "list", "--json"), &workers); err != nil {
			s.t.Fatal(err)
		}
		return len(workers.Workers) >= 1 && slices.ContainsFunc(workers.Workers, func(w ledger.Worker) bool { return w.Epoch >= 1 })
	})
	for _, w := range workers.Workers {
		if w.Epoch >= 1 {
			return w.ID, len(workers.Workers)
		}
	}
	return "", len(workers.Workers)
}

// rs15Qualification reads what the worker's own probe found, from the ledger a stopped daemon left.
func (s *suite) rs15Qualification() lanes.Qualification {
	s.t.Helper()
	replayed, err := lanes.ReplayQualifications(s.ledgerEvents())
	if err != nil {
		s.t.Fatal(err)
	}
	var q lanes.Qualification
	for _, found := range replayed {
		q = found
	}
	s.logf("rs15: probe: results=%d version=%s source=%s sign_in=%s degraded=%v", len(replayed), q.Version, q.Source, q.SignIn, q.Degraded)
	if len(replayed) != 1 {
		s.t.Errorf("the ledger replays %d probe results", len(replayed))
	}
	return q
}

// requireProbed holds E4 and E5 to a version this run's own probe qualified on this machine.
func (s *suite) requireProbed() {
	s.t.Helper()
	if q := s.rs15Qualification(); q.Source != lanes.QualifiedByProbe || q.Degraded {
		s.t.Errorf("the probe left the version %s, degraded=%v", q.Source, q.Degraded)
	}
}

// logInits says how each session in the stream started, a stopped run's included: whether it is a
// lane's, its version, model, sign-in source, permission mode, the vendor's tools it was offered, and
// how many MCP tools. It runs in cleanup after the daemons stop and before the scratch path goes.
func (s *suite) logInits() {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		return
	}
	for _, init := range everyInit(raw) {
		var vendor []string
		mcp := 0
		for _, tool := range init.Tools {
			if strings.HasPrefix(tool, "mcp__") {
				mcp++
			} else {
				vendor = append(vendor, tool)
			}
		}
		s.logf("rs15: init: lane=%v version=%s model=%s apiKeySource=%s permission=%s vendor_tools=%v mcp_tools=%d",
			laneInit(init), init.Version, init.Model, init.KeySource, init.Permission, vendor, mcp)
	}
}

// laneSessionInits reads the init of every lane session in the stream, and counts the others.
func (s *suite) laneSessionInits() (lane []initLine, others int) {
	s.t.Helper()
	raw, err := os.ReadFile(s.stream)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.t.Fatal(err)
	}
	for _, init := range everyInit(raw) {
		if laneInit(init) {
			lane = append(lane, init)
		} else {
			others++
		}
	}
	return lane, others
}

// generationCounts sums one hook event of a lane by generation.
func generationCounts(records []lanes.HookRecord, event string) map[int64]int64 {
	counts := map[int64]int64{}
	for _, record := range records {
		if record.Event == event {
			counts[record.Generation] += record.Count
		}
	}
	return counts
}

// rs15InitResult is what one `ropeshift init` showed, by which lines it printed and never their text.
type rs15InitResult struct {
	exit   int
	lines  map[string]bool
	signIn string
	// inMatrix says init did not call the installed version unqualified.
	inMatrix bool
	output   string
}

// answerPrompts types each answer once its prompt shows, as a person does, so nothing typed arrives
// with echo still on. It returns when the terminal closes.
func answerPrompts(typed *os.File, answers ...string) {
	var screen bytes.Buffer
	buffer := make([]byte, 256)
	asked := 0
	for {
		n, err := typed.Read(buffer)
		screen.Write(buffer[:n])
		for asked < strings.Count(screen.String(), ": ") && asked < len(answers) {
			_, _ = typed.WriteString(answers[asked] + "\n")
			asked++
		}
		if err != nil {
			return
		}
	}
}

// rs15Init runs `ropeshift init` the way a new user does, before any daemon runs, on a terminal that
// types the passphrase at each prompt. Its own daemon runs for its own length.
func (s *suite) rs15Init() rs15InitResult {
	s.t.Helper()
	typed, held, err := ptypair.Open()
	if err != nil {
		s.t.Fatal(err)
	}
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		answerPrompts(typed, apitest.Passphrase, apitest.Passphrase)
	}()
	cmd := exec.Command(filepath.Join(s.binDir, "ropeshift"), "init")
	cmd.Env = s.environment(s.daemonTags()...)
	cmd.Stdin = strings.NewReader("")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.ExtraFiles = []*os.File{held}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: rs15TerminalFD}
	err = cmd.Start()
	_ = held.Close()
	if err != nil {
		_ = typed.Close()
		s.t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(rs15InitLimit):
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err = <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Signal(syscall.SIGKILL)
			err = <-done
		}
		_ = typed.Close()
		s.t.Fatalf("STOP: init passed its limit of %v, and it exited: %v", rs15InitLimit, err)
	}
	_ = typed.Close()
	<-answered
	result := rs15InitResult{lines: map[string]bool{}, output: stdout.String() + stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		result.exit = exit.ExitCode()
	default:
		s.t.Fatal(err)
	}
	for name, words := range rs15InitLines {
		result.lines[name] = strings.Contains(stdout.String(), words)
	}
	for _, sign := range rs15SignIns {
		if result.signIn == "" && strings.Contains(stdout.String(), sign.words) {
			result.signIn = sign.kind
		}
	}
	result.inMatrix = !strings.Contains(stdout.String(), "not in the qualification matrix")
	return result
}

// rs15FirstWorkOrder is E1 on the real CLI: `ropeshift init` on a new installation, the sample contract
// it writes committed to a repository, review, a claim, a lane, acceptance, and done.
func rs15FirstWorkOrder(t *testing.T) {
	s := rs15Open(t, "rs15a", rs15E1Lane, false, rs15Charge{"e1-lane", rs15E1Charge})
	init := s.rs15Init()
	_, health := s.tryCLI("health")
	var unreachable *exec.ExitError
	daemonLeft := !errors.As(health, &unreachable) || unreachable.ExitCode() != cli.ExitUnreachable
	var printed []string
	for _, name := range slices.Sorted(maps.Keys(init.lines)) {
		printed = append(printed, fmt.Sprintf("%s=%v", name, init.lines[name]))
	}
	s.logf("rs15: e1: init exit=%d %s sign_in=%s in_matrix=%v daemon_left=%v", init.exit, strings.Join(printed, " "), init.signIn, init.inMatrix, daemonLeft)
	for name, shown := range init.lines {
		if !shown {
			t.Errorf("init did not print its %s line", name)
		}
	}
	if init.exit != cli.ExitOK || daemonLeft || slices.Contains(slices.Collect(maps.Values(init.lines)), false) {
		s.logf("rs15: e1: init printed:\n%s", scrubbed(init.output, s.scratch, s.home))
		t.Fatalf("init exited %d, and a daemon is left: %v", init.exit, daemonLeft)
	}
	sample, err := os.ReadFile(filepath.Join(s.paths.StateDir, cli.SampleContractName))
	if err != nil {
		t.Fatalf("init wrote no sample contract: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.paths.StateDir, profiles.UserFile), []byte(rs15E1UserProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	s.rs15Repo(rs15E1Lane, map[string]string{
		".ropeshift/profile.toml": rs15E1Profile, "Makefile": rs15E1Makefile, cli.SampleContractName: string(sample),
	})
	s.playStandIn([]any{standInReport()}, nil)

	d := s.startDaemonWith(s.rs15Override(rs15E1Flags))
	t.Cleanup(d.stop)
	screens := &screenLog{}
	watch := s.rs15Watch(d.stop, screens)
	id := s.waitForWorkOrder(d.stop)
	var walk []workorders.State
	note := func() workorders.State {
		state := s.workOrder(id).State
		if len(walk) == 0 || walk[len(walk)-1] != state {
			walk = append(walk, state)
		}
		return state
	}
	note()
	s.cli("work", "review", id)
	s.poll(d.stop, "review to make the work order ready", func() bool { return note() == workorders.Ready })
	worker, workers := s.rs15Worker(d)
	s.cli("work", "claim", id, "--worker", worker)
	s.cli("lane", "start", id, "--lane", rs15E1Lane)
	s.rs15Expect(d, id, "the lane's outcome", rs15FirstWait, watch, func() bool { return note() == workorders.Acceptance })
	readme, err := os.Stat(filepath.Join(s.worktree, "README.md"))
	wrote := err == nil && readme.Size() > 0
	s.cli("work", "accept", id)
	s.poll(d.stop, "acceptance to finish the work order", func() bool { return note() == workorders.Done })
	watch()
	left := s.settleProcesses(rs15E1Lane)
	d.stop()

	s.rs15Qualification()
	byLane := false
	for _, event := range s.eventsOf(id) {
		var change workorders.Change
		if json.Unmarshal(event.Data, &change) == nil && change.Outcome != "" {
			byLane = event.Actor.Kind == ledger.ActorLane
		}
	}
	outcome, reason := s.outcomeOf(id)
	laneInits, others := s.laneSessionInits()
	walked := make([]string, 0, len(walk))
	for _, state := range walk {
		walked = append(walked, string(state))
	}
	generations := s.generationsOf(rs15E1Lane)
	s.logf("rs15: e1: walk=%s outcome=%s reason=%q by_lane=%v readme_written=%v workers=%d lane_sessions=%d other_sessions=%d generations=%d processes_left=%d screens=%v",
		strings.Join(walked, ","), outcome, reason, byLane, wrote, workers, len(laneInits), others, len(generations), left, screens.names())

	order := []string{string(workorders.ContractReview), string(workorders.Ready), string(workorders.Acceptance), string(workorders.Done)}
	if got := slices.DeleteFunc(slices.Clone(walked), func(state string) bool { return !slices.Contains(order, state) }); !slices.Equal(got, order) {
		t.Errorf("the work order went through %v, want %v in that order", walked, order)
	}
	if outcome != workorders.OutcomeCompleted || !byLane {
		t.Errorf("the lane's outcome is %s, reported by the lane: %v", outcome, byLane)
	}
	if workers != 1 {
		t.Errorf("%d workers, want the one init registered", workers)
	}
	if !wrote && s.standIn == "" {
		t.Error("the lane wrote no README.md")
	}
	if len(laneInits) != 1 || len(generations) != 1 {
		t.Errorf("%d lane sessions in %d generations, want one", len(laneInits), len(generations))
	}
	if left != 0 {
		t.Errorf("%d processes of the lane remain", left)
	}
}

// standInAnswered is a resumed session that reports with the answer it was given.
func standInAnswered() []any {
	return []any{
		map[string]any{"init": true},
		map[string]any{"mcp": lanes.ToolLaneReport, "arguments": map[string]any{
			"outcome": "completed", "reason": rs15Answer, "artifacts": []string{"none"}, "checks": []map[string]any{{"name": "stand-in", "passed": true}},
		}},
		standInResult(),
	}
}

// rs15Questions lists what the ledger holds of questions.
func (s *suite) rs15Questions() []questions.Question {
	s.t.Helper()
	var list api.QuestionList
	if err := json.Unmarshal(s.cli("question", "list", "--json"), &list); err != nil {
		s.t.Fatal(err)
	}
	return list.Questions
}

// rs15ParkOnAQuestion is E4's question half on the real CLI: a lane asks a blocking question, parks
// with its narrative, holds no process while it waits, and comes back as a new generation once a
// member answers, with the answer in what it reports.
func rs15ParkOnAQuestion(t *testing.T) {
	s := rs15Open(t, "rs15b", rs15E4Lane, true, rs15Charge{"e4-parked", rs15E4ParkedCharge}, rs15Charge{"e4-resumed", rs15E4ResumeCharge})
	s.rs15Repo(rs15E4Lane, map[string]string{
		".ropeshift/profile.toml": rs15E4Profile,
		"contract.md":             rs15Contract("rs15e4", "Park on a question", "The lane reports the answer it was given", rs15E4Steps),
	})
	s.playStandIn([]any{standInAsk(rs15Question), standInHandoff(false)}, []any{standInAnswered()})

	d := s.startDaemonWith(s.rs15Override(rs15ResumableFlags))
	t.Cleanup(d.stop)
	screens := &screenLog{}
	watch := s.rs15Watch(d.stop, screens)
	id := s.dispatch(d, rs15E4Lane)
	s.rs15Expect(d, id, "the lane to park", rs15FirstWait, watch, func() bool { return s.workOrder(id).State == workorders.Parked })
	waiting := s.settleProcesses(rs15E4Lane)
	asked := s.rs15Questions()
	parkedView := s.laneView(rs15E4Lane)
	if len(asked) != 1 {
		s.stopWith(d.stop, "the ledger holds %d questions, want the one the lane asked", len(asked))
	}
	s.cli("question", "answer", asked[0].ID, "--answer", rs15Answer, "--id", "d-rs15")
	s.rs15Expect(d, id, "the lane's outcome after the answer", turnLimit, watch, func() bool {
		return s.workOrder(id).State == workorders.Acceptance && s.laneView(rs15E4Lane).Generation == 2
	})
	answered := s.rs15Questions()
	watch()
	left := s.settleProcesses(rs15E4Lane)
	state := s.workOrder(id).State
	d.stop()

	s.requireProbed()
	parked := s.readParked(rs15E4Lane, 1)
	generations := s.generationsOf(rs15E4Lane)
	reached := strings.Contains(strings.ToLower(s.reportedReason(id)), rs15Answer)
	status := questions.Status("")
	if len(answered) == 1 {
		status = answered[0].Status
	}
	native, resumed := false, ""
	if len(generations) == 2 {
		native, resumed = generations[0].SessionID == generations[1].SessionID, string(generations[1].StartReason)
	}
	s.logf("rs15: e4: parked_lane=%s processes_while_parked=%d narrative=%v generation_1_end=%s question=%s generations=%d resumed_by=%s same_session=%v answer_reached=%v state=%s processes_left=%d",
		parkedView.ActualState, waiting, parked.handoff.Narrative != nil, parked.generation.EndReason, status, len(generations), resumed, native, reached, state, left)

	if waiting != 0 {
		t.Errorf("%d processes of the lane ran while it waited", waiting)
	}
	if parked.degraded || parked.handoff.Narrative == nil || parked.generation.EndReason != workorders.EndParked {
		t.Errorf("the first generation ended %s, with a narrative: %v", parked.generation.EndReason, parked.handoff.Narrative != nil)
	}
	for _, gap := range s.sequenceGaps(rs15E4Lane, 1) {
		t.Error(gap)
	}
	if status != questions.StatusAnswered {
		t.Errorf("the question is %s", status)
	}
	if len(generations) != 2 || generations[1].StartReason != ledger.StartResume {
		t.Fatalf("generations %d, the second started by %s", len(generations), resumed)
	}
	if !native {
		t.Error("the resume did not continue the session that parked")
	}
	if !reached {
		t.Error("the new generation did not report the answer")
	}
	if state != workorders.Acceptance || left != 0 {
		t.Errorf("the lane ended %s, with %d processes left", state, left)
	}
}

// standInPaneHandoff plays a pane whose compaction cannot pass, its handoff, and the next generation's
// report. The first session marks itself, so the next generation's pane reports instead.
func standInPaneHandoff() []any {
	status := func(tokens int) map[string]any {
		return map[string]any{"status": map[string]any{"context_window": map[string]any{"current_usage": map[string]any{"input_tokens": tokens}}}}
	}
	return []any{
		[]any{
			map[string]any{"init": true},
			map[string]any{"after_first": rs15Handoff, "then": []any{status(9000), standInGenerationReport()}},
			map[string]any{"first_time": rs15Handoff, "then": []any{
				status(12000), map[string]any{"mcp": lanes.ToolQuestionSearch, "arguments": map[string]any{"query": "rs15"}},
			}},
		},
		[]any{status(13000), map[string]any{"mcp": lanes.ToolDecisionSearch, "arguments": map[string]any{"query": "rs15"}}},
		standInHandoff(true),
	}
}

// rs15ContextResult is what one E5 lane showed.
type rs15ContextResult struct {
	state       workorders.State
	generations []ledger.Generation
	compactions int64
	reasonSays2 bool
	left        int
}

// rs15RunContext takes one lane through its first compaction, the handoff the failed compaction
// brings, and its next generation's report, with nothing typed and nothing answered.
func (s *suite) rs15RunContext(d *daemon, lane string, watch func()) rs15ContextResult {
	s.t.Helper()
	id := s.dispatch(d, lane)
	s.rs15Expect(d, id, "the lane's compaction", rs15FirstWait, watch, func() bool {
		return eventCount(s.hookRecords(lane), "PostCompact") != 0
	})
	s.rs15Expect(d, id, "the lane's handoff", turnLimit, watch, func() bool { return s.laneView(lane).Generation == 2 })
	s.rs15Expect(d, id, "the next generation's outcome", turnLimit, watch, func() bool {
		return s.workOrder(id).State == workorders.Acceptance
	})
	watch()
	result := rs15ContextResult{
		state: s.workOrder(id).State, compactions: generationCounts(s.hookRecords(lane), "PostCompact")[1],
		left: s.settleProcesses(lane),
	}
	d.stop()
	result.generations = s.generationsOf(lane)
	result.reasonSays2 = s.reportedReason(id) == "generation 2"
	return result
}

// checkContext holds an E5 lane to what the row asks: a compaction in its first generation, a handoff
// that began the second, and that generation's own report, with no process left.
func (s *suite) checkContext(mode string, r rs15ContextResult, extra string) {
	s.t.Helper()
	started, handedOff, fresh := "", false, false
	if len(r.generations) == 2 {
		started, handedOff = string(r.generations[1].StartReason), r.generations[0].HandoffOut != ""
		fresh = r.generations[1].SessionID != "" && r.generations[1].SessionID != r.generations[0].SessionID
	}
	s.logf("rs15: e5 %s: state=%s post_compact_g1=%d generations=%d second_started_by=%s handoff_out=%v new_session=%v reason_says_two=%v processes_left=%d%s",
		mode, r.state, r.compactions, len(r.generations), started, handedOff, fresh, r.reasonSays2, r.left, extra)
	if r.compactions == 0 {
		s.t.Errorf("%s: no compaction in the first generation", mode)
	}
	if len(r.generations) != 2 || r.generations[1].StartReason != ledger.StartHandoff || !handedOff {
		s.t.Errorf("%s: %d generations, the second started by %s, with a handoff: %v", mode, len(r.generations), started, handedOff)
	}
	// A dry run's scripted CLI has one session ID, so only a real run can show a new one.
	if !fresh && s.standIn == "" {
		s.t.Errorf("%s: the handoff did not start a new session", mode)
	}
	if !r.reasonSays2 {
		s.t.Errorf("%s: the next generation did not report its generation", mode)
	}
	if r.state != workorders.Acceptance || r.left != 0 {
		s.t.Errorf("%s: the lane ended %s, with %d processes left", mode, r.state, r.left)
	}
}

// rs15HeadlessContext is E5 on a headless lane.
func rs15HeadlessContext(t *testing.T) {
	s := rs15Open(t, "rs15c", rs15HeadlessLane, true, rs15Charge{"e5h-first", rs15HeadlessCharge}, rs15Charge{"e5h-next", rs15NextCharge})
	s.rs15Repo(rs15HeadlessLane, map[string]string{
		".ropeshift/profile.toml": rs15ContextProfile(lanes.ProfileRunHeadless),
		"contract.md":             rs15Contract("rs15e5h", "Hold a lane's context", "The lane reports the generation it is in", rs15ContextSteps),
	})
	s.playStandIn(standInHeadless(), nil)
	d := s.startDaemonWith(s.rs15Override(rs15Flags(rs15ContextMaxTurns)))
	t.Cleanup(d.stop)
	screens := &screenLog{}
	result := s.rs15RunContext(d, rs15HeadlessLane, s.rs15Watch(d.stop, screens))
	s.requireProbed()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		t.Fatal(err)
	}
	boundaries := bytes.Count(raw, []byte(`"compact_boundary"`))
	s.checkContext("headless", result, fmt.Sprintf(" compact_boundaries=%d", boundaries))
	if boundaries == 0 {
		t.Error("headless: no compaction boundary in the stream")
	}
}

// rs15PaneContext is E5 on a lane in a pane, where every step a person could type, the worker does.
func rs15PaneContext(t *testing.T) {
	s := rs15Open(t, "rs15d", rs15PaneLane, true, rs15Charge{"e5t-first", rs15PaneCharge}, rs15Charge{"e5t-next", rs15PaneNextCharge})
	s.rs15Repo(rs15PaneLane, map[string]string{
		".ropeshift/profile.toml": rs15ContextProfile(lanes.ProfileRunTmux),
		"contract.md":             rs15Contract("rs15e5t", "Hold a lane's context", "The lane reports the generation it is in", rs15ContextSteps),
	})
	s.playStandIn(standInPaneHandoff(), nil)
	d := s.startDaemonWith(s.rs15Override(rs15Flags(rs15ContextMaxTurns)))
	t.Cleanup(d.stop)
	screens := &screenLog{}
	watch := s.rs15Watch(d.stop, screens)
	paneWatch := func() {
		watch()
		s.watchPane(d.stop, rs15PaneLane+"-g1", screens)
		s.watchPane(d.stop, rs15PaneLane+"-g2", screens)
		if tools := eventCount(s.hookRecords(rs15PaneLane), "PreToolUse"); tools > rs15ToolLimit {
			s.stopWith(d.stop, "%d tool calls passed the limit of %d", tools, rs15ToolLimit)
		}
	}
	result := s.rs15RunContext(d, rs15PaneLane, paneWatch)
	s.requireProbed()
	s.checkContext("tmux", result, fmt.Sprintf(" screens=%v", screens.names()))
}

// rs15KeyLaneRun is one headless lane on an Anthropic API key. The key reaches only the daemon that
// starts this lane, by name, and the lane's profile names it, so the driver passes it to that one
// session. The qualification probe's sessions run on the subscription, with no key.
func rs15KeyLaneRun(t *testing.T, key secretKey) {
	s := rs15Open(t, "rs15e", rs15KeyLane, false, rs15Charge{"key-lane", rs15KeyCharge})
	s.key = key
	s.rs15Repo(rs15KeyLane, map[string]string{
		".ropeshift/profile.toml": rs15KeyProfile,
		"contract.md":             rs15Contract("rs15key", "Run on an API key", "The lane reports its outcome", rs15ReportSteps),
	})
	s.playStandIn([]any{standInReport()}, nil)
	env, err := keyedEnvironment(s.daemonEnvironment(s.rs15Override(rs15Flags(rs15KeyMaxTurns))), key)
	if err != nil {
		t.Fatal(err)
	}
	d := s.launchDaemon(env)
	t.Cleanup(d.stop)
	screens := &screenLog{}
	watch := s.rs15Watch(d.stop, screens)
	id := s.dispatch(d, rs15KeyLane)
	s.rs15Expect(d, id, "the lane's outcome", rs15FirstWait, watch, func() bool { return s.workOrder(id).State == workorders.Acceptance })
	watch()
	left := s.settleProcesses(rs15KeyLane)
	state := s.workOrder(id).State
	d.stop()

	s.rs15Qualification()
	laneInits, others := s.laneSessionInits()
	raw, err := os.ReadFile(s.stream)
	if err != nil {
		t.Fatal(err)
	}
	failures := keyedInitFailures(everyInit(raw), s.standIn == "")
	var sources []string
	for _, lane := range laneInits {
		sources = append(sources, lane.KeySource)
	}
	outcome, reason := s.outcomeOf(id)
	s.logf("rs15: key: state=%s outcome=%s reason=%q lane_sessions=%d lane_sources=%v other_sessions=%d init_failures=%d processes_left=%d",
		state, outcome, reason, len(laneInits), sources, others, len(failures), left)
	for _, failure := range failures {
		t.Error(failure)
	}
	if len(laneInits) != 1 {
		t.Errorf("%d lane sessions, want one", len(laneInits))
	}
	if state != workorders.Acceptance || outcome != workorders.OutcomeCompleted || left != 0 {
		t.Errorf("the lane ended %s with %s, and %d processes left", state, outcome, left)
	}
}

// TestRS15 is the exit suite on the real CLI: E1's first work order from `ropeshift init` to done, E4's
// park on a real question and resume after its answer, E5's unattended compaction, handoff, and next
// generation in each run mode, and one lane on an API key. Each part opens its own scratch path and
// state directory, so its first lane waits on the qualification probe as a new installation's does,
// and a run holds one part, which keeps it under the ceiling.
func TestRS15(t *testing.T) {
	if os.Getenv(envStart) != "1" {
		t.Skipf("real sessions need %s=1", envStart)
	}
	if !onePartNamed(runPattern()) {
		t.Fatalf("refused: RS15 runs one part per run, so name one:\n%s", rs15Parts)
	}
	var run rs15Run
	part := func(t *testing.T) {
		t.Helper()
		if err := run.claim(t.Name()); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("E1", func(t *testing.T) { part(t); rs15FirstWorkOrder(t) })
	t.Run("E4", func(t *testing.T) { part(t); rs15ParkOnAQuestion(t) })
	t.Run("E5", func(t *testing.T) {
		t.Run("headless", func(t *testing.T) { part(t); rs15HeadlessContext(t) })
		t.Run("tmux", func(t *testing.T) { part(t); rs15PaneContext(t) })
	})
	t.Run("APIKey", func(t *testing.T) {
		key := testKey(t)
		part(t)
		rs15KeyLaneRun(t, key)
	})
}

func TestRefusedRS15WithNoPartNamed(t *testing.T) {
	tests := []struct {
		pattern string
		named   bool
	}{
		{"^TestRS15/E1$", true},
		{"^TestRS15/E4$", true},
		{"^TestRS15/E5/headless$", true},
		{"^TestRS15/E5/tmux$", true},
		{"^TestRS15/APIKey$", true},
		{"^TestRS15/E1$|^TestRS15/E1$", true},
		{"^TestRS15/E1$|^TestRS15/E4$", false},
		{"^TestRS15$", false},
		{"", false},
		{"^TestRS15/$", false},
		{"^TestRS15/E$", false},
		{"^TestRS15/E", false},
		{"^TestRS15/E5$", false},
		{"^TestRS15/E5/$", false},
		{"^TestRS15/(E1$", false},
		{"^TestRS15[/]E1$", false},
		{"^Test(RS15/E1)$", false},
		{`^TestRS15\/E1$`, false},
		{"^TestRS15/E1$|^TestRS15$", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			if got := onePartNamed(tt.pattern); got != tt.named {
				t.Errorf("onePartNamed(%q) = %v", tt.pattern, got)
			}
		})
	}
}

func TestRefusedASecondRS15PartInOneRun(t *testing.T) {
	var run rs15Run
	if err := run.claim("TestRS15/E1"); err != nil {
		t.Fatal(err)
	}
	if err := run.claim("TestRS15/E4"); err == nil {
		t.Error("a second part ran in one run")
	}
}
