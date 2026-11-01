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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"ropeshift.dev/ropeshift/test/doubles"
)

const (
	// envStart is the explicit start. `make check` never sets it.
	envStart = "ROPESHIFT_REAL_SESSIONS"
	// envStandIn runs a suite against a scripted CLI, so a dry run proves the harness and
	// spends nothing.
	envStandIn = "ROPESHIFT_REAL_STANDIN"
	// The ceiling of one run.
	maxSessions = 3
	maxTurns    = 20
	// The model and the spend of one session.
	sessionModel = "haiku"
	budgetUSD    = "1"
)

// laneTools are the MCP tools a lane's init may list.
var laneTools = []string{"mcp__ropeshift__lane_report", "mcp__ropeshift__lane_wait"}

// standInFixtures say what each suite's dry run plays.
var standInFixtures = map[string]string{
	"rs1": "rs2-stand-in", "rs2": "rs2-stand-in", "rs3": "rs3-stand-in", "rs4": "rs4-stand-in",
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
}

func newSpendLedger() *spendLedger { return &spendLedger{sessions: map[string]bool{}} }

func (l *spendLedger) charge(session string, turns int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.sessions[session] && len(l.sessions) >= maxSessions {
		return fmt.Errorf("the session ceiling of %d is reached", maxSessions)
	}
	if l.turns+turns > maxTurns {
		return fmt.Errorf("the turn ceiling of %d is reached", maxTurns)
	}
	l.sessions[session] = true
	l.turns += turns
	return nil
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
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	RateLimit *struct {
		Status  string `json:"status"`
		Windows map[string]struct {
			Status string `json:"status"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

// stopSignals reads a stream and names every stop condition in it: a failed result, a
// usage limit signal, a retry of the model API, or an authentication event. A routine
// rate limit event with status allowed is not a signal.
func stopSignals(stream []byte) []string {
	var found []string
	for line := range bytes.SplitSeq(stream, []byte("\n")) {
		var event streamLine
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch {
		case event.Type == "result" && (event.IsError || event.Subtype != "success"):
			found = append(found, "failed result: "+event.Subtype)
		case event.Type == "auth_status":
			found = append(found, "authentication event")
		case event.Type == "system" && event.Subtype == "api_retry":
			found = append(found, "api retry")
		case event.Type == "rate_limit_event" && event.RateLimit != nil:
			if event.RateLimit.Status != "allowed" && event.RateLimit.Status != "" {
				found = append(found, "rate limit: "+event.RateLimit.Status)
			}
			for name, window := range event.RateLimit.Windows {
				if window.Status != "allowed" && window.Status != "" {
					found = append(found, "rate limit window: "+name+" "+window.Status)
				}
			}
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
		{"a warning", `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning"}}`, 1},
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

func TestHookEventsAreCounted(t *testing.T) {
	stream := []byte(`{"type":"system","subtype":"hook_started"}` + "\n" + `{"type":"system","subtype":"init"}`)
	if got := hookEvents(stream); got != 1 {
		t.Errorf("hook events %d", got)
	}
}
