package lanes

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/messages"
)

// AskedQuestion is one question an agent put to its terminal's question tool, which is the
// one tool input a hook forwards, because a question is what the ledger is for.
type AskedQuestion struct {
	Text    string   `json:"text"`
	Options []string `json:"options,omitempty"`
}

// HookEvent is what a hook forwards. It holds only what the worker merges on, so no
// prompt, compaction summary, or tool input leaves the session, apart from Asked and the Path of a file-writing
// call.
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
	Status *StatusReport `json:"status,omitempty"`
	// Asked is what a question tool call asked, on a lane that defers it.
	Asked []AskedQuestion `json:"asked,omitempty"`
	// Digest names the call a tool event is about, which is all a hook forwards of the call's input apart from
	// Path.
	Digest string `json:"digest,omitempty"`
	// Path is the path a file-writing tool is called with, on its `PreToolUse`. It is the one thing a hook takes
	// from a call's input besides a question, and it is what a generation's digest lists as written. On an
	// `InstructionsLoaded`, it is the instruction file the session loaded, and Detail is its scope.
	Path string `json:"path,omitempty"`
	// Message names the message a prompt opened with, which is the admission signal. It is the
	// one thing a hook takes from a prompt.
	Message string `json:"message,omitempty"`
	// WaitRule is the ID of the wait rule that denied the call, on a denial. It is the only part of a denied
	// command a hook forwards, and the worker records it as a `wait_denied` event (FR-SPEND-2).
	WaitRule string `json:"wait_rule,omitempty"`
	// Background counts the background tasks a session's Stop says still run, such as a subagent or a command
	// it started in the background. Each one wakes the session with a turn of its own when it ends, so a turn
	// that stops with one is no end of the session's work. A task's description and command stay in the session.
	Background int   `json:"background,omitempty"`
	AtMs       int64 `json:"at_ms"`
	Malformed  bool  `json:"malformed,omitempty"`
}

// The Details of the Notifications that say a dialog waits for a person in a session's pane, which a key typed into
// the pane would answer: a permission prompt, an MCP server's form or browser URL, and a question a session puts to
// a person, such as a teammate's setup question. Each fires once its dialog has waited about six seconds
// (https://code.claude.com/docs/en/hooks#notification).
const (
	NotificationPermission     = "permission_prompt"
	NotificationElicitation    = "elicitation_dialog"
	NotificationElicitationURL = "elicitation_url_dialog"
	NotificationNeedsInput     = "agent_needs_input"
)

// The Details of the Notifications that say an MCP server's dialog closed: its response went back to the server, or
// the browser step it asked for is complete (https://code.claude.com/docs/en/hooks#notification).
const (
	NotificationElicitationResponse = "elicitation_response"
	NotificationElicitationComplete = "elicitation_complete"
)

// DialogOpens says a Notification of this type tells of a dialog that waits for a person. Any other type, such as one
// that says a session has been idle, tells of none.
func DialogOpens(notificationType string) bool {
	switch notificationType {
	case NotificationPermission, NotificationElicitation, NotificationElicitationURL, NotificationNeedsInput:
		return true
	}
	return false
}

// DialogCloses says a Notification of this type tells that an MCP server's dialog closed.
func DialogCloses(notificationType string) bool {
	return notificationType == NotificationElicitationResponse || notificationType == NotificationElicitationComplete
}

// EventStatusLine names a status line's report among hook events. The CLI has no hook of
// this name: the driver's status line command reports on the same socket.
const EventStatusLine = "StatusLine"

// DetailDenied is the Detail of a `PreToolUse` a hook refused. It is kept with the event, because a
// refused call never reaches a hook that would say it ended.
const DetailDenied = "denied"

// The two hooks that end a call `PostToolUse` does not, which fires only for one that succeeded.
const (
	EventToolFailed = "PostToolUseFailure"
	EventToolDenied = "PermissionDenied"
)

// EventCallDenied names what a worker keeps when it refuses a call a `PermissionRequest` held. The
// CLI has no hook of this name, and it reports no end for a call that was refused.
const EventCallDenied = "CallDenied"

// StatusReport is the part of a status line's input a worker uses.
type StatusReport struct {
	// ContextKnown is false when the line had no usage to read, which holds after a
	// compaction until the next call. A zero there is not a small context.
	ContextKnown  bool    `json:"context_known,omitempty"`
	ContextTokens int64   `json:"context_tokens,omitempty"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	// Usage is the fullest window this line named, and nil when it named none.
	Usage *UsageReading `json:"usage,omitempty"`
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
	Prompt           string `json:"prompt"`
	// BackgroundTasks are a Stop's tasks in flight (https://code.claude.com/docs/en/hooks#stop-input), which are
	// counted and never read: a task's description and command say what the session asked of it. It is kept raw, so
	// a value in a shape this build does not know counts nothing and costs nothing else of the Stop.
	BackgroundTasks json.RawMessage `json:"background_tasks"`
	// FilePath and MemoryType are the instruction file an `InstructionsLoaded` names, and its scope.
	FilePath   string `json:"file_path"`
	MemoryType string `json:"memory_type"`
	ToolInput  struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Questions    []struct {
			Question string `json:"question"`
			Options  []struct {
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	} `json:"tool_input"`
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
	// Limits has whichever windows the build reports, and none is assumed.
	Limits map[string]struct {
		Used     *float64 `json:"used_percentage"`
		ResetsAt int64    `json:"resets_at"`
	} `json:"rate_limits"`
}

// fullestWindow reads the window nearest its limit, because that is the one that stops a lane. A
// line that names none says nothing. Windows at one figure read in name order.
func (p statusPayload) fullestWindow() *UsageReading {
	var out *UsageReading
	for _, window := range slices.Sorted(maps.Keys(p.Limits)) {
		limit := p.Limits[window]
		if limit.Used == nil || (out != nil && *limit.Used <= out.UsedPercent) {
			continue
		}
		out = &UsageReading{Window: window, UsedPercent: *limit.Used, UsedKnown: true, ResetsAtMs: limit.ResetsAt * 1000}
	}
	return out
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
	report := StatusReport{CostUSD: payload.Cost.TotalUSD, Usage: payload.fullestWindow()}
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
	if event == EventToolFailed || event == EventToolDenied {
		// These are free text, and a failed tool's is its output, which does not leave the session.
		out.Detail = ""
	}
	out.Subject = payload.ToolUseID
	if out.Subject == "" {
		out.Subject = payload.SessionID
	}
	out.Message = messages.ReadFirstLine(payload.Prompt)
	if event == EventPreToolUse {
		out.Path = WrittenPath(payload.ToolName, payload.ToolInput.FilePath, payload.ToolInput.NotebookPath)
	}
	if event == EventStop {
		out.Background = countTasks(payload.BackgroundTasks)
	}
	if event == EventInstructionsLoaded {
		out.Path, out.Detail = payload.FilePath, payload.MemoryType
	}
	if payload.ToolName == ToolQuestion {
		for _, question := range payload.ToolInput.Questions {
			asked := AskedQuestion{Text: question.Question}
			for _, option := range question.Options {
				asked.Options = append(asked.Options, option.Label)
			}
			out.Asked = append(out.Asked, asked)
		}
	}
	return out
}

// countTasks counts the entries of a Stop's `background_tasks`, and finds none in a value that is no list.
func countTasks(raw json.RawMessage) int {
	var tasks []json.RawMessage
	if json.Unmarshal(raw, &tasks) != nil {
		return 0
	}
	return len(tasks)
}

// HookRecord is one event after merging: repeats add to Count and move LastMs.
type HookRecord struct {
	HookEvent
	FirstMs int64
	LastMs  int64
	Count   int64
}

// SourceClear is the source of a SessionStart a clear ran, which names the new session.
const SourceClear = "clear"

// Regrounds reports the sources after which a session has lost its place.
func Regrounds(source string) bool {
	return source == "compact" || source == SourceClear || source == "resume"
}

// Grounding is what a hook tells a session about its work once it has lost its place. A worker
// writes it, and a hook reads it with no worker.
type Grounding struct {
	Contract        string `json:"contract,omitempty"`
	ContractVersion int64  `json:"contract_version,omitempty"`
	// Pack is where this generation's context pack is on disk.
	Pack string `json:"pack,omitempty"`
}

// Text says who a session is. A generation of zero is one nobody can name.
func (g Grounding) Text(lane, workOrder string, generation int64) string {
	var out strings.Builder
	fmt.Fprintf(&out, "You are working in ropeshift lane %s", lane)
	if generation != 0 {
		fmt.Fprintf(&out, ", generation %d", generation)
	}
	fmt.Fprintf(&out, ", on work order %s", workOrder)
	if g.Contract != "" {
		fmt.Fprintf(&out, ", under contract %s version %d", g.Contract, g.ContractVersion)
	}
	out.WriteString(".")
	if g.Pack != "" {
		fmt.Fprintf(&out, " Your context pack is at %s.", g.Pack)
	}
	return out.String()
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
	// Roots are the directories a lane may write. None turns the path checks off.
	Roots []string `json:"roots,omitempty"`
	// Home is what `~` and `$HOME` mean to the session.
	Home string `json:"home,omitempty"`
	// DefaultBranches are the branches no push may reach.
	DefaultBranches []string `json:"default_branches,omitempty"`
	// Kept are the branches a lane continues and never moves (ADR-0023): no push may reach one, and no
	// command that moves or checks out a branch may name one.
	Kept []string `json:"kept,omitempty"`
	// ReadOnlyRoots are worktrees of other repositories a lane reads and never changes (ADR-0022): no git
	// command that writes may run in one, and like everything outside Roots, no write may reach one.
	ReadOnlyRoots []string `json:"read_only_roots,omitempty"`
	// ReadRoots, when set, are the only directories a file tool may read: a read, a search, or a listing
	// anywhere else is denied, whatever a rule allowed. None reads anywhere.
	ReadRoots []string `json:"read_roots,omitempty"`
	// AskReadRoots, when set, are the directories a lane reads with no prompt: a file tool or a shell command the
	// hook can read that reads anywhere else asks a person, in every permission mode (Asks).
	AskReadRoots []string `json:"ask_read_roots,omitempty"`
	// GitListingsInside says the worktree holds no link that leads out of it, so what a git listing names is a file
	// inside, which a command run on the listing reads. The worker's policy is compiled once and never reads it again.
	GitListingsInside bool `json:"git_listings_inside,omitempty"`
	// Program, when set, is the one program a shell may run: alone, with no substitution, variable, or
	// redirect to or from a file. Any other command is denied, whatever a rule or the session's mode allowed.
	Program string `json:"program,omitempty"`
	// Commands are the only commands of Program a shell may run, by name, such as `lane list`: a first
	// word of the program that names no command, as a route another process starts does, is denied.
	Commands []string `json:"commands,omitempty"`
	// Tools, when set, are the only tools the session may call: any other is denied, a tool of another
	// MCP server and one a later CLI adds included, whatever a rule allowed.
	Tools []string `json:"tools,omitempty"`
	// RefusedFlags are flags no command of Program may take, in any form, such as one that points it at
	// another listener than the one it should reach.
	RefusedFlags []string `json:"refused_flags,omitempty"`
	// QuestionTool says what a call of the agent's own question tool becomes.
	QuestionTool string `json:"question_tool,omitempty"`
	// AttachedMarker is the file a worker touches while a person is attached to the lane's pane. A
	// hook reads its modification time and nothing else, so it needs neither the worker nor tmux.
	AttachedMarker string `json:"attached_marker,omitempty"`
	// Hold denies every tool but OwnTools with this reason, for a lane that must stop and hand off.
	// A worker writes it, and the hook reads it with no worker.
	Hold string `json:"hold,omitempty"`
	// ApprovalWaitMs bounds a hook that waits on a decision.
	ApprovalWaitMs int64 `json:"approval_wait_ms,omitempty"`
	// Gates are the calls a person must approve. A hook asks for a shell command one names, however it is written,
	// and defers a call where it can (Defers), so a lane parks with no process waiting.
	Gates []GateRule `json:"gates,omitempty"`
	// GatesAskOnly says the session cannot honor a deferral, as a pane cannot, so a gated call asks and never defers.
	GatesAskOnly bool `json:"gates_ask_only,omitempty"`
	// OwnTools is the prefix of the lane's own tools, which a hold leaves open.
	OwnTools string `json:"own_tools,omitempty"`
	// Ground is what a session that lost its place is told of its work.
	Ground Grounding `json:"ground,omitzero"`
	// Waits is the versioned wait rule set a shell command is read against (FR-SPEND-2). A hook applies it from
	// this file, so a denial needs no worker.
	Waits *WaitRules `json:"waits,omitempty"`
	// links reads the disk for a file program's links (WithLinks). It is the reader's, not the file's.
	links LinkReader
	// real reads where a path leads on the disk (WithRealPaths). It is the reader's, not the file's.
	real RealPathReader
}

// ToolQuestion is the vendor's tool that asks the terminal. A lane must not ask there,
// because nobody may be watching and no ladder runs.
const ToolQuestion = "AskUserQuestion"

// What a question tool call becomes: a headless lane defers it and ends its turn, and an
// interactive lane is denied and told to use `ask`. A planning lane leaves the tool to the terminal
// while a person is attached to its pane, because the person is the one who answers, and is denied
// as an interactive lane is when nobody is.
const (
	QuestionToolDefer              = "defer"
	QuestionToolDeny               = "deny"
	QuestionToolTerminalIfAttached = "terminal_if_attached"
)

// AttachedStale is how old a lane's attached marker may be and still say a person is attached. The
// worker touches the marker about once a second while a client is attached to the lane's pane, so a
// marker older than this means the worker stopped, could not tell, or saw nobody.
const AttachedStale = 3 * time.Second

// Attached reads a marker's modification time against the hook's clock. A marker a little ahead is
// a clock a little off, and one far ahead is a clock that jumped, which says nothing. A marker that
// is missing or unreadable has no modification time, which reads as nobody attached.
func Attached(modified, now time.Time) bool {
	if modified.IsZero() {
		return false
	}
	age := now.Sub(modified)
	return age >= -AttachedStale && age <= AttachedStale
}

// ReasonUseAsk reaches the model, so it names the tool to use.
const ReasonUseAsk = "ropeshift policy: this lane asks through the ask tool. It records the question for the workspace, and it answers at once when it can."

// GateRule is one permission rule a profile gates, as a hook reads it.
type GateRule struct {
	Tool string `json:"tool"`
	// CommandPrefix narrows a rule to commands that start with it.
	CommandPrefix string `json:"command_prefix,omitempty"`
	// Exact says the rule's command ends with no wildcard, so the agent CLI matches that command and no longer one.
	Exact bool `json:"exact,omitempty"`
	// Bounded says the rule's wildcard was ` *` or `:*`, which ends the prefix at a word, and not a bare `*`, which matches any continuation.
	Bounded bool `json:"bounded,omitempty"`
}

// Gated says a call starts as a gate does. It reads a command only as written: Asks reads the other forms.
func (p DenyPolicy) Gated(call ToolCall) bool {
	for _, gate := range p.Gates {
		if gate.Tool == call.Tool && strings.HasPrefix(call.Command, gate.CommandPrefix) {
			return true
		}
	}
	return false
}

// ReadGate reads a permission rule such as `Bash(git tag *)`. A rule with no command reads as its
// tool, and the CLI's own check stands behind it.
func ReadGate(rule string) GateRule {
	tool, rest, found := strings.Cut(rule, "(")
	gate := GateRule{Tool: tool}
	if !found || tool != ToolBash {
		return gate
	}
	command := strings.TrimSuffix(rest, ")")
	body := command
	for _, suffix := range []string{":*", " *", "*"} {
		if trimmed := strings.TrimSuffix(body, suffix); trimmed != body {
			body, gate.Bounded = trimmed, suffix != "*"
			break
		}
	}
	gate.CommandPrefix, gate.Exact = body, body != "" && body == command
	return gate
}
