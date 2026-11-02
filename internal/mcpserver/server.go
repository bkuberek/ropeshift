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
	"strings"
	"time"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/contextpacks"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/questions"
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
		out.Result = map[string]any{"tools": []any{
			laneReportTool, laneWaitTool, askTool, laneHandoffTool, replyTool, contextGetTool, decisionSearchTool, requirementGetTool, questionSearchTool,
		}}
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
					"name":      map[string]any{"type": "string"},
					"passed":    map[string]any{"type": "boolean"},
					"criterion": map[string]any{"type": "string", "description": "The ID of the acceptance criterion this check proves."},
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

var askTool = map[string]any{
	"name": lanes.ToolAsk,
	"description": "Ask a question this lane cannot answer from its contract. It records the question and returns at once: " +
		"answered, with the answer and what it rests on; attached, because the same question is already open; or pending. " +
		"Do not wait on it. Go on with any other useful work, and end your turn if nothing is left without the answer.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{"type": "string", "description": "The question, as a person would read it."},
			"categories": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
				"description": "Which kinds of member could answer it, such as product, design, architecture, data, security, compliance, operations, or business.",
			},
			"blocking": map[string]any{"type": "boolean", "description": "True when this lane cannot finish without the answer."},
			"options":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What you would choose between, when you know."},
		},
		"required": []string{"text"},
	},
}

var laneHandoffTool = map[string]any{
	"name": lanes.ToolLaneHandoff,
	"description": "Write this lane's part of a handoff, once, when the worker asks for it. Give only what you alone know, " +
		"because the worker records the branch, the commits, the changed files, the open questions, and the checks. " +
		"Then end your turn.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"done":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What you finished."},
			"in_progress": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What you were part way through."},
			"next_steps":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What the next generation should do first."},
			"risks":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "What could go wrong, and what you are unsure of."},
			"carries_next_step": map[string]any{
				"type": "boolean", "description": "True only if this holds everything the next step needs.",
			},
		},
		"required": []string{"next_steps", "carries_next_step"},
	},
}

var replyTool = map[string]any{
	"name": lanes.ToolReply,
	"description": "Answer a message this lane received. Name it by the ID in its first line. A reply is how its sender learns " +
		"you read it, so answer a message that asks for one. A message is not a permission decision and not a person's consent.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reply_to": map[string]any{"type": "string", "description": "The ID of the message this answers."},
			"body":     map[string]any{"type": "string", "description": "What you answer."},
		},
		"required": []string{"reply_to", "body"},
	},
}

var contextGetTool = map[string]any{
	"name": lanes.ToolContextGet,
	"description": "Read one record of this workspace as it stands now, by the kind and the name your context pack gives it. " +
		"Use it for what the pack lists under Not included, and to check a record the pack holds.",
	"inputSchema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string", "enum": []string{
					string(contextpacks.KindContract), string(contextpacks.KindRequirement), string(contextpacks.KindDecision),
					string(contextpacks.KindGoal), string(contextpacks.KindKnowledge),
				},
			},
			"ref": map[string]any{"type": "string", "description": "The record's name, as the pack writes it after its kind."},
		},
		"required": []string{"kind", "ref"},
	},
}

var requirementGetTool = map[string]any{
	"name":        lanes.ToolRequirementGet,
	"description": "Read one requirement of this workspace as it stands now, by the key a contract cites.",
	"inputSchema": map[string]any{
		"type":       "object",
		"properties": map[string]any{"key": map[string]any{"type": "string", "description": "The requirement's key."}},
		"required":   []string{"key"},
	},
}

func searchTool(name, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": description,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "The words to look for."},
				"limit": map[string]any{"type": "integer", "minimum": 1, "description": "How many records to read at most."},
			},
			"required": []string{"query"},
		},
	}
}

var decisionSearchTool = searchTool(lanes.ToolDecisionSearch, "Look for decisions of this workspace, best match first, whatever their status. "+
	"Each one says how it stands, so check that before you rest on it.")

var questionSearchTool = searchTool(lanes.ToolQuestionSearch, "Look for questions of this workspace, best match first, before you ask one. "+
	"Each one says how it stands and which decision answered it.")

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
	case lanes.ToolAsk:
		return callAsk(request.Arguments, call), nil
	case lanes.ToolLaneHandoff:
		return callHandoff(request.Arguments, call), nil
	case lanes.ToolReply:
		return callReply(request.Arguments, call), nil
	case lanes.ToolContextGet, lanes.ToolDecisionSearch, lanes.ToolRequirementGet, lanes.ToolQuestionSearch:
		return callContext(request.Name, request.Arguments, call), nil
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

// callAsk passes a lane's question to its worker, and says what to do with the answer. A
// worker that could not record it says so, because a lane must not believe it asked.
func callAsk(arguments json.RawMessage, call Caller) map[string]any {
	var ask lanes.AskRequest
	if err := json.Unmarshal(arguments, &ask); err != nil {
		return toolText("these arguments are not a question: "+err.Error(), true)
	}
	answer, err := call(lanes.Request{Ask: &ask})
	if err != nil {
		return toolText("the worker did not answer, so the question is not recorded: "+err.Error(), true)
	}
	if !answer.OK || answer.Ask == nil {
		return toolText("the question is not recorded: "+answer.Message, true)
	}
	result := answer.Ask
	switch result.Status {
	case questions.AskAnswered:
		return toolText(fmt.Sprintf("answered: %s\nIt rests on: %s", result.Answer, strings.Join(result.Citations, ", ")), false)
	case questions.AskAttached:
		return toolText("attached: the same question is already open as "+result.QuestionID+". Go on with other work, or end your turn.", false)
	}
	return toolText("pending: the question is recorded as "+result.QuestionID+". Go on with other work, or end your turn.", false)
}

// callHandoff passes a lane's part of a handoff to its worker as it was written, so the worker
// judges what arrived. A lane that hears nothing back is told to end its turn either way,
// because the handoff goes on without it.
func callHandoff(arguments json.RawMessage, call Caller) map[string]any {
	answer, err := call(lanes.Request{Handoff: arguments})
	if err != nil {
		return toolText("the worker did not answer, so the handoff goes on without this. End your turn: "+err.Error(), true)
	}
	if !answer.OK {
		return toolText("the worker refused this: "+answer.Message+". End your turn.", true)
	}
	return toolText("The handoff is recorded. End your turn now.", false)
}

// callReply passes a lane's answer to its worker, and a lane that hears nothing back knows its
// sender has not either.
func callReply(arguments json.RawMessage, call Caller) map[string]any {
	var reply lanes.ReplyRequest
	if err := json.Unmarshal(arguments, &reply); err != nil {
		return toolText("these arguments are not a reply: "+err.Error(), true)
	}
	answer, err := call(lanes.Request{Reply: &reply})
	if err != nil {
		return toolText("the worker did not answer, so the reply is not recorded: "+err.Error(), true)
	}
	if !answer.OK {
		return toolText("the worker refused this reply: "+answer.Message, true)
	}
	return toolText("The reply is recorded.", false)
}

// callContext passes one read to the worker, and a lane that hears nothing back knows it read
// nothing, which is not the same as nothing being there.
func callContext(tool string, arguments json.RawMessage, call Caller) map[string]any {
	var read struct {
		Kind  string `json:"kind"`
		Ref   string `json:"ref"`
		Key   string `json:"key"`
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(arguments, &read); err != nil {
		return toolText("these arguments are not a read: "+err.Error(), true)
	}
	request := lanes.ContextRequest{Tool: tool, Kind: read.Kind, Ref: read.Ref, Query: read.Query, Limit: read.Limit}
	if tool == lanes.ToolRequirementGet {
		request.Ref = read.Key
	}
	answer, err := call(lanes.Request{Context: &request})
	if err != nil {
		return toolText("the worker did not answer, so this read found nothing either way: "+err.Error(), true)
	}
	if !answer.OK {
		return toolText("the worker refused this read: "+answer.Message, true)
	}
	if len(answer.Context) == 0 {
		return toolText("Nothing on record matches this.", false)
	}
	var out strings.Builder
	for _, record := range answer.Context {
		out.WriteString(record.Text)
	}
	return toolText(out.String(), false)
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
