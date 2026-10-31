// Package claudecode is the AgentDriver for the unmodified `claude` binary, in the
// headless run mode. It reads only the documented stream, flags, and settings, and
// never a transcript or a vendor credential.
package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
)

// Binary is the name the driver looks for on the lane's PATH.
const Binary = "claude"

// The variables the driver adds to a lane's environment.
const (
	envSessionState = "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1"
	envTempDir      = "CLAUDE_CODE_TMPDIR="
)

// A session's socket lands at <TempDir>/cc-socks/<pid>.sock, and a sockaddr_un holds 104
// bytes on macOS with its terminating NUL.
const (
	socketTail    = "/cc-socks/9999999.sock"
	maxSocketPath = 103
)

const (
	defaultStopGrace = 5 * time.Second
	versionTimeout   = 30 * time.Second
	eventBuffer      = 256
)

// ErrNoBinary means the lane's PATH holds no agent CLI.
var ErrNoBinary = errors.New("claude is not on PATH")

// Options configures a driver.
type Options struct {
	// Env is the environment of a version check. A session uses its spec's.
	Env []string
	// StopGrace is how long a session gets to end before the next signal.
	StopGrace time.Duration
}

// Driver runs headless sessions through a ProcessRunner.
type Driver struct {
	runner ports.ProcessRunner
	grace  time.Duration

	mu       sync.Mutex
	env      []string
	sessions map[string]*session
	listed   map[string][]listedSession
}

// New returns a driver.
func New(runner ports.ProcessRunner, options Options) *Driver {
	grace := options.StopGrace
	if grace == 0 {
		grace = defaultStopGrace
	}
	return &Driver{
		runner: runner, grace: grace, env: options.Env, sessions: map[string]*session{}, listed: map[string][]listedSession{},
	}
}

// UseEnv replaces the environment of a version check.
func (d *Driver) UseEnv(env []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.env = env
}

func (d *Driver) versionEnv() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.env
}

// findBinary searches the PATH a lane will run with, because the worker's own may
// differ. A spawn then names an absolute path, so nothing else chooses what runs.
func findBinary(env []string) (string, error) {
	for _, entry := range env {
		list, ok := strings.CutPrefix(entry, "PATH=")
		if !ok {
			continue
		}
		for _, dir := range filepath.SplitList(list) {
			candidate := filepath.Join(dir, Binary)
			if !filepath.IsAbs(candidate) {
				continue
			}
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate, nil
			}
		}
	}
	return "", ErrNoBinary
}

// version runs `claude --version`, which starts no session.
func (d *Driver) version(ctx context.Context, env []string) (string, error) {
	binary, err := findBinary(env)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	process, err := d.runner.Start(ctx, ports.ProcessSpec{Path: binary, Args: []string{"--version"}, Env: env, Stdout: &out})
	if err != nil {
		return "", err
	}
	finished := make(chan error, 1)
	go func() {
		_, err := process.Wait()
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			return "", err
		}
	case <-time.After(versionTimeout):
		_ = process.Signal(ports.SignalKill)
		return "", errors.New("claude --version did not answer")
	}
	return parseVersion(out.String()), nil
}

// Capabilities reports the design's values for the installed version. A version
// outside the matrix is marked, and the core shows `degraded_mode`.
func (d *Driver) Capabilities(ctx context.Context) (ports.Capabilities, error) {
	version, err := d.version(ctx, d.versionEnv())
	if err != nil {
		return ports.Capabilities{}, err
	}
	return ports.Capabilities{
		RunModes: []string{ports.RunModeHeadless}, ApprovalChannel: ports.ApprovalHook, DeferredTurn: true,
		Injection: ports.InjectionBetweenTools, ContextReport: ports.ContextLive, Compact: ports.CompactNative,
		NativeResume: true, UsageLimitReport: ports.UsageLimitResetTime,
		SignInKinds: []string{ports.SignInSubscription, ports.SignInAPIKey},
		Version:     version, Degraded: !Qualified(version),
	}, nil
}

// Doctor reports presence and version. Sign-in waits for the doctor unit, and it will
// use the vendor's status command and never a credential.
func (d *Driver) Doctor(ctx context.Context) (ports.DoctorReport, error) {
	version, err := d.version(ctx, d.versionEnv())
	if errors.Is(err, ErrNoBinary) {
		return ports.DoctorReport{Detail: err.Error()}, nil
	}
	if err != nil {
		return ports.DoctorReport{}, err
	}
	return ports.DoctorReport{Present: true, Version: version, Qualified: Qualified(version)}, nil
}

// Preflight checks a spec before anything starts.
func (d *Driver) Preflight(_ context.Context, spec ports.SessionSpec) (ports.PreflightReport, error) {
	var report ports.PreflightReport
	problem := func(format string, args ...any) {
		report.Problems = append(report.Problems, fmt.Sprintf(format, args...))
	}
	if spec.PermissionMode == "" {
		problem("no permission mode")
	}
	if spec.Model == "" {
		problem("no model")
	}
	if spec.ConfigDir == "" {
		problem("no directory for generated settings")
	}
	if info, err := os.Stat(spec.Dir); err != nil || !info.IsDir() {
		problem("no worktree at %s", spec.Dir)
	}
	if _, err := findBinary(spec.Env); err != nil {
		problem("%v", err)
	}
	if len(spec.TempDir)+len(socketTail) > maxSocketPath {
		problem("a session's socket under %s passes the %d-byte limit", spec.TempDir, maxSocketPath)
	}
	for _, entry := range spec.Env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			problem("the environment holds a session variable: %s", name)
		}
	}
	return report, nil
}

// args is the command the design names, with every start flag, so a resume states
// them again: neither `--settings` nor `--mcp-config` survives a resume.
func args(spec ports.SessionSpec) []string {
	settings, mcp := generatedPaths(spec)
	out := []string{
		"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
		"--include-partial-messages", "--replay-user-messages",
		"--permission-mode", spec.PermissionMode, "--model", spec.Model,
		"--settings", settings, "--mcp-config", mcp,
	}
	return append(out, spec.Override.Args()...)
}

// StartArgs is the command of a new session.
func StartArgs(spec ports.SessionSpec) []string { return args(spec) }

// ResumeArgs is the command of a resume, which is the same command with one more flag.
func ResumeArgs(spec ports.SessionSpec, from ports.ResumeRef) []string {
	return append(args(spec), "--resume", from.SessionID)
}

// Start launches a session and hands it the brief.
func (d *Driver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	return d.launch(ctx, spec, StartArgs(spec), lanes.CostMeter{})
}

// Resume continues an earlier session as a new generation, and its cost continues
// from the last running total.
func (d *Driver) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	return d.launch(ctx, spec, ResumeArgs(spec, from), lanes.CostMeter{SessionTotalUSD: from.SessionTotalUSD})
}

func (d *Driver) launch(ctx context.Context, spec ports.SessionSpec, arguments []string, meter lanes.CostMeter) (ports.SessionHandle, error) {
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
	env := sessionEnv(spec, Qualified(version))
	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return ports.SessionHandle{}, err
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		return ports.SessionHandle{}, err
	}
	stderr, err := openStderr(spec)
	if err != nil {
		return ports.SessionHandle{}, err
	}
	process, err := d.runner.Start(ctx, ports.ProcessSpec{
		Path: binary, Args: arguments, Dir: spec.Dir, Env: env, Stdin: stdinRead, Stdout: stdoutWrite, Stderr: stderr,
	})
	_ = stdinRead.Close()
	_ = stdoutWrite.Close()
	_ = stderr.Close()
	if err != nil {
		_ = stdinWrite.Close()
		_ = stdoutRead.Close()
		return ports.SessionHandle{}, err
	}
	s := &session{
		handle:  ports.SessionHandle{ID: sessionHandleID(spec), PID: process.PID()},
		process: process, stdin: stdinWrite, events: make(chan ports.DriverEvent, eventBuffer), done: make(chan struct{}),
		meter: meter,
	}
	log, err := openStreamLog(spec)
	if err != nil {
		_ = process.Signal(ports.SignalKill)
		return ports.SessionHandle{}, err
	}
	d.mu.Lock()
	d.sessions[s.handle.ID] = s
	d.mu.Unlock()
	go s.read(stdoutRead, log)
	if spec.Brief != "" {
		if _, err := s.write(ports.Input{Text: spec.Brief}); err != nil {
			_ = process.Signal(ports.SignalKill)
			return ports.SessionHandle{}, err
		}
	}
	return s.handle, nil
}

func sessionHandleID(spec ports.SessionSpec) string {
	return fmt.Sprintf("%s/%d", spec.Lane, spec.Generation)
}

// sessionEnv adds the driver's own variables. An undocumented one goes only to a
// version that passed its check.
func sessionEnv(spec ports.SessionSpec, qualified bool) []string {
	env := append(slices.Clone(spec.Env), envSessionState)
	if spec.TempDir != "" && qualified {
		env = append(env, envTempDir+spec.TempDir)
	}
	return env
}

// openStderr keeps a session's stderr beside its generated files.
func openStderr(spec ports.SessionSpec) (*os.File, error) {
	return os.OpenFile(filepath.Join(spec.ConfigDir, fmt.Sprintf("stderr-%d.log", spec.Generation)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// openStreamLog keeps a copy of the raw stream for a real-session suite's stop
// conditions. Outside the realsession build the path is always empty.
func openStreamLog(spec ports.SessionSpec) (io.WriteCloser, error) {
	path := spec.Override.StreamLog()
	if path == "" {
		return nil, nil
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}

// session is one running process.
type session struct {
	handle  ports.SessionHandle
	process ports.Process
	events  chan ports.DriverEvent
	done    chan struct{}

	mu       sync.Mutex
	stdin    *os.File
	stopping bool
	inFlight int
	meter    lanes.CostMeter
	seen     ports.Observation
}

// read is the one reader of the stream. It ends when the process closes stdout.
func (s *session) read(stdout *os.File, log io.WriteCloser) {
	defer stdout.Close()
	if log != nil {
		defer log.Close()
	}
	reader := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) != 0 {
			if log != nil {
				_, _ = log.Write(line)
			}
			s.take(line)
		}
		if err != nil {
			break
		}
	}
	s.finish()
}

func (s *session) take(line []byte) {
	p, err := parseLine(line)
	if err != nil {
		s.mu.Lock()
		s.seen.Malformed++
		s.mu.Unlock()
		s.events <- ports.DriverEvent{Kind: ports.EventMalformed}
		return
	}
	if !p.useful {
		return
	}
	s.mu.Lock()
	switch p.event.Kind {
	case ports.EventSessionBound:
		s.seen.SessionID, s.seen.ModelResolved = p.event.SessionID, p.event.ModelResolved
	case ports.EventUsage:
		s.seen.ContextTokens = p.event.ContextTokens
	}
	if p.turnEnded {
		s.inFlight = max(s.inFlight-1, 0)
		if p.stopped {
			s.meter.ObserveStopped()
		} else {
			p.event.Turn.CostUSD = s.meter.ObserveTurn(p.total)
		}
		p.event.Turn.Cost = s.meter
	}
	s.mu.Unlock()
	s.events <- p.event
}

// finish reaps the process and reports how it ended. No `result` for an input means
// the process died mid-turn, unless someone asked it to stop.
func (s *session) finish() {
	status, err := s.process.Wait()
	s.mu.Lock()
	crashed := !s.stopping && (s.inFlight > 0 || status.Code != 0 || status.Signal != "" || err != nil)
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	s.mu.Unlock()
	s.events <- ports.DriverEvent{Kind: ports.EventSessionEnded, End: &ports.SessionEnd{Crashed: crashed, Exit: status}}
	close(s.events)
	close(s.done)
}

// inputLine is one stream-json input. The driver builds it from a typed value, so a
// line that would end the session cannot be written.
type inputLine struct {
	Type    string       `json:"type"`
	Message inputMessage `json:"message"`
	UUID    string       `json:"uuid"`
}

type inputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	text := hex.EncodeToString(raw[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:], nil
}

// write is the one writer of stdin. It counts the input before it writes, so a process
// that dies at once still reads as having an input in flight.
func (s *session) write(in ports.Input) (ports.SendReceipt, error) {
	if in.ID == "" {
		id, err := newUUID()
		if err != nil {
			return ports.SendReceipt{}, err
		}
		in.ID = id
	}
	raw, err := json.Marshal(inputLine{Type: "user", Message: inputMessage{Role: "user", Content: in.Text}, UUID: in.ID})
	if err != nil {
		return ports.SendReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin == nil {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w", s.handle.ID, ports.ErrNoSession)
	}
	s.inFlight++
	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		s.inFlight--
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: %v", s.handle.ID, ports.ErrNoSession, err)
	}
	return ports.SendReceipt{ID: in.ID, Path: ports.SendStdin}, nil
}

func (d *Driver) session(h ports.SessionHandle) (*session, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.sessions[h.ID]
	if !ok {
		return nil, fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
	}
	return s, nil
}

// Observe returns what the stream showed. Only a reaped process reads dead, and a
// handle this driver does not know reads unknown.
func (d *Driver) Observe(_ context.Context, h ports.SessionHandle) (ports.Observation, error) {
	s, err := d.session(h)
	if err != nil {
		return ports.Observation{Liveness: ports.LivenessUnknown}, err
	}
	s.mu.Lock()
	out := s.seen
	s.mu.Unlock()
	out.Liveness = ports.LivenessAlive
	select {
	case <-s.done:
		out.Liveness = ports.LivenessDead
	default:
	}
	return out, nil
}

// Events returns the session's stream.
func (d *Driver) Events(_ context.Context, h ports.SessionHandle) (<-chan ports.DriverEvent, error) {
	s, err := d.session(h)
	if err != nil {
		return nil, err
	}
	return s.events, nil
}

// Send hands one input to the session on stdin.
func (d *Driver) Send(_ context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	s, err := d.session(h)
	if err != nil {
		return ports.SendReceipt{}, err
	}
	return s.write(in)
}

// Decide has no path yet: an approval returns through the waiting hook.
func (d *Driver) Decide(context.Context, ports.SessionHandle, ports.ApprovalDecision) error {
	return ports.ErrUnsupported
}

// Compact sends the native command as a stream-json message.
func (d *Driver) Compact(ctx context.Context, h ports.SessionHandle) error {
	_, err := d.Send(ctx, h, ports.Input{Text: "/compact"})
	return err
}

// Stop ends a session. Signals go only to this session's recorded process. A session
// that does not end within the grace takes the next signal, and SIGKILL is last.
func (d *Driver) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	s, err := d.session(h)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	switch how {
	case ports.StopInterrupt:
		if err := s.process.Signal(ports.SignalInterrupt); err != nil {
			return err
		}
		s.closeStdin()
	case ports.StopAtRest:
		s.closeStdin()
	case ports.StopNow:
		s.closeStdin()
		if err := s.process.Signal(ports.SignalKill); err != nil {
			return err
		}
	}
	for _, next := range []ports.Signal{ports.SignalTerminate, ports.SignalKill} {
		select {
		case <-s.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.grace):
		}
		if err := s.process.Signal(next); err != nil {
			return err
		}
	}
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *session) closeStdin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
}

var _ ports.AgentDriver = (*Driver)(nil)
