package claudecode

import (
	"encoding/json"

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
	Type            string       `json:"type"`
	Subtype         string       `json:"subtype"`
	SessionID       string       `json:"session_id"`
	Model           string       `json:"model"`
	UUID            string       `json:"uuid"`
	IsReplay        bool         `json:"isReplay"`
	IsError         bool         `json:"is_error"`
	TerminalReason  string       `json:"terminal_reason"`
	TotalCostUSD    float64      `json:"total_cost_usd"`
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
	// Message is an assistant message, of which the driver reads the usage and the subagent each Agent
	// call starts, and no other content.
	Message *streamMessage `json:"message"`
}

// streamMessage is what the driver reads of an assistant message.
type streamMessage struct {
	Usage   lanes.Usage `json:"usage"`
	Content []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input struct {
			SubagentType string `json:"subagent_type"`
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
		Usage lanes.Usage `json:"usage"`
	} `json:"message"`
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
}

func parseLine(raw []byte, windows bool) (parsed, error) {
	var line streamLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return parsed{}, err
	}
	switch line.Type {
	case "system":
		return parseSystem(line), nil
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
		return parseAssistant(line), nil
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
	if line.Message == nil {
		return parsed{}
	}
	if line.ParentToolUseID != nil {
		return parsed{parent: *line.ParentToolUseID, usage: line.Message.Usage}
	}
	started := map[string]string{}
	for _, block := range line.Message.Content {
		if block.Type == "tool_use" && (block.Name == agentTool || block.Name == "Task") && block.Input.SubagentType != "" {
			started[block.ID] = block.Input.SubagentType
		}
	}
	if len(started) == 0 {
		return parsed{}
	}
	return parsed{started: started}
}

// parseStreamEvent reads context from message_start, the one figure that is per API
// call. A child session's calls name their parent and are not this lane's context.
func parseStreamEvent(line streamLine) parsed {
	if line.Event == nil || line.Event.Type != "message_start" || line.ParentToolUseID != nil {
		return parsed{}
	}
	return parsed{useful: true, event: ports.DriverEvent{
		Kind: ports.EventUsage, ContextTokens: line.Event.Message.Usage.Context(),
	}}
}

// parseResult ends a turn. The driver reads this line and not the exit code, because
// SIGINT exits 0 with `is_error` true.
func parseResult(line streamLine) parsed {
	turn := ports.TurnResult{Status: ports.TurnCompleted, Usage: line.Usage, Deferred: line.StopReason == stopDeferred}
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
