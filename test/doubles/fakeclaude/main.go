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
}

// A session's flags, as the real CLI takes them.
type options struct {
	print, version bool
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
	if !opts.print {
		fail("a lane runs with -p")
	}
	s := &session{opts: opts, script: script}
	if opts.settings != "" {
		if s.settings, err = readSettings(opts.settings); err != nil {
			fail("settings: %v", err)
		}
	}
	s.run()
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
		case "-p", "--print":
			o.print = true
		case "--version":
			o.version = true
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

// hook runs an event's commands through a shell, as the CLI does, with the session's
// whole environment and the payload on stdin.
func (s *session) hook(event string, input map[string]any) {
	payload := map[string]any{"session_id": sessionID, "hook_event_name": event, "cwd": mustGetwd()}
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
				s.emit(map[string]any{"type": "system", "subtype": "hook_output", "hook": event, "output": string(out)})
			}
		}
	}
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
		cmd.Env = os.Environ()
		for name, value := range server.Env {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
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
