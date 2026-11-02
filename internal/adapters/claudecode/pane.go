package claudecode

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/crashpoint"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// A pane's size holds a capture to one line per dialog option.
const (
	paneWidth  = 130
	paneHeight = 45
)

const (
	defaultReadyTimeout = 60 * time.Second
	// checkPoll is the wait between reads of a screen still painting.
	checkPoll = 200 * time.Millisecond
	// checkSettle lets a key take effect before the next read, because a dialog that has
	// just opened refuses input for a moment, and a read must see what the key did.
	checkSettle = 500 * time.Millisecond
	// panePoll is how often a pane is read for its end. A pane has no stream to close.
	panePoll = 250 * time.Millisecond
	// compactPause lets the CLI take typed text as typing before Enter submits it. The
	// spikes used this gap.
	compactPause = 700 * time.Millisecond
	// socketDeadline bounds one write to a session's socket.
	socketDeadline = 2 * time.Second
	// stopDeadline bounds what cleaning up a failed start may take.
	stopDeadline = 5 * time.Second
)

// The hooks a pane's state derives from.
const (
	hookSessionStart     = "SessionStart"
	hookUserPromptSubmit = "UserPromptSubmit"
	hookStop             = "Stop"
	hookStopFailure      = "StopFailure"
	hookNotification     = "Notification"
	hookPostCompact      = "PostCompact"
	hookPostToolUse      = "PostToolUse"
)

// notificationNeedsInput is the Notification that says a session waits for an answer.
const notificationNeedsInput = "agent_needs_input"

// compactRequested is the trigger of a compaction someone asked for, which runs at rest as a turn of
// its own. A stream ends that turn with a result, and in a pane no Stop follows it, so its last hook
// is what ends it. An automatic compaction is part of the turn it fell in.
const compactRequested = "manual"

// The commands a pane runs.
const (
	lanePaneCommand = "lane-pane"
	compactCommand  = "/compact"
	keyEnter        = "Enter"
	socketPriority  = "next"
)

// panePhase is how far a start got, kept on disk before each step, so an adoption
// repeats what is safe and never what is not.
type panePhase string

const (
	// phaseOpening says a pane may or may not exist.
	phaseOpening panePhase = "opening"
	// phaseOpened says the check has not passed.
	phaseOpened panePhase = "opened"
	// phaseReady says the screen passed and no brief was tried.
	phaseReady panePhase = "ready"
	// phaseBriefing says a write of the brief may have happened, so it is never repeated.
	phaseBriefing panePhase = "briefing"
	// phaseBriefed says the socket took the brief.
	phaseBriefed panePhase = "briefed"
)

// paneState is what an adoption needs of a pane, because nothing else survives a worker.
type paneState struct {
	Name    string        `json:"name"`
	Phase   panePhase     `json:"phase"`
	Mode    checkMode     `json:"mode"`
	Timeout time.Duration `json:"timeout"`
	// Brief waits here until the check passes, so a worker that died during the check
	// can still hand it over.
	Brief string `json:"brief,omitempty"`
}

func panePath(spec ports.SessionSpec) string {
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("pane-%d.json", spec.Generation))
}

func readPaneState(spec ports.SessionSpec) (paneState, error) {
	var state paneState
	raw, err := os.ReadFile(panePath(spec))
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, fmt.Errorf("%s: %w", filepath.Base(panePath(spec)), err)
	}
	return state, nil
}

// writePaneState replaces the file in one step, so a kill leaves one whole state.
func writePaneState(spec ports.SessionSpec, state paneState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	next := panePath(spec) + ".next"
	if err := os.WriteFile(next, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(next, panePath(spec))
}

// paneArgs is the interactive command, which states the same mode and files as the
// headless one, because neither `--settings` nor `--mcp-config` survives a resume.
func paneArgs(spec ports.SessionSpec) []string {
	settings, mcp := generatedPaths(spec)
	out := []string{"--permission-mode", spec.PermissionMode, "--model", spec.Model, "--settings", settings, "--mcp-config", mcp}
	return append(append(out, packArgs(spec)...), spec.Override.Args()...)
}

// paneName is the pane's name on the terminal server. A lane ID holds no character tmux
// treats as a separator, because it names a directory, and this keeps it that way.
func paneName(spec ports.SessionSpec) string {
	lane := strings.NewReplacer(".", "_", ":", "_").Replace(spec.Lane)
	return fmt.Sprintf("%s-g%d", lane, spec.Generation)
}

// paneEnv is the session's environment, which the launcher sets exactly.
func paneEnv(spec ports.SessionSpec, qualified bool) []string {
	env := append(slices.Clone(spec.Env), lanes.EnvPolicy+"="+policyPath(spec))
	env = append(env, spec.Override.Env()...)
	if spec.TempDir != "" && qualified {
		env = append(env, envTempDir+spec.TempDir)
	}
	return env
}

// launchPath holds a launch in the session's private directory, or beside its generated
// files when it has none.
func launchPath(spec ports.SessionSpec) string {
	dir := spec.TempDir
	if dir == "" {
		dir = spec.ConfigDir
	}
	return filepath.Join(dir, fmt.Sprintf("pane-launch-%d.json", spec.Generation))
}

func readyTimeout(spec ports.SessionSpec) time.Duration {
	if spec.ReadyTimeout > 0 {
		return spec.ReadyTimeout
	}
	return defaultReadyTimeout
}

// launchPane opens a pane and runs the checked sequence. The session is known before the
// pane exists, because its first hook can arrive at once, and each step is on disk
// before it happens, so a worker that dies anywhere here leaves a session it can adopt.
func (d *Driver) launchPane(ctx context.Context, spec ports.SessionSpec, arguments []string, mode checkMode, meter lanes.CostMeter) (ports.SessionHandle, error) {
	if d.terminal == nil {
		return ports.SessionHandle{}, errors.New("no terminal for an interactive session")
	}
	binary, err := findBinary(spec.Env)
	if err != nil {
		return ports.SessionHandle{}, err
	}
	version, err := d.version(ctx, spec.Env)
	if err != nil {
		return ports.SessionHandle{}, err
	}
	if err := writeGenerated(spec); err != nil {
		return ports.SessionHandle{}, err
	}
	state := paneState{Name: paneName(spec), Phase: phaseOpening, Mode: mode, Timeout: readyTimeout(spec), Brief: spec.Brief}
	if err := writePaneState(spec, state); err != nil {
		return ports.SessionHandle{}, err
	}
	crashpoint.Hit("pane.start", "intent")
	launch := launchPath(spec)
	err = shimlink.WritePaneLaunch(launch, shimlink.PaneLaunch{
		Dir: spec.Dir, Path: binary, Args: arguments, Env: paneEnv(spec, Qualified(version)),
	})
	if err != nil {
		return ports.SessionHandle{}, err
	}
	s := d.newPaneSession(spec, state.Name, ports.SessionHandle{ID: sessionHandleID(spec)}, meter)
	if err := s.listen(spec, Qualified(version)); err != nil {
		return ports.SessionHandle{}, err
	}
	d.keep(s.handle.ID, s, spec)
	pane, err := d.terminal.Open(ctx, ports.PaneSpec{
		Name: state.Name, Dir: spec.Dir, Width: paneWidth, Height: paneHeight,
		Command: []string{spec.Binary, lanePaneCommand, launch},
	})
	if err != nil {
		s.receipts.close()
		d.forget(s.handle.ID)
		_ = os.Remove(launch)
		return ports.SessionHandle{}, err
	}
	s.setPID(pane.PID)
	state.Phase = phaseOpened
	if err := writePaneState(spec, state); err != nil {
		return ports.SessionHandle{}, err
	}
	crashpoint.Hit("pane.start", "opened")
	s.follow()
	if err := s.passCheck(ctx, spec, state); err != nil {
		s.abandon(ctx)
		d.forget(s.handle.ID)
		return ports.SessionHandle{}, err
	}
	return s.handle, nil
}

// passCheck is everything between an opened pane and a briefed session.
func (s *paneSession) passCheck(ctx context.Context, spec ports.SessionSpec, state paneState) error {
	check := startCheck{mode: state.Mode, timeout: state.Timeout, poll: checkPoll, settle: checkSettle}
	started := time.Now()
	if err := runStartCheck(ctx, s.terminal, s.name, check); err != nil {
		if keepErr := keepScreen(ctx, s.terminal, s.name, spec); keepErr != nil {
			return errors.Join(err, keepErr)
		}
		return err
	}
	crashpoint.Hit("pane.start", "checked")
	if err := s.awaitSocket(ctx, state.Timeout-time.Since(started), check); err != nil {
		return err
	}
	state.Phase = phaseReady
	if err := writePaneState(spec, state); err != nil {
		return err
	}
	return s.brief(ctx, spec, state)
}

// awaitSocket waits for the SessionStart hook, because a pane has no other way to say
// where its socket is.
func (s *paneSession) awaitSocket(ctx context.Context, limit time.Duration, check startCheck) error {
	select {
	case <-s.socketKnown:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(limit):
		return fmt.Errorf("%w: no SessionStart hook named a socket", check.failure())
	}
}

// brief hands over the first prompt at most once: the intent is on disk before the write,
// so a kill between them reads as a brief that may have arrived, and observation decides
// what happens next.
func (s *paneSession) brief(ctx context.Context, spec ports.SessionSpec, state paneState) error {
	if state.Brief == "" {
		state.Phase = phaseBriefed
		return writePaneState(spec, state)
	}
	state.Phase = phaseBriefing
	if err := writePaneState(spec, state); err != nil {
		return err
	}
	crashpoint.Hit("pane.start", "briefing")
	if _, err := s.send(ctx, ports.Input{Text: state.Brief}); err != nil {
		return err
	}
	crashpoint.Hit("pane.start", "briefed")
	state.Phase, state.Brief = phaseBriefed, ""
	return writePaneState(spec, state)
}

// findPane looks for a session in a pane. A pane that reads dead has its end to give, and
// a pane the server does not hold ended with no record. The listing can only hold a
// session back from reading dead. A start that never reached its pane found nothing.
func (d *Driver) findPane(ctx context.Context, spec ports.SessionSpec, state paneState) (ports.Located, error) {
	handle := ports.SessionHandle{ID: sessionHandleID(spec)}
	if d.terminal == nil {
		return ports.Located{}, errors.New("a pane's session, and no terminal to read it")
	}
	rows, listing := d.listing(ctx, spec)
	pane, err := d.terminal.Pane(ctx, state.Name)
	switch {
	case err == nil:
		handle.PID = pane.PID
		if pane.Dead {
			return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessDead, Readable: true}, Handle: handle}, nil
		}
		return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}, Handle: handle}, nil
	case !errors.Is(err, ports.ErrNotFound):
		return ports.Located{}, err
	case listing != nil:
		return ports.Located{}, listing
	case len(liveRows(rows)) != 0:
		return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessUnknown}, Handle: handle}, nil
	case state.Phase == phaseOpening:
		return ports.Located{}, nil
	}
	return ports.Located{Session: lanes.Session{Found: true, Liveness: lanes.LivenessDead, Readable: true}, Handle: handle}, nil
}

// adoptPane follows a pane from where a worker stopped. What hooks said while no worker
// listened is in the journal, and it replays first, in the order it happened, so an end
// reads after the turn that came before it. A pane still in its check goes on with it,
// and one that may have its brief is left alone.
func (d *Driver) adoptPane(ctx context.Context, h ports.SessionHandle, from ports.AdoptFrom, state paneState) error {
	if d.terminal == nil {
		return fmt.Errorf("%s: %w: no terminal", h.ID, ports.ErrNoSession)
	}
	spec := from.Spec
	s := d.newPaneSession(spec, state.Name, h, from.Cost)
	s.handle.PID = h.PID
	s.floor = from.Cursor
	version, err := d.version(ctx, d.versionEnv())
	if err != nil {
		version = ""
	}
	if err := s.listen(spec, Qualified(version)); err != nil {
		return err
	}
	records := slices.Clone(from.Hooks)
	slices.SortStableFunc(records, func(a, b lanes.HookRecord) int { return cmp.Compare(a.LastMs, b.LastMs) })
	for _, record := range records {
		// A clear moves a lane's generation under this process, and no later generation has one.
		if record.Generation < spec.Generation {
			continue
		}
		event := record.HookEvent
		event.AtMs = record.LastMs
		s.takeHook(event)
	}
	d.keep(h.ID, s, spec)
	s.follow()
	switch state.Phase {
	case phaseOpening, phaseOpened:
		if err := s.passCheck(ctx, spec, state); err != nil {
			s.abandon(ctx)
			d.forget(h.ID)
			return err
		}
	case phaseReady:
		return s.brief(ctx, spec, state)
	case phaseBriefing:
		if repeatsBrief() {
			return s.brief(ctx, spec, state)
		}
	}
	return nil
}

// paneSession is one session in a pane. Everything a worker learns of it comes from its
// hooks and status line, which arrive on other goroutines, so one queue keeps them in
// order and the end last.
type paneSession struct {
	handle   ports.SessionHandle
	terminal ports.Terminal
	name     string
	events   chan ports.DriverEvent
	done     chan struct{}
	gone     chan struct{}
	// socketKnown closes when a hook names the session's socket.
	socketKnown chan struct{}
	follows     sync.Once
	abandons    sync.Once
	// qualified says this version passed the check of the socket's surfaces, and receipts is
	// where its receipts come back, when it has an address.
	qualified bool
	receipts  *receiver

	mu       sync.Mutex
	wake     *sync.Cond
	queue    []ports.DriverEvent
	last     bool
	stopping bool
	socket   string
	// promptMs and stopMs are hook times, so a repeat or a reorder of either is safe: the
	// session is in a turn when the last prompt came after the last stop.
	promptMs int64
	stopMs   int64
	// floor is where the worker stopped reading, so a replay does not end a turn twice.
	floor    int64
	waiting  bool
	totalUSD float64
	// approvals names the calls a hook is waiting on, and no key reaches a pane until each ends,
	// because an Enter on a permission prompt is an answer.
	approvals map[string]bool
	meter     lanes.CostMeter
	seen      ports.Observation
}

func (d *Driver) newPaneSession(spec ports.SessionSpec, name string, handle ports.SessionHandle, meter lanes.CostMeter) *paneSession {
	s := &paneSession{
		handle: handle, terminal: d.terminal, name: name, events: make(chan ports.DriverEvent, eventBuffer),
		done: make(chan struct{}), gone: make(chan struct{}), socketKnown: make(chan struct{}), meter: meter, approvals: map[string]bool{},
	}
	s.totalUSD = meter.SessionTotalUSD
	s.wake = sync.NewCond(&s.mu)
	return s
}

// listen opens this session's address for receipts, before anything can answer one.
func (s *paneSession) listen(spec ports.SessionSpec, qualified bool) error {
	receipts, err := openReceiver(spec, qualified, s.refused)
	s.qualified, s.receipts = qualified, receipts
	return err
}

func (s *paneSession) setPID(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handle.PID = pid
}

// follow starts the two goroutines of a session once.
func (s *paneSession) follow() {
	s.follows.Do(func() {
		go s.pump()
		go s.watch()
	})
}

// abandon ends a session that failed its start, with a signal, and takes its pane away. A
// lane that stopped on an unknown screen keeps the screen on disk for a person.
func (s *paneSession) abandon(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stopDeadline)
	defer cancel()
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	s.receipts.close()
	_ = s.terminal.Signal(ctx, s.name, ports.SignalTerminate)
	_ = s.terminal.Close(ctx, s.name)
	s.abandons.Do(func() {
		close(s.gone)
		s.mu.Lock()
		s.wake.Broadcast()
		s.mu.Unlock()
	})
}

func (s *paneSession) push(events ...ports.DriverEvent) {
	s.queue = append(s.queue, events...)
	s.wake.Broadcast()
}

// pump is the one writer of the events channel, so the end is always the last event.
func (s *paneSession) pump() {
	for {
		s.mu.Lock()
		for len(s.queue) == 0 && !s.last && !s.isGone() {
			s.wake.Wait()
		}
		if s.isGone() {
			s.mu.Unlock()
			return
		}
		if len(s.queue) == 0 {
			s.mu.Unlock()
			close(s.events)
			close(s.done)
			return
		}
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		select {
		case s.events <- next:
		case <-s.gone:
			return
		}
	}
}

func (s *paneSession) isGone() bool {
	select {
	case <-s.gone:
		return true
	default:
		return false
	}
}

// watch reads the pane until it ends, and a read that failed says nothing about that.
func (s *paneSession) watch() {
	ticker := time.NewTicker(panePoll)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), shimDialDeadline)
		pane, err := s.terminal.Pane(ctx, s.name)
		cancel()
		switch {
		case errors.Is(err, ports.ErrNotFound):
			s.finish(ports.ExitStatus{}, true)
			return
		case err == nil && pane.Dead:
			s.finish(pane.Exit, false)
			return
		}
		select {
		case <-s.gone:
			return
		case <-ticker.C:
		}
	}
}

// finish reports how the session ended and removes its pane. A pane that vanished, or a
// process that ended inside a turn or on its own with a failure, crashed, unless someone
// asked it to stop.
func (s *paneSession) finish(status ports.ExitStatus, vanished bool) {
	s.mu.Lock()
	inTurn := s.promptMs > s.stopMs
	crashed := !s.stopping && (vanished || inTurn || status.Code != 0 || status.Signal != "")
	s.push(ports.DriverEvent{Kind: ports.EventSessionEnded, End: &ports.SessionEnd{Crashed: crashed, Exit: status}})
	s.last = true
	s.wake.Broadcast()
	s.mu.Unlock()
	s.receipts.close()
	ctx, cancel := context.WithTimeout(context.Background(), stopDeadline)
	defer cancel()
	_ = s.terminal.Close(ctx, s.name)
}

func (s *paneSession) stream() <-chan ports.DriverEvent { return s.events }

func (s *paneSession) ended() <-chan struct{} { return s.done }

// takeHook turns one hook into what it means. A hook repeats and reorders freely, so each
// case reads by meaning: only the newest stop ends a turn, and one the worker already
// read ends nothing.
func (s *paneSession) takeHook(event lanes.HookEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.Socket != "" && s.socket == "" {
		s.socket = event.Socket
		close(s.socketKnown)
	}
	switch event.Event {
	case hookSessionStart:
		s.note(event)
	case hookUserPromptSubmit:
		s.promptMs = max(s.promptMs, event.AtMs)
		s.waiting = false
		s.note(event)
		s.push(ports.DriverEvent{Kind: ports.EventSessionBound, SessionID: s.seen.SessionID, ModelResolved: s.seen.ModelResolved})
		s.push(ports.DriverEvent{Kind: ports.EventInputAdmitted})
	case hookStop:
		s.endTurn(event, ports.TurnCompleted, "")
	case hookStopFailure:
		s.endTurn(event, ports.TurnFailed, event.Detail)
	case lanes.EventStatusLine:
		s.takeStatus(event)
	case hookNotification:
		s.waiting = event.Detail == notificationNeedsInput
	case eventPermissionRequest:
		s.approvals[event.Digest] = true
	case hookPostToolUse:
		delete(s.approvals, event.Digest)
	case hookPostCompact:
		s.push(ports.DriverEvent{Kind: ports.EventCompaction, Detail: event.Detail})
		if event.Detail == compactRequested {
			s.endCompaction(event)
		}
	}
}

// endCompaction ends the turn a compaction ran as, and says that is all it held, because a person
// can run one, and nothing here tells that from one the worker asked for.
func (s *paneSession) endCompaction(event lanes.HookEvent) {
	before := len(s.queue)
	s.endTurn(event, ports.TurnCompleted, "")
	if len(s.queue) > before {
		s.queue[len(s.queue)-1].Turn.Compaction = true
	}
}

// note reads a session's identity from whatever carried it. A clear changes the session
// under one process, so this runs on every hook that names one. A worker hears of it with
// the first prompt and not before: SessionStart fires with no prompt at all, so it is no
// evidence that a brief arrived, and a binding is what moves a lane out of `briefing`.
func (s *paneSession) note(event lanes.HookEvent) {
	if event.SessionID != "" {
		s.seen.SessionID = event.SessionID
	}
	if event.Model != "" {
		s.seen.ModelResolved = event.Model
	}
}

func (s *paneSession) endTurn(event lanes.HookEvent, status ports.TurnStatus, reason string) {
	if event.AtMs <= s.stopMs || event.AtMs <= s.floor {
		s.stopMs = max(s.stopMs, event.AtMs)
		return
	}
	s.stopMs, s.waiting = event.AtMs, false
	clear(s.approvals)
	turn := ports.TurnResult{Status: status, Reason: reason, CostUSD: s.meter.ObserveTurn(s.totalUSD)}
	turn.Cost = s.meter
	s.push(ports.DriverEvent{Kind: ports.EventTurnEnded, Turn: &turn, Seq: event.AtMs})
}

// takeStatus reads a status line. Its context covers one call, during a turn, and a line
// with no usage is unknown and not a fall to zero.
func (s *paneSession) takeStatus(event lanes.HookEvent) {
	if event.Model != "" {
		s.seen.ModelResolved = event.Model
	}
	if event.Status == nil {
		return
	}
	s.totalUSD = max(s.totalUSD, event.Status.CostUSD)
	if event.Status.ContextKnown {
		s.seen.ContextTokens = event.Status.ContextTokens
		s.push(ports.DriverEvent{Kind: ports.EventUsage, ContextTokens: event.Status.ContextTokens})
	}
	if event.Status.Usage != nil {
		s.push(ports.DriverEvent{Kind: ports.EventLimit, Usage: event.Status.Usage})
	}
}

// observe reads the pane. A pane proves only that its process has not exited, and a read
// that failed is unknown.
func (s *paneSession) observe(ctx context.Context) ports.Observation {
	s.mu.Lock()
	out := s.seen
	s.mu.Unlock()
	select {
	case <-s.done:
		out.Liveness = ports.LivenessDead
		return out
	default:
	}
	out.Liveness = ports.LivenessUnknown
	attempt, cancel := context.WithTimeout(ctx, shimDialDeadline)
	defer cancel()
	if pane, err := s.terminal.Pane(attempt, s.name); err == nil {
		out.Liveness = ports.LivenessAlive
		if pane.Dead {
			out.Liveness = ports.LivenessDead
		}
	}
	return out
}

// send writes one line to the session's socket, which only a hook of this session could
// have named, and whose name carries the process that owns it. The write proves only
// that the socket took it: `UserPromptSubmit` is what shows the session did.
func (s *paneSession) send(ctx context.Context, in ports.Input) (ports.SendReceipt, error) {
	s.mu.Lock()
	socket, pid := s.socket, s.handle.PID
	s.mu.Unlock()
	from := ""
	if in.Peer || in.MidTurn {
		if !s.qualified {
			return ports.SendReceipt{}, fmt.Errorf("%s: %w: this version passed no check of the socket", s.handle.ID, ports.ErrNoChannel)
		}
		from = s.receipts.from()
	}
	return writeSocket(ctx, s.handle.ID, socket, pid, in, from)
}

// refused reports what a receipt said.
func (s *paneSession) refused(input string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.push(ports.DriverEvent{Kind: ports.EventInputRefused, InputID: input})
}

// compact goes by keys, because a command in a message's text is not executed. No key goes
// to a session that is waiting for an answer: the Enter would answer it.
func (s *paneSession) compact(ctx context.Context) error {
	s.mu.Lock()
	waiting := s.waiting || len(s.approvals) != 0
	s.mu.Unlock()
	if waiting {
		return ports.ErrAwaitingAnswer
	}
	if err := s.terminal.Type(ctx, s.name, compactCommand); err != nil {
		return err
	}
	pause(ctx, compactPause)
	return s.terminal.Keys(ctx, s.name, keyEnter)
}

// beginStop sends one signal, because one SIGINT ended an interactive process in the
// spikes. A lane that must end now takes SIGKILL.
func (s *paneSession) beginStop(ctx context.Context, how ports.StopMode) {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	signal := ports.SignalInterrupt
	if how == ports.StopNow {
		signal = ports.SignalKill
	}
	_ = s.terminal.Signal(ctx, s.name, signal)
}

func (s *paneSession) escalate(ctx context.Context, signal ports.Signal) {
	_ = s.terminal.Signal(ctx, s.name, signal)
}
