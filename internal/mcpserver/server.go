// Package mcpserver is `ropeshift mcp`, an MCP server over stdio that an agent CLI
// starts per session. It talks only to the local worker, and its tools depend on the
// caller. It is written by hand because a lane needs three methods, and the SDK would
// add a dependency to a process that starts inside a session's budget.
package mcpserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// protocolVersion is the MCP revision this server speaks.
const protocolVersion = "2025-06-18"

const (
	codeParse          = -32700
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

const (
	dialDeadline   = time.Second
	answerDeadline = 10 * time.Second
)

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Caller sends one request to the worker.
type Caller func(lanes.Request) (lanes.Reply, error)

// Main serves one session's stdio until its input closes.
func Main(stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	socket, token := getenv(api.EnvWorkerSocket), getenv(api.EnvLaneToken)
	call := func(request lanes.Request) (lanes.Reply, error) {
		request.Version, request.Token = lanes.SocketProtocol, token
		return api.CallLane(socket, request, dialDeadline, answerDeadline)
	}
	if err := Serve(stdin, stdout, call); err != nil {
		fmt.Fprintf(stderr, "ropeshift mcp: %v\n", err)
		return 1
	}
	return 0
}

// Serve reads one JSON-RPC message per line. A notification takes no answer.
func Serve(stdin io.Reader, stdout io.Writer, call Caller) error {
	reader := bufio.NewReader(stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) != 0 {
			if werr := answer(line, stdout, call); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func answer(line []byte, stdout io.Writer, call Caller) error {
	var in message
	if err := json.Unmarshal(line, &in); err != nil {
		return reply(stdout, response{ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "this line does not parse"}})
	}
	if len(in.ID) == 0 {
		return nil
	}
	out := response{ID: in.ID}
	switch in.Method {
	case "initialize":
		out.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "ropeshift", "version": "0"},
		}
	case "ping":
		out.Result = map[string]any{}
	case "tools/list":
		out.Result = map[string]any{"tools": []any{laneReportTool, laneWaitTool}}
	case "tools/call":
		out.Result, out.Error = callTool(in.Params, call)
	default:
		out.Error = &rpcError{Code: codeMethodNotFound, Message: "no such method: " + in.Method}
	}
	return reply(stdout, out)
}

func reply(stdout io.Writer, out response) error {
	out.JSONRPC = "2.0"
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(raw, '\n'))
	return err
}

var laneReportTool = map[string]any{
	"name": lanes.ToolLaneReport,
	"description": "Report this lane's outcome. Call it once, when the work is done or cannot go on. " +
		"Use completed when the contract is met, blocked when the lane cannot finish and no answer would change that, " +
		"and failed when the work ended abnormally.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"outcome": map[string]any{
				"type": "string", "enum": []string{
					string(workorders.OutcomeCompleted), string(workorders.OutcomeBlocked), string(workorders.OutcomeFailed),
				},
			},
			"reason":    map[string]any{"type": "string", "description": "Why the lane ended this way."},
			"artifacts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"checks": map[string]any{"type": "array", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string"},
					"passed": map[string]any{"type": "boolean"},
				},
				"required": []string{"name", "passed"},
			}},
		},
		"required": []string{"outcome"},
	},
}

var laneWaitTool = map[string]any{
	"name": lanes.ToolLaneWait,
	"description": "Say that this lane is waiting on something outside it, such as a CI run, so that a quiet lane " +
		"is not taken for a stuck one. A deadline still counts while it waits.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reason":  map[string]any{"type": "string", "description": "What the lane is waiting on."},
			"minutes": map[string]any{"type": "integer", "minimum": 1, "description": "How long the wait may last."},
		},
		"required": []string{"minutes"},
	},
}

func toolText(text string, failed bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": failed}
}

// callTool refuses a tool outside the caller's set as a protocol error, and a report
// the worker refused as a tool error, which the agent reads and can act on.
func callTool(params json.RawMessage, call Caller) (any, *rpcError) {
	var request struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	switch request.Name {
	case lanes.ToolLaneWait:
		return callWait(request.Arguments, call), nil
	case lanes.ToolLaneReport:
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: "no such tool: " + request.Name}
	}
	var report workorders.Report
	if err := json.Unmarshal(request.Arguments, &report); err != nil {
		return toolText("these arguments are not a report: "+err.Error(), true), nil
	}
	if err := report.Validate(); err != nil {
		return toolText(err.Error(), true), nil
	}
	answer, err := call(lanes.Request{Report: &report})
	if err != nil {
		return toolText("the worker did not answer, so the outcome is not recorded: "+err.Error(), true), nil
	}
	if !answer.OK {
		return toolText("the worker refused this report: "+answer.Message, true), nil
	}
	return toolText("The outcome is recorded. End your turn now.", false), nil
}

// callWait passes a lane's wait to its worker. A worker that is not there cannot watch the
// lane anyway, so a failure here costs nothing but the lane's word.
func callWait(arguments json.RawMessage, call Caller) map[string]any {
	var wait lanes.WaitDeclaration
	if err := json.Unmarshal(arguments, &wait); err != nil || wait.Minutes <= 0 {
		return toolText("a wait needs a number of minutes", true)
	}
	answer, err := call(lanes.Request{Wait: &wait})
	if err != nil {
		return toolText("the worker did not answer, so the wait is not recorded: "+err.Error(), true)
	}
	if !answer.OK {
		return toolText("the worker refused this wait: "+answer.Message, true)
	}
	return toolText("The wait is recorded.", false)
}
