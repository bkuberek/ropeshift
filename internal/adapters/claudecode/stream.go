package claudecode

import (
	"bytes"
	"encoding/json"
	"strings"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// abortedStreaming is the terminal reason of an interrupted turn. It exits 0, so only
// this word separates a stopped turn from a finished one.
const abortedStreaming = "aborted_streaming"

// blockingLimit is the terminal reason of a turn at a limit. The page names it and does not define
// it, so what it means here is a choice: a real limit corrects it.
const blockingLimit = "blocking_limit"

// stopDeferred is the stop reason of a turn that ended on a deferred call.
const stopDeferred = "tool_deferred"

// percentOfUtilization turns the stream's fraction into the status line's percent.
const percentOfUtilization = 100

// streamLine holds the fields the driver reads from one stream-json line. It never
// reads message content, and an unknown type is ignored.
type streamLine struct {
	Type           string  `json:"type"`
	Subtype        string  `json:"subtype"`
	SessionID      string  `json:"session_id"`
	Model          string  `json:"model"`
	UUID           string  `json:"uuid"`
	IsReplay       bool    `json:"isReplay"`
	IsError        bool    `json:"is_error"`
	TerminalReason string  `json:"terminal_reason"`
	TotalCostUSD   float64 `json:"total_cost_usd"`
	// DurationMs and DurationAPIMs are a `result`'s figures: the turn's duration, and the session's running
	// total of API time. A line that names none leaves them nil.
	DurationMs      *int64       `json:"duration_ms"`
	DurationAPIMs   *int64       `json:"duration_api_ms"`
	Usage           lanes.Usage  `json:"usage"`
	ParentToolUseID *string      `json:"parent_tool_use_id"`
	Event           *streamEvent `json:"event"`
	// RateLimit is one notice of a sign-in's limit. It comes with a process's first API response,
	// and then when a figure moves.
	RateLimit *rateLimitInfo `json:"rate_limit_info"`
	// Error and Attempt are a retry's. Another line can hold a different shape under the same
	// name, and one word of a line must not cost the rest of it.
	Error   json.RawMessage `json:"error"`
	Attempt int64           `json:"attempt"`
	// StopReason says what ended a turn, which a deferred call names.
	StopReason string `json:"stop_reason"`
	Compact    *struct {
		Trigger string `json:"trigger"`
	} `json:"compact_metadata"`
	// MessagingSocket is where a session reads messages. It changes with the process.
	MessagingSocket string `json:"messaging_socket_path"`
	// Message is a line's message, whose shape differs by type: a user message's content can be text.
	// The driver reads only an assistant message's usage and the subagent each of its Agent calls starts.
	Message json.RawMessage `json:"message"`
}

// streamMessage is what the driver reads of an assistant message. Its text is read only for an assistant's
// session, whose replies are the conversation.
type streamMessage struct {
	ID      string      `json:"id"`
	Model   string      `json:"model"`
	Usage   lanes.Usage `json:"usage"`
	Content []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Text string `json:"text"`
		// Input is read for the agent a call starts and for the path a file-writing call names, and for no
		// other field of a call.
		Input struct {
			SubagentType string `json:"subagent_type"`
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		} `json:"input"`
	} `json:"content"`
}

type rateLimitInfo struct {
	Status      string   `json:"status"`
	ResetsAt    int64    `json:"resetsAt"`
	Window      string   `json:"rateLimitType"`
	Utilization *float64 `json:"utilization"`
	// Windows is where T8 saw the figure. Only a version whose check of it passed is read here.
	Windows map[string]struct {
		Utilization *float64 `json:"utilization"`
	} `json:"unifiedWindows"`
}

// reading turns a notice into what a worker reads. No window is assumed, so the highest figure any
// of them gives is the one that can hold work back.
func (i rateLimitInfo) reading(windows bool) lanes.UsageReading {
	out := lanes.UsageReading{Status: i.Status, ResetsAtMs: i.ResetsAt * 1000, Window: i.Window}
	take := func(fraction *float64) {
		if fraction != nil {
			out.UsedKnown, out.UsedPercent = true, max(out.UsedPercent, *fraction*percentOfUtilization)
		}
	}
	take(i.Utilization)
	if windows {
		for _, window := range i.Windows {
			take(window.Utilization)
		}
	}
	return out
}

type streamEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string      `json:"id"`
		Model string      `json:"model"`
		Usage lanes.Usage `json:"usage"`
	} `json:"message"`
	// Usage is a `message_delta`'s: the call's output so far.
	Usage lanes.Usage `json:"usage"`
}

// parsed is what one line means. A line can mean nothing.
type parsed struct {
	event  ports.DriverEvent
	useful bool
	// turnEnded says this line ends a turn.
	turnEnded bool
	// stopped says the turn ended without a cost.
	stopped bool
	total   float64
	// socket is the session's messaging socket, on a line that names one.
	socket string
	// started maps each Agent call this line makes to the agent it starts, and parent is the call a
	// subagent's message runs under, whose usage it carries (FR-AGENT-12).
	started map[string]string
	parent  string
	usage   lanes.Usage
	// tools are the calls an assistant message made, a subagent's included.
	tools []lanes.ToolUse
	// call is one API call's usage, which the turn's running cost is estimated from.
	call *streamCall
	// init is what a session's `system/init` says it loaded.
	init *initReport
}

func parseLine(raw []byte, windows bool) (parsed, error) {
	return parseLineOf(raw, windows, false)
}

// parseLineOf reads one line, and the text of an assistant message when replies says the session's text
// is what a person reads.
func parseLineOf(raw []byte, windows, replies bool) (parsed, error) {
	var line streamLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return parsed{}, err
	}
	switch line.Type {
	case "system":
		out := parseSystem(line)
		if line.Subtype == "init" {
			var init initReport
			if json.Unmarshal(raw, &init) == nil {
				out.init = &init
			}
		}
		return out, nil
	case "stream_event":
		return parseStreamEvent(line), nil
	case "result":
		return parseResult(line), nil
	case "rate_limit_event":
		if line.RateLimit != nil {
			reading := line.RateLimit.reading(windows)
			return parsed{useful: true, event: ports.DriverEvent{Kind: ports.EventLimit, Detail: reading.Status, Usage: &reading}}, nil
		}
	case "assistant":
		out, replied := parsed{}, false
		if replies {
			out, replied = parseReply(line)
		}
		if !replied {
			out = parseAssistant(line)
		}
		out.call = assistantCall(line)
		return withTools(out, toolUses(line)), nil
	case "user":
		if line.IsReplay && line.UUID != "" {
			return parsed{useful: true, event: ports.DriverEvent{Kind: ports.EventInputAdmitted, InputID: line.UUID}}, nil
		}
	}
	return parsed{}, nil
}

func parseSystem(line streamLine) parsed {
	switch line.Subtype {
	case "init":
		return parsed{useful: true, socket: line.MessagingSocket, event: ports.DriverEvent{
			Kind: ports.EventSessionBound, SessionID: line.SessionID, ModelResolved: line.Model,
		}}
	case "api_retry":
		var category string
		if json.Unmarshal(line.Error, &category) != nil {
			category = ""
		}
		return parsed{useful: true, event: ports.DriverEvent{Kind: ports.EventRetry, Detail: category, Attempt: line.Attempt}}
	case "compact_boundary":
		event := ports.DriverEvent{Kind: ports.EventCompaction}
		if line.Compact != nil {
			event.Detail = line.Compact.Trigger
		}
		return parsed{useful: true, event: event}
	}
	return parsed{}
}

// parseAssistant reads which subagent each Agent call of the session starts, and the usage of a message a
// subagent sends, which names the call it runs under. Subagent stream messages carry
// `parent_tool_use_id` (landscape/claude-code.md); whether every subagent's usage reaches the stream is
// a qualified surface, so usage it does not report stays the lane's own.
func parseAssistant(line streamLine) parsed {
	var message streamMessage
	if len(line.Message) == 0 || json.Unmarshal(line.Message, &message) != nil {
		return parsed{}
	}
	if line.ParentToolUseID != nil {
		return parsed{parent: *line.ParentToolUseID, usage: message.Usage}
	}
	started := map[string]string{}
	for _, block := range message.Content {
		if block.Type == "tool_use" && (block.Name == agentTool || block.Name == "Task") && block.Input.SubagentType != "" {
			started[block.ID] = block.Input.SubagentType
		}
	}
	if len(started) == 0 {
		return parsed{}
	}
	return parsed{started: started}
}

// assistantCall is the API call an assistant message reports: its id, its model, and its usage, whoever made it. A
// message that names no id, or shows none of its usage, says nothing a turn's cost can use, because nothing tells its call
// from another's. The client writes a message once for each content block of a call, with the same id, so a call reported
// more than once is one call.
func assistantCall(line streamLine) *streamCall {
	if len(line.Message) == 0 {
		return nil
	}
	var message struct {
		ID    string      `json:"id"`
		Model string      `json:"model"`
		Usage lanes.Usage `json:"usage"`
	}
	if json.Unmarshal(line.Message, &message) != nil || message.ID == "" || message.Usage == (lanes.Usage{}) {
		return nil
	}
	call := &streamCall{kind: callMessage, id: message.ID, model: message.Model, usage: message.Usage}
	if line.ParentToolUseID != nil {
		call.parent = *line.ParentToolUseID
	}
	return call
}

// toolUseMarker is what an assistant message holds when it made a call, so a message of text alone is never
// decoded for calls.
var toolUseMarker = []byte(`"tool_use"`)

// toolUses reads the calls an assistant message made, a subagent's among them, and takes from each its tool's
// name and, for a file-writing tool, the path it was called with. The rest of a call's input stays unread.
func toolUses(line streamLine) []lanes.ToolUse {
	if !bytes.Contains(line.Message, toolUseMarker) {
		return nil
	}
	var message streamMessage
	if json.Unmarshal(line.Message, &message) != nil {
		return nil
	}
	var out []lanes.ToolUse
	for _, block := range message.Content {
		if block.Type != "tool_use" || block.Name == "" {
			continue
		}
		out = append(out, lanes.ToolUse{Name: block.Name, Path: lanes.WrittenPath(block.Name, block.Input.FilePath, block.Input.NotebookPath)})
	}
	return out
}

// withTools gives a line the calls it made. A line that has nothing else to say still says that, so no call is
// lost to a line the driver would otherwise skip.
func withTools(p parsed, tools []lanes.ToolUse) parsed {
	if len(tools) == 0 {
		return p
	}
	p.tools, p.event.Tools = tools, tools
	if !p.useful {
		p.useful, p.event.Kind = true, ports.EventToolUse
	}
	return p
}

// parseReply reads the text of one message of an assistant's session: its text blocks joined, under the
// vendor message's ID. A message of a subagent, and one with only tool calls or thinking, holds no reply.
func parseReply(line streamLine) (parsed, bool) {
	var message streamMessage
	if line.ParentToolUseID != nil || len(line.Message) == 0 || json.Unmarshal(line.Message, &message) != nil {
		return parsed{}, false
	}
	var texts []string
	for _, block := range message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			texts = append(texts, block.Text)
		}
	}
	id := message.ID
	if id == "" {
		id = line.UUID
	}
	if len(texts) == 0 || id == "" {
		return parsed{}, false
	}
	return parsed{useful: true, event: ports.DriverEvent{Kind: ports.EventReply, Text: strings.Join(texts, "\n\n"), MessageID: id}}, true
}

// parseStreamEvent reads context from message_start, the one figure that is per API
// call. A child session's calls name their parent and are not this lane's context. Both kinds of line also say what
// their API call has cost so far, which a subagent's calls add to the turn's, because the exact total counts them.
func parseStreamEvent(line streamLine) parsed {
	if line.Event == nil {
		return parsed{}
	}
	parent := ""
	if line.ParentToolUseID != nil {
		parent = *line.ParentToolUseID
	}
	switch line.Event.Type {
	case "message_start":
		call := &streamCall{kind: callStart, id: line.Event.Message.ID, parent: parent, model: line.Event.Message.Model, usage: line.Event.Message.Usage}
		if line.ParentToolUseID != nil {
			return parsed{call: call}
		}
		return parsed{call: call, useful: true, event: ports.DriverEvent{
			Kind: ports.EventUsage, ContextTokens: line.Event.Message.Usage.Context(),
		}}
	case "message_delta":
		return parsed{call: &streamCall{kind: callDelta, parent: parent, usage: line.Event.Usage}}
	}
	return parsed{}
}

// parseResult ends a turn. The driver reads this line and not the exit code, because
// SIGINT exits 0 with `is_error` true.
func parseResult(line streamLine) parsed {
	turn := ports.TurnResult{
		Status: ports.TurnCompleted, Usage: line.Usage, Deferred: line.StopReason == stopDeferred,
		DurationMs: line.DurationMs, APIMs: line.DurationAPIMs,
	}
	stopped := false
	switch {
	case line.TerminalReason == abortedStreaming:
		turn.Status, turn.Reason, stopped = ports.TurnStopped, abortedStreaming, true
	case line.TerminalReason == blockingLimit:
		turn.Status, turn.Reason = ports.TurnFailed, lanes.FailureUsageLimit
	case line.IsError || (line.Subtype != "" && line.Subtype != "success"):
		turn.Status, turn.Reason = ports.TurnFailed, line.Subtype
	}
	return parsed{
		useful: true, turnEnded: true, stopped: stopped, total: line.TotalCostUSD,
		event: ports.DriverEvent{Kind: ports.EventTurnEnded, SessionID: line.SessionID, Turn: &turn},
	}
}
