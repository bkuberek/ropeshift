// Package claudecode is the AgentDriver for the unmodified `claude` binary, in both run
// modes: headless under `ropeshift lane-shim`, and interactive in a pane on a private
// terminal server. Either way a session outlives its worker. It reads only the documented
// stream, flags, hooks, and settings, and never a transcript or a vendor credential.
package claudecode

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"ropeshift.com/ropeshift/internal/api/shimlink"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
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
	// shimStartLimit bounds the wait for a new shim's first answer.
	shimStartLimit = 30 * time.Second
	// shimDialDeadline bounds one try at reaching a shim.
	shimDialDeadline = time.Second
	// lostShimGrace is how long a session gets to finish its turn after its shim dies.
	// Its stdin closed, so it ends by itself, and the spike saw that take seconds.
	lostShimGrace = time.Minute
	// lostPoll is how often a session whose shim died is checked for its end.
	lostPoll = 50 * time.Millisecond
)

// ErrSessionExists means a shim already holds this generation, so a retry that reached
// this far must look for it and not start a second.
var ErrSessionExists = errors.New("a shim already holds this generation")

// ErrNoBinary means the lane's PATH holds no agent CLI.
var ErrNoBinary = errors.New("claude is not on PATH")

// Options configures a driver.
type Options struct {
	// Env is the environment of a version check. A session uses its spec's.
	Env []string
	// StopGrace is how long a session gets to end before the next signal.
	StopGrace time.Duration
	// ShimLinger is how long a shim stays after its session ends. Zero takes the shim's
	// default.
	ShimLinger time.Duration
	// JournalCap bounds a shim's journal in bytes. Zero takes the shim's default.
	JournalCap int64
	// Terminal runs interactive sessions. Nil leaves the driver headless only.
	Terminal ports.Terminal
}

// Driver runs sessions, each under a shim or in a pane, so each outlives this process.
type Driver struct {
	runner   ports.ProcessRunner
	terminal ports.Terminal
	grace    time.Duration
	linger   time.Duration
	cap      int64

	mu  sync.Mutex
	env []string
	// current is what a worker's probe observed of one version on this machine. Once a worker has
	// handed one over, it alone qualifies a version, and only the version it names.
	current  lanes.Qualification
	sessions map[string]liveSession
	// policies names the file each session's hooks read.
	policies map[string]string
	listed   map[string][]listedSession
}

// liveSession is one session this process follows, in either run mode.
type liveSession interface {
	// stream is the session's events.
	stream() <-chan ports.DriverEvent
	// ended closes when the session's end has been read.
	ended() <-chan struct{}
	// observe reads the session, and an incomplete read is unknown.
	observe(ctx context.Context) ports.Observation
	// send hands the session one input.
	send(ctx context.Context, in ports.Input) (ports.SendReceipt, error)
	// compact asks for the native compaction.
	compact(ctx context.Context) error
	// beginStop asks the session to end, and later steps escalate.
	beginStop(ctx context.Context, how ports.StopMode)
	// escalate sends the next signal of a stop.
	escalate(ctx context.Context, signal ports.Signal)
	// takeHook hands the session what one of its hooks reported.
	takeHook(event lanes.HookEvent)
}

// New returns a driver.
func New(runner ports.ProcessRunner, options Options) *Driver {
	grace := options.StopGrace
	if grace == 0 {
		grace = defaultStopGrace
	}
	return &Driver{
		runner: runner, terminal: options.Terminal, grace: grace, linger: options.ShimLinger, cap: options.JournalCap, env: options.Env,
		sessions: map[string]liveSession{}, policies: map[string]string{}, listed: map[string][]listedSession{},
	}
}

// Qualify hands the driver what a probe observed of a version on this machine. From then on a
// version qualifies only through it, so a version that changes under a worker runs on documented
// surfaces until its own probe ends, whichever way the last one went.
func (d *Driver) Qualify(q lanes.Qualification) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.current = q
}

// qualificationFor is what holds for a version this driver is about to run. Before a worker has
// handed over a probe's result, the shipped matrix answers, as it does for the doctor.
func (d *Driver) qualificationFor(version string) lanes.Qualification {
	d.mu.Lock()
	current := d.current
	d.mu.Unlock()
	interactive := d.terminal != nil
	switch {
	case current.Version == "":
		return judged(MatrixRow(version), interactive)
	case current.For(version):
		return judged(current, interactive)
	}
	return unqualified(version)
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

// version runs `claude --version`, which starts no session. Only a version it prints counts: a CLI that
// cannot start, does not answer, exits with a failure, ends on a signal, or prints no version has failed
// its check, and nothing runs on it.
func (d *Driver) version(ctx context.Context, env []string) (string, error) {
	binary, err := findBinary(env)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	process, err := d.runner.Start(ctx, ports.ProcessSpec{Path: binary, Args: []string{"--version"}, Env: env, Stdout: &out})
	if err != nil {
		return "", versionFailed("could not start: %v", withoutPath(err))
	}
	type ending struct {
		status ports.ExitStatus
		err    error
	}
	finished := make(chan ending, 1)
	go func() {
		status, err := process.Wait()
		finished <- ending{status, err}
	}()
	select {
	case end := <-finished:
		switch {
		case end.err != nil:
			return "", versionFailed("could not be read: %v", end.err)
		case end.status.Signal != "":
			return "", versionFailed("ended on signal %s", end.status.Signal)
		case end.status.Code != 0:
			return "", versionFailed("exited with status %d", end.status.Code)
		}
	case <-time.After(versionTimeout):
		_ = process.Signal(ports.SignalKill)
		return "", versionFailed("did not answer within %v", versionTimeout)
	}
	version := parseVersion(out.String())
	if version == "" {
		return "", versionFailed("printed no version")
	}
	return version, nil
}

// versionFailed says how `claude --version` failed. It names the command and never the binary's path,
// because a doctor's detail reaches the ledger.
func versionFailed(format string, args ...any) error {
	return fmt.Errorf(Binary+" --version "+format, args...)
}

// withoutPath is why a start failed, without the path an operating system's error names.
func withoutPath(err error) error {
	var path *fs.PathError
	if errors.As(err, &path) {
		return path.Err
	}
	return err
}

// Capabilities reports the design's values for the installed version. A version
// outside the matrix is marked, and the core shows `degraded_mode`.
func (d *Driver) Capabilities(ctx context.Context) (ports.Capabilities, error) {
	version, err := d.version(ctx, d.versionEnv())
	if err != nil {
		return ports.Capabilities{}, err
	}
	q := d.qualificationFor(version)
	use := UseOf(q)
	var modes []string
	// A probe on a subscription, or on a sign-in nobody could read, that did not see `-p` keep its
	// login leaves no headless lane, and the tmux fallback in ADR-0002 is what is left. A version no
	// probe checked keeps the mode, and so does an API key, whose lanes never needed the login.
	if q.Source != lanes.QualifiedByProbe || q.SignIn == ports.SignInAPIKey || use.HeadlessOnSubscription {
		modes = append(modes, ports.RunModeHeadless)
	}
	if d.terminal != nil {
		modes = append(modes, ports.RunModeInteractive)
	}
	injection := ports.InjectionBetweenTools
	if !use.PeerInjection {
		injection = ports.InjectionNone
	}
	return ports.Capabilities{
		RunModes: modes, ApprovalChannel: ports.ApprovalHook, DeferredTurn: true,
		Injection: injection, ContextReport: ports.ContextLive, Compact: ports.CompactNative,
		NativeResume: true, UsageLimitReport: ports.UsageLimitResetTime,
		SignInKinds: []string{ports.SignInSubscription, ports.SignInAPIKey},
		Version:     version, Degraded: q.Degraded, Resume: resumeCosts,
		Subagents: roster.SubagentsNative, SubagentTools: roster.ToolsEnforced,
	}, nil
}

// resumeCosts is this driver's data for the resume table (D12). The documented cache lasts an
// hour for a main conversation on a subscription and five minutes with an API key. A ratio is a
// model's one-hour write price over its cache read price, taken from list prices, so on a
// subscription it is a guide. A model with none skips the table's row.
var resumeCosts = lanes.ResumeCosts{
	Lifetimes: map[string]time.Duration{ports.SignInSubscription: time.Hour, ports.SignInAPIKey: 5 * time.Minute},
	Ratios:    map[string]int64{"claude-haiku-5-5": 20, "claude-opus-5-5": 40, "claude-sonnet-5-5": 40},
}

// DriverName is what a work order's needs call this driver.
const DriverName = "claude-code"

// Doctor reports presence, version, qualification, and sign-in. It runs the vendor's status
// command and reads no credential, and only the kind leaves this function. A CLI that is not on
// PATH and one that failed its version check are both not present for any work, and the detail says
// which, so the sign-in of a CLI that cannot run is never read.
func (d *Driver) Doctor(ctx context.Context) (ports.DoctorReport, error) {
	version, err := d.version(ctx, d.versionEnv())
	if err != nil {
		return ports.DoctorReport{Driver: DriverName, SignInStatus: lanes.SignInUnknown, Detail: err.Error()}, nil
	}
	kind, status := d.signIn(ctx)
	return ports.DoctorReport{
		Driver: DriverName, Present: true, Version: version, Qualified: !d.qualificationFor(version).Degraded, SignIn: kind, SignInStatus: status,
	}, nil
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
	if spec.PermissionMode == lanes.BypassMode {
		problem("permission mode %s: %v", spec.PermissionMode, lanes.ErrBypassMode)
	}
	if spec.Model == "" {
		problem("no model")
	}
	if spec.ConfigDir == "" {
		problem("no directory for generated settings")
	}
	if spec.Assistant && len(spec.Commands) == 0 {
		problem("an assistant's session names no command its shell may run")
	}
	if info, err := os.Stat(spec.Dir); err != nil || !info.IsDir() {
		problem("no worktree at %s", spec.Dir)
	}
	if _, err := findBinary(spec.Env); err != nil {
		problem("%v", err)
	}
	if spec.Binary == "" {
		problem("no ropeshift binary to run the shim")
	}
	if spec.SystemPromptFile != "" {
		if _, err := os.Stat(spec.SystemPromptFile); err != nil {
			problem("no context pack at %s", spec.SystemPromptFile)
		}
	}
	if spec.RunMode == ports.RunModeInteractive && d.terminal == nil {
		problem("no terminal for an interactive session")
	}
	for _, found := range inboundProblems(spec) {
		problem("%s", found)
	}
	if len(spec.TempDir)+len(socketTail) > maxSocketPath {
		problem("a session's socket under %s passes the %d-byte limit", spec.TempDir, maxSocketPath)
	}
	if socket := shimSocket(spec); len(socket) > maxSocketPath {
		problem("the shim's socket at %s passes the %d-byte limit", socket, maxSocketPath)
	}
	for _, entry := range spec.Env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "CLAUDECODE" || strings.HasPrefix(name, "CLAUDE_CODE_") {
			problem("the environment holds a session variable: %s", name)
		}
	}
	for _, found := range agentProblems(spec) {
		problem("%s", found)
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
	return append(append(out, packArgs(spec)...), spec.Override.Args()...)
}

// packArgs states what every start and every resume says of a session beyond its files: its pack,
// which sits in its system prompt, so generations of one version share a cache, and the vendor tools
// it holds, which an MCP server's tools outlast.
func packArgs(spec ports.SessionSpec) []string {
	var out []string
	if spec.OnlyTools {
		out = append(out, "--tools", "")
	}
	if spec.SystemPromptFile != "" {
		out = append(out, "--append-system-prompt-file", spec.SystemPromptFile)
	}
	return append(out, agentArgs(spec)...)
}

// StartArgs is the command of a new session.
func StartArgs(spec ports.SessionSpec) []string { return args(spec) }

// ResumeArgs is the command of a resume, which is the same command with one more flag.
func ResumeArgs(spec ports.SessionSpec, from ports.ResumeRef) []string {
	return append(args(spec), "--resume", from.SessionID)
}

// Start launches a session and hands it the brief.
func (d *Driver) Start(ctx context.Context, spec ports.SessionSpec) (ports.SessionHandle, error) {
	if spec.PermissionMode == lanes.BypassMode {
		return ports.SessionHandle{}, lanes.ErrBypassMode
	}
	if spec.RunMode == ports.RunModeInteractive {
		return d.launchPane(ctx, spec, paneArgs(spec), checkFresh, lanes.CostMeter{})
	}
	return d.launch(ctx, spec, StartArgs(spec), lanes.CostMeter{})
}

// Resume continues an earlier session as a new generation, and its cost continues
// from the last running total.
func (d *Driver) Resume(ctx context.Context, spec ports.SessionSpec, from ports.ResumeRef) (ports.SessionHandle, error) {
	if spec.PermissionMode == lanes.BypassMode {
		return ports.SessionHandle{}, lanes.ErrBypassMode
	}
	meter := lanes.CostMeter{SessionTotalUSD: from.SessionTotalUSD}
	if spec.RunMode == ports.RunModeInteractive {
		return d.launchPane(ctx, spec, append(paneArgs(spec), "--resume", from.SessionID), checkResume, meter)
	}
	return d.launch(ctx, spec, ResumeArgs(spec, from), meter)
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
	// What qualifies the version read here, at the launch, is what this session uses for its life:
	// a version that changed since the worker last looked runs on documented surfaces.
	q := d.qualificationFor(version)
	if err := writeGenerated(spec); err != nil {
		return ports.SessionHandle{}, err
	}
	if err := writeLaunchedVersion(spec, version); err != nil {
		return ports.SessionHandle{}, err
	}
	stderr, err := openStderr(spec)
	if err != nil {
		return ports.SessionHandle{}, err
	}
	config := shimlink.Config{
		Dir: shimDir(spec), Socket: shimSocket(spec), Command: append([]string{binary}, arguments...),
		Linger: d.linger, JournalCap: d.cap,
	}
	env, carried := carriedPast(sessionEnv(spec, UseOf(q)))
	if len(carried) != 0 {
		if config.EnvFile, err = writeCarried(spec, carried); err != nil {
			_ = stderr.Close()
			return ports.SessionHandle{}, err
		}
		// The shim removes it once it reads it, before it answers; this holds a start that failed first.
		defer func() { _ = os.Remove(config.EnvFile) }()
	}
	process, err := d.runner.Start(ctx, ports.ProcessSpec{
		Path: spec.Binary, Args: shimlink.Args(config), Dir: spec.Dir, Env: env,
		Stderr: stderr, Detached: true, Outlive: true,
	})
	_ = stderr.Close()
	if err != nil {
		return ports.SessionHandle{}, err
	}
	client, err := awaitShim(ctx, process, config.Socket)
	if err != nil {
		return ports.SessionHandle{}, err
	}
	handle := ports.SessionHandle{ID: sessionHandleID(spec), PID: process.PID(), Version: version, Degraded: q.Degraded}
	s, err := d.follow(ctx, spec, handle, client, 0, meter, UseOf(q))
	if err != nil {
		_ = client.Signal(ctx, shimlink.SignalKill)
		return ports.SessionHandle{}, err
	}
	if spec.Brief != "" {
		if _, err := s.write(ctx, ports.Input{Text: spec.Brief}); err != nil {
			_ = client.Signal(ctx, shimlink.SignalKill)
			return ports.SessionHandle{}, err
		}
	}
	return s.handle, nil
}

// awaitShim waits for the first answer of the shim it started, and reaps it whenever it
// ends, because a process with no reader of its status stays a zombie under a live
// worker. Any other shim that answers already holds this generation, and following it
// would hand a running session a second brief.
func awaitShim(ctx context.Context, process ports.Process, socket string) (*shimlink.Client, error) {
	exited := make(chan ports.ExitStatus, 1)
	go func() {
		status, _ := process.Wait()
		exited <- status
	}()
	deadline := time.Now().Add(shimStartLimit)
	for time.Now().Before(deadline) {
		attempt, cancel := context.WithTimeout(ctx, shimDialDeadline)
		client, err := shimlink.Dial(attempt, socket)
		cancel()
		if err == nil {
			if client.PID() != process.PID() {
				endRefused(ctx, process, exited)
				return nil, ErrSessionExists
			}
			return client, nil
		}
		select {
		case status := <-exited:
			if status.Code == shimlink.ExitLocked {
				return nil, ErrSessionExists
			}
			return nil, fmt.Errorf("the shim exited before it answered: %+v", status)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lostPoll):
		}
	}
	return nil, errors.New("the shim did not answer")
}

// endRefused ends the shim of a refused start before the refusal returns. It has not
// reached its lock yet, and left alone it would reach it after the holder has gone, then
// hold the generation and start a session nobody follows.
func endRefused(ctx context.Context, process ports.Process, exited <-chan ports.ExitStatus) {
	_ = process.Signal(ports.SignalKill)
	select {
	case <-exited:
	case <-ctx.Done():
	case <-time.After(shimDialDeadline):
	}
}

// follow attaches after a cursor and starts the one reader of a session's stream.
func (d *Driver) follow(ctx context.Context, spec ports.SessionSpec, handle ports.SessionHandle, client *shimlink.Client, cursor int64, meter lanes.CostMeter, use Use) (*session, error) {
	s := d.newSession(spec, handle, client, meter)
	stream, err := client.Attach(ctx, cursor)
	if err != nil {
		return nil, err
	}
	log, err := openStreamLog(spec)
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if err := s.listen(spec, use); err != nil {
		_ = stream.Close()
		return nil, err
	}
	d.keep(s.handle.ID, s, spec)
	go s.read(stream, log, cursor)
	return s, nil
}

func (d *Driver) newSession(spec ports.SessionSpec, handle ports.SessionHandle, client *shimlink.Client, meter lanes.CostMeter) *session {
	return &session{
		handle: handle, client: client, dir: shimDir(spec), socket: shimSocket(spec), grace: d.grace,
		events: make(chan ports.DriverEvent, eventBuffer), done: make(chan struct{}), meter: meter, replies: spec.Assistant,
	}
}

// keepSocket reads what an earlier process heard, because a stream read from a cursor can hold no
// init, and a session's socket does not change while its process lives.
func (s *session) keepSocket(hooks []lanes.HookRecord) {
	for _, record := range hooks {
		s.takeHook(record.HookEvent)
	}
}

// listen opens this session's address for receipts, before anything can answer one.
func (s *session) listen(spec ports.SessionSpec, use Use) error {
	receipts, err := openReceiver(spec, use.Receipts, s.refused)
	s.use, s.receipts = use, receipts
	return err
}

// refused reports what a receipt said. A receipt arrives on its own connection, so this holds the
// lock that keeps a late one from a stream that has closed, and a full stream loses it, which
// leaves a message `handed_off` until it expires.
func (s *session) refused(input string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.over {
		return
	}
	select {
	case s.events <- ports.DriverEvent{Kind: ports.EventInputRefused, InputID: input}:
	default:
	}
}

func (d *Driver) keep(id string, s liveSession, spec ports.SessionSpec) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessions[id], d.policies[id] = s, policyPath(spec)
}

func (d *Driver) forget(id string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.sessions, id)
	delete(d.policies, id)
}

func sessionHandleID(spec ports.SessionSpec) string {
	return fmt.Sprintf("%s/%d", spec.Lane, spec.Generation)
}

// sessionEnv adds the driver's own variables. An undocumented one goes only to a
// version that passed its check.
// carriedPast splits from a session's environment what the shim must not hold itself and passes to the
// session: a delegate's credential, for which the binary refuses to start a shim, so no delegate can run
// one to start a program of its own.
func carriedPast(env []string) (own, carried []string) {
	for _, entry := range env {
		if strings.HasPrefix(entry, authority.EnvDelegateToken+"=") {
			carried = append(carried, entry)
			continue
		}
		own = append(own, entry)
	}
	return own, carried
}

// writeCarried leaves what a shim passes to its session in a file only the owner reads, each entry ended
// by a NUL byte, beside the generation's generated files.
func writeCarried(spec ports.SessionSpec, carried []string) (string, error) {
	path := filepath.Join(spec.ConfigDir, fmt.Sprintf("carried-%d.env", spec.Generation))
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	_, err = file.WriteString(strings.Join(carried, "\x00") + "\x00")
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return path, err
}

func sessionEnv(spec ports.SessionSpec, use Use) []string {
	env := append(withoutTelemetry(spec.Env), envSessionState, lanes.EnvPolicy+"="+policyPath(spec))
	if spec.TempDir != "" && use.PrivateSockets {
		env = append(env, envTempDir+spec.TempDir)
	}
	env = append(env, agentEnv(spec)...)
	return append(env, TelemetryEnv(spec)...)
}

// ContentFlags are the agent's switches for prompt, response, and tool content in its telemetry. All
// are off by default in the CLI, and only a profile's opt-in turns them on (FR-OBS-5).
func ContentFlags() []string {
	return []string{"OTEL_LOG_USER_PROMPTS", "OTEL_LOG_ASSISTANT_RESPONSES", "OTEL_LOG_TOOL_DETAILS", "OTEL_LOG_TOOL_CONTENT", "OTEL_LOG_RAW_API_BODIES"}
}

// The export intervals, in milliseconds. A short log interval keeps the `api_request` log useful as
// the second context source, and the metric interval keeps `top` current.
const (
	metricExportMs = "10000"
	logsExportMs   = "5000"
)

// TelemetryEnv points the session's telemetry at the worker's receiver. Telemetry is enabled only
// through the environment, because a repository's settings files cannot set its destination. The
// protocol is OTLP/HTTP JSON, which is what the receiver takes (D3).
func TelemetryEnv(spec ports.SessionSpec) []string {
	t := spec.Telemetry
	if t.Endpoint == "" {
		return nil
	}
	env := []string{
		"CLAUDE_CODE_ENABLE_TELEMETRY=1",
		"OTEL_METRICS_EXPORTER=otlp",
		"OTEL_LOGS_EXPORTER=otlp",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/json",
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + t.Endpoint,
		"OTEL_METRIC_EXPORT_INTERVAL=" + metricExportMs,
		"OTEL_LOGS_EXPORT_INTERVAL=" + logsExportMs,
	}
	if t.Header != "" {
		env = append(env, "OTEL_EXPORTER_OTLP_HEADERS="+t.Header)
	}
	if attributes := t.Tags.ResourceAttributes(); attributes != "" {
		env = append(env, "OTEL_RESOURCE_ATTRIBUTES="+attributes)
	}
	if t.Traces {
		env = append(env, "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1", "OTEL_TRACES_EXPORTER=otlp")
	}
	if t.Content {
		for _, flag := range ContentFlags() {
			env = append(env, flag+"=1")
		}
	}
	return env
}

// withoutTelemetry drops every telemetry variable a lane's environment holds, so only the driver says
// where telemetry goes and whether content is in it. A profile can pass a variable through by name,
// and none of these may reach a session that way.
func withoutTelemetry(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "OTEL_") || name == "CLAUDE_CODE_ENABLE_TELEMETRY" || name == "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA" ||
			name == "ENABLE_ENHANCED_TELEMETRY_BETA" {
			continue
		}
		out = append(out, entry)
	}
	return out
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

// launchedVersionPath names the file that says which version a generation's process started on.
func launchedVersionPath(spec ports.SessionSpec) string {
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("version-%d", spec.Generation))
}

// writeLaunchedVersion keeps the version a session is about to start on, so a worker that adopts it
// later qualifies the process and not whatever is installed by then.
func writeLaunchedVersion(spec ports.SessionSpec, version string) error {
	return os.WriteFile(launchedVersionPath(spec), []byte(version+"\n"), 0o600)
}

// launchedVersion reads that file, and empty when a launch left none.
func launchedVersion(spec ports.SessionSpec) string {
	raw, err := os.ReadFile(launchedVersionPath(spec))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// shimDir holds a generation's journal, state, and lock.
func shimDir(spec ports.SessionSpec) string {
	return filepath.Join(spec.ConfigDir, fmt.Sprintf("shim-%d", spec.Generation))
}

// shimSocket sits in the session's short private directory when it has one, because a
// socket path has a 104-byte limit.
func shimSocket(spec ports.SessionSpec) string {
	dir := spec.TempDir
	if dir == "" {
		dir = spec.ConfigDir
	}
	return filepath.Join(dir, fmt.Sprintf("s%d.sock", spec.Generation))
}

// session is one session under a shim.
type session struct {
	handle ports.SessionHandle
	client *shimlink.Client
	dir    string
	socket string
	grace  time.Duration
	events chan ports.DriverEvent
	done   chan struct{}

	mu       sync.Mutex
	stopping bool
	closed   bool
	inFlight int
	last     int64
	meter    lanes.CostMeter
	seen     ports.Observation
	// peerSocket is the session's messaging socket, which its init or a hook named, and over says
	// its stream has ended.
	peerSocket string
	over       bool
	// replies says the session is an assistant's, whose text the stream reports.
	replies bool
	// use is what this session's version passed, read once at its launch, and receipts is where its
	// receipts come back, when it has an address.
	use      Use
	receipts *receiver
	// agents maps each Agent call of this stream to the agent it started.
	agents map[string]string
}

// read is the one reader of a session's stream. It ends at the child's exit, which is
// always the last record, or when the shim goes.
func (s *session) read(stream *shimlink.Stream, log io.WriteCloser, cursor int64) {
	defer stream.Close()
	if log != nil {
		defer log.Close()
	}
	s.last = cursor
	for {
		record, err := stream.Next()
		if err != nil {
			s.drain(log)
			return
		}
		if s.takeRecord(record, log) {
			return
		}
	}
}

// drain finishes a session whose shim is gone. The journal keeps what the shim wrote, and
// the session ends by itself because its stdin closed, so this reads the rest and waits.
func (s *session) drain(log io.Writer) {
	records, err := shimlink.ReadJournal(s.dir, s.last)
	if err != nil {
		s.finish(ports.ExitStatus{}, err)
		return
	}
	for _, record := range records {
		if s.takeRecord(record, log) {
			return
		}
	}
	s.awaitChild()
	s.finish(ports.ExitStatus{}, shimlink.ErrUnreachable)
}

// awaitChild waits for a session that lost its shim, and ends one that does not end:
// nothing else can reach it, and a lane must not end with its session still writing.
func (s *session) awaitChild() {
	state, err := shimlink.ReadState(s.dir)
	if err != nil {
		return
	}
	for deadline := time.Now().Add(lostShimGrace); time.Now().Before(deadline); time.Sleep(lostPoll) {
		if !processExists(state.ChildPID) {
			return
		}
	}
	_ = syscall.Kill(state.ChildPID, syscall.SIGKILL)
}

// takeRecord applies one record, and reports the child's end. An input counts here, in
// stream order, so an adoption counts the same from the journal.
func (s *session) takeRecord(record shimlink.Record, log io.Writer) bool {
	if record.Seq != 0 {
		s.last = record.Seq
	}
	switch {
	case record.Line != "":
		if log != nil {
			_, _ = io.WriteString(log, record.Line+"\n")
		}
		s.take([]byte(record.Line), record.Seq)
	case record.Input != "":
		s.mu.Lock()
		s.inFlight++
		s.mu.Unlock()
	case record.Gap != 0:
		s.events <- ports.DriverEvent{Kind: ports.EventMalformed, Detail: "the journal rotated past this reader", Seq: record.Gap}
	case record.Exit != nil:
		s.finish(exitStatus(*record.Exit), nil)
		return true
	}
	return false
}

func exitStatus(exit shimlink.Exit) ports.ExitStatus {
	status := ports.ExitStatus{Code: exit.Code}
	if exit.Signal != "" {
		status = ports.ExitStatus{Signal: ports.Signal(exit.Signal)}
	}
	return status
}

func (s *session) take(line []byte, seq int64) {
	p, err := parseLineOf(line, s.use.WindowFigures, s.replies)
	if err != nil {
		s.mu.Lock()
		s.seen.Malformed++
		s.mu.Unlock()
		s.events <- ports.DriverEvent{Kind: ports.EventMalformed, Seq: seq}
		return
	}
	if event, ok := s.agentUsage(p); ok {
		event.Seq = seq
		s.events <- event
		return
	}
	if !p.useful {
		return
	}
	s.mu.Lock()
	if p.socket != "" {
		s.peerSocket = p.socket
	}
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
	p.event.Seq = seq
	s.events <- p.event
}

// agentUsage keeps which agent each Agent call started, and turns a subagent's message into the usage of
// the agent it runs under. A message under a call this stream never named stays the session's own.
func (s *session) agentUsage(p parsed) (ports.DriverEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for call, agent := range p.started {
		if s.agents == nil {
			s.agents = map[string]string{}
		}
		s.agents[call] = agent
	}
	agent := s.agents[p.parent]
	if p.parent == "" || agent == "" || p.usage == (lanes.Usage{}) {
		return ports.DriverEvent{}, false
	}
	usage := p.usage
	return ports.DriverEvent{Kind: ports.EventAgentUsage, Agent: agent, Tokens: &usage}, true
}

// finish reports how the session ended, and lets the shim go. No `result` for an input
// means the process died mid-turn, unless someone asked it to stop.
func (s *session) finish(status ports.ExitStatus, err error) {
	s.mu.Lock()
	crashed := !s.stopping && (s.inFlight > 0 || status.Code != 0 || status.Signal != "" || err != nil)
	last := s.last
	s.over = true
	s.mu.Unlock()
	s.receipts.close()
	if s.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shimDialDeadline)
		defer cancel()
		_ = s.client.Shutdown(ctx)
	}
	s.events <- ports.DriverEvent{Kind: ports.EventSessionEnded, End: &ports.SessionEnd{Crashed: crashed, Exit: status}, Seq: last}
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

// write hands one input to the shim, which is the one writer of stdin and writes each ID
// at most once. A line built from a typed value always passes its check, so a refusal
// here means the session has gone.
func (s *session) write(ctx context.Context, in ports.Input) (ports.SendReceipt, error) {
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
	if s.client == nil {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w", s.handle.ID, ports.ErrNoSession)
	}
	if _, err := s.client.Input(ctx, in.ID, string(raw)); err != nil {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: %v", s.handle.ID, ports.ErrNoSession, err)
	}
	return ports.SendReceipt{ID: in.ID, Path: ports.SendStdin}, nil
}

func (d *Driver) session(h ports.SessionHandle) (liveSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	s, ok := d.sessions[h.ID]
	if !ok {
		return nil, fmt.Errorf("%s: %w", h.ID, ports.ErrNoSession)
	}
	return s, nil
}

// Observe returns what the session showed. An unknown handle reads unknown, because it is
// no evidence of an end.
func (d *Driver) Observe(ctx context.Context, h ports.SessionHandle) (ports.Observation, error) {
	s, err := d.session(h)
	if err != nil {
		return ports.Observation{Liveness: ports.LivenessUnknown}, err
	}
	return s.observe(ctx), nil
}

// observe returns what the stream showed, and pings the shim, which is the handshake on
// this path. Only a session whose end was read reads dead, and a failed ping reads
// unknown.
func (s *session) observe(ctx context.Context) ports.Observation {
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
	if s.client != nil {
		attempt, cancel := context.WithTimeout(ctx, shimDialDeadline)
		defer cancel()
		if s.client.Ping(attempt) == nil {
			out.Liveness = ports.LivenessAlive
		}
	}
	return out
}

// Events returns the session's stream.
func (d *Driver) Events(_ context.Context, h ports.SessionHandle) (<-chan ports.DriverEvent, error) {
	s, err := d.session(h)
	if err != nil {
		return nil, err
	}
	return s.stream(), nil
}

// TakeHook hands a session what its hook reported.
func (d *Driver) TakeHook(_ context.Context, event lanes.HookEvent) {
	d.mu.Lock()
	s, ok := d.sessions[hookSessionID(event)]
	d.mu.Unlock()
	if ok {
		s.takeHook(event)
	}
}

func hookSessionID(event lanes.HookEvent) string {
	return fmt.Sprintf("%s/%d", event.Lane, event.Generation)
}

func (s *session) stream() <-chan ports.DriverEvent { return s.events }

func (s *session) ended() <-chan struct{} { return s.done }

// takeHook keeps the one thing a stream cannot say before a first prompt: a session binds its
// socket first, and it has no init until then. Its state comes from its stream.
func (s *session) takeHook(event lanes.HookEvent) {
	if event.Socket == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peerSocket = event.Socket
}

// Send hands one input to the session, by whatever path its run mode has.
func (d *Driver) Send(ctx context.Context, h ports.SessionHandle, in ports.Input) (ports.SendReceipt, error) {
	s, err := d.session(h)
	if err != nil {
		return ports.SendReceipt{}, err
	}
	return s.send(ctx, in)
}

// send takes the path that suits who is speaking. The worker's own input goes in on stdin, and
// anyone else's on the socket, which keeps peer marking: stdin reads as a user turn, which is more
// standing than a message has. A version whose socket passed no check has no such path, and stdin
// does not stand in for it. The worker's word for a running turn takes the socket too, because
// stdin waits for the turn to end.
func (s *session) send(ctx context.Context, in ports.Input) (ports.SendReceipt, error) {
	if !in.Peer && !in.MidTurn {
		return s.write(ctx, in)
	}
	if !s.use.PeerInjection {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: this version passed no check of the socket", s.handle.ID, ports.ErrNoChannel)
	}
	state, err := shimlink.ReadState(s.dir)
	if err != nil {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: %v", s.handle.ID, ports.ErrNoChannel, err)
	}
	s.mu.Lock()
	socket := s.peerSocket
	s.mu.Unlock()
	if socket == "" {
		return ports.SendReceipt{}, fmt.Errorf("%s: %w: no socket", s.handle.ID, ports.ErrNoChannel)
	}
	return writeSocket(ctx, s.handle.ID, socket, state.ChildPID, in, s.receipts.from())
}

// Decide has no path yet: an approval returns through the waiting hook.
func (d *Driver) Decide(context.Context, ports.SessionHandle, ports.ApprovalDecision) error {
	return ports.ErrUnsupported
}

// Compact asks for the native command.
func (d *Driver) Compact(ctx context.Context, h ports.SessionHandle) error {
	s, err := d.session(h)
	if err != nil {
		return err
	}
	return s.compact(ctx)
}

// compact sends the command as a stream-json message.
func (s *session) compact(ctx context.Context) error {
	_, err := s.write(ctx, ports.Input{Text: "/compact"})
	return err
}

// HoldTools changes the file this session's hooks read, which takes effect at its next tool call.
func (d *Driver) HoldTools(_ context.Context, h ports.SessionHandle, reason string) error {
	if _, err := d.session(h); err != nil {
		return err
	}
	d.mu.Lock()
	path := d.policies[h.ID]
	d.mu.Unlock()
	return holdPolicy(path, reason)
}

// Stop ends a session. A session that does not end within the grace takes the next
// signal, and SIGKILL is last. A session whose reader is gone reads ended, so a failed
// request here is not a failed stop.
func (d *Driver) Stop(ctx context.Context, h ports.SessionHandle, how ports.StopMode) error {
	s, err := d.session(h)
	if err != nil {
		return err
	}
	s.beginStop(ctx, how)
	for _, next := range []ports.Signal{ports.SignalTerminate, ports.SignalKill} {
		select {
		case <-s.ended():
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.grace):
		}
		s.escalate(ctx, next)
	}
	select {
	case <-s.ended():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// beginStop goes through the shim, which sends a signal only to the session it holds.
func (s *session) beginStop(ctx context.Context, how ports.StopMode) {
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	switch how {
	case ports.StopInterrupt:
		s.signal(ctx, shimlink.SignalInterrupt)
		s.closeStdin(ctx)
	case ports.StopAtRest:
		s.closeStdin(ctx)
	case ports.StopNow:
		s.closeStdin(ctx)
		s.signal(ctx, shimlink.SignalKill)
	}
}

func (s *session) escalate(ctx context.Context, signal ports.Signal) {
	if signal == ports.SignalKill {
		s.signal(ctx, shimlink.SignalKill)
		return
	}
	s.signal(ctx, shimlink.SignalTerminate)
}

func (s *session) signal(ctx context.Context, name string) {
	if s.client != nil {
		_ = s.client.Signal(ctx, name)
	}
}

func (s *session) closeStdin(ctx context.Context) {
	s.mu.Lock()
	closed := s.closed
	s.closed = true
	s.mu.Unlock()
	if !closed && s.client != nil {
		_ = s.client.CloseStdin(ctx)
	}
}

var _ ports.AgentDriver = (*Driver)(nil)
