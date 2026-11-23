package ports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/roster"
)

// The values of a capability that has a closed list.
const (
	RunModeHeadless    = "headless"
	RunModeInteractive = "interactive"

	ApprovalHook = "hook"

	InjectionBetweenTools = "between_tools"
	// InjectionNone says a peer's message has no path into a session on this version.
	InjectionNone = "none"

	ContextLive = "live"

	CompactNative = "native"

	UsageLimitResetTime = "reset_time"

	SignInSubscription = "subscription"
	SignInAPIKey       = "api_key"
)

// ErrNoSession means a handle names nothing this driver is running.
var ErrNoSession = errors.New("no such session")

// ErrStopUnconfirmed means a stop sent its last signal and read no end of the session, so the session may still run
// and a caller that must have it stopped asks again.
var ErrStopUnconfirmed = errors.New("the stop sent its last signal and read no end of the session")

// ErrNoChannel means a peer's message has no path into this session: nothing was written.
var ErrNoChannel = errors.New("no channel for a peer's message")

// ErrAwaitingAnswer means a session is waiting on an answer, so a key could give one.
var ErrAwaitingAnswer = errors.New("the session is waiting for an answer")

// ErrInputUnconfirmed means input typed into a session's pane showed no sign that the session took it, though its
// Enter was pressed again. Its text may stand in the session's prompt, so it is no input the session holds and none
// to type again. A session that takes no line may show a dialog no hook told of, so it is ErrAwaitingAnswer too.
var ErrInputUnconfirmed = fmt.Errorf("the session did not take typed input: %w", ErrAwaitingAnswer)

// ErrVersionMoved means the installed version changed while a probe ran, so its result names no
// one version and a new probe is due.
var ErrVersionMoved = errors.New("the installed version changed during the probe")

// The two ways a checked start stops, and a cause one of them can name. Neither session ran a turn.
var (
	// ErrUnknownScreen means a fresh start never reached a screen the driver knows.
	ErrUnknownScreen = errors.New("the start stopped on an unknown screen")
	// ErrResumeNotReady means a resume did not reach a ready state in its time.
	ErrResumeNotReady = errors.New("the resume did not reach a ready state")
	// ErrTrustKeyLost is why a checked start stopped at the trust dialog: the dialog did not act on the one Enter that
	// confirms it, and still asked at the end, with its focus left on the accept option or put back on `No, exit`. No
	// second Enter goes, because a late first one could answer the next dialog. The start's error wraps it beside
	// ErrUnknownScreen or ErrResumeNotReady, so a caller of either reads the start as it did.
	ErrTrustKeyLost = errors.New("the trust dialog did not take its Enter")
)

// Capabilities says what a driver supports for the installed version (FR-DRV-5). The
// promise lives here, and what actually happened lives in a SendReceipt.
type Capabilities struct {
	RunModes         []string
	ApprovalChannel  string
	DeferredTurn     bool
	Injection        string
	BriefAck         bool
	ContextReport    string
	Compact          string
	NativeResume     bool
	UsageLimitReport string
	SignInKinds      []string
	// Version is the installed version.
	Version string
	// Degraded says a surface the driver relies on passed no check on this version, so it uses
	// documented, version-independent surfaces in its place.
	Degraded bool
	// Source is where the surfaces the driver would use for this version come from: lanes.QualifiedByMatrix for the
	// release's shipped matrix, lanes.QualifiedByProbe for a probe on this machine, and empty for documented
	// surfaces only.
	Source string
	// Resume is the driver's data for the resume table.
	Resume lanes.ResumeCosts
	// Subagents says whether the CLI starts the roster's agents (roster.SubagentsNative or
	// roster.SubagentsNone), and SubagentTools whether it enforces their tools (roster.ToolsEnforced or
	// roster.ToolsInstructionsOnly). A driver that says nothing has neither (ADR-0025).
	Subagents     string
	SubagentTools string
}

// Support is what a driver's capabilities say of the roster's agents.
func (c Capabilities) Support() roster.Support {
	return roster.Support{Subagents: c.Subagents, SubagentTools: c.SubagentTools}
}

// SessionAgent is one roster agent a session may start, fitted inside the session's rules, with the model
// it runs on this driver (ADR-0025).
type SessionAgent struct {
	roster.Fitted
	Model string
}

// DoctorReport says what is present and usable. It uses vendor status commands and
// never reads credentials, and it holds a kind and a status, never an identity.
type DoctorReport struct {
	// Driver names the driver a work order can ask for.
	Driver    string
	Present   bool
	Version   string
	Qualified bool
	// SignIn is the kind of sign-in, empty when there is none or it could not be read.
	SignIn string
	// SignInStatus says whether it is signed in, as far as the vendor's command said.
	SignInStatus string
	Detail       string
}

// PreflightReport says whether a spec can start. An empty list passes.
type PreflightReport struct {
	Problems []string
	// Notices are what the driver left out of the session's setup that a person may want to know of, and that stops
	// nothing, such as a repository's MCP server named after ropeshift's own.
	Notices []string
	// Settings is what the lane's worktree changed of the project settings the session would read, in a key that runs
	// code or widens access, against the repository's at the lane's base. Nil says they match, or the driver has no
	// base to read them at.
	Settings *lanes.SettingsChange
}

// SessionSpec is one session to start. Env is the whole environment, so nothing is
// inherited, and the driver adds only its own variables to it.
type SessionSpec struct {
	Lane       string
	WorkOrder  string
	Generation int64
	// Contract and ContractVersion name the contract this work order pins.
	Contract        string
	ContractVersion int64
	// Dir is the lane's worktree.
	Dir string
	// Env is the lane's environment from the allowlist.
	Env []string
	// Model is the requested model, which may be an alias.
	Model string
	// PermissionMode is stated on every start, because the two run modes start
	// differently with no flag.
	PermissionMode string
	// Brief is the first prompt.
	Brief string
	// SystemPromptFile is the context pack, which a session holds in its system prompt on a start
	// and on a resume. Empty holds none.
	SystemPromptFile string
	// ConfigDir receives the generated settings, with a new name per generation.
	ConfigDir string
	// Binary is the stable path of the ropeshift binary (D18).
	Binary string
	// Tools are this caller's ropeshift MCP tools, which the settings allow.
	Tools []string
	// OnlyTools has a session hold nothing but Tools. A vendor's own tools would not be a caller's
	// to use, and each adds to what every prompt costs.
	OnlyTools bool
	// Repository is the checkout the worktree came from, whose local settings a worktree
	// session shares.
	Repository string
	// Base is the commit the lane's worktree began from, which a driver reads the repository's own files at, such as
	// its skills and subagents, and never from the worktree a lane can edit (FR-PROF-5). Empty names none.
	Base string
	// SettingsBase is the commit the lane's work began from, its claim's base, which a driver reads the repository's
	// `.mcp.json` and the status line of its project settings at, compares the project settings in the worktree with, and
	// restores them to, and never the tip of a branch the lane continues, which a lane wrote (ADR-0040). Empty reads at
	// Base.
	SettingsBase string
	// SettingsUnbased says the lane has no commit its settings can be compared with, as a review of a lane that recorded
	// none has: the repository's side is empty, so a driver reads none of the repository's `.mcp.json` servers or status
	// line, holds every setting the worktree holds that runs code, and never reads at Base, which holds what the lane
	// under review wrote (ADR-0040). SettingsBase is empty with it.
	SettingsUnbased bool
	// SettingsAtBase says a member chose to run the lane with the repository's settings: before the session starts, the
	// driver restores the project settings files its preflight compares to what they hold at SettingsBase (ADR-0040).
	SettingsAtBase bool
	// Credential binds an API key to the CLI, for a lane that has one.
	Credential SecretBinding
	// TempDir is a short private directory for the session's sockets. Empty leaves
	// them where the CLI puts them.
	TempDir string
	// RunMode is RunModeHeadless or RunModeInteractive. Empty means headless.
	RunMode string
	// ReadOnly says the session reads and reports: it holds no rule that lets it stage or commit, it may
	// not edit a file with a tool, and it may not commit, push, or move a branch or a tag. A review lane's
	// session is one.
	ReadOnly bool
	// Assistant says the session is a member's assistant (ADR-0024): it reads and searches its own files,
	// uses its own tools, and runs a shell only for the `ropeshift` commands Commands names, and it is denied
	// every other tool and command. Its driver reports the text it writes, as EventReply.
	Assistant bool
	// Commands are the commands of the ropeshift binary an assistant's shell may run, by name, such as
	// `lane list`. Routes are the binary's first words that name no command, which the worker and an agent
	// CLI start, such as `lane-shim`; an assistant's rules deny each by name.
	Commands []string
	Routes   []string
	// Planning says the lane runs a `plan` work order, whose person answers its questions in the
	// conversation. A driver leaves the agent's own question tool to an attached terminal for it.
	Planning bool
	// ReadyTimeout bounds a checked start. Zero takes the driver's default.
	ReadyTimeout time.Duration
	// DefaultBranch is the repository's own, which the generation's policy guards.
	DefaultBranch string
	// Kept are the branches the lane continues, which nothing it runs may push to or move (ADR-0023).
	Kept []string
	// ReadOnlyDirs are worktrees of other repositories the session reads and never changes (ADR-0022). A
	// driver lets the session read them with no prompt, and denies every write and every git command that
	// writes there.
	ReadOnlyDirs []string
	// ApprovalWait bounds a hook that waits on a decision.
	ApprovalWait time.Duration
	// Gates are the permission rules a person must approve.
	Gates []string
	// Override carries a real-session suite's guard flags. It is empty outside the
	// realsession build.
	Override lanes.SessionOverride
	// Telemetry says where the session's own telemetry goes. An empty endpoint sends none.
	Telemetry SessionTelemetry
	// Agents are the roster agents this session may start, each fitted inside its rules, which a driver
	// renders in lane-scoped configuration only, on every start and resume (ADR-0025). Empty renders none.
	Agents []SessionAgent
	// Effort is the session's effort, from the roster agent the lane runs as. Empty leaves the CLI's own.
	Effort string
	// Deny are the tool categories the lane's own agent denies, which narrow its rules and never widen
	// them.
	Deny []string
	// Waits is the wait rule set the generation's hook reads (FR-SPEND-2), from the project's spend policy in
	// force when the lane started. A zero Version means the session has none: an orchestrator's and an
	// assistant's shells are not a lane's.
	Waits lanes.WaitRules
	// MaxBudgetUSD is the client's own cap on a headless generation's spend, which a driver passes where its
	// qualification observed the flag (FR-SPEND-5): what remains of the work order's hard limit and the headroom
	// beyond it. It is a backstop for a worker that is slow to stop the lane, and never the limit. Zero sets none.
	MaxBudgetUSD float64
}

// ReasonMaxBudget is the reason of a turn a client ended at its own budget cap, `error_max_budget_usd` in its result.
// The lane parks as at a hard limit, because the cap was the limit and a little more.
const ReasonMaxBudget = "error_max_budget_usd"

// SessionTelemetry points a session's telemetry at the worker's receiver, tagged with what produced it
// (FR-OBS-2). Content stays out unless the profile's opt-in says otherwise (FR-OBS-5).
type SessionTelemetry struct {
	// Endpoint is the receiver's base URL, on loopback.
	Endpoint string
	// Header is the `name=value` header the receiver requires, so only a lane writes to it.
	Header  string
	Tags    lanes.TelemetryTags
	Traces  bool
	Content bool
}

// SessionHandle names one running session.
type SessionHandle struct {
	ID  string
	PID int
	// Version is the version the session's process started on, read at its launch, and Degraded
	// says what qualified that version left it on documented surfaces only. Source says where the
	// surfaces it started with came from, as Capabilities.Source does. All three are this process's
	// own, because a version can change between a worker's look and a launch.
	Version  string
	Degraded bool
	Source   string
}

// ProbeSpec is one qualification probe: a few short sessions on this machine, with its settings and
// its sign-in, that show which of a version's undocumented surfaces still work (FR-DRV-2).
type ProbeSpec struct {
	// Dir is a new directory the probe owns, where its sessions run and its files go.
	Dir string
	// TempDir is a short private directory for the probe's sockets.
	TempDir string
	// EvidenceDir is where the probe keeps what it saw of each check that did not pass, so a person can look at it
	// afterwards. The driver replaces its own part of it with each probe's, and it never lies within Dir, which the
	// probe clears. It is empty when nothing is kept.
	EvidenceDir string
	// Env is the environment from the allowlist, as a lane gets it.
	Env []string
	// Model is the model the probe's sessions ask for.
	Model string
	// Binary is the stable path of the ropeshift binary, whose hook reports what a session did.
	Binary string
	// SignIn is the worker's kind of sign-in, because one check reads a subscription's login.
	SignIn string
	// Override carries a real-session suite's guard flags. It is empty outside the realsession build.
	Override lanes.SessionOverride
}

// ResumeRef names an earlier session to continue as a new generation.
type ResumeRef struct {
	SessionID string
	// SessionTotalUSD is the last running total seen, so cost continues from there.
	SessionTotalUSD float64
}

// Liveness has three values, because an incomplete read is not a negative answer.
type Liveness = lanes.Liveness

// The values of Liveness. Dead needs positive evidence.
const (
	LivenessAlive   = lanes.LivenessAlive
	LivenessDead    = lanes.LivenessDead
	LivenessUnknown = lanes.LivenessUnknown
)

// Observation is a session's state as the driver sees it.
type Observation struct {
	Liveness      Liveness
	SessionID     string
	ModelResolved string
	ContextTokens int64
	// Malformed counts stream lines that did not parse.
	Malformed int
	// Attached says a person's terminal is attached to the session's pane. A session with no pane, and
	// a read that could not tell, say nobody is.
	Attached bool
	// AwaitsPerson says the session's pane shows something only a person can answer, which no key of the worker's
	// may: a dialog a hook told of, or typed input the session did not take, whose text may stand in its prompt. A
	// session with no pane says no.
	AwaitsPerson bool
}

// Located is what Find learned of a session from an earlier process.
type Located struct {
	lanes.Session
	Handle SessionHandle
}

// AdoptFrom is where a restarted worker picks a session up.
type AdoptFrom struct {
	// Spec names the session the way Find was asked for it.
	Spec SessionSpec
	// Cursor is the last stream position this worker finished with. Anything at or
	// before it is dropped, because a kill between reading and saving redelivers.
	Cursor int64
	// Cost continues a generation's meter.
	Cost lanes.CostMeter
	// Hooks are what a session's hooks reported, for a driver with no stream to reread.
	Hooks []lanes.HookRecord
	// Approvals are the calls this session still waits on a decision for, each as a hook names its call, and
	// UnnamedCall where what it waits on could not be read.
	Approvals []string
}

// UnnamedCall stands for a call that may wait on an answer and that nobody could name. Only the end of its
// turn ends that wait.
const UnnamedCall = ""

// EventKind sorts a driver's normalized events.
type EventKind string

// The kinds of DriverEvent.
const (
	// EventSessionBound carries the session and the resolved model. It arrives on
	// every turn, because a session's identity is current state.
	EventSessionBound EventKind = "session_bound"
	// EventUsage carries the context of one API call.
	EventUsage EventKind = "usage"
	// EventCost carries the session's running cost total at one moment of a turn, in RunningUSD, where the session
	// reports one before its turn ends: a status line does, and a headless stream gives a total only at a turn's
	// end. It is the usage report a hard spend limit is held to during a turn (FR-SPEND-5).
	EventCost EventKind = "cost"
	// EventAgentUsage carries the tokens of one message a roster agent spent, where the driver reports
	// which agent spent them (FR-AGENT-12). They count toward the lane, under the agent's name.
	EventAgentUsage EventKind = "agent_usage"
	// EventTurnEnded carries how a turn ended, which is the only signal a turn is
	// over. An exit code cannot say, because a stopped turn exits 0.
	EventTurnEnded EventKind = "turn_ended"
	// EventInputAdmitted carries the ID of an input the session took.
	EventInputAdmitted EventKind = "input_admitted"
	// EventInputRefused carries the ID of an input the session refused, from a receipt.
	EventInputRefused EventKind = "input_refused"
	// EventCompaction says a compaction ran.
	EventCompaction EventKind = "compaction"
	// EventLimit carries a usage limit signal.
	EventLimit EventKind = "limit"
	// EventRetry says the CLI is retrying the model API.
	EventRetry EventKind = "retry"
	// EventMalformed says a stream line did not parse.
	EventMalformed EventKind = "malformed"
	// EventReply carries the text of one message an assistant's session wrote, with the vendor's ID of
	// that message. A message that holds only tool calls or thinking is none.
	EventReply EventKind = "reply"
	// EventToolUse carries the calls one message made, which a generation's digest counts. Any event can carry
	// the calls of its message in Tools, and this is the event of a message that has nothing else to say.
	EventToolUse EventKind = "tool_use"
	// EventSessionEnded is the last event of a session.
	EventSessionEnded EventKind = "session_ended"
)

// TurnStatus says how a turn ended.
type TurnStatus string

// The values of TurnStatus.
const (
	TurnCompleted TurnStatus = "completed"
	TurnStopped   TurnStatus = "stopped"
	TurnFailed    TurnStatus = "failed"
)

// TurnResult is one turn's end.
type TurnResult struct {
	Status TurnStatus
	// Reason is the vendor's word for a turn that did not complete.
	Reason string
	Usage  lanes.Usage
	// CostUSD is this turn's share of the running total.
	CostUSD float64
	// Cost is the generation's cost so far.
	Cost lanes.CostMeter
	// Deferred says the turn ended on a call a hook deferred, which waits in the session's transcript.
	Deferred bool
	// Compaction says this turn held only a compaction, which ends no work of the lane's unless the
	// lane asked for it. A person can run one at a pane, and its last hook is all a driver reads.
	Compaction bool
	// DurationMs is how long the turn took and APIMs the session's running total of API time, where the
	// stream says so. A driver that reads neither leaves them nil.
	DurationMs *int64
	APIMs      *int64
	// EndedMs is when the hook that ended the turn fired, by its clock, where a driver reads turns from hooks. A
	// driver that reads a stream leaves it zero.
	EndedMs int64
}

// SessionEnd says how a session ended.
type SessionEnd struct {
	// Crashed says the process died mid-turn, which is no `result` for an input.
	Crashed bool
	// InputUnanswered says at least one input the session took never got its result, whoever ended the
	// session: a kill mid-turn, at a stop or at an interrupt that did not land, crashes nothing and still
	// leaves the turn it ended unreported.
	InputUnanswered bool
	Exit            ExitStatus
}

// DriverEvent is one normalized event.
type DriverEvent struct {
	Kind          EventKind
	SessionID     string
	ModelResolved string
	ContextTokens int64
	// RunningUSD is an EventCost's running total for the session, which only grows.
	RunningUSD float64
	Turn       *TurnResult
	End        *SessionEnd
	// InputID is the input an EventInputAdmitted names.
	InputID string
	// Detail is a short reason: a limit's status, a retry's error, or a compaction's trigger.
	Detail string
	// Usage is what an EventLimit read of its sign-in.
	Usage *lanes.UsageReading
	// Attempt counts an EventRetry's tries.
	Attempt int64
	// Agent names the roster agent an EventAgentUsage counts toward, and Tokens what it spent.
	Agent  string
	Tokens *lanes.Usage
	// Text and MessageID are an EventReply's.
	Text      string
	MessageID string
	// Tools are the calls the event's message made, with the path a file-writing call named and no other
	// input.
	Tools []lanes.ToolUse
	// Setup is what an EventSessionBound's session loaded at its start, as its CLI reported it. A driver that
	// reads no such report, or a session whose start it did not see, leaves it nil.
	Setup *lanes.Setup
	// Seq is this event's position in a session's stream. Zero means the driver keeps
	// no stream that outlives its process.
	Seq int64
}

// Input is one message to a session. The driver assigns its ID when it is empty.
type Input struct {
	ID   string
	Text string
	// Peer says this comes from anyone but the worker, so it must keep a peer's standing. A driver
	// with no such path says ErrNoChannel, and never gives it the standing of a worker's input.
	Peer bool
	// MidTurn says this must reach a turn already running, on the path a session reads between tool
	// calls. A driver with no such path says ErrNoChannel.
	MidTurn bool
}

// The paths a SendReceipt can state.
const (
	SendStdin  = "stdin"
	SendSocket = "socket"
	// SendKeys is typing into a pane, the fallback on a version whose socket passed no check.
	SendKeys = "keys"
)

// SendReceipt states what actually happened to an input.
type SendReceipt struct {
	ID   string
	Path string
}

// ApprovalDecision answers a waiting session.
type ApprovalDecision struct {
	Allow  bool
	Reason string
}

// StopMode says how to end a session.
type StopMode string

// The ways to stop.
const (
	// StopAtRest ends a session between turns.
	StopAtRest StopMode = "at_rest"
	// StopInterrupt interrupts the turn first.
	StopInterrupt StopMode = "interrupt"
	// StopNow ends the process at once.
	StopNow StopMode = "now"
)

// AgentDriver adapts one agent CLI. The core never calls a vendor CLI directly.
type AgentDriver interface {
	// Capabilities reports what this driver supports for the installed version.
	Capabilities(ctx context.Context) (Capabilities, error)
	// Doctor reports presence, version, qualification, and sign-in kind. It uses
	// vendor status commands and never reads credentials.
	Doctor(ctx context.Context) (DoctorReport, error)
	// Probe runs the qualification probe on the installed version, and returns what each check
	// observed. A check that could not run reads unobserved, never passed.
	Probe(ctx context.Context, spec ProbeSpec) (lanes.Qualification, error)
	// ProbeChecks lists the checks a probe runs now for a worker on this kind of sign-in: every surface the driver
	// checks, less the ones this sign-in or this machine cannot observe. A result holds for its version only if it
	// passed every one of them.
	ProbeChecks(signIn string) []lanes.Surface
	// Qualify hands the driver a probe's result. From then on an undocumented surface is used only
	// on the version that result names, and only where its check passed.
	Qualify(q lanes.Qualification)
	// ReadUsage reads what a sign-in has used as free text, with no session and no tokens. Nothing
	// outside this machine may see it, because it can name local plugins and subagents. A run mode
	// with no such read says ErrUnsupported.
	ReadUsage(ctx context.Context, spec SessionSpec) (string, error)
	// Preflight checks a spec before start.
	Preflight(ctx context.Context, spec SessionSpec) (PreflightReport, error)
	// SettingsChange is what a running session's worktree changed now of the settings its session reads, as Preflight
	// reports it, for the hook the agent CLI runs before it applies a settings file that changed during the session. It
	// answers in a hook's time, and nil says nothing that runs code or widens access differs (ADR-0040).
	SettingsChange(ctx context.Context, spec SessionSpec) *lanes.SettingsChange
	// Start launches a session for a generation and returns its handle.
	Start(ctx context.Context, spec SessionSpec) (SessionHandle, error)
	// Resume continues an earlier session as a new generation.
	Resume(ctx context.Context, spec SessionSpec, from ResumeRef) (SessionHandle, error)
	// Observe returns the session's state as the driver sees it. An incomplete read
	// returns liveness unknown, never dead.
	Observe(ctx context.Context, h SessionHandle) (Observation, error)
	// Find looks for a generation's session from an earlier process, and changes
	// nothing. An incomplete read is an error, never a session that is not there.
	Find(ctx context.Context, spec SessionSpec) (Located, error)
	// Adopt takes over a session Find located, so Events reads it from from.Cursor.
	Adopt(ctx context.Context, h SessionHandle, from AdoptFrom) error
	// Events streams normalized events. The channel closes after EventSessionEnded.
	Events(ctx context.Context, h SessionHandle) (<-chan DriverEvent, error)
	// TakeHook hands a driver what a session's hook reported. The event names its lane
	// and generation, so one can arrive while Start is still running. A driver with a
	// stream ignores it, and one with none derives its events from it.
	TakeHook(ctx context.Context, event lanes.HookEvent)
	// Send hands input to the session. The receipt states what actually happened.
	Send(ctx context.Context, h SessionHandle, in Input) (SendReceipt, error)
	// Decide returns an approval decision to the waiting session.
	Decide(ctx context.Context, h SessionHandle, d ApprovalDecision) error
	// Compact requests native compaction where the driver has a path for it. It runs as a turn of its
	// own, which an EventCompaction shows ran, and an EventTurnEnded ends either way.
	Compact(ctx context.Context, h SessionHandle) error
	// HoldTools has a session's hooks deny every tool call but the lane's own, with a reason the
	// model reads, and an empty reason lifts it. A hook decides this from a file, so it needs no
	// worker, and a session with no such path says ErrUnsupported.
	HoldTools(ctx context.Context, h SessionHandle, reason string) error
	// Stop ends the session, interrupting the turn first when the mode asks.
	Stop(ctx context.Context, h SessionHandle, how StopMode) error
}
