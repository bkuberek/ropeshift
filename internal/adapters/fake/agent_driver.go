package fake

import (
	"context"
	"fmt"
	"sync"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// DriverTurn ends a scripted turn.
type DriverTurn struct {
	Status ports.TurnStatus
	Reason string
	Usage  lanes.Usage
	// TotalUSD is the session's running total, as a vendor reports it.
	TotalUSD float64
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
	// Hold waits until the session is stopped.
	Hold bool
	// Crash ends the process with no end to its turn.
	Crash bool
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
	version  string
	degraded bool
	sessions map[string]*driverSession
	specs    []ports.SessionSpec
	resumed  []ports.ResumeRef
	left     map[string]ports.Located
	adopted  []ports.AdoptFrom
	findErr  error
	stopped  bool
}

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
}

// NewAgentDriver returns a driver with an empty script.
func NewAgentDriver() *AgentDriver {
	return &AgentDriver{version: "1.0.0", sessions: map[string]*driverSession{}, left: map[string]ports.Located{}}
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

// Capabilities reports the capabilities of the real headless driver.
func (d *AgentDriver) Capabilities(context.Context) (ports.Capabilities, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ports.Capabilities{
		RunModes: []string{ports.RunModeHeadless}, ApprovalChannel: ports.ApprovalHook, DeferredTurn: true,
		Injection: ports.InjectionBetweenTools, ContextReport: ports.ContextLive, Compact: ports.CompactNative,
		NativeResume: true, UsageLimitReport: ports.UsageLimitResetTime,
		SignInKinds: []string{ports.SignInSubscription, ports.SignInAPIKey},
		Version:     d.version, Degraded: d.degraded,
	}, nil
}

// Doctor reports the installed version.
func (d *AgentDriver) Doctor(context.Context) (ports.DoctorReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return ports.DoctorReport{Present: true, Version: d.version, Qualified: !d.degraded}, nil
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
		stops: make(chan ports.StopMode, 1), done: make(chan struct{}), meter: meter,
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
		case step.Turn != nil:
			result := ports.TurnResult{Status: step.Turn.Status, Reason: step.Turn.Reason, Usage: step.Turn.Usage}
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
	session.inputs <- in
	return ports.SendReceipt{ID: in.ID, Path: ports.SendStdin}, nil
}

// Decide has no path in a headless fake, as in the driver at this unit.
func (d *AgentDriver) Decide(context.Context, ports.SessionHandle, ports.ApprovalDecision) error {
	return ports.ErrUnsupported
}

// Compact has no scripted effect.
func (d *AgentDriver) Compact(context.Context, ports.SessionHandle) error { return nil }

// Stop ends a session and waits for its last event to be sent.
func (d *AgentDriver) Stop(_ context.Context, h ports.SessionHandle, how ports.StopMode) error {
	session, err := d.session(h)
	if err != nil {
		return err
	}
	select {
	case session.stops <- how:
	default:
	}
	return nil
}

var _ ports.AgentDriver = (*AgentDriver)(nil)
