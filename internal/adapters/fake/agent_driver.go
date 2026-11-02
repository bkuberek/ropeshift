package fake

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// FakeDriverName stands for the one driver phase 1 has.
const FakeDriverName = "claude-code"

// DriverTurn ends a scripted turn.
type DriverTurn struct {
	Status ports.TurnStatus
	Reason string
	Usage  lanes.Usage
	// TotalUSD is the session's running total, as a vendor reports it.
	TotalUSD float64
	// Deferred says the turn ended on a call a hook deferred.
	Deferred bool
	// Compaction says this turn held only a compaction.
	Compaction bool
}

// DriverStep is one thing a scripted session does. Exactly one field is set.
type DriverStep struct {
	// Bound reports the session and the resolved model.
	Bound bool
	// Context reports one API call's usage.
	Context *lanes.Usage
	// Turn ends the turn.
	Turn *DriverTurn
	// Malformed reports a line that did not parse.
	Malformed bool
	// Do runs in order with the events, as a tool call in a session would.
	Do func(spec ports.SessionSpec)
	// Refused reports a receipt that says the session refused this input.
	Refused string
	// Limit reports what a session read of its sign-in.
	Limit *lanes.UsageReading
	// Retry reports the CLI retrying on this error, for the Attempt-th time.
	Retry   string
	Attempt int64
	// Hold waits until the session is stopped.
	Hold bool
	// Crash ends the process with no end to its turn.
	Crash bool
	// Compaction reports the boundary of a compaction that ran.
	Compaction bool
}

// DriverScript is one session. Each turn answers one input, and the brief is the
// first.
type DriverScript struct {
	SessionID string
	Model     string
	Turns     [][]DriverStep
	// Silent admits no input, for a path with no acknowledgement of a brief.
	Silent bool
}

// AgentDriver runs scripted sessions in memory. No process starts.
type AgentDriver struct {
	mu       sync.Mutex
	next     DriverScript
	queue    []DriverScript
	sent     []ports.Input
	sends    int
	started  int
	unknown  bool
	version  string
	degraded bool
	sessions map[string]*driverSession
	specs    []ports.SessionSpec
	resumed  []ports.ResumeRef
	left     map[string]ports.Located
	adopted  []ports.AdoptFrom
	findErr  error
	stopped  bool
	hooks    []lanes.HookEvent
	onStop   func()
	signIn   string
	present  bool
	costs    lanes.ResumeCosts
	noResume bool
	// noPeerChannel stands for a version whose socket surfaces passed no check.
	noPeerChannel bool
	// ignoresPeers stands for a session that took a write and will never reach it: frozen,
	// refusing, or behind a long tool.
	ignoresPeers atomic.Bool
	// signInStatus is what the doctor reads, and empty reads as signed in.
	signInStatus string
	doctors      int
	usageText    string
	usageErr     error
	usageReads   int
	compacts     int
	compactErr   error
	sendErr      error
	noCompact    bool
	holds        []string
	// midTurnRefused stands for a socket that takes nothing, on a version that says it can.
	midTurnRefused bool
}

// compactInput is what a compaction reads as, in a list of inputs.
const compactInput = "/compact"

type driverSession struct {
	spec    ports.SessionSpec
	script  DriverScript
	events  chan ports.DriverEvent
	inputs  chan ports.Input
	stops   chan ports.StopMode
	done    chan struct{}
	meter   lanes.CostMeter
	mu      sync.Mutex
	context int64
	bad     int
	// ignoresPeers is its driver's, so a test can say this of a session already running.
	ignoresPeers *atomic.Bool
}

// NewAgentDriver returns a driver with an empty script.
func NewAgentDriver() *AgentDriver {
	return &AgentDriver{
		version: "1.0.0", sessions: map[string]*driverSession{}, left: map[string]ports.Located{},
		signIn: ports.SignInAPIKey, present: true, costs: DefaultResumeCosts(),
	}
}

// DefaultResumeCosts holds a cache long enough for any test, and no model's ratio.
func DefaultResumeCosts() lanes.ResumeCosts {
	return lanes.ResumeCosts{Lifetimes: map[string]time.Duration{ports.SignInSubscription: time.Hour, ports.SignInAPIKey: time.Hour}}
}

// SetResumeCosts sets the driver's data for the resume table.
func (d *AgentDriver) SetResumeCosts(costs lanes.ResumeCosts) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.costs = costs
}

// SetIgnoresPeers says whether a session takes a peer's write and does nothing with it.
func (d *AgentDriver) SetIgnoresPeers(on bool) { d.ignoresPeers.Store(on) }

// SetPeerChannel says whether a peer's message has a path into a session.
func (d *AgentDriver) SetPeerChannel(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noPeerChannel = !on
}

// SetNativeResume says whether this driver can continue a session.
func (d *AgentDriver) SetNativeResume(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noResume = !on
}

func sessionID(lane string, generation int64) string { return fmt.Sprintf("%s/%d", lane, generation) }

// Leave places a session as an earlier process left it, for Find to report.
func (d *AgentDriver) Leave(lane string, generation int64, session lanes.Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id := sessionID(lane, generation)
	d.left[id] = ports.Located{Session: session, Handle: ports.SessionHandle{ID: id}}
}

// FailFind makes every Find an incomplete read, until it is given nil.
func (d *AgentDriver) FailFind(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.findErr = err
}

// StopEverySession ends what runs now and what starts later, so a test can end while a
// lane is still on its way to a session.
func (d *AgentDriver) StopEverySession() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	for _, session := range d.sessions {
		session.stop()
	}
}

func (s *driverSession) stop() {
	select {
	case s.stops <- ports.StopNow:
	default:
	}
}

// SetSignIn sets the kind the doctor reports.
func (d *AgentDriver) SetSignIn(kind string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signIn = kind
}

// SetAbsent makes the doctor report no CLI on this machine.
func (d *AgentDriver) SetAbsent() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.present = false
}

// OnStop runs a function when a stop reaches the driver, as a session that writes once
// more on its way out would.
func (d *AgentDriver) OnStop(do func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onStop = do
}

// Adopted lists where each adoption picked a session up.
func (d *AgentDriver) Adopted() []ports.AdoptFrom {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ports.AdoptFrom{}, d.adopted...)
}

// Play sets what the next session does.
func (d *AgentDriver) Play(script DriverScript) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.next = script
}

// PlayAfter sets what the sessions after the first one do, in order. The last repeats.
func (d *AgentDriver) PlayAfter(scripts ...DriverScript) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.queue = append(d.queue, scripts...)
}

// ReadUnknown makes every Observe an incomplete read, until it is given false.
func (d *AgentDriver) ReadUnknown(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.unknown = on
}

// Sent lists every input Send took, each with the ID the driver gave it.
func (d *AgentDriver) Sent() []ports.Input {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ports.Input{}, d.sent...)
}

// Resumed lists every resume, in order.
func (d *AgentDriver) Resumed() []ports.ResumeRef {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ports.ResumeRef{}, d.resumed...)
}

// SetVersion sets the installed version, and whether it is qualified.
func (d *AgentDriver) SetVersion(version string, degraded bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.version, d.degraded = version, degraded
}

// Specs lists every session spec, in order.
func (d *AgentDriver) Specs() []ports.SessionSpec {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]ports.SessionSpec{}, d.specs...)
}

// Capabilities reports the capabilities of the real driver.
func (d *AgentDriver) Capabilities(context.Context) (ports.Capabilities, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	injection, compact := ports.InjectionBetweenTools, ports.CompactNative
	if d.noPeerChannel {
		injection = ports.InjectionNone
	}
	if d.noCompact {
		compact = ""
	}
	return ports.Capabilities{
		RunModes: []string{ports.RunModeHeadless, ports.RunModeInteractive}, ApprovalChannel: ports.ApprovalHook, DeferredTurn: true,
		Injection: injection, ContextReport: ports.ContextLive, Compact: compact,
		NativeResume: !d.noResume, UsageLimitReport: ports.UsageLimitResetTime,
		SignInKinds: []string{ports.SignInSubscription, ports.SignInAPIKey},
		Version:     d.version, Degraded: d.degraded, Resume: d.costs,
	}, nil
}

// Doctor reports the installed version.
func (d *AgentDriver) Doctor(context.Context) (ports.DoctorReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	status := d.signInStatus
	if status == "" {
		status = lanes.SignedIn
	}
	d.doctors++
	return ports.DoctorReport{
		Driver: FakeDriverName, Present: d.present, Version: d.version, Qualified: !d.degraded, SignIn: d.signIn, SignInStatus: status,
	}, nil
}

// SetSignInStatus says what the doctor reads of this machine's sign-in.
func (d *AgentDriver) SetSignInStatus(status string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signInStatus = status
}

// Doctors counts the times something asked.
func (d *AgentDriver) Doctors() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.doctors
}

// SetUsageRead says what `ReadUsage` answers.
func (d *AgentDriver) SetUsageRead(text string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.usageText, d.usageErr = text, err
}

// UsageReads counts the times something read it.
func (d *AgentDriver) UsageReads() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.usageReads
}

// ReadUsage answers what a test set, with no session.
func (d *AgentDriver) ReadUsage(context.Context, ports.SessionSpec) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.usageReads++
	return d.usageText, d.usageErr
}

// Preflight applies the checks a spec must pass on every driver.
func (d *AgentDriver) Preflight(_ context.Context, spec ports.SessionSpec) (ports.PreflightReport, error) {
	var report ports.PreflightReport
	if spec.PermissionMode == "" {
		report.Problems = append(report.Problems, "no permission mode")
	}
	if spec.Model == "" {
		report.Problems = append(report.Problems, "no model")
	}
	return report, nil
}

// Start runs the next script.
func (d *AgentDriver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	return d.open(spec, ports.ResumeRef{})
}

// Resume runs the next script, continuing the cost of an earlier session.
func (d *AgentDriver) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	d.mu.Lock()
	d.resumed = append(d.resumed, from)
	d.mu.Unlock()
	return d.open(spec, from)
}

func (d *AgentDriver) open(spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started != 0 && len(d.queue) != 0 {
		d.next, d.queue = d.queue[0], d.queue[1:]
	}
	d.started++
	session := d.newSession(spec, lanes.CostMeter{SessionTotalUSD: from.SessionTotalUSD})
	handle := ports.SessionHandle{ID: sessionID(spec.Lane, spec.Generation), PID: len(d.specs) + 1}
	d.specs = append(d.specs, spec)
	d.sessions[handle.ID] = session
	session.inputs <- ports.Input{Text: spec.Brief}
	go session.run()
	if d.stopped {
		session.stop()
	}
	return handle, nil
}

func (d *AgentDriver) newSession(spec ports.SessionSpec, meter lanes.CostMeter) *driverSession {
	return &driverSession{
		spec: spec, script: d.next, events: make(chan ports.DriverEvent, 256), inputs: make(chan ports.Input, 16),
		stops: make(chan ports.StopMode, 1), done: make(chan struct{}), meter: meter, ignoresPeers: &d.ignoresPeers,
	}
}

// Find reads the sessions this driver runs, then what an earlier process left.
func (d *AgentDriver) Find(_ context.Context, spec ports.SessionSpec) (ports.Located, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.findErr != nil {
		return ports.Located{}, d.findErr
	}
	id := sessionID(spec.Lane, spec.Generation)
	if _, ok := d.sessions[id]; ok {
		return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessAlive}, Handle: ports.SessionHandle{ID: id}}, nil
	}
	return d.left[id], nil
}

// Adopt plays the next script as the rest of a session an earlier process started.
func (d *AgentDriver) Adopt(_ context.Context, h ports.SessionHandle, from ports.AdoptFrom) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.left[h.ID]; !ok {
		return fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
	}
	d.started++
	session := d.newSession(ports.SessionSpec{}, from.Cost)
	delete(d.left, h.ID)
	d.adopted = append(d.adopted, from)
	d.sessions[h.ID] = session
	session.inputs <- ports.Input{}
	go session.run()
	if d.stopped {
		session.stop()
	}
	return nil
}

func (d *AgentDriver) session(h ports.SessionHandle) (*driverSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	session, ok := d.sessions[h.ID]
	if !ok {
		return nil, fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
	}
	return session, nil
}

func (s *driverSession) run() {
	defer close(s.done)
	defer close(s.events)
	turn := 0
	for {
		select {
		case input := <-s.inputs:
			if input.Peer && s.ignoresPeers.Load() {
				continue
			}
			if !s.script.Silent {
				s.events <- ports.DriverEvent{Kind: ports.EventInputAdmitted, InputID: input.ID}
			}
			if turn >= len(s.script.Turns) {
				continue
			}
			end, stopped := s.playTurn(s.script.Turns[turn])
			turn++
			if stopped {
				s.events <- ports.DriverEvent{Kind: ports.EventSessionEnded, End: &end}
				return
			}
		case <-s.stops:
			s.events <- ports.DriverEvent{Kind: ports.EventSessionEnded, End: &ports.SessionEnd{}}
			return
		}
	}
}

// playTurn reports whether the session ended inside the turn.
func (s *driverSession) playTurn(steps []DriverStep) (ports.SessionEnd, bool) {
	for _, step := range steps {
		switch {
		case step.Bound:
			s.events <- ports.DriverEvent{Kind: ports.EventSessionBound, SessionID: s.script.SessionID, ModelResolved: s.script.Model}
		case step.Context != nil:
			s.mu.Lock()
			s.context = step.Context.Context()
			s.mu.Unlock()
			s.events <- ports.DriverEvent{Kind: ports.EventUsage, ContextTokens: step.Context.Context()}
		case step.Malformed:
			s.mu.Lock()
			s.bad++
			s.mu.Unlock()
			s.events <- ports.DriverEvent{Kind: ports.EventMalformed}
		case step.Do != nil:
			step.Do(s.spec)
		case step.Refused != "":
			s.events <- ports.DriverEvent{Kind: ports.EventInputRefused, InputID: step.Refused}
		case step.Limit != nil:
			s.events <- ports.DriverEvent{Kind: ports.EventLimit, Detail: step.Limit.Status, Usage: step.Limit}
		case step.Retry != "":
			s.events <- ports.DriverEvent{Kind: ports.EventRetry, Detail: step.Retry, Attempt: step.Attempt}
		case step.Compaction:
			s.events <- ports.DriverEvent{Kind: ports.EventCompaction, Detail: "manual"}
		case step.Turn != nil:
			result := ports.TurnResult{Status: step.Turn.Status, Reason: step.Turn.Reason, Usage: step.Turn.Usage, Compaction: step.Turn.Compaction, Deferred: step.Turn.Deferred}
			result.CostUSD = s.meter.ObserveTurn(step.Turn.TotalUSD)
			result.Cost = s.meter
			s.events <- ports.DriverEvent{Kind: ports.EventTurnEnded, Turn: &result}
		case step.Hold:
			if mode := <-s.stops; mode == ports.StopInterrupt {
				s.meter.ObserveStopped()
				s.events <- ports.DriverEvent{Kind: ports.EventTurnEnded, Turn: &ports.TurnResult{
					Status: ports.TurnStopped, Reason: "aborted_streaming", Cost: s.meter,
				}}
			}
			return ports.SessionEnd{}, true
		case step.Crash:
			return ports.SessionEnd{Crashed: true, Exit: ports.ExitStatus{Code: 1}}, true
		}
	}
	return ports.SessionEnd{}, false
}

// Observe reads the session.
func (d *AgentDriver) Observe(_ context.Context, h ports.SessionHandle) (ports.Observation, error) {
	session, err := d.session(h)
	if err != nil {
		return ports.Observation{Liveness: ports.LivenessUnknown}, err
	}
	d.mu.Lock()
	unknown := d.unknown
	d.mu.Unlock()
	if unknown {
		return ports.Observation{Liveness: ports.LivenessUnknown}, nil
	}
	observation := ports.Observation{Liveness: ports.LivenessAlive, SessionID: session.script.SessionID, ModelResolved: session.script.Model}
	select {
	case <-session.done:
		observation.Liveness = ports.LivenessDead
	default:
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	observation.ContextTokens, observation.Malformed = session.context, session.bad
	return observation, nil
}

// Events returns the session's stream.
func (d *AgentDriver) Events(_ context.Context, h ports.SessionHandle) (<-chan ports.DriverEvent, error) {
	session, err := d.session(h)
	if err != nil {
		return nil, err
	}
	return session.events, nil
}

// Send starts the next turn.
func (d *AgentDriver) Send(_ context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	session, err := d.session(h)
	if err != nil {
		return ports.SendReceipt{}, err
	}
	select {
	case <-session.done:
		return ports.SendReceipt{}, fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
	default:
	}
	d.mu.Lock()
	if failure := d.sendErr; failure != nil {
		d.sendErr = nil
		d.mu.Unlock()
		return ports.SendReceipt{}, failure
	}
	if (in.Peer || in.MidTurn) && (d.noPeerChannel || (in.MidTurn && d.midTurnRefused)) {
		d.mu.Unlock()
		return ports.SendReceipt{}, fmt.Errorf("%s: %w", h.ID, ports.ErrNoChannel)
	}
	d.sends++
	if in.ID == "" {
		in.ID = fmt.Sprintf("input-%d", d.sends)
	}
	d.sent = append(d.sent, in)
	d.mu.Unlock()
	session.inputs <- in
	path := ports.SendStdin
	if in.Peer || in.MidTurn {
		path = ports.SendSocket
	}
	return ports.SendReceipt{ID: in.ID, Path: path}, nil
}

// Decide has no path in a headless fake, as in the driver at this unit.
func (d *AgentDriver) Decide(context.Context, ports.SessionHandle, ports.ApprovalDecision) error {
	return ports.ErrUnsupported
}

// TakeHook keeps what a hook reported.
func (d *AgentDriver) TakeHook(_ context.Context, event lanes.HookEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hooks = append(d.hooks, event)
}

// Hooks lists what TakeHook took, in order.
func (d *AgentDriver) Hooks() []lanes.HookEvent {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]lanes.HookEvent{}, d.hooks...)
}

// Compact is one more input, which plays the next turn as the compaction.
func (d *AgentDriver) Compact(_ context.Context, h ports.SessionHandle) error {
	session, err := d.session(h)
	if err != nil {
		return err
	}
	d.mu.Lock()
	failure := d.compactErr
	d.compacts++
	d.mu.Unlock()
	if failure != nil {
		return failure
	}
	session.inputs <- ports.Input{Text: compactInput}
	return nil
}

// FailSend makes the next Send say this.
func (d *AgentDriver) FailSend(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sendErr = err
}

// FailCompact makes every Compact say this.
func (d *AgentDriver) FailCompact(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.compactErr = err
}

// SetCompact says whether this driver has a path to a compaction.
func (d *AgentDriver) SetCompact(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.noCompact = !on
}

// Compacts counts the compactions asked for.
func (d *AgentDriver) Compacts() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.compacts
}

// HoldTools keeps what a session's hooks would deny with.
func (d *AgentDriver) HoldTools(_ context.Context, h ports.SessionHandle, reason string) error {
	if _, err := d.session(h); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.holds = append(d.holds, reason)
	return nil
}

// Holds lists what HoldTools took, in order.
func (d *AgentDriver) Holds() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.holds...)
}

// AdmitInput reports that the newest session took an input, as its stream would between two tool
// calls, whatever turn it is in.
func (d *AgentDriver) AdmitInput(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.specs) == 0 {
		return
	}
	last := d.specs[len(d.specs)-1]
	if session, ok := d.sessions[sessionID(last.Lane, last.Generation)]; ok {
		session.events <- ports.DriverEvent{Kind: ports.EventInputAdmitted, InputID: id}
	}
}

// SetMidTurnRefused says a turn in progress can be reached by nothing, though the version says it can.
func (d *AgentDriver) SetMidTurnRefused(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.midTurnRefused = on
}

// Stop ends a session and waits for its last event to be sent.
func (d *AgentDriver) Stop(_ context.Context, h ports.SessionHandle, how ports.StopMode) error {
	session, err := d.session(h)
	if err != nil {
		return err
	}
	d.mu.Lock()
	onStop := d.onStop
	d.mu.Unlock()
	if onStop != nil {
		onStop()
	}
	select {
	case session.stops <- how:
	default:
	}
	return nil
}

var _ ports.AgentDriver = (*AgentDriver)(nil)
