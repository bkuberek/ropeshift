// Package hook is `ropeshift hook <event>`, which an agent CLI runs synchronously. A
// slow or failing hook would block or break the agent, so it always exits 0, does no
// work beyond one short request, and spools what the worker could not take. It reads
// only `ROPESHIFT_*`, because nothing else reaches every hook.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
)

const (
	// maxInput caps what a hook reads from stdin.
	maxInput = 4 << 20
	// dialDeadline and ackDeadline keep the worst case under NFR-PERF-2.
	dialDeadline = 15 * time.Millisecond
	ackDeadline  = 25 * time.Millisecond
	// hardDeadline ends a hook that passed everything else, below the 100 ms limit.
	hardDeadline = 90 * time.Millisecond
)

const (
	eventPreToolUse   = "PreToolUse"
	eventSessionStart = "SessionStart"
)

const unavailablePolicy = "the policy file cannot be read"

// config is one invocation, from its argument and its environment.
type config struct {
	event      string
	socket     string
	session    string
	spool      string
	token      string
	policy     string
	lane       string
	workOrder  string
	generation int64
}

func read(event string, getenv func(string) string) config {
	generation, _ := strconv.ParseInt(getenv(api.EnvGeneration), 10, 64)
	return config{
		event: event, socket: getenv(api.EnvWorkerSocket), spool: getenv(api.EnvSpool), token: getenv(api.EnvLaneToken),
		policy: getenv(api.EnvPolicy), lane: getenv(api.EnvLaneID), workOrder: getenv(api.EnvWorkOrder),
		session: getenv(lanes.EnvMessagingSocket), generation: generation,
	}
}

// Main runs one hook. It has no failure path: whatever happens, the process exits 0.
func Main(args []string, stdin io.Reader, stdout io.Writer, getenv func(string) string) int {
	defer func() { _ = recover() }()
	if len(args) == 0 {
		return 0
	}
	cfg := read(args[0], getenv)
	timer := time.AfterFunc(hardDeadline, func() { expire(cfg, stdout) })
	defer timer.Stop()
	run(cfg, stdin, stdout)
	return 0
}

// expire ends a hook that passed its deadline. A policy hook fails closed.
func expire(cfg config, stdout io.Writer) {
	if cfg.event == eventPreToolUse && cfg.policy != "" {
		deny(stdout, "the policy check ran out of time")
	}
	os.Exit(0)
}

func run(cfg config, stdin io.Reader, stdout io.Writer) {
	input, _ := io.ReadAll(io.LimitReader(stdin, maxInput))
	event := lanes.NewHookEvent(cfg.event, input)
	event.Lane, event.Generation, event.AtMs = cfg.lane, cfg.generation, time.Now().UnixMilli()
	switch cfg.event {
	case eventPreToolUse:
		if !decide(cfg, input, stdout) {
			event.Asked = nil
		}
	case eventSessionStart:
		event.Socket = cfg.session
		reground(cfg, event, stdout)
	}
	forward(cfg, event)
}

// decide comes first, so a denial never depends on the worker. It reports whether it
// deferred the agent's question tool, which is the one case that forwards tool input.
func decide(cfg config, input []byte, stdout io.Writer) bool {
	if cfg.policy == "" {
		return false
	}
	raw, err := os.ReadFile(cfg.policy)
	if err != nil {
		deny(stdout, unavailablePolicy)
		return false
	}
	var policy lanes.DenyPolicy
	if json.Unmarshal(raw, &policy) != nil {
		deny(stdout, unavailablePolicy)
		return false
	}
	var call struct {
		Tool  string `json:"tool_name"`
		Cwd   string `json:"cwd"`
		Input struct {
			Command  string `json:"command"`
			File     string `json:"file_path"`
			Notebook string `json:"notebook_path"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(input, &call) != nil {
		deny(stdout, unavailablePolicy)
		return false
	}
	if call.Tool == lanes.ToolQuestion {
		switch policy.QuestionTool {
		case lanes.QuestionToolDefer:
			write(stdout, map[string]string{"hookEventName": eventPreToolUse, "permissionDecision": "defer"})
			return true
		case lanes.QuestionToolDeny:
			deny(stdout, lanes.ReasonUseAsk)
			return false
		}
	}
	target := call.Input.File
	if target == "" {
		target = call.Input.Notebook
	}
	reason, denied := policy.Decide(lanes.ToolCall{Tool: call.Tool, Command: call.Input.Command, Path: target, Cwd: call.Cwd})
	if denied {
		deny(stdout, reason)
	}
	return false
}

func deny(stdout io.Writer, reason string) {
	write(stdout, map[string]string{
		"hookEventName": eventPreToolUse, "permissionDecision": "deny", "permissionDecisionReason": reason,
	})
}

// reground tells a session that lost its place who it is. SessionStart is the one
// event that can add context after a compaction.
func reground(cfg config, event lanes.HookEvent, stdout io.Writer) {
	if !lanes.Regrounds(event.Detail) {
		return
	}
	write(stdout, map[string]string{
		"hookEventName": eventSessionStart,
		"additionalContext": fmt.Sprintf("You are working in ropeshift lane %s, generation %d, on work order %s.",
			cfg.lane, cfg.generation, cfg.workOrder),
	})
}

func write(stdout io.Writer, specific map[string]string) {
	line, err := json.Marshal(map[string]any{"hookSpecificOutput": specific})
	if err == nil {
		_, _ = stdout.Write(line)
	}
}

// forward sends one short request. A refusal is an answer, so it spools nothing, and
// a hook that missed its ack spools an event the worker may have, which is safe
// because the worker merges by meaning.
func forward(cfg config, event lanes.HookEvent) {
	if cfg.socket != "" {
		request := lanes.Request{Version: lanes.SocketProtocol, Token: cfg.token, Hook: &event}
		if _, err := api.CallLane(cfg.socket, request, dialDeadline, ackDeadline); err == nil {
			return
		}
	}
	spool(cfg.spool, event)
}

// spool appends one line with one write, so parallel hooks cannot interleave. The
// line holds no token: only the session's environment does.
func spool(path string, event lanes.HookEvent) {
	if path == "" {
		return
	}
	line, err := json.Marshal(event)
	if err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(line, '\n'))
}
