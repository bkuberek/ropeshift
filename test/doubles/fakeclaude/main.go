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
	"net"
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
	// ResumeTurns replaces Turns for a session started with --resume.
	ResumeTurns [][]step `json:"resume_turns,omitempty"`
	// AuthMethod is what `claude auth status` reports. Empty reports an API key, so a lane
	// with no run mode in its profile stays headless.
	AuthMethod string `json:"auth_method,omitempty"`
	// Inbound stands for a stricter project value of `crossSessionInbound`, which beats what
	// `--settings` says.
	Inbound string `json:"inbound,omitempty"`
	// Lanes gives a lane a script of its own, for a test with two lanes on one binary.
	Lanes map[string]laneScript `json:"lanes,omitempty"`
	// Matrix names the qualification matrix's row this fixture stands for.
	Matrix string `json:"matrix,omitempty"`
	// UsageText is what `/usage` prints.
	UsageText string `json:"usage_text,omitempty"`
}

// usageCommand reads a sign-in's usage, which takes no tokens and starts no turn.
const usageCommand = "/usage"

// signedOutMark beside the binary says the sign-in expired, until a test removes it.
const signedOutMark = "signed-out"

// laneScript replaces a fixture's turns for one lane.
type laneScript struct {
	Turns       [][]step `json:"turns"`
	ResumeTurns [][]step `json:"resume_turns,omitempty"`
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
	// MCP calls one tool on the generated server. Arguments are the tool's input, and Refusable
	// reads a refusal as the real CLI does, as an answer the session goes on from.
	MCP       string         `json:"mcp,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Refusable bool           `json:"refusable,omitempty"`
	// KeepAnswer keeps what the call answered, for a test to read.
	KeepAnswer bool `json:"keep_answer,omitempty"`
	// Hold waits for a signal, as a turn in progress does.
	Hold bool `json:"hold,omitempty"`
	// Exit ends the process at once, with no result.
	Exit *int `json:"exit,omitempty"`
	// WaitFile holds the turn until this file appears beside the binary, so a test can
	// choose what a session is doing when a worker dies.
	WaitFile string `json:"wait_file,omitempty"`
	// SleepMs is a turn taking its time.
	SleepMs int `json:"sleep_ms,omitempty"`
	// ResetsInS says when `$resets_at` in this step's line falls, in seconds from now, because a
	// fixed time in a fixture would soon be in the past.
	ResetsInS int64 `json:"resets_in_s,omitempty"`
	// Witness appends Line to a log a test reads, with the time.
	Witness string `json:"witness,omitempty"`
	Line    string `json:"line,omitempty"`
	// FirstTime plays Then once, across every session of this binary, and marks that with a file.
	// AfterFirst plays Then for every session once that file is there.
	FirstTime  string `json:"first_time,omitempty"`
	AfterFirst string `json:"after_first,omitempty"`
	Then       []step `json:"then,omitempty"`
	// AwaitPeer reads one message between two tool calls, as a turn in progress does.
	AwaitPeer bool `json:"await_peer,omitempty"`
	// AwaitHold waits until the policy file holds tools ("held") or does not ("free"), so a fixture
	// can make its next tool call on one side of a worker's hold.
	AwaitHold string `json:"await_hold,omitempty"`
	// Status runs the status line once, with this payload merged in, as a reply does.
	Status map[string]any `json:"status,omitempty"`
	// Clear runs the native command: SessionEnd under the old session, and SessionStart under a new
	// one, in the same process. Compact runs a compaction with this trigger, in the middle of a turn.
	Clear   bool   `json:"clear,omitempty"`
	Compact string `json:"compact,omitempty"`
	// Tool calls a built-in tool: the PreToolUse hook decides, and a command that passes
	// runs through a shell, in the session's directory.
	Tool    string `json:"tool,omitempty"`
	Command string `json:"command,omitempty"`
	File    string `json:"file,omitempty"`
	// Prompts says the call would prompt, so PermissionRequest fires, and with no answer from it the
	// call is refused, as `-p` refuses one. AnsweredAt is a person's answer on the screen: when this
	// file appears the tool runs, with the hook still waiting behind the prompt.
	Prompts    bool   `json:"prompts,omitempty"`
	AnsweredAt string `json:"answered_at,omitempty"`
	// DeclinedAt is the same person saying no: the prompt goes, and no tool runs.
	DeclinedAt string `json:"declined_at,omitempty"`
	// OnScreen draws the prompt as a pane does, and takes a person's Enter on it.
	OnScreen bool `json:"on_screen,omitempty"`
	// Blocked stands for what stops a call after its hook: a deny rule, or a hook that timed
	// out with `onFailure: block`. This double has neither, so it runs the hook and stops.
	Blocked bool `json:"blocked,omitempty"`
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
	// pane means this session draws on a terminal and writes no stream, and keys is what it reads.
	pane bool
	keys <-chan string
	// socket is where this session reads messages, which only a session with a private directory
	// binds, as the driver's generated environment gives one.
	socket string
	// failedTurn says a turn ended on StopFailure, which has no Stop after it.
	failedTurn bool
	// clears counts the clears this process ran, which each begin a new session.
	clears int
	// deferred says a hook ended this turn on a call, so the turn's script stops there.
	deferred bool
	// peers is what the socket read, which a turn can take between its tool calls.
	peers chan string
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
	if len(os.Args) > 1 && os.Args[1] == "auth" {
		authStatus(script, os.Args[2:])
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
	if slices.Contains(os.Args[1:], usageCommand) {
		witness("usage", usageCommand)
		fmt.Print(script.UsageText)
		return
	}
	remember(self, os.Args[1:])
	if own, ok := script.Lanes[os.Getenv("ROPESHIFT_LANE_ID")]; ok {
		script.Turns, script.ResumeTurns = own.Turns, own.ResumeTurns
	}
	if opts.resume != "" && len(script.ResumeTurns) != 0 {
		script.Turns = script.ResumeTurns
	}
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

// authStatus answers `claude auth status`, and refuses every other form. It prints fields
// that name a person, as the real command does, so a test can show none of them travels.
func authStatus(script fixture, args []string) {
	if len(args) != 1 || args[0] != "status" {
		fail("refused: auth %v", args)
	}
	method := script.AuthMethod
	if method == "" {
		method = "api_key"
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(os.Args[0]), signedOutMark)); err == nil {
		method = "none"
	}
	answer, err := json.Marshal(map[string]any{
		"loggedIn": method != "none", "authMethod": method, "apiProvider": "firstParty",
		"email": "person@example.invalid", "orgId": "org-fixture", "orgName": "Fixture Organization",
		"subscriptionType": "fixture-plan", "configDirectory": "/fixture/config",
	})
	if err != nil {
		fail("%v", err)
	}
	fmt.Println(string(answer))
	if method == "none" {
		os.Exit(1)
	}
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

func flagValue(args []string, name string) string {
	if at := slices.Index(args, name); at >= 0 && at+1 < len(args) {
		return args[at+1]
	}
	return ""
}

// remember leaves what this process saw for a test to read.
func remember(self string, args []string) {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		env[name] = value
	}
	seen := map[string]any{"args": args, "env": env}
	if file := flagValue(args, "--append-system-prompt-file"); file != "" {
		text, err := os.ReadFile(file)
		if err != nil {
			fail("the system prompt file: %v", err)
		}
		seen["system_prompt"] = string(text)
	}
	raw, err := json.Marshal(seen)
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
	CrossSessionInbound string `json:"crossSessionInbound"`
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
	s.peers = make(chan string)
	if os.Getenv("CLAUDE_CODE_TMPDIR") != "" {
		s.socket = listen(s.peers)
		if err := os.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", s.socket); err != nil {
			fail("%v", err)
		}
	}
	s.hook("SessionStart", map[string]any{"source": "startup"})
	turn := 0
	next := func() {
		if turn < len(s.script.Turns) {
			s.play(s.script.Turns[turn], interrupts)
			turn++
		}
	}
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
			witness("prompts", strconv.Quote(messageText(line)))
			s.replay(line)
			if messageText(line) == "/clear" {
				s.clearOnStream()
				continue
			}
			if messageText(line) == "/compact" {
				s.compactOnStream()
				continue
			}
			next()
		case line := <-s.peers:
			if s.admits(line) {
				s.replayPeer(line)
				next()
			}
		}
	}
}

// compactOnStream is the command as a headless session runs it: its hooks, a boundary, and a result
// whose usage is zero, because the command itself tells nobody what context fell to.
func (s *session) compactOnStream() {
	s.compact()
	s.emit(map[string]any{"type": "system", "subtype": "compact_boundary", "compact_metadata": map[string]any{"trigger": "manual", "post_tokens": 1}})
	s.emit(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "terminal_reason": "completed", "num_turns": 1, "total_cost_usd": s.costUSD,
		"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
	})
}

// awaitHold waits for the file a hook reads to say one thing.
func awaitHold(want string) {
	path := os.Getenv("ROPESHIFT_POLICY")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err != nil {
			fail("the policy: %v", err)
		}
		var policy struct {
			Hold string `json:"hold"`
		}
		if json.Unmarshal(raw, &policy) != nil {
			fail("a hook would read this as no policy: %s", raw)
		}
		if (policy.Hold != "") == (want == "held") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	fail("the policy never read %s", want)
}

// admits is the inbound policy, as T6 observed it: a refused or held message reaches no hook and
// no model, and the only sign of one goes to a sender that named an address.
func (s *session) admits(line string) bool {
	policy := s.script.Inbound
	if policy == "" {
		policy = s.settings.CrossSessionInbound
	}
	switch policy {
	case "refuse":
		s.receipt(line, "expired", "refused")
		return false
	case "hold":
		s.emit(map[string]any{"type": "system", "subtype": "peer_message_hold", "state": "held", "message_uuid": messageID(line)})
		s.receipt(line, "held", "")
		return false
	}
	s.hook("UserPromptSubmit", map[string]any{"prompt": messageText(line)})
	return true
}

// replayPeer is the headless acknowledgement: the sender's `uuid`, with a peer's origin.
func (s *session) replayPeer(line string) {
	s.emit(map[string]any{
		"type": "user", "isReplay": true, "uuid": messageID(line), "origin": map[string]any{"kind": "peer"},
		"message": map[string]any{"role": "user", "content": messageText(line)},
	})
}

// receipt answers a reply address, which the receiver does only in its own socket directory.
func (s *session) receipt(line, status, detail string) {
	address, msgID := messageFrom(line)
	path, ok := strings.CutPrefix(address, "uds:")
	if !ok {
		return
	}
	socket := s.socket
	if socket == "" {
		socket = os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET")
	}
	if filepath.Dir(path) != filepath.Dir(socket) {
		return
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		return
	}
	defer conn.Close()
	raw, err := json.Marshal(map[string]any{
		"type": "control", "action": "peer_message_status", "status": status, "status_detail": detail, "orig_msg_id": msgID,
	})
	if err != nil {
		fail("%v", err)
	}
	if _, err := conn.Write(append(raw, '\n')); err != nil {
		return
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
	defer func() { s.deferred = false }()
	for _, st := range steps {
		if s.deferred {
			return
		}
		switch {
		case st.Init:
			s.emit(s.init())
		case st.Hook != "":
			s.hook(st.Hook, st.Input)
			s.failedTurn = s.failedTurn || st.Hook == "StopFailure"
		case st.Emit != nil:
			if cost, ok := st.Emit["total_cost_usd"].(float64); ok {
				s.costUSD = cost
			}
			s.emit(resetsAt(st.Emit, st.ResetsInS))
		case st.Raw != "":
			writeLine([]byte(st.Raw))
		case st.MCP != "":
			answer := s.callTool(st.MCP, st.Arguments, st.Refusable)
			if st.KeepAnswer {
				witness("tool", answer)
			}
		case st.Witness != "":
			witness(st.Witness, fmt.Sprintf("%s %d", st.Line, time.Now().UnixMilli()))
		case st.FirstTime != "":
			mark := filepath.Join(filepath.Dir(os.Args[0]), st.FirstTime)
			if _, err := os.Stat(mark); err != nil {
				if err := os.WriteFile(mark, nil, 0o600); err != nil {
					fail("%v", err)
				}
				s.play(st.Then, interrupts)
			}
		case st.AfterFirst != "":
			if _, err := os.Stat(filepath.Join(filepath.Dir(os.Args[0]), st.AfterFirst)); err == nil {
				s.play(st.Then, interrupts)
			}
		case st.AwaitPeer:
			if line := <-s.peers; s.admits(line) {
				s.replayPeer(line)
			}
		case st.AwaitHold != "":
			awaitHold(st.AwaitHold)
		case st.Clear:
			s.clear()
		case st.Compact != "":
			s.compactBy(st.Compact)
		case st.WaitFile != "":
			awaitFile(filepath.Join(filepath.Dir(os.Args[0]), st.WaitFile))
		case st.SleepMs != 0:
			time.Sleep(time.Duration(st.SleepMs) * time.Millisecond)
		case st.Status != nil:
			s.statusLine(resetsAt(st.Status, st.ResetsInS))
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

// resetsAt writes a real time into a line that says `$resets_at`.
func resetsAt(line map[string]any, seconds int64) map[string]any {
	at := time.Now().Unix() + seconds
	var stamp func(value any) any
	stamp = func(value any) any {
		switch typed := value.(type) {
		case string:
			if typed == "$resets_at" {
				return at
			}
		case map[string]any:
			out := make(map[string]any, len(typed))
			for key, item := range typed {
				out[key] = stamp(item)
			}
			return out
		}
		return value
	}
	return stamp(line).(map[string]any)
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
	line := map[string]any{
		"type": "system", "subtype": "init", "model": model, "apiKeySource": "none", "cwd": mustGetwd(),
		"tools": []string{}, "mcp_servers": s.servers(), "plugins": []string{},
		"permissionMode": s.opts.permissionMode, "claude_code_version": s.script.Version,
	}
	if s.socket != "" {
		line["messaging_socket_path"] = s.socket
	}
	return line
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
		line["session_id"] = s.currentSession()
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
	outputs := s.hook("PreToolUse", map[string]any{"tool_name": st.Tool, "tool_use_id": toolUseID(st), "tool_input": input})
	for _, output := range outputs {
		var decision struct {
			Specific struct {
				Decision string `json:"permissionDecision"`
				Reason   string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if json.Unmarshal(output, &decision) != nil {
			continue
		}
		switch decision.Specific.Decision {
		case "deny":
			witness("denied", decision.Specific.Reason)
			return
		case "defer":
			witness("deferred", st.Tool)
			s.deferred = true
			s.emit(map[string]any{
				"type": "result", "subtype": "success", "is_error": false, "stop_reason": "tool_deferred", "num_turns": 1,
				"total_cost_usd": s.costUSD, "deferred_tool_use": map[string]any{"id": "toolu_builtin", "name": st.Tool, "input": input},
			})
			return
		}
	}
	if st.Blocked {
		witness("blocked", st.Tool)
		return
	}
	if !s.permitted(st, input) {
		return
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
	s.hook("PostToolUse", map[string]any{"tool_name": st.Tool, "tool_use_id": toolUseID(st), "tool_input": input})
}

func toolUseID(st step) string {
	return "toolu_" + st.Tool + strconv.Itoa(len(st.Command)+len(st.File))
}

// permitted runs a call's permission flow. A hook that answers decides it. A session with a screen
// also takes a person's answer, which comes first if it comes first, and the hook goes on waiting
// behind it, as T5 observed.
func (s *session) permitted(st step, input map[string]any) bool {
	if !st.Prompts && st.AnsweredAt == "" && st.DeclinedAt == "" && !st.OnScreen {
		return true
	}
	payload := map[string]any{"tool_name": st.Tool, "tool_input": input}
	if st.OnScreen {
		go s.hook("PermissionRequest", payload)
		s.awaitEnter(st.Command)
		return true
	}
	if st.AnsweredAt != "" || st.DeclinedAt != "" {
		go s.hook("PermissionRequest", payload)
		if st.DeclinedAt != "" {
			awaitFile(filepath.Join(filepath.Dir(os.Args[0]), st.DeclinedAt))
			witness("denied", "a person")
			return false
		}
		awaitFile(filepath.Join(filepath.Dir(os.Args[0]), st.AnsweredAt))
		return true
	}
	for _, output := range s.hook("PermissionRequest", payload) {
		var answer struct {
			Specific struct {
				Decision struct {
					Behavior string `json:"behavior"`
					Message  string `json:"message"`
				} `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		if json.Unmarshal(output, &answer) != nil {
			continue
		}
		if answer.Specific.Decision.Behavior == "allow" {
			return true
		}
		witness("denied", answer.Specific.Decision.Message)
		return false
	}
	witness("denied", "no approver")
	return false
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
				if event == "SessionStart" {
					witness("grounding", string(bytes.TrimSpace(out)))
				}
				s.emit(map[string]any{"type": "system", "subtype": "hook_output", "hook": event, "output": string(out)})
			}
		}
	}
	return outputs
}

func (s *session) currentSession() string {
	base := sessionID
	if s.pane {
		base = s.paneSessionID()
	}
	if s.clears == 0 {
		return base
	}
	return base[:len(base)-1] + strconv.Itoa(s.clears)
}

// clear is the command as T2 observed it: one process, the old session's end, and a new session's
// start under the same tags.
func (s *session) clear() {
	s.hook("SessionEnd", map[string]any{"reason": "clear"})
	s.clears++
	s.hook("SessionStart", map[string]any{"source": "clear", "model": s.modelName()})
}

// clearOnStream is the command in stream-json input, which ends with a result of its own.
func (s *session) clearOnStream() {
	s.clear()
	s.emit(map[string]any{
		"type": "result", "subtype": "success", "is_error": false, "terminal_reason": "completed", "num_turns": 1, "total_cost_usd": s.costUSD,
		"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
	})
}

// compactBy runs a compaction's three hooks in the order T3 observed, whoever started it.
func (s *session) compactBy(trigger string) {
	s.hook("PreCompact", map[string]any{"trigger": trigger})
	s.hook("SessionStart", map[string]any{"source": "compact", "model": s.modelName()})
	s.hook("PostCompact", map[string]any{"trigger": trigger, "compact_summary": "a summary that must stay in the hook"})
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
func (s *session) callTool(name string, arguments map[string]any, refusable bool) string {
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
	s.hook("PreToolUse", map[string]any{"tool_name": "mcp__ropeshift__" + name, "tool_use_id": "toolu_" + name, "tool_input": arguments})
	s.emit(map[string]any{"type": "assistant", "message": map[string]any{"content": []map[string]any{
		{"type": "tool_use", "name": "mcp__ropeshift__" + name},
	}}})
	result := client.call("tools/call", map[string]any{"name": name, "arguments": arguments})
	isError, _ := result["isError"].(bool)
	if isError && !refusable {
		fail("%s was refused: %v", name, result["content"])
	}
	s.emit(map[string]any{"type": "user", "message": map[string]any{"content": []map[string]any{
		{"type": "tool_result", "is_error": isError},
	}}})
	s.hook("PostToolUse", map[string]any{"tool_name": "mcp__ropeshift__" + name, "tool_use_id": "toolu_" + name})
	return fmt.Sprint(result["content"])
}
