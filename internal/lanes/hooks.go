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
	Detail    string `json:"detail,omitempty"`
	AtMs      int64  `json:"at_ms"`
	Malformed bool   `json:"malformed,omitempty"`
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
	out.SessionID, out.Tool = payload.SessionID, payload.ToolName
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
