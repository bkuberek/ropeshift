package lanes

import "encoding/json"

// HookEvent is what a hook forwards. It holds only what the worker merges on, so no
// prompt, tool input, or compaction summary leaves the session.
type HookEvent struct {
	Lane       string `json:"lane,omitempty"`
	Generation int64  `json:"generation,omitempty"`
	Event      string `json:"event"`
	// Subject is what the event is about, such as a tool call. Hook payloads have no
	// sequence field, so a repeat is found by meaning.
	Subject   string `json:"subject,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Tool      string `json:"tool,omitempty"`
	// Detail is the event's one word: a source, a reason, a trigger, or an error.
	Detail string `json:"detail,omitempty"`
	// Model is the model a session names at its start, or on its status line.
	Model string `json:"model,omitempty"`
	// Socket is the session's messaging socket, which a SessionStart hook reports,
	// because a pane has no stream to read it from.
	Socket string `json:"socket,omitempty"`
	// Status is what one run of the status line said.
	Status    *StatusReport `json:"status,omitempty"`
	AtMs      int64         `json:"at_ms"`
	Malformed bool          `json:"malformed,omitempty"`
}

// EventStatusLine names a status line's report among hook events. The CLI has no hook of
// this name: the driver's status line command reports on the same socket.
const EventStatusLine = "StatusLine"

// StatusReport is the part of a status line's input a worker uses.
type StatusReport struct {
	// ContextKnown is false when the line had no usage to read, which holds after a
	// compaction until the next call. A zero there is not a small context.
	ContextKnown  bool    `json:"context_known,omitempty"`
	ContextTokens int64   `json:"context_tokens,omitempty"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
}

// hookPayload names the fields a hook reads from its input.
type hookPayload struct {
	SessionID        string `json:"session_id"`
	ToolName         string `json:"tool_name"`
	ToolUseID        string `json:"tool_use_id"`
	Source           string `json:"source"`
	Reason           string `json:"reason"`
	Trigger          string `json:"trigger"`
	NotificationType string `json:"notification_type"`
	Error            string `json:"error"`
	Model            string `json:"model"`
}

// statusPayload names the fields of a status line's input a worker reads, and nothing
// else, so no prompt or account field leaves the session.
type statusPayload struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID string `json:"id"`
	} `json:"model"`
	Cost struct {
		TotalUSD float64 `json:"total_cost_usd"`
	} `json:"cost"`
	Window struct {
		Current *struct {
			Input         int64 `json:"input_tokens"`
			CacheCreation int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`
}

// NewStatusEvent reads one status line's input. Context is the sum of one call's input
// tokens. The line reads zero or null until the next call after a compaction, and neither
// is a context, so both report nothing.
func NewStatusEvent(input []byte) HookEvent {
	out := HookEvent{Event: EventStatusLine}
	var payload statusPayload
	if err := json.Unmarshal(input, &payload); err != nil {
		out.Malformed = true
		return out
	}
	out.SessionID, out.Subject, out.Model = payload.SessionID, payload.SessionID, payload.Model.ID
	report := StatusReport{CostUSD: payload.Cost.TotalUSD}
	if current := payload.Window.Current; current != nil {
		report.ContextTokens = current.Input + current.CacheCreation + current.CacheRead
		report.ContextKnown = report.ContextTokens > 0
	}
	out.Status = &report
	return out
}

// NewHookEvent reads one hook's input. Input that does not parse is still an event,
// because a hook must report what it saw and exit 0.
func NewHookEvent(event string, input []byte) HookEvent {
	out := HookEvent{Event: event}
	var payload hookPayload
	if err := json.Unmarshal(input, &payload); err != nil {
		out.Malformed = true
		return out
	}
	out.SessionID, out.Tool, out.Model = payload.SessionID, payload.ToolName, payload.Model
	for _, detail := range []string{payload.Source, payload.Reason, payload.Trigger, payload.NotificationType, payload.Error} {
		if detail != "" {
			out.Detail = detail
			break
		}
	}
	out.Subject = payload.ToolUseID
	if out.Subject == "" {
		out.Subject = payload.SessionID
	}
	return out
}

// HookRecord is one event after merging: repeats add to Count and move LastMs.
type HookRecord struct {
	HookEvent
	FirstMs int64
	LastMs  int64
	Count   int64
}

// Regrounds reports the sources after which a session has lost its place.
func Regrounds(source string) bool {
	return source == "compact" || source == "clear" || source == "resume"
}

// DenyRule denies one tool call before it runs.
type DenyRule struct {
	Tool string `json:"tool"`
	// CommandPrefix narrows a rule to commands that start with it.
	CommandPrefix string `json:"command_prefix,omitempty"`
	Reason        string `json:"reason"`
}

// DenyPolicy is a generation's compiled policy file. A hook reads only this file to
// deny, so a denial needs no worker.
type DenyPolicy struct {
	Deny []DenyRule `json:"deny"`
}
