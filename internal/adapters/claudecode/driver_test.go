package claudecode_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/secrets"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/doubles"
)

var update = flag.Bool("update", false, "rewrite the golden flag lists")

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(doubles.Main(m))
}

func goldenSpec() ports.SessionSpec {
	return ports.SessionSpec{
		Lane: "lane-1", Generation: 2, Model: "opus", PermissionMode: "default", ConfigDir: "/state/lanes/lane-1",
	}
}

func golden(t *testing.T, name string, got []string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	text := strings.Join(got, "\n") + "\n"
	if *update {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if text != string(want) {
		t.Errorf("flags\n got %s\nwant %s", text, want)
	}
}

func TestStartFlags(t *testing.T) {
	golden(t, "start.flags", claudecode.StartArgs(goldenSpec()))
}

func TestResumeFlags(t *testing.T) {
	got := claudecode.ResumeArgs(goldenSpec(), ports.ResumeRef{SessionID: "the-session"})
	golden(t, "resume.flags", got)
	start := claudecode.StartArgs(goldenSpec())
	if !slices.Equal(got[:len(start)], start) {
		t.Error("a resume dropped a start flag, and no flag survives a resume by itself")
	}
}

// A pack sits in the system prompt on a start and on a resume, because no flag survives a resume by
// itself, and a session with none takes no flag.
func TestAPackGoesInTheSystemPrompt(t *testing.T) {
	spec := goldenSpec()
	for name, got := range map[string][]string{
		"a start": claudecode.StartArgs(spec), "a resume": claudecode.ResumeArgs(spec, ports.ResumeRef{SessionID: "s"}),
	} {
		if slices.Contains(got, "--append-system-prompt-file") {
			t.Errorf("%s with no pack: %v", name, got)
		}
	}
	spec.SystemPromptFile = "/lanes/one/pack.md"
	for name, got := range map[string][]string{
		"a start": claudecode.StartArgs(spec), "a resume": claudecode.ResumeArgs(spec, ports.ResumeRef{SessionID: "s"}),
	} {
		at := slices.Index(got, "--append-system-prompt-file")
		if at < 0 || got[at+1] != spec.SystemPromptFile {
			t.Errorf("%s: %v", name, got)
		}
	}
}

// `--tools ""` takes away the vendor's tools and leaves an MCP server's, which RS12 read in a real
// session's init. A lane keeps its own, because it has work to do with them.
func TestASessionHoldsOnlyItsToolsWhenItSaysSo(t *testing.T) {
	spec := goldenSpec()
	if slices.Contains(claudecode.StartArgs(spec), "--tools") {
		t.Errorf("a lane lost its tools: %v", claudecode.StartArgs(spec))
	}
	spec.OnlyTools = true
	from := ports.ResumeRef{SessionID: "session-1"}
	for name, got := range map[string][]string{"start": claudecode.StartArgs(spec), "resume": claudecode.ResumeArgs(spec, from)} {
		at := slices.Index(got, "--tools")
		if at < 0 || got[at+1] != "" {
			t.Errorf("%s: flags %v", name, got)
		}
	}
}

func TestNoBareFlagAndAnExplicitPermissionMode(t *testing.T) {
	got := claudecode.StartArgs(goldenSpec())
	if slices.Contains(got, "--bare") {
		t.Error("--bare does not use a subscription login")
	}
	mode := slices.Index(got, "--permission-mode")
	if mode < 0 || got[mode+1] != "default" {
		t.Errorf("flags %v", got)
	}
}

func session(t *testing.T, fixture string, change func(*ports.SessionSpec)) (ports.AgentDriver, string, ports.SessionSpec) {
	t.Helper()
	path := doubles.InstallFakeClaude(t, fixture)
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second})
	spec := ports.SessionSpec{
		Lane: "lane-1", WorkOrder: "wo", Generation: 1, Dir: t.TempDir(), Model: "opus", PermissionMode: "default",
		Brief: "the brief", ConfigDir: filepath.Join(t.TempDir(), "generated"), Env: []string{"PATH=" + path, "ROPESHIFT_LANE_ID=lane-1"},
		Binary: doubles.Ropeshift(t), TempDir: apitest.ShortDir(t),
	}
	doubles.ReapShims(t, spec.ConfigDir)
	if change != nil {
		change(&spec)
	}
	return driver, path, spec
}

func play(t *testing.T, driver ports.AgentDriver, spec ports.SessionSpec) []ports.DriverEvent {
	t.Helper()
	ctx := t.Context()
	handle, err := driver.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	return drain(t, driver, handle)
}

func drain(t *testing.T, driver ports.AgentDriver, handle ports.SessionHandle) []ports.DriverEvent {
	t.Helper()
	ctx := t.Context()
	events, err := driver.Events(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	var out []ports.DriverEvent
	deadline := time.After(20 * time.Second)
	for {
		select {
		case event, open := <-events:
			if !open {
				return out
			}
			out = append(out, event)
			if event.Kind == ports.EventTurnEnded {
				if err := driver.Stop(ctx, handle, ports.StopAtRest); err != nil {
					t.Fatal(err)
				}
			}
		case <-deadline:
			t.Fatal("the session did not end")
		}
	}
}

func TestAnUnqualifiedVersionIsDegraded(t *testing.T) {
	driver, path, spec := session(t, "unqualified-version", nil)
	got, err := driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Degraded || got.Version != "9.9.9" {
		t.Errorf("capabilities %+v", got)
	}
	doctor, err := driver.Doctor(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if doctor.Qualified || !doctor.Present {
		t.Errorf("doctor %+v", doctor)
	}
	play(t, driver, spec)
	if value := doubles.SeenEnv(t, path, "CLAUDE_CODE_TMPDIR"); value != "" {
		t.Errorf("an undocumented surface reached an unqualified version: %s", value)
	}
}

// TestACLIThatFailsItsVersionCheckIsNotUsable is a `claude` on PATH whose `--version` does not answer
// with a version. The doctor reports it not usable and names the failed check, the driver offers no
// capabilities on it, and a start fails before any session process exists.
func TestACLIThatFailsItsVersionCheckIsNotUsable(t *testing.T) {
	tests := []struct{ name, body, how string }{
		{"it exits with an error", "exit 70", "exited with status 70"},
		{"a signal ends it", "kill -KILL $$", "ended on signal SIGKILL"},
		{"it prints nothing", "exit 0", "printed no version"},
		{"it prints something that is not a version", "echo 'error: no configuration'", "printed no version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := doubles.InstallBrokenClaude(t, tt.body)
			driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}})
			want := "claude --version " + tt.how
			report, err := driver.Doctor(t.Context())
			if err != nil || report.Present || report.Detail != want || report.Version != "" || report.SignInStatus != lanes.SignInUnknown {
				t.Errorf("doctor %+v, %v, want not present with %q", report, err, want)
			}
			if got, err := driver.Capabilities(t.Context()); err == nil || err.Error() != want {
				t.Errorf("capabilities %+v, %v, want %q", got, err, want)
			}
			spec := ports.SessionSpec{
				Lane: "lane-1", WorkOrder: "wo", Generation: 1, Dir: t.TempDir(), Model: "opus", PermissionMode: "default",
				Brief: "the brief", ConfigDir: filepath.Join(t.TempDir(), "generated"), Env: []string{"PATH=" + path},
				Binary: doubles.Ropeshift(t), TempDir: apitest.ShortDir(t),
			}
			doubles.ReapShims(t, spec.ConfigDir)
			if err := os.MkdirAll(spec.ConfigDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if handle, err := driver.Start(t.Context(), spec); err == nil || err.Error() != want {
				t.Errorf("start %+v, %v, want %q", handle, err, want)
			}
			for _, run := range doubles.BrokenClaudeRuns(t, path) {
				if run != "--version" {
					t.Errorf("a CLI that failed its version check ran with %q", run)
				}
			}
		})
	}
}

func TestAQualifiedVersionGetsItsPrivateDirectory(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	got, err := driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Degraded {
		t.Errorf("capabilities %+v", got)
	}
	play(t, driver, spec)
	if value := doubles.SeenEnv(t, path, "CLAUDE_CODE_TMPDIR"); value != spec.TempDir {
		t.Errorf("CLAUDE_CODE_TMPDIR %q", value)
	}
	if value := doubles.SeenEnv(t, path, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS"); value != "1" {
		t.Errorf("session state events %q", value)
	}
}

func TestTheSessionEnvironmentIsTheSpecs(t *testing.T) {
	t.Setenv("ROPESHIFT_TEST_PARENT_VARIABLE", "must-not-pass")
	driver, path, spec := session(t, "two-turns", nil)
	play(t, driver, spec)
	seen := doubles.SeenBy(t, doubles.BinDir(path))
	if len(seen) != 1 {
		t.Fatalf("%d sessions", len(seen))
	}
	for name := range seen[0].Env {
		if !slices.Contains([]string{"PATH", "ROPESHIFT_LANE_ID", "ROPESHIFT_POLICY", "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS", "CLAUDE_CODE_TMPDIR"}, name) {
			t.Errorf("%s reached the session", name)
		}
	}
}

func telemetrySpec() ports.SessionSpec {
	spec := goldenSpec()
	spec.Telemetry = ports.SessionTelemetry{
		Endpoint: "http://127.0.0.1:7318", Header: "x-ropeshift-telemetry=0123abcd",
		Tags: lanes.TelemetryTags{
			Workspace: "default", WorkOrder: "wo-1", Lane: "lane-1", Role: lanes.RoleWriter, Generation: 2, Profile: "default",
			Repo: "example service", Branch: "lane/checkout,retry", Worker: "solo", Driver: claudecode.DriverName,
		},
	}
	return spec
}

// TestTelemetryEnvironment is the golden list of what a lane's agent is told about telemetry: on,
// sent to the worker's receiver over OTLP/HTTP JSON, tagged, and with no content flag (FR-OBS-2,
// FR-OBS-5).
func TestTelemetryEnvironment(t *testing.T) {
	got := claudecode.TelemetryEnv(telemetrySpec())
	golden(t, "telemetry.env", got)
	for _, entry := range got {
		name, value, _ := strings.Cut(entry, "=")
		if name == "OTEL_RESOURCE_ATTRIBUTES" {
			for pair := range strings.SplitSeq(value, ",") {
				if _, tag, _ := strings.Cut(pair, "="); strings.ContainsAny(tag, " ,") {
					t.Errorf("attribute %q holds a space or a comma", pair)
				}
			}
		}
		if slices.Contains(claudecode.ContentFlags(), name) {
			t.Errorf("%s is set with no opt-in", name)
		}
	}
}

func TestContentFlagsNeedTheProfilesOptIn(t *testing.T) {
	spec := telemetrySpec()
	spec.Telemetry.Content = true
	got := claudecode.TelemetryEnv(spec)
	for _, flag := range claudecode.ContentFlags() {
		if !slices.Contains(got, flag+"=1") {
			t.Errorf("%s is unset under the opt-in", flag)
		}
	}
	spec.Telemetry.Content, spec.Telemetry.Traces = false, true
	got = claudecode.TelemetryEnv(spec)
	if !slices.Contains(got, "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA=1") || !slices.Contains(got, "OTEL_TRACES_EXPORTER=otlp") {
		t.Errorf("traces asked for and not set: %v", got)
	}
}

func TestNoReceiverSendsNoTelemetry(t *testing.T) {
	spec := goldenSpec()
	if got := claudecode.TelemetryEnv(spec); len(got) != 0 {
		t.Errorf("a lane with no receiver was told %v", got)
	}
}

// A lane's environment holds what its profile passed through, and nothing it passed can point the
// agent's telemetry elsewhere or turn content on.
func TestTheSessionSeesOnlyTheDriversTelemetry(t *testing.T) {
	driver, path, spec := session(t, "two-turns", func(spec *ports.SessionSpec) {
		spec.Env = append(spec.Env, "OTEL_LOG_USER_PROMPTS=1", "OTEL_EXPORTER_OTLP_ENDPOINT=https://elsewhere.example", "OTEL_LOG_RAW_API_BODIES=1")
		spec.Telemetry = telemetrySpec().Telemetry
	})
	play(t, driver, spec)
	seen := doubles.SeenBy(t, doubles.BinDir(path))
	if len(seen) != 1 {
		t.Fatalf("%d sessions", len(seen))
	}
	env := seen[0].Env
	for _, flag := range claudecode.ContentFlags() {
		if value, set := env[flag]; set {
			t.Errorf("%s=%s reached the session", flag, value)
		}
	}
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "http://127.0.0.1:7318" || env["CLAUDE_CODE_ENABLE_TELEMETRY"] != "1" ||
		env["OTEL_EXPORTER_OTLP_PROTOCOL"] != "http/json" || !strings.Contains(env["OTEL_RESOURCE_ATTRIBUTES"], "ropeshift.lane.id=lane-1") {
		t.Errorf("env %v", env)
	}
}

func TestAResumeStatesEveryFlagAndContinuesTheCost(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	handle, err := driver.Resume(t.Context(), spec, ports.ResumeRef{SessionID: "the-session", SessionTotalUSD: 0.05})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(t, driver, handle)
	var turn *ports.TurnResult
	for _, event := range events {
		if event.Kind == ports.EventTurnEnded {
			turn = event.Turn
		}
	}
	if turn == nil || turn.Cost.CostUSD < 0.049 || turn.Cost.CostUSD > 0.051 {
		t.Errorf("turn %+v", turn)
	}
	seen := doubles.SeenBy(t, doubles.BinDir(path))
	if len(seen) != 1 || !slices.Contains(seen[0].Args, "--resume") || !slices.Contains(seen[0].Args, "--settings") ||
		!slices.Contains(seen[0].Args, "--mcp-config") {
		t.Errorf("seen %+v", seen)
	}
}

func TestStreamSignalsAreNormalized(t *testing.T) {
	driver, _, spec := session(t, "signals", nil)
	kinds := map[ports.EventKind]string{}
	for _, event := range play(t, driver, spec) {
		kinds[event.Kind] = event.Detail
	}
	if kinds[ports.EventLimit] != "allowed" || kinds[ports.EventCompaction] != "auto" {
		t.Errorf("kinds %v", kinds)
	}
	if _, ok := kinds[ports.EventRetry]; !ok {
		t.Errorf("kinds %v", kinds)
	}
}

type generatedSettings struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Command   string `json:"command"`
			OnFailure string `json:"onFailure"`
		} `json:"hooks"`
	} `json:"hooks"`
	Permissions struct {
		Allow []string `json:"allow"`
	} `json:"permissions"`
	APIKeyHelper string `json:"apiKeyHelper"`
}

func readSettings(t *testing.T, spec ports.SessionSpec) (generatedSettings, string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(spec.ConfigDir, "settings-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out generatedSettings
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, string(raw)
}

// waitFor reads a stream until one event, and returns what it read.
func waitFor(t *testing.T, events <-chan ports.DriverEvent, kind ports.EventKind) []ports.DriverEvent {
	t.Helper()
	var out []ports.DriverEvent
	deadline := time.After(30 * time.Second)
	for {
		select {
		case event, open := <-events:
			if !open {
				t.Fatalf("the stream closed before %s: %+v", kind, out)
			}
			out = append(out, event)
			if event.Kind == kind {
				return out
			}
		case <-deadline:
			t.Fatalf("no %s in %+v", kind, out)
		}
	}
}

// restarted is the driver a new worker process builds: it holds nothing of the last one.
func restarted(path string) ports.AgentDriver {
	return claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second})
}

// TestARestartedWorkerAdoptsASessionMidTurn is FR-LANE-17 at the driver: a new driver,
// which knows nothing, finds the session, reads on, and stops it.
func TestARestartedWorkerAdoptsASessionMidTurn(t *testing.T) {
	first, path, spec := session(t, "stopped-turn", nil)
	handle, err := first.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events, err := first.Events(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	bound := waitFor(t, events, ports.EventSessionBound)
	next := restarted(path)
	located, err := next.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !located.Found || !located.Readable || located.Liveness != lanes.LivenessAlive {
		t.Fatalf("located %+v", located)
	}
	cursor := bound[len(bound)-1].Seq
	if cursor == 0 {
		t.Fatal("an event carried no position")
	}
	if err := next.Adopt(t.Context(), located.Handle, ports.AdoptFrom{Spec: spec, Cursor: cursor}); err != nil {
		t.Fatal(err)
	}
	if err := next.Stop(t.Context(), located.Handle, ports.StopInterrupt); err != nil {
		t.Fatal(err)
	}
	adopted := drain(t, next, located.Handle)
	if adopted[0].Seq <= cursor {
		t.Errorf("an adoption redelivered position %d from %d", adopted[0].Seq, cursor)
	}
	last := adopted[len(adopted)-1]
	if last.Kind != ports.EventSessionEnded || last.End.Crashed {
		t.Errorf("events %+v", adopted)
	}
}

// A turn that ended while no worker was there is read from the journal, in order, after
// the shim that wrote it has gone.
func TestASessionThatEndedWhileTheWorkerWasAwayIsRead(t *testing.T) {
	path := doubles.InstallFakeClaude(t, "two-turns")
	first := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, ShimLinger: 10 * time.Millisecond})
	spec := ports.SessionSpec{
		Lane: "lane-1", WorkOrder: "wo", Generation: 1, Dir: t.TempDir(), Model: "opus", PermissionMode: "default",
		Brief: "the brief", ConfigDir: filepath.Join(t.TempDir(), "generated"), Env: []string{"PATH=" + path},
		Binary: doubles.Ropeshift(t), TempDir: apitest.ShortDir(t),
	}
	handle, err := first.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events, err := first.Events(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, ports.EventTurnEnded)
	if err := first.Stop(t.Context(), handle, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(spec.TempDir, "s1.sock")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	next := restarted(path)
	located, err := next.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !located.Found || !located.Readable || located.Liveness != lanes.LivenessDead {
		t.Fatalf("located %+v", located)
	}
	if err := next.Adopt(t.Context(), located.Handle, ports.AdoptFrom{Spec: spec}); err != nil {
		t.Fatal(err)
	}
	read := drain(t, next, located.Handle)
	kinds := make([]ports.EventKind, 0, len(read))
	for _, event := range read {
		kinds = append(kinds, event.Kind)
	}
	if !slices.Contains(kinds, ports.EventTurnEnded) || kinds[len(kinds)-1] != ports.EventSessionEnded {
		t.Errorf("events %v", kinds)
	}
}

// TestAKilledShimEndsTheSessionAndAResumeRecoversIt is the failure table's row: stdin
// closes, the session ends with its turn, and a new shim resumes it.
func TestAKilledShimEndsTheSessionAndAResumeRecoversIt(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	handle, err := driver.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	events, err := driver.Events(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, ports.EventTurnEnded)
	state, err := shimlink.ReadState(filepath.Join(spec.ConfigDir, "shim-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(state.ShimPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	ended := waitFor(t, events, ports.EventSessionEnded)
	if end := ended[len(ended)-1].End; !end.Crashed {
		t.Errorf("a lost shim read as a clean end: %+v", end)
	}
	if err := syscall.Kill(state.ChildPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the session outlived its shim: %v", err)
	}
	spec.Generation, spec.Brief = 2, "go on"
	resumed, err := driver.Resume(t.Context(), spec, ports.ResumeRef{SessionID: "the-session"})
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Stop(t.Context(), resumed, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	read := drain(t, driver, resumed)
	if last := read[len(read)-1]; last.Kind != ports.EventSessionEnded || last.End.Crashed {
		t.Errorf("events %+v", read)
	}
	sawResume := false
	for _, seen := range doubles.SeenBy(t, doubles.BinDir(path)) {
		sawResume = sawResume || slices.Contains(seen.Args, "--resume")
	}
	if !sawResume {
		t.Error("no session resumed")
	}
}

// A retry that reaches a generation a shim holds must find it, not start a second.
func TestASecondStartOfOneGenerationIsRefused(t *testing.T) {
	driver, path, spec := session(t, "stopped-turn", nil)
	handle, err := driver.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = driver.Stop(context.Background(), handle, ports.StopNow) }()
	_, err = restarted(path).Start(t.Context(), spec)
	if !errors.Is(err, claudecode.ErrSessionExists) {
		t.Errorf("second start: %v", err)
	}
}

// countingRunner remembers the last process it started.
type countingRunner struct {
	ports.ProcessRunner
	mu   sync.Mutex
	last ports.Process
}

func (r *countingRunner) Start(ctx context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	process, err := r.ProcessRunner.Start(ctx, spec)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = process
	return process, err
}

// TestARefusedStartLeavesNoShimRunning holds the shim a refused start launched to the end
// of that start. One left running reaches its lock after the holder has gone, and then it
// holds the generation and starts a session nobody follows.
func TestARefusedStartLeavesNoShimRunning(t *testing.T) {
	driver, path, spec := session(t, "stopped-turn", nil)
	handle, err := driver.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = driver.Stop(context.Background(), handle, ports.StopNow) }()
	runner := &countingRunner{ProcessRunner: osexec.Runner{}}
	second := claudecode.New(runner, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second})
	if _, err := second.Start(t.Context(), spec); !errors.Is(err, claudecode.ErrSessionExists) {
		t.Fatalf("second start: %v", err)
	}
	if err := syscall.Kill(runner.last.PID(), 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the refused shim %d is still running: %v", runner.last.PID(), err)
	}
}

func TestAFailedPingReadsUnknownAndNeverDead(t *testing.T) {
	driver, _, spec := session(t, "stopped-turn", nil)
	handle, err := driver.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = driver.Stop(context.Background(), handle, ports.StopNow) }()
	observation, err := driver.Observe(t.Context(), handle)
	if err != nil || observation.Liveness != lanes.LivenessAlive {
		t.Fatalf("observation %+v: %v", observation, err)
	}
	socket := filepath.Join(spec.TempDir, "s1.sock")
	if err := os.Rename(socket, socket+".moved"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Rename(socket+".moved", socket) }()
	observation, err = driver.Observe(t.Context(), handle)
	if err != nil || observation.Liveness != lanes.LivenessUnknown {
		t.Errorf("observation %+v: %v", observation, err)
	}
}

func writeFixture(t *testing.T, path string, body map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(doubles.BinDir(path), doubles.FixtureFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestListingIsReadOnceForTheWorktree holds the rule on `claude agents`: it runs at
// adoption, once, and only with --cwd set to the lane's own worktree.
func TestListingIsReadOnceForTheWorktree(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	for range 3 {
		if _, err := driver.Find(t.Context(), spec); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	for _, seen := range doubles.SeenBy(t, doubles.BinDir(path)) {
		if len(seen.Args) == 0 || seen.Args[0] != "agents" {
			continue
		}
		reads++
		if !slices.Equal(seen.Args, []string{"agents", "--json", "--cwd", spec.Dir}) {
			t.Errorf("args %v", seen.Args)
		}
	}
	if reads != 1 {
		t.Errorf("%d reads of the listing", reads)
	}
}

// A session the listing shows has no shim to read, and a restart must not start a second
// one beside it, so it reads unknown until it has gone.
func TestListedSessionReadsUnknown(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	writeFixture(t, path, map[string]any{"agents": []map[string]any{{"pid": os.Getpid()}}})
	located, err := driver.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !located.Found || located.Liveness != lanes.LivenessUnknown || located.Readable {
		t.Errorf("located %+v", located)
	}
}

func TestListedProcessThatExitedIsNotFound(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	gone := exec.Command("/usr/bin/true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, map[string]any{"agents": []map[string]any{{"pid": gone.Process.Pid}}})
	located, err := driver.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if located.Found {
		t.Errorf("located %+v", located)
	}
}

// An incomplete read is an error, and an error is never a session that is not there.
func TestFailedListingIsAnError(t *testing.T) {
	driver, path, spec := session(t, "two-turns", nil)
	writeFixture(t, path, map[string]any{"agents_exit": 1})
	if located, err := driver.Find(t.Context(), spec); err == nil {
		t.Errorf("located %+v with no error", located)
	}
}

// TestGeneratedMCPNamesTheToken holds both halves: the server is told about the lane
// token by name, and the value reaches no file.
func TestGeneratedMCPNamesTheToken(t *testing.T) {
	const token = "the-lane-token-value"
	driver, _, spec := session(t, "two-turns", func(spec *ports.SessionSpec) {
		spec.Env = append(spec.Env, lanes.EnvLaneToken+"="+token, lanes.EnvWorkerSocket+"=/run/worker.sock")
	})
	play(t, driver, spec)
	raw, err := os.ReadFile(filepath.Join(spec.ConfigDir, "mcp-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Servers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		lanes.EnvLaneToken: "${" + lanes.EnvLaneToken + "}", lanes.EnvWorkerSocket: "${" + lanes.EnvWorkerSocket + "}",
	}
	if got := config.Servers[claudecode.ServerName].Env; !maps.Equal(got, want) {
		t.Errorf("server env %v, want %v", got, want)
	}
	err = filepath.WalkDir(spec.ConfigDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(body), token) {
			t.Errorf("%s holds the lane token", entry.Name())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedSettings(t *testing.T) {
	binding, err := secrets.Resolver{GOOS: "darwin"}.Resolve(t.Context(), "keychain:the entry")
	if err != nil {
		t.Fatal(err)
	}
	stable := filepath.Join(t.TempDir(), "state dir", "ropeshift")
	if err := os.MkdirAll(filepath.Dir(stable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(doubles.Ropeshift(t), stable); err != nil {
		t.Fatal(err)
	}
	driver, _, spec := session(t, "two-turns", func(spec *ports.SessionSpec) {
		spec.Binary, spec.Tools, spec.Credential = stable, []string{"lane_report"}, binding
	})
	play(t, driver, spec)
	settings, raw := readSettings(t, spec)
	if !slices.Equal(settings.Permissions.Allow, []string{"mcp__ropeshift__lane_report", "Bash(git add *)", "Bash(git commit *)"}) {
		t.Errorf("allow %v", settings.Permissions.Allow)
	}
	for event, groups := range settings.Hooks {
		want := "'" + stable + "' hook " + event
		if len(groups) != 1 || groups[0].Hooks[0].Command != want {
			t.Errorf("%s: %+v", event, groups)
		}
		fails := groups[0].Hooks[0].OnFailure
		if (event == "PreToolUse") != (fails == "block") {
			t.Errorf("%s fails with %q", event, fails)
		}
	}
	if len(settings.Hooks) == 0 {
		t.Error("no hooks")
	}
	if !strings.Contains(settings.APIKeyHelper, "find-generic-password -s 'the entry'") {
		t.Errorf("helper %q", settings.APIKeyHelper)
	}
	if strings.Contains(raw, "TOKEN") {
		t.Error("the settings name a token")
	}
	mcp, err := os.ReadFile(filepath.Join(spec.ConfigDir, "mcp-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcp, &config); err != nil {
		t.Fatal(err)
	}
	server := config.Servers[claudecode.ServerName]
	if server.Command != stable || !slices.Equal(server.Args, []string{"mcp"}) {
		t.Errorf("server %+v", server)
	}
	info, err := os.Stat(filepath.Join(spec.ConfigDir, "settings-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
}

// TestPreflightReadsTheInboundPolicy covers the one way a lane's generated `accept` can lose:
// the vendor honors a stricter value from a project's settings over `--settings`, which
// would silently stop a pane's brief and every message to a lane.
func TestPreflightReadsTheInboundPolicy(t *testing.T) {
	tests := []struct {
		name, file, body, want string
	}{
		{"nothing set", "", "", ""},
		{"accept passes", ".claude/settings.json", `{"crossSessionInbound":"accept"}`, ""},
		{"a shared file that holds", ".claude/settings.json", `{"crossSessionInbound":"hold"}`, "hold"},
		{"a shared file that refuses", ".claude/settings.json", `{"crossSessionInbound":"refuse"}`, "refuse"},
		{"a local file that refuses", ".claude/settings.local.json", `{"crossSessionInbound":"refuse"}`, "refuse"},
		{"the checkout's local file", "checkout/.claude/settings.local.json", `{"crossSessionInbound":"hold"}`, "hold"},
		{"a file the CLI would skip", ".claude/settings.json", `{not json`, ""},
		{"another key entirely", ".claude/settings.json", `{"model":"haiku"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver, _, spec := session(t, "two-turns", nil)
			spec.Repository = filepath.Join(spec.Dir, "checkout")
			if tt.file != "" {
				path := filepath.Join(spec.Dir, tt.file)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report, err := driver.Preflight(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			problems := strings.Join(report.Problems, ";")
			if tt.want == "" && problems != "" {
				t.Errorf("problems %v", report.Problems)
			}
			if tt.want != "" && !strings.Contains(problems, "crossSessionInbound to "+tt.want) {
				t.Errorf("problems %v", report.Problems)
			}
		})
	}
}

func TestPreflightRefusesWhatCannotStart(t *testing.T) {
	driver, _, spec := session(t, "two-turns", nil)
	tests := []struct {
		name   string
		change func(*ports.SessionSpec)
		want   string
	}{
		{"no worktree", func(s *ports.SessionSpec) { s.Dir = "/nowhere/at/all" }, "no worktree"},
		{"no model", func(s *ports.SessionSpec) { s.Model = "" }, "no model"},
		{"no agent on PATH", func(s *ports.SessionSpec) { s.Env = []string{"PATH=" + t.TempDir()} }, "not on PATH"},
		{"a parent session's marker", func(s *ports.SessionSpec) { s.Env = append(s.Env, "CLAUDE_CODE_CHILD_SESSION=1") }, "session variable"},
		{"a socket past the limit", func(s *ports.SessionSpec) { s.TempDir = "/" + strings.Repeat("d", 90) }, "byte limit"},
		{"a shim's socket past the limit", func(s *ports.SessionSpec) { s.TempDir, s.ConfigDir = "", "/"+strings.Repeat("d", 110) }, "byte limit"},
		{"a pack that is not there", func(s *ports.SessionSpec) { s.SystemPromptFile = "/nowhere/pack.md" }, "no context pack"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := spec
			tt.change(&bad)
			report, err := driver.Preflight(t.Context(), bad)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(strings.Join(report.Problems, ";"), tt.want) {
				t.Errorf("problems %v", report.Problems)
			}
		})
	}
}

// spawns starts every process on the real runner and keeps each spec it started.
type spawns struct {
	mu    sync.Mutex
	specs []ports.ProcessSpec
}

func (s *spawns) Start(ctx context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return osexec.Runner{}.Start(ctx, spec)
}

func (s *spawns) started() []ports.ProcessSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.specs)
}

// A shim must outlive a stop of the worker's service, so its spawn says so, and a version read,
// which ends at once, does not (D20).
func TestTheShimOutlivesTheWorkersService(t *testing.T) {
	_, path, spec := session(t, "two-turns", nil)
	runner := &spawns{}
	play(t, claudecode.New(runner, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second}), spec)
	shims := 0
	for _, started := range runner.started() {
		shim := started.Path == spec.Binary && slices.Contains(started.Args, "lane-shim")
		if shim {
			shims++
		}
		if started.Outlive != shim || (shim && !started.Detached) {
			t.Errorf("%s %v: outlive %v, detached %v", started.Path, started.Args, started.Outlive, started.Detached)
		}
	}
	if shims != 1 {
		t.Errorf("%d shims started", shims)
	}
}
