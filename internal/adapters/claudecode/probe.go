package claudecode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// EnvProbe marks a probe's session, so a hook or a double can tell it from a lane's.
const EnvProbe = lanes.TagPrefix + "PROBE"

// The probe's bounds. A probe holds a worker's dispatch, so each wait is short, and a check whose
// wait ran out reads unobserved or failed, which keeps its surface off. A test shortens them.
var (
	probeSocketWait  = 20 * time.Second
	probeTurnWait    = 2 * time.Minute
	probeReceiptWait = 20 * time.Second
	probeHoldWait    = 20 * time.Second
	probeExitWait    = 10 * time.Second
	probeScreenWait  = time.Minute
)

const (
	probePoll = 50 * time.Millisecond
	// probeLane names the probe in its hooks' spool, which only the probe reads.
	probeLane = "probe"
	// probePrompt asks for the smallest turn a session can take.
	probePrompt = "This turn checks the session's protocol. Reply with the single word ok, and call no tool."
	// probeMessageID opens the probe's socket message, in the first-line format a lane's messages use.
	probeMessageID = "PROBE"
	// probePane names the probe's pane on the worker's private server. A lane's pane name always
	// ends in its generation, so this cannot be one.
	probePane = "probe-screens"
)

// The inbound policies the probe runs its sessions under.
const (
	inboundRefuse = "refuse"
	inboundHold   = "hold"
)

// ErrVersionMoved means the installed version changed while a probe ran.
var ErrVersionMoved = ports.ErrVersionMoved

// Probe runs the qualification probe on the installed version (FR-DRV-2). It starts a few short
// sessions with this machine's own settings, as a lane would, and runs each check of the
// qualification matrix on what they do:
//
//   - under `accept`, one turn opened by a message on the session's own socket: the socket line,
//     the private socket directory, `unifiedWindows`, `command_lifecycle`, and the login on `-p`;
//   - under `refuse`, a message with a reply address: receipts;
//   - under `hold`, a message after one turn: `peer_message_hold`;
//   - in a pane, a start in a new directory: the known start screens.
//
// A check that could not run reads unobserved, and only a pass makes a surface usable.
func (d *Driver) Probe(ctx context.Context, spec ports.ProbeSpec) (lanes.Qualification, error) {
	if spec.Dir == "" || spec.TempDir == "" || spec.Binary == "" {
		return lanes.Qualification{}, errors.New("a probe needs a directory, a socket directory, and the ropeshift binary")
	}
	// A worker's probe runs with the environment its version checks run with, which is a lane's.
	if len(spec.Env) == 0 {
		spec.Env = d.versionEnv()
	}
	// The probe owns its directories: it clears what a worker that died during the last one left,
	// and leaves nothing behind.
	for _, dir := range []string{spec.Dir, spec.TempDir} {
		if err := os.RemoveAll(dir); err != nil {
			return lanes.Qualification{}, err
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return lanes.Qualification{}, err
		}
		defer os.RemoveAll(dir)
	}
	binary, err := findBinary(spec.Env)
	if err != nil {
		return lanes.Qualification{}, err
	}
	version, err := d.version(ctx, spec.Env)
	if err != nil {
		return lanes.Qualification{}, err
	}
	q := unqualified(version)
	q.Source, q.SignIn = lanes.QualifiedByProbe, spec.SignIn
	p := &prober{d: d, spec: spec, binary: binary, checks: q.Checks}
	p.accept(ctx)
	p.refuse(ctx)
	p.hold(ctx)
	p.screens(ctx)
	if err := ctx.Err(); err != nil {
		return lanes.Qualification{}, err
	}
	if after, err := d.version(ctx, spec.Env); err != nil || after != version {
		return lanes.Qualification{}, fmt.Errorf("%w: from %s", ErrVersionMoved, version)
	}
	return judged(q, d.terminal != nil), nil
}

// prober is one probe's run.
type prober struct {
	d      *Driver
	spec   ports.ProbeSpec
	binary string
	checks map[lanes.Surface]lanes.CheckResult
	count  int
}

func (p *prober) set(surface lanes.Surface, result lanes.CheckResult) { p.checks[surface] = result }

// observed is a check's result from what was seen: a pass, a failure where the session got far
// enough to show the surface, and unobserved where it did not.
func observed(reached, saw bool) lanes.CheckResult {
	switch {
	case saw:
		return lanes.CheckPassed
	case reached:
		return lanes.CheckFailed
	}
	return lanes.CheckUnobserved
}

// accept runs the checks one admitted message can show. The message opens the session's only turn
// when the socket is in its directory, and a turn from stdin stands in when it is not, so the
// stream's own checks still run.
func (p *prober) accept(ctx context.Context) {
	s, err := p.start(ctx, inboundAccept)
	if err != nil {
		return
	}
	defer s.end()
	socket := s.awaitSocket(ctx)
	id, sent, turn := "", false, false
	if socket != "" {
		id, sent = s.message(ctx, socket, "")
		turn = sent && s.awaitTurn(ctx)
	}
	// No socket, a refused write, or a message the session never took: a turn from stdin still runs
	// the stream's checks, and its init can name a socket the hook did not.
	if !turn {
		turn = s.prompt(ctx) && s.awaitTurn(ctx)
		if turn && socket == "" {
			if socket = s.initSocket(); socket != "" {
				if id, sent = s.message(ctx, socket, ""); sent {
					s.awaitTurn(ctx)
				}
			}
		}
	}
	init, hasInit := s.firstInit()
	hasLimit, windows := s.rateLimits()
	admitted := s.spooled(hookUserPromptSubmit) && s.replayed(id)
	p.set(SurfaceSocketLine, observed(sent && turn, sent && admitted))
	p.set(SurfaceSocketDir, observed(socket != "", s.private(socket)))
	p.set(SurfaceUnifiedWindows, observed(hasLimit, windows))
	p.set(SurfaceCommandLifecycle, observed(turn, s.saw("command_lifecycle")))
	// A sign-in nobody could read is a subscription, as it is everywhere else, so only an API key's
	// probe leaves the login unchecked.
	if p.spec.SignIn != ports.SignInAPIKey {
		login := init.APIKeySource == "none" && init.MessagingSocket != "" && hasLimit
		p.set(SurfacePrintKeepsLogin, observed(hasInit, login))
	}
}

// refuse sends with a reply address to a session that refuses, which answers there and nowhere
// else. A session that answers nothing before its first turn gets one, and a second message.
func (p *prober) refuse(ctx context.Context) {
	s, err := p.start(ctx, inboundRefuse)
	if err != nil {
		return
	}
	defer s.end()
	socket := s.awaitSocket(ctx)
	if !s.private(socket) {
		return
	}
	receipts := make(chan string, 4)
	r, err := openReceiver(ports.SessionSpec{TempDir: s.sockets, Generation: 1}, true, func(input string) {
		select {
		case receipts <- input:
		default:
		}
	})
	if err != nil {
		return
	}
	defer r.close()
	refused := func(id string) bool {
		deadline := time.After(probeReceiptWait)
		for {
			select {
			case got := <-receipts:
				if got == id {
					return true
				}
			case <-deadline:
				return false
			case <-ctx.Done():
				return false
			}
		}
	}
	id, sent := s.message(ctx, socket, r.from())
	saw := sent && refused(id)
	if !saw && sent && s.prompt(ctx) && s.awaitTurn(ctx) {
		id, sent = s.message(ctx, socket, r.from())
		saw = sent && refused(id)
	}
	p.set(SurfaceReceipts, observed(sent, saw))
}

// hold sends to a session that holds, after a turn, which is when the spikes saw the stream say so.
// The end of stdin drops a held message, and the stream says that too.
func (p *prober) hold(ctx context.Context) {
	s, err := p.start(ctx, inboundHold)
	if err != nil {
		return
	}
	defer s.end()
	socket := s.awaitSocket(ctx)
	if !s.prompt(ctx) || !s.awaitTurn(ctx) {
		return
	}
	if socket == "" {
		socket = s.initSocket()
	}
	if socket == "" {
		return
	}
	id, sent := s.message(ctx, socket, "")
	if !sent {
		return
	}
	held := s.waitFor(ctx, probeHoldWait, func() bool { return s.held(id) })
	if !held {
		s.closeInput()
		s.awaitExit()
		held = s.held(id)
	}
	p.set(SurfacePeerMessageHold, observed(true, held))
}

// screens starts a pane in a new directory and runs the start check on it, which answers the trust
// dialog only on an exact match and stops on any screen it does not know. No prompt is sent.
func (p *prober) screens(ctx context.Context) {
	terminal := p.d.terminal
	if terminal == nil {
		return
	}
	dir, config := p.dirs("screens")
	sockets := filepath.Join(p.spec.TempDir, "p")
	for _, path := range []string{dir, config, sockets} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return
		}
	}
	token, err := newUUID()
	if err != nil {
		return
	}
	settings, mcp, err := writeProbeFiles(config, p.spec.Binary, inboundAccept)
	if err != nil {
		return
	}
	launch := filepath.Join(config, "launch.json")
	args := append([]string{"--permission-mode", "default", "--model", p.spec.Model, "--settings", settings, "--mcp-config", mcp}, p.spec.Override.PaneArgs()...)
	env := append(p.env(config, token, sockets), p.spec.Override.Env()...)
	if err := shimlink.WritePaneLaunch(launch, shimlink.PaneLaunch{Dir: dir, Path: p.binary, Args: args, Env: env}); err != nil {
		return
	}
	// One name for every probe's pane, so a probe closes what a worker that died during the last one
	// left, before it opens its own.
	name := probePane
	_ = terminal.Close(ctx, name)
	if _, err := terminal.Open(ctx, ports.PaneSpec{
		Name: name, Dir: dir, Width: paneWidth, Height: paneHeight, Command: []string{p.spec.Binary, lanePaneCommand, launch},
	}); err != nil {
		return
	}
	defer p.closePane(context.WithoutCancel(ctx), name)
	checking, stop := context.WithCancel(ctx)
	died := p.watchPane(checking, stop, name)
	err = runStartCheck(checking, terminal, name, startCheck{mode: checkFresh, timeout: probeScreenWait, poll: checkPoll, settle: checkSettle})
	stop()
	dead := <-died
	switch {
	case err == nil:
		p.set(SurfaceStartScreens, lanes.CheckPassed)
	case dead, errors.Is(err, ports.ErrUnknownScreen):
		p.set(SurfaceStartScreens, lanes.CheckFailed)
	}
}

// watchPane ends a start check whose session exited, because a dead pane keeps its last screen and
// the check would read it until its wait ran out, holding every lane that waits on the probe. It
// reports whether the pane died, once the check has stopped.
func (p *prober) watchPane(ctx context.Context, stop context.CancelFunc, name string) <-chan bool {
	died := make(chan bool, 1)
	go func() {
		for ctx.Err() == nil {
			pane, err := p.d.terminal.Pane(ctx, name)
			if errors.Is(err, ports.ErrNotFound) || (err == nil && pane.Dead) {
				stop()
				died <- true
				return
			}
			pause(ctx, probePoll)
		}
		died <- false
	}()
	return died
}

// closePane ends the probe's pane by SIGINT, which ended an interactive process in the spikes, and
// removes it either way.
func (p *prober) closePane(ctx context.Context, name string) {
	terminal := p.d.terminal
	_ = terminal.Signal(ctx, name, ports.SignalInterrupt)
	deadline := time.Now().Add(probeExitWait)
	for time.Now().Before(deadline) {
		pane, err := terminal.Pane(ctx, name)
		if err != nil || pane.Dead {
			break
		}
		time.Sleep(probePoll)
	}
	_ = terminal.Close(ctx, name)
}

// dirs makes the names of one session's working directory and its generated files.
func (p *prober) dirs(name string) (dir, config string) {
	return filepath.Join(p.spec.Dir, name), filepath.Join(p.spec.Dir, name+"-config")
}

// env is a probe session's environment: the allowlist with no session or telemetry variable, the
// private socket directory, and tags whose hooks spool to the probe and reach no worker.
func (p *prober) env(config, token, sockets string) []string {
	var env []string
	for _, entry := range withoutTelemetry(p.spec.Env) {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, lanes.TagPrefix) || name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		envTempDir+sockets, lanes.EnvSpool+"="+filepath.Join(config, "spool"), lanes.EnvLaneToken+"="+token,
		lanes.EnvLaneID+"="+probeLane, lanes.EnvGeneration+"=1", EnvProbe+"=1",
	)
}

// probeFiles are what a probe session's `--settings` and `--mcp-config` hold: the two hooks a check
// reads, the inbound policy under test, and no server.
func writeProbeFiles(config, binary, inbound string) (settings, mcp string, err error) {
	file := settingsFile{Permissions: permissions{Allow: []string{}}, CrossSessionInbound: inbound, Hooks: map[string][]hookGroup{}}
	for _, event := range []string{hookSessionStart, hookUserPromptSubmit} {
		command := hookCommand{Type: "command", Command: lanes.ShellQuote(binary) + " hook " + event, Timeout: hookTimeoutSeconds}
		file.Hooks[event] = []hookGroup{{Hooks: []hookCommand{command}}}
	}
	settings, mcp = filepath.Join(config, "settings.json"), filepath.Join(config, "mcp.json")
	for path, body := range map[string]any{settings: file, mcp: mcpFile{Servers: map[string]mcpServer{}}} {
		raw, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return "", "", err
		}
		if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
			return "", "", err
		}
	}
	return settings, mcp, nil
}

// start launches one headless probe session. It runs as this process's child with no shim: a probe
// is short, and a worker that dies takes its stdin with it, which ends the session.
func (p *prober) start(ctx context.Context, inbound string) (*probeSession, error) {
	p.count++
	name := inbound
	dir, config := p.dirs(name)
	sockets := filepath.Join(p.spec.TempDir, strconv.Itoa(p.count))
	for _, path := range []string{dir, config, sockets} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, err
		}
	}
	token, err := newUUID()
	if err != nil {
		return nil, err
	}
	settings, mcp, err := writeProbeFiles(config, p.spec.Binary, inbound)
	if err != nil {
		return nil, err
	}
	args := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--replay-user-messages",
		"--permission-mode", "default", "--model", p.spec.Model, "--settings", settings, "--mcp-config", mcp, "--tools", "",
	}
	args = append(args, p.spec.Override.Args()...)
	env := append(p.env(config, token, sockets), p.spec.Override.Env()...)
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		_ = inR.Close()
		_ = inW.Close()
		return nil, err
	}
	stderr, err := os.OpenFile(filepath.Join(config, "stderr"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		for _, f := range []*os.File{inR, inW, outR, outW} {
			_ = f.Close()
		}
		return nil, err
	}
	process, err := p.d.runner.Start(ctx, ports.ProcessSpec{Path: p.binary, Args: args, Dir: dir, Env: env, Stdin: inR, Stdout: outW, Stderr: stderr})
	_ = inR.Close()
	_ = outW.Close()
	_ = stderr.Close()
	if err != nil {
		_ = inW.Close()
		_ = outR.Close()
		return nil, err
	}
	log, err := probeStreamLog(p.spec.Override)
	if err != nil {
		log = nil
	}
	s := &probeSession{
		process: process, input: inW, sockets: sockets, spool: filepath.Join(config, "spool"), token: token,
		turns: make(chan struct{}, 8), exited: make(chan struct{}), read: make(chan struct{}),
	}
	go s.readStream(outR, log)
	go func() {
		_, _ = process.Wait()
		close(s.exited)
	}()
	return s, nil
}

// probeStreamLog copies a probe's streams beside a real-session suite's lane stream, so the suite's
// guards read them. It is nil outside that build.
func probeStreamLog(override lanes.SessionOverride) (io.WriteCloser, error) {
	path := override.StreamLog()
	if path == "" {
		return nil, nil
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// probeSession is one headless session of a probe, and everything its stream said.
type probeSession struct {
	process ports.Process
	input   *os.File
	sockets string
	spool   string
	token   string
	turns   chan struct{}
	exited  chan struct{}
	read    chan struct{}

	// mu guards the stream's lines. inMu guards stdin apart from it, so a write that waits on a full
	// pipe never holds back the reader that would drain the other one.
	mu       sync.Mutex
	lines    []probeLine
	inMu     sync.Mutex
	inClosed bool
}

// probeLine holds the fields of a stream line a check reads.
type probeLine struct {
	Type            string         `json:"type"`
	Subtype         string         `json:"subtype"`
	UUID            string         `json:"uuid"`
	IsReplay        bool           `json:"isReplay"`
	Origin          *probeOrigin   `json:"origin"`
	APIKeySource    string         `json:"apiKeySource"`
	MessagingSocket string         `json:"messaging_socket_path"`
	MessageUUID     string         `json:"message_uuid"`
	RateLimit       *rateLimitInfo `json:"rate_limit_info"`
}

// probeOrigin is who a replayed message came from.
type probeOrigin struct {
	Kind string `json:"kind"`
}

func (l probeLine) hasWindows() bool {
	if l.RateLimit == nil {
		return false
	}
	for _, window := range l.RateLimit.Windows {
		if window.Utilization != nil {
			return true
		}
	}
	return false
}

func (s *probeSession) readStream(out *os.File, log io.WriteCloser) {
	defer close(s.read)
	defer out.Close()
	if log != nil {
		defer log.Close()
	}
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 1<<16), 1<<24)
	for scanner.Scan() {
		if log != nil {
			_, _ = log.Write(append(scanner.Bytes(), '\n'))
		}
		var line probeLine
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		s.mu.Lock()
		s.lines = append(s.lines, line)
		s.mu.Unlock()
		if line.Type == "result" {
			select {
			case s.turns <- struct{}{}:
			default:
			}
		}
	}
}

// waitFor polls a condition until it holds, the wait runs out, or the session ends.
func (s *probeSession) waitFor(ctx context.Context, limit time.Duration, done func() bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if done() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-s.read:
			return done()
		case <-time.After(probePoll):
		}
	}
	return done()
}

// awaitSocket finds the session's messaging socket before its first prompt: bound in the private
// directory under its process's ID, or named by its SessionStart hook wherever it went.
func (s *probeSession) awaitSocket(ctx context.Context) string {
	own := filepath.Join(s.sockets, socketsDir, strconv.Itoa(s.process.PID())+".sock")
	var found string
	s.waitFor(ctx, probeSocketWait, func() bool {
		if info, err := os.Stat(own); err == nil && info.Mode()&os.ModeSocket != 0 {
			found = own
			return true
		}
		for _, event := range s.hooks() {
			if event.Event == hookSessionStart && event.Socket != "" {
				found = event.Socket
				return true
			}
		}
		return false
	})
	return found
}

// private reports a socket in the session's own directory, under the platform's path limit.
func (s *probeSession) private(socket string) bool {
	return socket != "" && filepath.Dir(socket) == filepath.Join(s.sockets, socketsDir) && len(socket) <= maxSocketPath
}

// message writes the probe's message to the socket, opening with a first line a hook can name.
func (s *probeSession) message(ctx context.Context, socket, from string) (string, bool) {
	id, err := newUUID()
	if err != nil {
		return "", false
	}
	text := "[ropeshift message " + probeMessageID + " kind=probe age=0m00s tier=fresh from=worker]\n" + probePrompt
	_, err = writeSocket(ctx, probeLane, socket, s.process.PID(), ports.Input{ID: id, Text: text}, from)
	return id, err == nil
}

// prompt opens a turn on stdin, which is documented, so the stream's own checks run whatever the
// socket did.
func (s *probeSession) prompt(context.Context) bool {
	id, err := newUUID()
	if err != nil {
		return false
	}
	raw, err := json.Marshal(inputLine{Type: "user", Message: inputMessage{Role: "user", Content: probePrompt}, UUID: id})
	if err != nil {
		return false
	}
	s.inMu.Lock()
	defer s.inMu.Unlock()
	if s.inClosed {
		return false
	}
	_, err = s.input.Write(append(raw, '\n'))
	return err == nil
}

// awaitTurn waits for the next turn's `result`.
func (s *probeSession) awaitTurn(ctx context.Context) bool {
	select {
	case <-s.turns:
		return true
	case <-s.read:
		select {
		case <-s.turns:
			return true
		default:
			return false
		}
	case <-ctx.Done():
		return false
	case <-time.After(probeTurnWait):
		return false
	}
}

func (s *probeSession) snapshot() []probeLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]probeLine(nil), s.lines...)
}

func (s *probeSession) firstInit() (probeLine, bool) {
	for _, line := range s.snapshot() {
		if line.Type == "system" && line.Subtype == "init" {
			return line, true
		}
	}
	return probeLine{}, false
}

func (s *probeSession) initSocket() string {
	init, _ := s.firstInit()
	return init.MessagingSocket
}

// rateLimits reports a `rate_limit_event`, and whether any of them carried a window's figure.
func (s *probeSession) rateLimits() (seen, windows bool) {
	for _, line := range s.snapshot() {
		if line.Type == "rate_limit_event" && line.RateLimit != nil {
			seen = true
			windows = windows || line.hasWindows()
		}
	}
	return seen, windows
}

// replayed reports the headless acknowledgement of a socket message: its own `uuid`, marked as a
// peer's.
func (s *probeSession) replayed(id string) bool {
	for _, line := range s.snapshot() {
		if line.Type == "user" && line.IsReplay && line.UUID == id && line.Origin != nil && line.Origin.Kind == "peer" {
			return true
		}
	}
	return false
}

func (s *probeSession) saw(kind string) bool {
	for _, line := range s.snapshot() {
		if line.Type == kind || (line.Type == "system" && line.Subtype == kind) {
			return true
		}
	}
	return false
}

func (s *probeSession) held(id string) bool {
	for _, line := range s.snapshot() {
		if line.Type == "system" && line.Subtype == "peer_message_hold" && line.MessageUUID == id {
			return true
		}
	}
	return false
}

// hooks reads what the session's hooks spooled. A line that does not carry this session's seal is
// not its own and is skipped.
func (s *probeSession) hooks() []lanes.HookEvent {
	raw, err := os.ReadFile(s.spool)
	if err != nil {
		return nil
	}
	digest := lanes.TokenDigest(s.token)
	var out []lanes.HookEvent
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		event, err := lanes.OpenHook([]byte(line), func(string) (string, error) { return digest, nil })
		if err == nil {
			out = append(out, event)
		}
	}
	return out
}

// spooled reports a hook that named the probe's message by its first line, which is the
// admission signal in both run modes.
func (s *probeSession) spooled(event string) bool {
	for _, hook := range s.hooks() {
		if hook.Event == event && hook.Message == probeMessageID {
			return true
		}
	}
	return false
}

func (s *probeSession) closeInput() {
	s.inMu.Lock()
	defer s.inMu.Unlock()
	if !s.inClosed {
		s.inClosed = true
		_ = s.input.Close()
	}
}

func (s *probeSession) awaitExit() bool {
	select {
	case <-s.exited:
		<-s.read
		return true
	case <-time.After(probeExitWait):
		return false
	}
}

// end closes stdin, which ends a session between turns, and signals only the process it started
// when that is not enough.
func (s *probeSession) end() {
	s.closeInput()
	if s.awaitExit() {
		return
	}
	_ = s.process.Signal(ports.SignalInterrupt)
	if s.awaitExit() {
		return
	}
	_ = s.process.Signal(ports.SignalKill)
	s.awaitExit()
}
