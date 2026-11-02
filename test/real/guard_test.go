//go:build realsession

// Package real holds the suites that start real agent sessions. Other sessions run on
// the same login, so everything here protects them: a suite compiles only with the
// realsession tag, refuses to run without an explicit start, charges every turn
// before it runs, and stops at the first sign of trouble with no retry.
package real

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/test/doubles"
)

const (
	// envStart is the explicit start. `make check` never sets it.
	envStart = "ROPESHIFT_REAL_SESSIONS"
	// envStandIn runs a suite against a scripted CLI, so a dry run proves the harness and
	// spends nothing.
	envStandIn = "ROPESHIFT_REAL_STANDIN"
	// envOnly runs one half of a suite that has two.
	envOnly = "ROPESHIFT_REAL_ONLY"
	// The ceiling of one run.
	maxSessions = 3
	maxTurns    = 20
	// The model and the spend of one session.
	sessionModel = "haiku"
	budgetUSD    = "1"
)

// laneTools are the MCP tools a lane's init may list.
var laneTools = []string{
	"mcp__ropeshift__lane_report", "mcp__ropeshift__lane_wait", "mcp__ropeshift__ask", "mcp__ropeshift__lane_handoff", "mcp__ropeshift__reply",
	"mcp__ropeshift__context_get", "mcp__ropeshift__decision_search", "mcp__ropeshift__requirement_get", "mcp__ropeshift__question_search",
}

// standInFixtures say what each suite's dry run plays.
var standInFixtures = map[string]string{
	"rs1": "rs2-stand-in", "rs2": "rs2-stand-in", "rs3": "rs3-stand-in", "rs4": "rs4-stand-in", "rs5": "rs5-stand-in",
	"rs6": "rs6-stand-in", "rs7": "rs7-stand-in", "rs8": "rs8-stand-in", "rs9": "rs9-stand-in",
	"rs10": "rs10-stand-in", "rs11": "rs11-stand-in",
}

// allowedEnvironment is everything a session or the daemon takes from this process, so
// no variable of a parent session can reach them. Tags are added by name.
var allowedEnvironment = []string{"HOME", "USER", "LOGNAME", "PATH", "TERM", "LANG"}

// refusedArguments never reach a session.
var refusedArguments = []string{
	"--bare", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
	"bypassPermissions", "auth", "setup-token", "respawn", "/login", "/logout",
}

// guardFlags are passed on every session, so no user hook, plugin, or MCP server loads
// and no transcript is left behind.
var guardFlags = []string{
	"--setting-sources", "project", "--strict-mcp-config", "--tools", "",
	"--max-budget-usd", budgetUSD, "--no-session-persistence",
}

// paneGuardFlags are the guard flags a pane takes. `--no-session-persistence` and
// `--max-budget-usd` work only with -p, and the first stops an interactive start, so a
// pane's transcript stays off the disk through paneEnv instead.
var paneGuardFlags = []string{"--setting-sources", "project", "--strict-mcp-config", "--tools", "Bash"}

// paneEnv keeps a pane's prompts and transcript out of the home directory. The vendor
// documents it for ephemeral scripted sessions.
var paneEnv = []string{"CLAUDE_CODE_SKIP_PROMPT_HISTORY=1"}

// spendLedger charges each turn before it runs and refuses past the ceiling.
type spendLedger struct {
	mu       sync.Mutex
	sessions map[string]bool
	turns    int
	// sessionCeiling and turnCeiling replace maxSessions and maxTurns for a suite that needs more
	// and has the room for it.
	sessionCeiling, turnCeiling int
}

func newSpendLedger() *spendLedger { return &spendLedger{sessions: map[string]bool{}} }

func (l *spendLedger) charge(session string, turns int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	sessions, ceiling := l.ceilings()
	if !l.sessions[session] && len(l.sessions) >= sessions {
		return fmt.Errorf("the session ceiling of %d is reached", sessions)
	}
	if l.turns+turns > ceiling {
		return fmt.Errorf("the turn ceiling of %d is reached", ceiling)
	}
	l.sessions[session] = true
	l.turns += turns
	return nil
}

// ceilings reads what this run may spend.
func (l *spendLedger) ceilings() (sessions, turns int) {
	sessions, turns = maxSessions, maxTurns
	if l.sessionCeiling != 0 {
		sessions = l.sessionCeiling
	}
	if l.turnCeiling != 0 {
		turns = l.turnCeiling
	}
	return sessions, turns
}

func (l *spendLedger) used() (sessions, turns int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.sessions), l.turns
}

// checkArguments refuses anything on the list, in any position.
func checkArguments(args []string) error {
	for _, arg := range args {
		if slices.Contains(refusedArguments, arg) || strings.Contains(arg, "bypassPermissions") {
			return fmt.Errorf("refused argument: %s", arg)
		}
	}
	return nil
}

// cleanEnvironment builds a process's whole environment from the allowlist and the
// suite's own tags, and refuses a tag a session could mistake for its own.
func cleanEnvironment(lookup func(string) (string, bool), tags ...string) ([]string, error) {
	out := make([]string, 0, len(allowedEnvironment)+len(tags))
	for _, name := range allowedEnvironment {
		if value, ok := lookup(name); ok {
			out = append(out, name+"="+value)
		}
	}
	for _, tag := range tags {
		if strings.HasPrefix(tag, "CLAUDE") || strings.HasPrefix(tag, "ANTHROPIC") {
			return nil, fmt.Errorf("refused variable: %s", strings.SplitN(tag, "=", 2)[0])
		}
		out = append(out, tag)
	}
	return out, nil
}

// withoutCanary removes the test layers' canary from a PATH, because this suite is the
// one layer that runs the real binary.
func withoutCanary(path string) string {
	var kept []string
	for dir := range strings.SplitSeq(path, string(os.PathListSeparator)) {
		if !strings.HasSuffix(dir, filepath.Join("test", "doubles", "canary")) {
			kept = append(kept, dir)
		}
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

// streamLine holds what a stop condition reads from one line of a session's stream.
type streamLine struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	IsError bool   `json:"is_error"`
	// TerminalReason says why a turn ended.
	TerminalReason string         `json:"terminal_reason"`
	RateLimit      *rateLimitInfo `json:"rate_limit_info"`
}

// limitWindow is one window's own figures.
type limitWindow struct {
	Status      string   `json:"status"`
	Utilization *float64 `json:"utilization"`
	ResetsAt    *int64   `json:"resetsAt"`
}

// rateLimitInfo is what one event says of a sign-in's limits. `rateLimitType` names the window the
// event's status is about, and `unifiedWindows` is every window it carries.
type rateLimitInfo struct {
	limitWindow
	Window  string                 `json:"rateLimitType"`
	Windows map[string]limitWindow `json:"unifiedWindows"`
}

const (
	limitAllowed = "allowed"
	limitWarning = "allowed_warning"
	// limitStopAt is the utilization that ends a run on any window, whatever its status says.
	limitStopAt = 0.90
)

// overTheLine names a figure at or past the line.
func overTheLine(name string, figure *float64) []string {
	if figure == nil || *figure < limitStopAt {
		return nil
	}
	return []string{fmt.Sprintf("rate limit window: %s at %v", name, *figure)}
}

// stops names what ends a run in one event. A warning alone does not, because a warning says only
// that some window is filling: it ends a run when it cannot say which, or how full. A status past a
// warning ends one, and so does any window at the line, whatever status came with it.
func (i rateLimitInfo) stops() []string {
	var found []string
	if i.Status != limitAllowed && i.Status != limitWarning {
		found = append(found, "rate limit: "+i.Status)
	}
	named := i.Window
	if named == "" {
		named = "the event's own window"
	}
	found = append(found, overTheLine(named, i.Utilization)...)
	figure := i.Utilization
	if window, ok := i.Windows[i.Window]; ok && figure == nil {
		figure = window.Utilization
	}
	if i.Status == limitWarning && i.Window == "" {
		found = append(found, "a limit warning that names no window")
	}
	if i.Status == limitWarning && figure == nil {
		found = append(found, "a limit warning with no utilization")
	}
	for _, name := range slices.Sorted(maps.Keys(i.Windows)) {
		window := i.Windows[name]
		found = append(found, overTheLine(name, window.Utilization)...)
		if window.Status != "" && window.Status != limitAllowed && window.Status != limitWarning {
			found = append(found, "rate limit window: "+name+" "+window.Status)
		}
		if window.Status == limitWarning && window.Utilization == nil {
			found = append(found, "a limit warning on "+name+" with no utilization")
		}
	}
	return found
}

// orNone reads a figure for the record, where an event that left one out must say so.
func orNone[T any](value *T) string {
	if value == nil {
		return "none"
	}
	return fmt.Sprint(*value)
}

// limitReadings says what each rate limit event in a stream reports, so a stop, and a warning a
// run goes on past, both leave the window and the figures on the record. It keeps those fields and
// no others.
func limitReadings(stream []byte) []string {
	var found []string
	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		var event streamLine
		if json.Unmarshal(line, &event) != nil || event.Type != "rate_limit_event" || event.RateLimit == nil {
			continue
		}
		info := event.RateLimit
		window := info.Window
		if window == "" {
			window = "none"
		}
		parts := []string{fmt.Sprintf("status=%s window=%s utilization=%s resets_at=%s", info.Status, window, orNone(info.Utilization), orNone(info.ResetsAt))}
		for _, name := range slices.Sorted(maps.Keys(info.Windows)) {
			w := info.Windows[name]
			parts = append(parts, fmt.Sprintf("%s status=%s utilization=%s resets_at=%s", name, w.Status, orNone(w.Utilization), orNone(w.ResetsAt)))
		}
		found = append(found, strings.Join(parts, "; "))
	}
	return found
}

// abortedStreaming is how a result reads when SIGINT ended its turn, which is how a worker stops
// a session it parks.
const abortedStreaming = "aborted_streaming"

// stopSignals reads a stream and names every stop condition in it: a failed result, a
// usage limit signal, a retry of the model API, or an authentication event. A rate limit
// event stops a run as rateLimitInfo.stops says, and a turn a stop ended is not a signal.
func stopSignals(stream []byte) []string {
	var found []string
	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		var event streamLine
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch {
		case event.Type == "result" && event.TerminalReason == abortedStreaming:
		case event.Type == "result" && (event.IsError || event.Subtype != "success"):
			found = append(found, "failed result: "+event.Subtype)
		case event.Type == "auth_status":
			found = append(found, "authentication event")
		case event.Type == "system" && event.Subtype == "api_retry":
			found = append(found, "api retry")
		case event.Type == "rate_limit_event" && event.RateLimit == nil:
			found = append(found, "a rate limit event with nothing in it")
		case event.Type == "rate_limit_event":
			found = append(found, event.RateLimit.stops()...)
		}
	}
	return found
}

var stopPatterns = regexp.MustCompile(`(?i)login expired|not logged in|please run /login|invalid api key|authentication|usage limit|rate limit`)

// stderrSignals names a stop condition in a session's stderr.
func stderrSignals(stderr []byte) []string {
	if stopPatterns.Match(stderr) {
		return []string{"a stop pattern on stderr"}
	}
	return nil
}

// initLine is a stream's system/init. It reads fields by name, so no prompt, reply, or
// account field is kept.
type initLine struct {
	Type       string   `json:"type"`
	Subtype    string   `json:"subtype"`
	Model      string   `json:"model"`
	KeySource  string   `json:"apiKeySource"`
	Version    string   `json:"claude_code_version"`
	Permission string   `json:"permissionMode"`
	Tools      []string `json:"tools"`
	Servers    []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Plugins []struct {
		Source string `json:"source"`
	} `json:"plugins"`
}

// firstInit returns a stream's first system/init.
func firstInit(stream []byte) (initLine, error) {
	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		var event initLine
		if json.Unmarshal(line, &event) == nil && event.Type == "system" && event.Subtype == "init" {
			return event, nil
		}
	}
	return initLine{}, fmt.Errorf("no system/init in the stream")
}

// checkInit stops a run unless init shows no user configuration: a subscription login,
// no plugin but a built-in one, exactly the MCP servers this suite asked for, and no
// MCP tool but its own. A built-in tool is not user configuration, and an accepted
// report already proves the suite's own tool worked.
func checkInit(init initLine, servers, tools []string) error {
	if init.KeySource != "none" {
		return fmt.Errorf("apiKeySource %q is not a subscription login", init.KeySource)
	}
	for _, plugin := range init.Plugins {
		if !strings.HasSuffix(plugin.Source, "@builtin") {
			return fmt.Errorf("a user plugin loaded: %s", plugin.Source)
		}
	}
	var names []string
	for _, server := range init.Servers {
		names = append(names, server.Name)
		if server.Status == "failed" {
			return fmt.Errorf("MCP server %s failed", server.Name)
		}
	}
	slices.Sort(names)
	want := slices.Sorted(slices.Values(servers))
	if !slices.Equal(names, want) {
		return fmt.Errorf("MCP servers %v, want %v", names, want)
	}
	for _, tool := range init.Tools {
		if strings.HasPrefix(tool, "mcp__") && !slices.Contains(tools, tool) {
			return fmt.Errorf("MCP tool %s, want only %v", tool, tools)
		}
	}
	return nil
}

// hookEvents counts hook events in a stream. A probe with no harness settings must
// report none, or a user hook is running.
func hookEvents(stream []byte) int {
	found := 0
	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		var event streamLine
		if json.Unmarshal(line, &event) == nil && event.Type == "system" && strings.HasPrefix(event.Subtype, "hook_") {
			found++
		}
	}
	return found
}

// projectName is the name the CLI derives from a working directory.
func projectName(dir string) string {
	var name strings.Builder
	for _, r := range dir {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			name.WriteRune(r)
		} else {
			name.WriteRune('-')
		}
	}
	return name.String()
}

// projectDirectory is where the CLI keeps a project's files.
func projectDirectory(home, dir string) string {
	return filepath.Join(home, ".claude", "projects", projectName(dir))
}

// removeEmptyProject takes away what the CLI makes for a session even with
// --no-session-persistence: an empty memory directory in a project directory. It runs
// only on a directory inside the suite's own scratch path, and rmdir cannot remove
// anything with content, so whatever else is there stays for the leftovers check.
func removeEmptyProject(home, scratch, dir string) error {
	if !filepath.IsAbs(scratch) || (dir != scratch && !strings.HasPrefix(dir, scratch+string(filepath.Separator))) {
		return fmt.Errorf("refused: %s is outside this suite's scratch path", dir)
	}
	project := projectDirectory(home, dir)
	if !strings.HasPrefix(filepath.Base(project), projectName(scratch)) {
		return fmt.Errorf("refused: %s does not name this suite's scratch path", filepath.Base(project))
	}
	for _, path := range []string{filepath.Join(project, "memory"), project} {
		switch err := syscall.Rmdir(path); {
		case err == nil, errors.Is(err, syscall.ENOENT):
		case errors.Is(err, syscall.ENOTEMPTY):
			return nil
		default:
			return fmt.Errorf("rmdir %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

// sessionName is the form a session's ID takes, so a name can never reach out of a directory.
var sessionName = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// removeTranscript takes away what a resumable session leaves in a scratch project: its
// transcript and its own directory. It removes only the named session of a project that encodes
// this suite's scratch path, so anything else stays for the leftovers check.
func removeTranscript(home, scratch, dir, session string) error {
	if !filepath.IsAbs(scratch) || (dir != scratch && !strings.HasPrefix(dir, scratch+string(filepath.Separator))) {
		return fmt.Errorf("refused: %s is outside this suite's scratch path", dir)
	}
	if !sessionName.MatchString(session) {
		return fmt.Errorf("refused: %q does not name a session", session)
	}
	project := projectDirectory(home, dir)
	if !strings.HasPrefix(filepath.Base(project), projectName(scratch)) {
		return fmt.Errorf("refused: %s does not name this suite's scratch path", filepath.Base(project))
	}
	if err := os.Remove(filepath.Join(project, session+".jsonl")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(filepath.Join(project, session))
}

// claudeOn finds the CLI on an environment's PATH, because a session runs from that
// environment and not from this process's.
func claudeOn(env []string) (string, error) {
	for _, entry := range env {
		path, ok := strings.CutPrefix(entry, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(path) {
			candidate := filepath.Join(dir, "claude")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate, nil
			}
		}
	}
	return "", errors.New("claude is not on the session's PATH")
}

// settingsDigest hashes the user-level settings file, which a run must not change.
func settingsDigest(home string) ([32]byte, error) {
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

// processesHolding lists the live processes whose command line names a directory.
func processesHolding(dir string) ([]string, error) {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	var found []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 1<<20), 1<<24)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), dir) {
			found = append(found, strings.TrimSpace(scanner.Text()))
		}
	}
	return found, scanner.Err()
}

// stillHeld keeps the recorded IDs a processesHolding listing names.
func stillHeld(listing []string, recorded []int) []int {
	var held []int
	for _, pid := range recorded {
		id := strconv.Itoa(pid)
		if slices.ContainsFunc(listing, func(line string) bool {
			first, _, _ := strings.Cut(line, " ")
			return first == id
		}) {
			held = append(held, pid)
		}
	}
	return held
}

func TestMain(m *testing.M) {
	os.Exit(doubles.Main(m))
}

func TestLedgerRefusesPastTheCeiling(t *testing.T) {
	l := newSpendLedger()
	if err := l.charge("a", maxTurns); err != nil {
		t.Fatal(err)
	}
	if err := l.charge("a", 1); err == nil {
		t.Error("a turn past the ceiling was charged")
	}
	second := newSpendLedger()
	for i := range maxSessions {
		if err := second.charge(fmt.Sprint(i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := second.charge("one more", 1); err == nil {
		t.Error("a session past the ceiling was charged")
	}
	second.sessionCeiling = maxSessions + 1
	if err := second.charge("one more", 1); err != nil {
		t.Errorf("a suite's own ceiling: %v", err)
	}
	if err := second.charge("one past that", 1); err == nil {
		t.Error("a session past a suite's own ceiling was charged")
	}
}

// TestRefusedSignInReadInThisBuild holds a real-session build to the rule that it runs no
// sign-in command in any form, not even one that only reads.
func TestRefusedSignInReadInThisBuild(t *testing.T) {
	if lanes.ReadsVendorStatus {
		t.Error("a real-session build would run the vendor's status command")
	}
}

func TestRefusedArguments(t *testing.T) {
	for _, arg := range refusedArguments {
		if err := checkArguments([]string{"-p", arg}); err == nil {
			t.Errorf("%s passed", arg)
		}
	}
	if err := checkArguments([]string{"--permission-mode", "bypassPermissions"}); err == nil {
		t.Error("a bypass mode passed")
	}
	if err := checkArguments(guardFlags); err != nil {
		t.Errorf("the guard flags were refused: %v", err)
	}
}

func TestEnvironmentIsTheAllowlist(t *testing.T) {
	parent := map[string]string{
		"HOME": "/h", "PATH": "/p", "CLAUDECODE": "1", "CLAUDE_CODE_MESSAGING_SOCKET": "/s", "CLAUDE_CODE_SESSION_ID": "x",
		"ANTHROPIC_API_KEY": "k", "OTHER": "v",
	}
	got, err := cleanEnvironment(func(name string) (string, bool) { value, ok := parent[name]; return value, ok }, "ROPESHIFT_X=1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"HOME=/h", "PATH=/p", "ROPESHIFT_X=1"}; !slices.Equal(got, want) {
		t.Errorf("environment %v", got)
	}
	if _, err := cleanEnvironment(func(string) (string, bool) { return "", false }, "CLAUDE_CONFIG_DIR=/x"); err == nil {
		t.Error("a CLAUDE variable passed")
	}
}

func TestCanaryLeavesThePath(t *testing.T) {
	path := strings.Join([]string{"/repo/test/doubles/canary", "/usr/bin"}, string(os.PathListSeparator))
	if got := withoutCanary(path); got != "/usr/bin" {
		t.Errorf("PATH %q", got)
	}
}

// A stand-in must win over any `claude` later on the PATH, so a dry run cannot reach the
// real binary.
func TestCanaryAndClaudeResolveFromTheSessionEnvironment(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	for _, dir := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dir, "claude"), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := claudeOn([]string{"HOME=/h", "PATH=" + first + string(os.PathListSeparator) + second})
	if err != nil || got != filepath.Join(first, "claude") {
		t.Errorf("claude at %s: %v", got, err)
	}
	if _, err := claudeOn([]string{"PATH=" + t.TempDir()}); err == nil {
		t.Error("an empty PATH found a claude")
	}
}

func TestStopConditions(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{"a finished turn", `{"type":"result","subtype":"success","is_error":false}`, 0},
		{"a routine rate limit event", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"seven_day":{"status":"allowed"}}}}`, 0},
		{"a failed result", `{"type":"result","subtype":"error_max_budget_usd","is_error":true}`, 1},
		{"a turn the worker's own stop ended", `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"aborted_streaming"}`, 0},
		{"a turn that failed on its own", `{"type":"result","subtype":"error_during_execution","is_error":true,"terminal_reason":"max_turns"}`, 1},
		{"a window that is not allowed", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","unifiedWindows":{"seven_day":{"status":"rejected"}}}}`, 1},
		{"a retry", `{"type":"system","subtype":"api_retry"}`, 1},
		{"an authentication event", `{"type":"auth_status"}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stopSignals([]byte(tt.line)); len(got) != tt.want {
				t.Errorf("signals %v", got)
			}
		})
	}
	if len(stderrSignals([]byte("Login expired"))) != 1 || len(stderrSignals([]byte("an ordinary warning"))) != 0 {
		t.Error("stderr patterns")
	}
}

// TestStopOnALimitReading holds a warning to the figures it came with: a run goes on past one only
// when it can say which window warned and how full that window is, and that no window is close.
func TestStopOnALimitReading(t *testing.T) {
	tests := []struct {
		name string
		info string
		stop bool
	}{
		{"a warning well short of the limit", `{"status":"allowed_warning","rateLimitType":"five_hour","utilization":0.31,"resetsAt":1800000000}`, false},
		{"a warning with its figure on the window it names", `{"status":"allowed_warning","rateLimitType":"seven_day","unifiedWindows":{"seven_day":{"status":"allowed","utilization":0.31}}}`, false},
		{"a warning just short of the line", `{"status":"allowed_warning","rateLimitType":"seven_day","utilization":0.8999}`, false},
		{"a warning from one window", `{"status":"allowed","unifiedWindows":{"seven_day_opus":{"status":"allowed_warning","utilization":0.5}}}`, false},
		{"an allowed event with no figures", `{"status":"allowed"}`, false},
		{"a warning on the line", `{"status":"allowed_warning","rateLimitType":"seven_day","utilization":0.90}`, true},
		{"a warning under a window past the line", `{"status":"allowed_warning","rateLimitType":"seven_day","utilization":0.2,"unifiedWindows":{"five_hour":{"status":"allowed","utilization":0.9}}}`, true},
		{"an allowed event past the line", `{"status":"allowed","utilization":0.95}`, true},
		{"an allowed window past the line", `{"status":"allowed","unifiedWindows":{"five_hour":{"status":"allowed","utilization":0.9}}}`, true},
		{"a rejection with low figures", `{"status":"rejected","rateLimitType":"seven_day","utilization":0.1}`, true},
		{"a rejected window under a warning", `{"status":"allowed_warning","rateLimitType":"seven_day","utilization":0.1,"unifiedWindows":{"five_hour":{"status":"rejected","utilization":0.1}}}`, true},
		{"a status nobody named", `{"status":"throttled","rateLimitType":"seven_day","utilization":0.1}`, true},
		{"no status at all", `{"rateLimitType":"seven_day","utilization":0.1}`, true},
		{"a warning that names no window", `{"status":"allowed_warning","utilization":0.31}`, true},
		{"a warning with no figure", `{"status":"allowed_warning","rateLimitType":"seven_day"}`, true},
		{"a warning whose figure belongs to another window", `{"status":"allowed_warning","rateLimitType":"seven_day","unifiedWindows":{"five_hour":{"status":"allowed","utilization":0.2}}}`, true},
		{"a warning from one window with no figure", `{"status":"allowed","unifiedWindows":{"seven_day_opus":{"status":"allowed_warning"}}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stopSignals([]byte(`{"type":"rate_limit_event","rate_limit_info":` + tt.info + `}`))
			if (len(got) != 0) != tt.stop {
				t.Errorf("signals %v, want a stop: %v", got, tt.stop)
			}
		})
	}
	t.Run("an event that says nothing", func(t *testing.T) {
		if got := stopSignals([]byte(`{"type":"rate_limit_event"}`)); len(got) == 0 {
			t.Error("a run went on past an event it could not read")
		}
	})
}

// TestStopReadingsNameTheirWindow holds a run's record to what a person needs after a warning: the
// window, the figure, and the reset, on every event, and an empty answer for an empty one.
func TestStopReadingsNameTheirWindow(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","rateLimitType":"five_hour","utilization":0.31,"resetsAt":1800000000,` +
			`"unifiedWindows":{"seven_day":{"status":"allowed","utilization":0.29,"resetsAt":1800000900},"five_hour":{"status":"allowed_warning","utilization":0.31,"resetsAt":1800000000}}}}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
	}, "\n")
	want := []string{
		"status=allowed_warning window=five_hour utilization=0.31 resets_at=1800000000" +
			"; five_hour status=allowed_warning utilization=0.31 resets_at=1800000000" +
			"; seven_day status=allowed utilization=0.29 resets_at=1800000900",
		"status=allowed window=none utilization=none resets_at=none",
	}
	if got := limitReadings([]byte(stream)); !slices.Equal(got, want) {
		t.Errorf("readings\n%q\nwant\n%q", got, want)
	}
}

func TestInitCheckStopsOnUserConfiguration(t *testing.T) {
	base := func() initLine {
		var out initLine
		if err := json.Unmarshal([]byte(`{"apiKeySource":"none","tools":["mcp__ropeshift__lane_report"],
"mcp_servers":[{"name":"ropeshift","status":"connected"}],"plugins":[{"source":"x@builtin"}]}`), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	servers, tools := []string{"ropeshift"}, []string{"mcp__ropeshift__lane_report"}
	if err := checkInit(base(), servers, tools); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*initLine){
		"an API key":       func(i *initLine) { i.KeySource = "ANTHROPIC_API_KEY" },
		"a user plugin":    func(i *initLine) { i.Plugins[0].Source = "x@market" },
		"another server":   func(i *initLine) { i.Servers = append(i.Servers, i.Servers[0]); i.Servers[1].Name = "user" },
		"a failed server":  func(i *initLine) { i.Servers[0].Status = "failed" },
		"another MCP tool": func(i *initLine) { i.Tools = append(i.Tools, "mcp__user__tool") },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			line := base()
			change(&line)
			if err := checkInit(line, servers, tools); err == nil {
				t.Error("passed")
			}
		})
	}
}

func TestLeftoverProjectRemoval(t *testing.T) {
	const scratch = "/tmp/rs2-scratch"
	inside := filepath.Join(scratch, "repository")
	tests := []struct {
		name string
		dir  string
		// files are created in the project directory, and must all survive.
		files   []string
		refused bool
		gone    bool
	}{
		{name: "the empty memory directory the CLI leaves", dir: inside, gone: true},
		{name: "the scratch path itself", dir: scratch, gone: true},
		{name: "a note in memory", dir: inside, files: []string{"memory/note.md"}},
		{name: "a transcript beside memory", dir: inside, files: []string{"session.jsonl"}},
		{name: "a directory outside the scratch path", dir: "/tmp/other/repository", refused: true},
		{name: "a sibling sharing the prefix", dir: "/tmp/rs2-scratch-other", refused: true},
		{name: "no directory at all", dir: "", refused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			project := projectDirectory(home, tt.dir)
			if err := os.MkdirAll(filepath.Join(project, "memory"), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, file := range tt.files {
				if err := os.WriteFile(filepath.Join(project, file), []byte("kept"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := removeEmptyProject(home, scratch, tt.dir)
			if refused := err != nil; refused != tt.refused {
				t.Fatalf("error %v, want refused=%v", err, tt.refused)
			}
			_, statErr := os.Stat(project)
			if gone := errors.Is(statErr, os.ErrNotExist); gone != tt.gone {
				t.Fatalf("project gone=%v, want %v", gone, tt.gone)
			}
			for _, file := range tt.files {
				if _, err := os.Stat(filepath.Join(project, file)); err != nil {
					t.Errorf("%s did not survive: %v", file, err)
				}
			}
			if tt.refused {
				if _, err := os.Stat(filepath.Join(project, "memory")); err != nil {
					t.Errorf("a refused call touched the project: %v", err)
				}
			}
		})
	}
	t.Run("an absent project is not an error", func(t *testing.T) {
		if err := removeEmptyProject(t.TempDir(), scratch, inside); err != nil {
			t.Error(err)
		}
	})
}

func TestLeftoverTranscriptRemoval(t *testing.T) {
	const scratch = "/tmp/rs7-scratch"
	const session = "00000000-0000-4000-8000-000000000000"
	inside := filepath.Join(scratch, "repository")
	setup := func(t *testing.T, dir string) (string, string) {
		home := t.TempDir()
		project := projectDirectory(home, dir)
		if err := os.MkdirAll(filepath.Join(project, session, "subagents"), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{session + ".jsonl", "another.jsonl"} {
			if err := os.WriteFile(filepath.Join(project, name), []byte("kept"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return home, project
	}
	t.Run("the named session goes and nothing else", func(t *testing.T) {
		home, project := setup(t, inside)
		if err := removeTranscript(home, scratch, inside, session); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(project, session+".jsonl")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the transcript remains: %v", err)
		}
		if _, err := os.Stat(filepath.Join(project, session)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("its directory remains: %v", err)
		}
		if _, err := os.Stat(filepath.Join(project, "another.jsonl")); err != nil {
			t.Errorf("another session went: %v", err)
		}
	})
	refusals := []struct {
		name    string
		dir     string
		session string
	}{
		{"a directory outside the scratch path", "/tmp/other/repository", session},
		{"a sibling sharing the prefix", "/tmp/rs7-scratch-other", session},
		{"a name that climbs out", inside, "../../settings"},
		{"an empty name", inside, ""},
		{"a name of no session", inside, "another"},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			home, project := setup(t, tt.dir)
			if err := removeTranscript(home, scratch, tt.dir, tt.session); err == nil {
				t.Error("it was not refused")
			}
			if _, err := os.Stat(filepath.Join(project, session+".jsonl")); err != nil {
				t.Errorf("a refused call touched the project: %v", err)
			}
		})
	}
}

// A recorded ID outlives its process, so only a listing that still names it may be signaled.
func TestLeftoverIDsAreSignaledOnlyWhileHeld(t *testing.T) {
	listing := []string{"101 /tmp/rs4x/bin/ropeshift lane-shim", "1012 /tmp/rs4x/claude -p"}
	tests := []struct {
		name     string
		recorded []int
		want     []int
	}{
		{name: "a session and its shim", recorded: []int{1012, 101}, want: []int{1012, 101}},
		{name: "an ID that starts like a listed one", recorded: []int{10}},
		{name: "an ID this suite's processes do not hold", recorded: []int{999}},
		{name: "an ID that was never set", recorded: []int{0}},
		{name: "one of two ended", recorded: []int{999, 101}, want: []int{101}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stillHeld(listing, tt.recorded); !slices.Equal(got, tt.want) {
				t.Errorf("held %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRedactKeepsShapeAndNoWord(t *testing.T) {
	line := "❯ Reply from person@example.com in /tmp/rs3abc 12"
	got := redact(line)
	if strings.ContainsAny(got, "@") || strings.Contains(got, "person") || strings.Contains(got, "12") {
		t.Errorf("redact kept %q", got)
	}
	if !strings.HasPrefix(got, "❯ ") {
		t.Errorf("redact lost the shape: %q", got)
	}
	if kept := redact("   Yes, I trust this folder"); kept != "   Yes, I trust this folder" {
		t.Errorf("a known word went: %q", kept)
	}
}

// TestHookRecordFindsACallNeverMade holds RS4's strongest claim to something that can fail:
// a call the record does not hold is reported, however quiet its effect was.
func TestHookRecordFindsACallNeverMade(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	lines := `{"tool_name":"Bash","tool_input":{"command":"git push --force origin HEAD:refs/heads/a"}}
{"tool_name":"mcp__ropeshift__lane_report","tool_input":{"outcome":"completed"}}
not a line of JSON
{"tool_name":"Bash","tool_input":{"command":"cd here && echo inside > inside.txt"}}
`
	if err := os.WriteFile(record, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	calls, err := attempts(record)
	if err != nil || len(calls) != 2 {
		t.Fatalf("calls %v: %v", calls, err)
	}
	got := missing(calls, []string{"git push --force origin HEAD:refs/heads/a", "echo inside > inside.txt", "echo slow > slow.txt"})
	if !slices.Equal(got, []string{"echo slow > slow.txt"}) {
		t.Errorf("missing %v", got)
	}
}

// TestHookStandInHoldsOnlyACommand keeps RS4's markers on the calls they name. A session may
// write a marker in its report, and a hook that held that call would stop the lane reporting.
func TestHookStandInHoldsOnlyACommand(t *testing.T) {
	dir := t.TempDir()
	reached := filepath.Join(dir, "reached")
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\ncat > /dev/null\n: > \""+reached+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(dir, "stand-in")
	if err := os.WriteFile(script, []byte(slowHook(real, filepath.Join(dir, "record"), "0")), 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		input   string
		reaches bool
	}{
		{name: "a command with the slow marker", input: `{"tool_name":"Bash","tool_input":{"command":"echo slow > rs4-slow.txt"}}`},
		{name: "a command with the rule marker", input: `{"tool_name":"Bash","tool_input":{"command":"echo rule > rs4-rule.txt"}}`},
		{name: "a spaced command with the slow marker", input: `{"tool_name": "Bash", "tool_input": {"command": "echo slow > rs4-slow.txt"}}`},
		{name: "a plain command", input: `{"tool_name":"Bash","tool_input":{"command":"echo pane > pane-inside.txt"}}`, reaches: true},
		{name: "a report naming the slow marker", input: `{"tool_name":"mcp__ropeshift__lane_report","tool_input":{"reason":"rs4-slow.txt was refused"}}`, reaches: true},
		{name: "a report naming the rule marker", input: `{"tool_name":"mcp__ropeshift__lane_report","tool_input":{"artifacts":["rs4-rule.txt"]}}`, reaches: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.Remove(reached); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			cmd := exec.Command(script, "hook", "PreToolUse")
			cmd.Stdin = strings.NewReader(tt.input)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if got := exists4(reached); got != tt.reaches {
				t.Errorf("reached the real hook: %v, want %v", got, tt.reaches)
			}
		})
	}
}

func TestHookRecordCountsEveryToolAndNamesEveryStep(t *testing.T) {
	record := filepath.Join(t.TempDir(), "record")
	lines := `{"tool_name":"Bash","tool_input":{"command":"echo one"}}
{"tool_name":"Bash","tool_input":{"command":"echo two"}}
{"tool_name":"mcp__ropeshift__lane_report","tool_input":{"outcome":"completed"}}
not a line of JSON
`
	if err := os.WriteFile(record, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	called := toolsCalled(record)
	if called["Bash"] != 2 || called["mcp__ropeshift__lane_report"] != 1 || len(called) != 2 {
		t.Errorf("called %v", called)
	}
	if got := toolsCalled(filepath.Join(t.TempDir(), "absent")); got != nil {
		t.Errorf("an absent record counted %v", got)
	}
	commands := []string{"cd here && echo one", "echo two"}
	got := unattempted(commands, map[string]string{"first": "echo one", "second": "echo two", "third": "echo three"})
	if !slices.Equal(got, []string{"third"}) {
		t.Errorf("unattempted %v", got)
	}
}

func TestHookReasonsKeepACodeAndHideAReply(t *testing.T) {
	tests := []struct{ name, reason, want string }{
		{name: "no reason", reason: "", want: ""},
		{name: "a worker's code", reason: "no_outcome", want: "no_outcome"},
		{name: "a session's words", reason: "step four was refused at /tmp/rs4x", want: "the session's own"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := reasonClass(tt.reason); got != tt.want {
				t.Errorf("class %q", got)
			}
		})
	}
}

func TestHookRefusalsAreCountedByClass(t *testing.T) {
	stream := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"ropeshift policy: a force push is denied"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"Permission to edit this path has been denied."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"ok"}]}}
{"type":"user","message":{"content":"a plain prompt"}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"exit 1"}]}}
`)
	got := refusals(stream)
	if got["hook"] != 1 || got["rule"] != 1 || got["other"] != 1 {
		t.Errorf("refusals %v", got)
	}
}

func TestInitOfAHeadlessLaneOffersNoQuestionTool(t *testing.T) {
	const lane = "rs5-headless"
	tests := []struct {
		name   string
		tools  []string
		asked  questions.Question
		found  bool
		failed int
	}{
		{name: "the lane reached ask, and no tool was offered", tools: []string{"mcp__ropeshift__ask"}, asked: questions.Question{AskedBy: lane}, found: true},
		{name: "the question tool was offered", tools: []string{lanes.ToolQuestion}, asked: questions.Question{AskedBy: lane}, found: true, failed: 1},
		{name: "the lane never reached ask", failed: 1},
		{name: "another lane asked", asked: questions.Question{AskedBy: "another"}, found: true, failed: 1},
		{name: "offered, and never asked", tools: []string{lanes.ToolQuestion}, failed: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headlessAskFailures(initLine{Tools: tt.tools}, tt.asked, tt.found, lane); len(got) != tt.failed {
				t.Errorf("failures %v, want %d", got, tt.failed)
			}
		})
	}
}

func TestHookEventsAreCounted(t *testing.T) {
	stream := []byte(`{"type":"system","subtype":"hook_started"}` + "\n" + `{"type":"system","subtype":"init"}`)
	if got := hookEvents(stream); got != 1 {
		t.Errorf("hook events %d", got)
	}
}
