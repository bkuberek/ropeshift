// Command fakeclaude stands for the agent CLI in every layer but the real one. It
// reads the flags the real driver passes, speaks stream-json from a fixture, runs the
// generated hook commands, and calls the generated MCP server, so the real driver,
// hooks, and MCP server run with no session. It holds no credential and reaches no
// network.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// fixture is one scripted session. Source names the matrix row or RS suite it comes
// from, so a real run can correct it.
type fixture struct {
	Source  string   `json:"source"`
	Version string   `json:"version"`
	Model   string   `json:"model"`
	Turns   [][]step `json:"turns"`
	// Agents is what `claude agents --json` prints, and AgentsExit how it ends.
	Agents     []map[string]any `json:"agents,omitempty"`
	AgentsExit int              `json:"agents_exit,omitempty"`
	// Pane is what a session does with no -p, in a terminal.
	Pane paneScript `json:"pane,omitempty"`
}

// step is one thing the session does. Exactly one field is set.
type step struct {
	// Init emits system/init.
	Init bool `json:"init,omitempty"`
	// Hook runs the generated commands of one event, with Input merged into the
	// payload.
	Hook  string         `json:"hook,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	// Emit writes one stream line, with this session's ID added.
	Emit map[string]any `json:"emit,omitempty"`
	// Raw writes one line as it stands.
	Raw string `json:"raw,omitempty"`
	// MCP calls one tool on the generated server. Arguments are the tool's input.
	MCP       string         `json:"mcp,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	// Hold waits for a signal, as a turn in progress does.
	Hold bool `json:"hold,omitempty"`
	// Exit ends the process at once, with no result.
	Exit *int `json:"exit,omitempty"`
	// WaitFile holds the turn until this file appears beside the binary, so a test can
	// choose what a session is doing when a worker dies.
	WaitFile string `json:"wait_file,omitempty"`
	// SleepMs is a turn taking its time.
	SleepMs int `json:"sleep_ms,omitempty"`
	// Status runs the status line once, with this payload merged in, as a reply does.
	Status map[string]any `json:"status,omitempty"`
	// Tool calls a built-in tool: the PreToolUse hook decides, and a command that passes
	// runs through a shell, in the session's directory.
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
	File    string `json:"file,omitempty"`
}

// A session's flags, as the real CLI takes them.
type options struct {
	print, version bool
	inputFormat    string
	settings       string
	mcpConfig      string
	model          string
	resume         string
	permissionMode string
}

const sessionID = "00000000-0000-4000-8000-000000000000"

var refused = []string{"--bare", "--dangerously-skip-permissions", "bypassPermissions", "auth", "setup-token", "respawn"}

type session struct {
	opts     options
	script   fixture
	settings settings
	server   *mcpClient
	costUSD  float64
	// pane means this session draws on a terminal and writes no stream.
	pane bool
}

func main() {
	self := os.Args[0]
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(self), "fixture.json"))
	if err != nil {
		fail("no fixture: %v", err)
	}
	var script fixture
	if err := json.Unmarshal(raw, &script); err != nil {
		fail("bad fixture: %v", err)
	}
	if len(os.Args) > 1 && os.Args[1] == "agents" {
		listAgents(self, script)
		return
	}
	opts := parse(os.Args[1:])
	if opts.version {
		version := script.Version
		if version == "" {
			version = "2.1.316"
		}
		fmt.Printf("%s (Claude Code)\n", version)
		return
	}
	remember(self, os.Args[1:])
	s := &session{opts: opts, script: script}
	if opts.settings != "" {
		if s.settings, err = readSettings(opts.settings); err != nil {
			fail("settings: %v", err)
		}
	}
	if !opts.print {
		s.runPane()
		return
	}
	s.run()
}

// listAgents answers `claude agents --json --cwd <dir>`, which starts no session. It
// names only the directory it was given, because the driver must never list another.
func listAgents(self string, script fixture) {
	remember(self, os.Args[1:])
	if script.AgentsExit != 0 {
		os.Exit(script.AgentsExit)
	}
	raw, err := json.Marshal(script.Agents)
	if err != nil {
		fail("%v", err)
	}
	if script.Agents == nil {
		raw = []byte("[]")
	}
	writeLine(raw)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fakeclaude: "+format+"\n", args...)
	os.Exit(1)
}

func parse(args []string) options {
	var o options
	for i := 0; i < len(args); i++ {
		value := func() string {
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		if slices.Contains(refused, args[i]) {
			fail("refused argument %s", args[i])
		}
		switch args[i] {
		case "--tui":
		case "-p", "--print":
			o.print = true
		case "--version":
			o.version = true
		case "--input-format":
			o.inputFormat = value()
		case "--settings":
			o.settings = value()
		case "--mcp-config":
			o.mcpConfig = value()
		case "--model":
			o.model = value()
		case "--resume":
			o.resume = value()
		case "--permission-mode":
			o.permissionMode = value()
		}
	}
	if slices.Contains([]string{"bypassPermissions"}, o.permissionMode) {
		fail("refused permission mode")
	}
	return o
}

// remember leaves what this process saw for a test to read.
func remember(self string, args []string) {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	raw, err := json.Marshal(map[string]any{"args": args, "env": env})
	if err != nil {
		fail("%v", err)
	}
	dir := filepath.Join(filepath.Dir(self), "seen")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail("%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())+".json"), raw, 0o600); err != nil {
		fail("%v", err)
	}
}

// settings is the part of a generated settings file a session reads.
type settings struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Type      string `json:"type"`
			Command   string `json:"command"`
			Timeout   int    `json:"timeout"`
			OnFailure string `json:"onFailure"`
		} `json:"hooks"`
	} `json:"hooks"`
	Permissions struct {
		Allow []string `json:"allow"`
	} `json:"permissions"`
	StatusLine struct {
		Command string `json:"command"`
	} `json:"statusLine"`
}

func readSettings(path string) (settings, error) {
	var out settings
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

func (s *session) run() {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, syscall.SIGINT)
	if s.opts.inputFormat != "stream-json" {
		s.onePrompt(interrupts)
		return
	}
	lines := make(chan string)
	go readInput(lines)
	s.hook("SessionStart", map[string]any{"source": "startup"})
	turn := 0
	for {
		select {
		case <-interrupts:
			s.end()
			return
		case line, open := <-lines:
			if !open {
				s.end()
				return
			}
			s.replay(line)
			if turn >= len(s.script.Turns) {
				continue
			}
			s.play(s.script.Turns[turn], interrupts)
			turn++
		}
	}
}

// onePrompt is a run with no --input-format: stdin is one plain prompt and one turn, and
// the process ends with it, as the real CLI does.
func (s *session) onePrompt(interrupts <-chan os.Signal) {
	if _, err := io.ReadAll(os.Stdin); err != nil {
		fail("%v", err)
	}
	if len(s.script.Turns) != 0 {
		s.play(s.script.Turns[0], interrupts)
	}
	s.end()
}

// readInput validates each line as the real CLI does: one that does not parse ends
// the session with exit 1 and no result.
func readInput(lines chan<- string) {
	defer close(lines)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<16), 1<<24)
	for scanner.Scan() {
		if !json.Valid(scanner.Bytes()) {
			fail("a stream-json line did not parse")
		}
		lines <- scanner.Text()
	}
}

func (s *session) replay(line string) {
	var message map[string]any
	if json.Unmarshal([]byte(line), &message) != nil {
		return
	}
	if message["uuid"] != nil {
		message["isReplay"] = true
		s.emit(message)
	}
}

func (s *session) end() {
	s.hook("SessionEnd", map[string]any{"reason": "other"})
	if s.server != nil {
		s.server.close()
	}
	os.Exit(0)
}

func (s *session) play(steps []step, interrupts <-chan os.Signal) {
	for _, st := range steps {
		switch {
		case st.Init:
			s.emit(s.init())
		case st.Hook != "":
			s.hook(st.Hook, st.Input)
		case st.Emit != nil:
			if cost, ok := st.Emit["total_cost_usd"].(float64); ok {
				s.costUSD = cost
			}
			s.emit(st.Emit)
		case st.Raw != "":
			writeLine([]byte(st.Raw))
		case st.MCP != "":
			s.callTool(st.MCP, st.Arguments)
		case st.WaitFile != "":
			awaitFile(filepath.Join(filepath.Dir(os.Args[0]), st.WaitFile))
		case st.SleepMs != 0:
			time.Sleep(time.Duration(st.SleepMs) * time.Millisecond)
		case st.Status != nil:
			s.statusLine(st.Status)
		case st.Tool != "":
			s.callBuiltin(st)
		case st.Hold:
			<-interrupts
			s.emit(map[string]any{
				"type": "result", "subtype": "error_during_execution", "is_error": true,
				"terminal_reason": "aborted_streaming", "total_cost_usd": s.costUSD,
			})
			s.end()
		case st.Exit != nil:
			os.Exit(*st.Exit)
		}
	}
}

func awaitFile(path string) {
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *session) init() map[string]any {
	model := s.script.Model
	if model == "" {
		model = "claude-fixture-5-5"
	}
	return map[string]any{
		"type": "system", "subtype": "init", "model": model, "apiKeySource": "none", "cwd": mustGetwd(),
		"tools": []string{}, "mcp_servers": s.servers(), "plugins": []string{},
		"permissionMode": s.opts.permissionMode, "claude_code_version": s.script.Version,
	}
}

func mustGetwd() string {
	dir, err := os.Getwd()
	if err != nil {
		fail("%v", err)
	}
	return dir
}

func (s *session) servers() []map[string]string {
	config, err := readMCPConfig(s.opts.mcpConfig)
	if err != nil {
		return nil
	}
	out := []map[string]string{}
	for name := range config.Servers {
		out = append(out, map[string]string{"name": name, "status": "connected"})
	}
	return out
}

func (s *session) emit(line map[string]any) {
	if s.pane {
		return
	}
	if line["session_id"] == nil {
		line["session_id"] = sessionID
	}
	raw, err := json.Marshal(line)
	if err != nil {
		fail("%v", err)
	}
	writeLine(raw)
}

func writeLine(raw []byte) {
	if _, err := os.Stdout.Write(append(raw, '\n')); err != nil {
		os.Exit(1)
	}
}

// callBuiltin runs one built-in tool the way the CLI does: a denial from the hook stops it
// and reaches the model, and a call that passes takes effect.
func (s *session) callBuiltin(st step) {
	input := map[string]any{}
	if st.Command != "" {
		input["command"] = st.Command
	}
	if st.File != "" {
		input["file_path"] = st.File
	}
	outputs := s.hook("PreToolUse", map[string]any{"tool_name": st.Tool, "tool_use_id": "toolu_builtin", "tool_input": input})
	for _, output := range outputs {
		var decision struct {
			Specific struct {
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if json.Unmarshal(output, &decision) == nil && decision.Specific.Decision == "deny" {
			witness("denied", decision.Specific.Reason)
			return
		}
	}
	if st.Command != "" {
		cmd := exec.Command("/bin/sh", "-c", st.Command)
		if out, err := cmd.CombinedOutput(); err != nil {
			fail("tool: %v: %s", err, out)
		}
	}
	if st.File != "" {
		if err := os.WriteFile(st.File, nil, 0o600); err != nil {
			fail("tool: %v", err)
		}
	}
	witness("ran", st.Tool)
	s.hook("PostToolUse", map[string]any{"tool_name": st.Tool, "tool_use_id": "toolu_builtin"})
}

// hook runs an event's commands through a shell, as the CLI does, with the session's
// whole environment and the payload on stdin, and returns what they printed.
func (s *session) hook(event string, input map[string]any) [][]byte {
	var outputs [][]byte
	payload := map[string]any{"session_id": s.currentSession(), "hook_event_name": event, "cwd": mustGetwd()}
	for key, value := range input {
		payload[key] = value
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		fail("%v", err)
	}
	for _, group := range s.settings.Hooks[event] {
		for _, h := range group.Hooks {
			cmd := exec.Command("/bin/sh", "-c", h.Command)
			cmd.Stdin = bytes.NewReader(raw)
			cmd.Stderr = os.Stderr
			out, err := cmd.Output()
			if err != nil {
				fail("hook %s failed: %v", event, err)
			}
			if len(bytes.TrimSpace(out)) != 0 {
				outputs = append(outputs, out)
				s.emit(map[string]any{"type": "system", "subtype": "hook_output", "hook": event, "output": string(out)})
			}
		}
	}
	return outputs
}

func (s *session) currentSession() string {
	if s.pane {
		return s.paneSessionID()
	}
	return sessionID
}

// mcpConfig is a generated MCP configuration.
type mcpConfig struct {
	Servers map[string]struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	} `json:"mcpServers"`
}

func readMCPConfig(path string) (mcpConfig, error) {
	var out mcpConfig
	if path == "" {
		return out, fmt.Errorf("no configuration")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(raw, &out)
}

// serverBaseline is all a stdio server inherits here. This is the CLI with
// CLAUDE_CODE_MCP_ALLOWLIST_ENV set, the strictest thing the vendor documents, so a
// server that relied on inheriting the session's environment fails on this double.
var serverBaseline = []string{"HOME", "PATH"}

// serverEnvironment is a stdio server's whole environment.
func serverEnvironment(configured map[string]string) []string {
	var env []string
	for _, name := range serverBaseline {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	for name, value := range configured {
		env = append(env, name+"="+expandReference(value))
	}
	return env
}

var reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-[^}]*)?\}`)

// expandReference follows the vendor's rule: ${VAR}, or ${VAR:-default} when VAR is
// unset, and a reference with no value and no default stays as written.
func expandReference(value string) string {
	return reference.ReplaceAllStringFunc(value, func(match string) string {
		parts := reference.FindStringSubmatch(match)
		if found, ok := os.LookupEnv(parts[1]); ok {
			return found
		}
		if fallback, ok := strings.CutPrefix(parts[2], ":-"); ok {
			return fallback
		}
		return match
	})
}

// mcpClient speaks newline-delimited JSON-RPC to one stdio server.
type mcpClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	nextID int
}

func (s *session) connect() *mcpClient {
	if s.server != nil {
		return s.server
	}
	config, err := readMCPConfig(s.opts.mcpConfig)
	if err != nil {
		fail("mcp: %v", err)
	}
	for _, server := range config.Servers {
		cmd := exec.Command(server.Command, server.Args...)
		cmd.Env = serverEnvironment(server.Env)
		cmd.Stderr = os.Stderr
		stdin, err := cmd.StdinPipe()
		if err != nil {
			fail("mcp: %v", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			fail("mcp: %v", err)
		}
		if err := cmd.Start(); err != nil {
			fail("mcp: %v", err)
		}
		s.server = &mcpClient{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
		s.server.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "fakeclaude", "version": "0"}})
		s.server.notify("notifications/initialized")
		return s.server
	}
	fail("mcp: no server")
	return nil
}

func (c *mcpClient) notify(method string) {
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		fail("%v", err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		fail("mcp: %v", err)
	}
}

func (c *mcpClient) call(method string, params map[string]any) map[string]any {
	c.nextID++
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	if err != nil {
		fail("%v", err)
	}
	if _, err := c.stdin.Write(append(raw, '\n')); err != nil {
		fail("mcp: %v", err)
	}
	line, err := c.stdout.ReadBytes('\n')
	if err != nil {
		fail("mcp: no answer to %s: %v", method, err)
	}
	var reply struct {
		Result map[string]any `json:"result"`
		Error  map[string]any `json:"error"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		fail("mcp: %v", err)
	}
	if reply.Error != nil {
		fail("mcp: %s: %v", method, reply.Error)
	}
	return reply.Result
}

func (c *mcpClient) close() {
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
	}
}

// callTool refuses a call the settings do not allow, because a headless session has
// nobody to answer a prompt, and that is what the allow rule exists to prevent.
func (s *session) callTool(name string, arguments map[string]any) {
	if !slices.Contains(s.settings.Permissions.Allow, "mcp__ropeshift__"+name) {
		fail("%s would wait on a permission prompt", name)
	}
	client := s.connect()
	listed := client.call("tools/list", map[string]any{})
	tools, _ := listed["tools"].([]any)
	found := false
	for _, tool := range tools {
		if entry, ok := tool.(map[string]any); ok && entry["name"] == name {
			found = true
		}
	}
	if !found {
		fail("the server does not list %s", name)
	}
	s.hook("PreToolUse", map[string]any{"tool_name": "mcp__ropeshift__" + name, "tool_use_id": "toolu_fixture", "tool_input": arguments})
	result := client.call("tools/call", map[string]any{"name": name, "arguments": arguments})
	if isError, _ := result["isError"].(bool); isError {
		fail("%s was refused: %v", name, result["content"])
	}
	s.hook("PostToolUse", map[string]any{"tool_name": "mcp__ropeshift__" + name, "tool_use_id": "toolu_fixture"})
}
