package claudecode

import (
	"encoding/json"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// abortedStreaming is the terminal reason of an interrupted turn. It exits 0, so only
// this word separates a stopped turn from a finished one.
const abortedStreaming = "aborted_streaming"

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
	RateLimit       *struct {
		Status string `json:"status"`
	} `json:"rate_limit_info"`
	Compact *struct {
		Trigger string `json:"trigger"`
	} `json:"compact_metadata"`
	// MessagingSocket is where a session reads messages. It changes with the process.
	MessagingSocket string `json:"messaging_socket_path"`
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
}

func parseLine(raw []byte) (parsed, error) {
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
			return parsed{useful: true, event: ports.DriverEvent{Kind: ports.EventLimit, Detail: line.RateLimit.Status}}, nil
		}
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
		return parsed{useful: true, event: ports.DriverEvent{Kind: ports.EventRetry}}
	case "compact_boundary":
		event := ports.DriverEvent{Kind: ports.EventCompaction}
		if line.Compact != nil {
			event.Detail = line.Compact.Trigger
		}
		return parsed{useful: true, event: event}
	}
	return parsed{}
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
	turn := ports.TurnResult{Status: ports.TurnCompleted, Usage: line.Usage}
	stopped := false
	switch {
	case line.TerminalReason == abortedStreaming:
		turn.Status, turn.Reason, stopped = ports.TurnStopped, abortedStreaming, true
	case line.IsError || (line.Subtype != "" && line.Subtype != "success"):
		turn.Status, turn.Reason = ports.TurnFailed, line.Subtype
	}
	return parsed{
		useful: true, turnEnded: true, stopped: stopped, total: line.TotalCostUSD,
		event: ports.DriverEvent{Kind: ports.EventTurnEnded, SessionID: line.SessionID, Turn: &turn},
	}
}
