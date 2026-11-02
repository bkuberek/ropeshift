package ports

import (
	"context"
	"errors"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
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

// ErrNoChannel means a peer's message has no path into this session: nothing was written.
var ErrNoChannel = errors.New("no channel for a peer's message")

// ErrAwaitingAnswer means a session is waiting on an answer, so a key could give one.
var ErrAwaitingAnswer = errors.New("the session is waiting for an answer")

// The two ways a checked start stops. Neither session ran a turn.
var (
	// ErrUnknownScreen means a fresh start never reached a screen the driver knows.
	ErrUnknownScreen = errors.New("the start stopped on an unknown screen")
	// ErrResumeNotReady means a resume did not reach a ready state in its time.
	ErrResumeNotReady = errors.New("the resume did not reach a ready state")
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
	// Degraded says the version is outside the qualification matrix, so the driver
	// uses documented, version-independent surfaces only.
	Degraded bool
	// Resume is the driver's data for the resume table.
	Resume lanes.ResumeCosts
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
}

// SessionSpec is one session to start. Env is the whole environment, so nothing is
// inherited, and the driver adds only its own variables to it.
type SessionSpec struct {
	Lane       string
	WorkOrder  string
	Generation int64
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
	// Repository is the checkout the worktree came from, whose local settings a worktree
	// session shares.
	Repository string
	// Credential binds an API key to the CLI, for a lane that has one.
	Credential SecretBinding
	// TempDir is a short private directory for the session's sockets. Empty leaves
	// them where the CLI puts them.
	TempDir string
	// RunMode is RunModeHeadless or RunModeInteractive. Empty means headless.
	RunMode string
	// ReadyTimeout bounds a checked start. Zero takes the driver's default.
	ReadyTimeout time.Duration
	// DefaultBranch is the repository's own, which the generation's policy guards.
	DefaultBranch string
	// Gates are the permission rules a person must approve.
	Gates []string
	// Override carries a real-session suite's guard flags. It is empty outside the
	// realsession build.
	Override lanes.SessionOverride
}

// SessionHandle names one running session.
type SessionHandle struct {
	ID  string
	PID int
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
}

// EventKind sorts a driver's normalized events.
type EventKind string

// The kinds of DriverEvent.
const (
	// EventSessionBound carries the session and the resolved model. It arrives on
	// every turn, because a session's identity is current state.
	EventSessionBound EventKind = "session_bound"
	// EventUsage carries the context of one API call.
	EventUsage EventKind = "usage"
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
	// Compaction says this turn held only a compaction, which ends no work of the lane's unless the
	// lane asked for it. A person can run one at a pane, and its last hook is all a driver reads.
	Compaction bool
}

// SessionEnd says how a session ended.
type SessionEnd struct {
	// Crashed says the process died mid-turn, which is no `result` for an input.
	Crashed bool
	Exit    ExitStatus
}

// DriverEvent is one normalized event.
type DriverEvent struct {
	Kind          EventKind
	SessionID     string
	ModelResolved string
	ContextTokens int64
	Turn          *TurnResult
	End           *SessionEnd
	// InputID is the input an EventInputAdmitted names.
	InputID string
	// Detail is a short reason: a limit's status, a retry's error, or a compaction's trigger.
	Detail string
	// Usage is what an EventLimit read of its sign-in.
	Usage *lanes.UsageReading
	// Attempt counts an EventRetry's tries.
	Attempt int64
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
	// ReadUsage reads what a sign-in has used as free text, with no session and no tokens. Nothing
	// outside this machine may see it, because it can name local plugins and subagents. A run mode
	// with no such read says ErrUnsupported.
	ReadUsage(ctx context.Context, spec SessionSpec) (string, error)
	// Preflight checks a spec before start.
	Preflight(ctx context.Context, spec SessionSpec) (PreflightReport, error)
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
