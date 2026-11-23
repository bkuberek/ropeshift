package claudecode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
)

// probeRig is the real driver on a private terminal server, fakeclaude standing for a version, and a
// probe's directories.
func probeRig(t *testing.T, drops ...string) (*claudecode.Driver, ports.ProbeSpec, string) {
	t.Helper()
	doubles.RequireTmux(t)
	return probeRigOn(t, doubles.PrivateTmux(t), drops...)
}

// probeRigOn is probeRig on a terminal the test gives.
func probeRigOn(t *testing.T, terminal ports.Terminal, drops ...string) (*claudecode.Driver, ports.ProbeSpec, string) {
	t.Helper()
	t.Cleanup(claudecode.ShortenProbeWaits(3 * time.Second))
	path := doubles.InstallFakeClaudeVersion(t, "lane-completed", "2.1.399")
	if len(drops) != 0 {
		dir, _, _ := strings.Cut(path, string(os.PathListSeparator))
		file := filepath.Join(dir, doubles.FixtureFile)
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var script map[string]any
		if err := json.Unmarshal(raw, &script); err != nil {
			t.Fatal(err)
		}
		script["probe_drops"] = drops
		if raw, err = json.Marshal(script); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	home, shared := t.TempDir(), apitest.ShortDir(t)
	env := []string{"PATH=" + path, "HOME=" + home, "FAKECLAUDE_SOCKETS=" + shared}
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: env, Terminal: terminal, StopGrace: 2 * time.Second})
	spec := ports.ProbeSpec{
		Dir: t.TempDir(), TempDir: apitest.ShortDir(t), Env: env, Model: "haiku", Binary: doubles.Ropeshift(t), SignIn: ports.SignInSubscription,
	}
	return driver, spec, path
}

// notPassed lists the surfaces a probe did not pass.
func notPassed(q lanes.Qualification) []lanes.Surface {
	var out []lanes.Surface
	for _, surface := range claudecode.Surfaces {
		if !q.Passed(surface) {
			out = append(out, surface)
		}
	}
	return out
}

// TestAProbeOfAWholeStandInPassesEveryCheck runs the probe on a stand-in with every surface, and its
// result qualifies the version it read, from what it saw.
func TestAProbeOfAWholeStandInPassesEveryCheck(t *testing.T) {
	driver, spec, _ := probeRig(t)
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if missing := notPassed(q); len(missing) != 0 {
		t.Errorf("a whole stand-in left %v not passed: %+v", missing, q.Checks)
	}
	if q.Version != "2.1.399" || q.Source != lanes.QualifiedByProbe || q.Degraded || q.SignIn != ports.SignInSubscription {
		t.Errorf("qualification %+v", q)
	}
}

// TestEachCheckFailsOnAStandInThatDropsItsSurface is the proof that no check passes by itself: a
// stand-in that leaves out or changes one surface fails that check. A socket line nobody reads also
// leaves the receipt and the hold unseen, a socket outside the private directory leaves no address a
// receipt can reach, and a bare `-p` binds no socket at all, so those checks follow theirs.
func TestEachCheckFailsOnAStandInThatDropsItsSurface(t *testing.T) {
	for _, tt := range []struct {
		drop string
		want []lanes.Surface
	}{
		{"socket_line", []lanes.Surface{claudecode.SurfaceSocketLine, claudecode.SurfaceReceipts, claudecode.SurfacePeerMessageHold}},
		{"uuid", []lanes.Surface{claudecode.SurfaceSocketLine}},
		{"receipts", []lanes.Surface{claudecode.SurfaceReceipts}},
		{"peer_message_hold", []lanes.Surface{claudecode.SurfacePeerMessageHold}},
		{"socket_dir", []lanes.Surface{claudecode.SurfaceReceipts, claudecode.SurfaceSocketDir}},
		{"unified_windows", []lanes.Surface{claudecode.SurfaceUnifiedWindows}},
		{"command_lifecycle", []lanes.Surface{claudecode.SurfaceCommandLifecycle}},
		{"login", []lanes.Surface{
			claudecode.SurfaceSocketLine, claudecode.SurfaceReceipts, claudecode.SurfacePeerMessageHold, claudecode.SurfaceSocketDir,
			claudecode.SurfaceUnifiedWindows, claudecode.SurfacePrintKeepsLogin,
		}},
		{"start_screens", []lanes.Surface{claudecode.SurfaceStartScreens}},
		{"max_budget_usd", []lanes.Surface{claudecode.SurfaceMaxBudget}},
		{"stream_usage", []lanes.Surface{claudecode.SurfaceStreamUsage}},
	} {
		t.Run(tt.drop, func(t *testing.T) {
			driver, spec, _ := probeRig(t, tt.drop)
			q, err := driver.Probe(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if got := notPassed(q); !slices.Equal(got, tt.want) {
				t.Errorf("not passed %v, want %v: %+v", got, tt.want, q.Checks)
			}
			degrades := slices.ContainsFunc(tt.want, func(s lanes.Surface) bool {
				return s != claudecode.SurfaceCommandLifecycle && s != claudecode.SurfacePeerMessageHold && s != claudecode.SurfaceMaxBudget &&
					s != claudecode.SurfaceStreamUsage
			})
			if q.Degraded != degrades {
				t.Errorf("degraded %v, want %v", q.Degraded, degrades)
			}
		})
	}
}

// A login check made on an API key cannot read a subscription's login, so it stays unobserved, and the probe does
// not ask for it.
func TestAProbeOnAnAPIKeyLeavesTheLoginUnobserved(t *testing.T) {
	driver, spec, _ := probeRig(t)
	spec.SignIn = ports.SignInAPIKey
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Checks[claudecode.SurfacePrintKeepsLogin] != lanes.CheckUnobserved || q.Degraded || !q.PassedAll(driver.ProbeChecks(ports.SignInAPIKey)) {
		t.Errorf("qualification %+v", q)
	}
}

// Window utilization on a `rate_limit_event` belongs to a subscription, so an API key's probe does not ask for it: the
// check stays unobserved whether the stream carries the figures or not, and the result is no worse for it.
func TestAProbeOnAnAPIKeyLeavesTheWindowsUnobserved(t *testing.T) {
	for _, tt := range []struct {
		name  string
		drops []string
	}{
		{name: "a stream that carries the windows"},
		{name: "a stream that drops them", drops: []string{"unified_windows"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			driver, spec, _ := probeRig(t, tt.drops...)
			spec.SignIn = ports.SignInAPIKey
			q, err := driver.Probe(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if got := q.Checks[claudecode.SurfaceUnifiedWindows]; got != lanes.CheckUnobserved {
				t.Errorf("an API key's probe reads the windows as %s, want unobserved", got)
			}
			if q.Degraded || !q.PassedAll(driver.ProbeChecks(ports.SignInAPIKey)) {
				t.Errorf("qualification %+v", q)
			}
		})
	}
}

// A probe runs every surface of the matrix, except the login and the windows where the sign-in is an API key and the
// screens where the driver has no terminal, and a result holds only when it passed all of those.
func TestAProbeRunsEveryCheckItCanObserve(t *testing.T) {
	t.Parallel()
	headless := claudecode.New(osexec.Runner{}, claudecode.Options{})
	withTerminal := claudecode.New(osexec.Runner{}, claudecode.Options{Terminal: fake.NewTerminal()})
	for _, tt := range []struct {
		name    string
		driver  *claudecode.Driver
		signIn  string
		without []lanes.Surface
	}{
		{"a subscription with a terminal", withTerminal, ports.SignInSubscription, nil},
		{"an unknown sign-in with a terminal", withTerminal, "", nil},
		{"an API key with a terminal", withTerminal, ports.SignInAPIKey, []lanes.Surface{claudecode.SurfacePrintKeepsLogin, claudecode.SurfaceUnifiedWindows}},
		{"a subscription with no terminal", headless, ports.SignInSubscription, []lanes.Surface{claudecode.SurfaceStartScreens}},
		{"an API key with no terminal", headless, ports.SignInAPIKey, []lanes.Surface{
			claudecode.SurfacePrintKeepsLogin, claudecode.SurfaceUnifiedWindows, claudecode.SurfaceStartScreens,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := slices.DeleteFunc(slices.Clone(claudecode.Surfaces), func(s lanes.Surface) bool { return slices.Contains(tt.without, s) })
			if got := tt.driver.ProbeChecks(tt.signIn); !slices.Equal(got, want) {
				t.Errorf("a probe runs %v, want %v", got, want)
			}
		})
	}
}

// A pane whose session exits during the start check ends the check at once, with the screens
// failed, and does not hold every lane for the check's whole wait.
func TestAProbePaneThatDiesEndsTheScreenCheck(t *testing.T) {
	driver, spec, _ := probeRig(t, "start_exit")
	t.Cleanup(claudecode.ShortenProbeWaits(30 * time.Second))
	started := time.Now()
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Checks[claudecode.SurfaceStartScreens] != lanes.CheckFailed {
		t.Errorf("a pane that died reads %s", q.Checks[claudecode.SurfaceStartScreens])
	}
	if took := time.Since(started); took > 20*time.Second {
		t.Errorf("the probe took %v, and waited on a pane that had died", took)
	}
}

// requireNoTmuxServer fails the test when the terminal's server still runs a moment after a stopped probe returned.
// The server ends itself with its last pane, and one that outlives the worker keeps every descriptor the worker
// handed it, so a stop that left the pane leaves the server too.
func requireNoTmuxServer(t *testing.T, server *tmux.Terminal) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); doubles.TmuxRunning(t, server); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stopped probe left the terminal server running")
		}
	}
}

// stoppingTerminal ends a probe's context once tmux has made the probe's pane, as a worker that stops
// then does. With cut, the open answers with that end, as a call the stop cut short after tmux made the
// pane. Without it, the end comes once the pane shows its trust dialog, which takes no interrupt.
type stoppingTerminal struct {
	ports.Terminal
	stop context.CancelFunc
	cut  bool
}

func (s *stoppingTerminal) Open(ctx context.Context, spec ports.PaneSpec) (ports.Pane, error) {
	pane, err := s.Terminal.Open(context.WithoutCancel(ctx), spec)
	defer s.stop()
	if err != nil {
		return pane, err
	}
	if s.cut {
		return ports.Pane{}, context.Canceled
	}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if screen, err := s.Screen(ctx, spec.Name); err != nil || strings.Contains(screen, "Accessing workspace") {
			break
		}
	}
	return pane, nil
}

// A probe stopped as its pane opens removes the pane at once, whether the open answered or the stop
// cut it short. A stopping worker has a bounded time to end, and a pane it leaves keeps an agent CLI
// session and the terminal server running after it, with every descriptor the worker inherited.
func TestAProbeStoppedAtItsPaneLeavesNoPane(t *testing.T) {
	for _, tt := range []struct {
		name string
		cut  bool
	}{{"opened", false}, {"cut short", true}} {
		t.Run(tt.name, func(t *testing.T) {
			doubles.RequireTmux(t)
			server := doubles.PrivateTmux(t)
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			driver, spec, _ := probeRigOn(t, &stoppingTerminal{Terminal: server, stop: stop, cut: tt.cut})
			// The pane's session ignores the interrupt a probe that ends on its own sends first, so a
			// stopped probe that waited for it would wait this long.
			t.Cleanup(claudecode.ShortenProbeWaits(30 * time.Second))
			started := time.Now()
			if _, err := driver.Probe(ctx, spec); !errors.Is(err, context.Canceled) {
				t.Errorf("a stopped probe returned %v", err)
			}
			if took := time.Since(started); took > 10*time.Second {
				t.Errorf("the stopped probe took %v to end", took)
			}
			if pane, err := server.Pane(context.WithoutCancel(ctx), claudecode.ProbePane); !errors.Is(err, ports.ErrNotFound) {
				t.Errorf("the stopped probe left its pane: %+v, %v", pane, err)
			}
			requireNoTmuxServer(t, server)
		})
	}
}

// stopAtFirstCall ends a probe's context at the first call the probe makes on its terminal, which is the
// closing of a pane an earlier probe left, and counts the panes the probe then asks to open.
type stopAtFirstCall struct {
	ports.Terminal
	stop   context.CancelFunc
	opened atomic.Int32
}

func (s *stopAtFirstCall) Close(ctx context.Context, name string) error {
	s.stop()
	return s.Terminal.Close(ctx, name)
}

func (s *stopAtFirstCall) Open(ctx context.Context, spec ports.PaneSpec) (ports.Pane, error) {
	s.opened.Add(1)
	return s.Terminal.Open(ctx, spec)
}

// A probe that is stopped before it opens its pane opens none, so a stopping worker starts no pane, and with
// it no session of the agent CLI and no terminal server, that nothing is left to end.
func TestAProbeStoppedBeforeItsPaneOpensOpensNone(t *testing.T) {
	doubles.RequireTmux(t)
	server := doubles.PrivateTmux(t)
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	terminal := &stopAtFirstCall{Terminal: server, stop: stop}
	driver, spec, _ := probeRigOn(t, terminal)
	started := time.Now()
	if _, err := driver.Probe(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped probe returned %v", err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("the stopped probe took %v to end", took)
	}
	if n := terminal.opened.Load(); n != 0 {
		t.Errorf("the probe opened %d panes after it was stopped", n)
	}
	if pane, err := server.Pane(context.WithoutCancel(ctx), claudecode.ProbePane); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the stopped probe left a pane: %+v, %v", pane, err)
	}
	requireNoTmuxServer(t, server)
}

// A sign-in nobody could read is a subscription, as everywhere else, so the login check runs.
func TestAProbeOnAnUnknownSignInChecksTheLogin(t *testing.T) {
	driver, spec, _ := probeRig(t, "login")
	spec.SignIn = ""
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Checks[claudecode.SurfacePrintKeepsLogin] != lanes.CheckFailed || !q.Degraded {
		t.Errorf("qualification %+v", q)
	}
}

// A version that changes while the probe runs leaves a result that names no one version.
func TestAProbeRefusesAVersionThatMovedUnderIt(t *testing.T) {
	driver, spec, path := probeRig(t, "start_screens")
	done := make(chan struct{})
	go func() {
		defer close(done)
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if found, _ := filepath.Glob(filepath.Join(spec.Dir, "screens-config", "launch.json")); len(found) != 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		doubles.SetFakeClaudeVersion(t, path, "2.1.400")
	}()
	_, err := driver.Probe(t.Context(), spec)
	<-done
	if !errors.Is(err, claudecode.ErrVersionMoved) {
		t.Errorf("err %v", err)
	}
}

// gatedRunner starts processes with the system's runner, and holds each headless session's start until
// every one of the probe's headless checks has asked for its own. A probe whose checks run one after
// another never gets a second start while its first is held, so the gate opens only on checks that overlap.
type gatedRunner struct {
	ports.ProcessRunner
	want int
	open chan struct{}

	mu      sync.Mutex
	waiting int
}

// headlessChecks is how many of the probe's checks start a headless session: accept, refuse, hold, and budget.
const headlessChecks = 4

func newGatedRunner() *gatedRunner {
	return &gatedRunner{ProcessRunner: osexec.Runner{}, want: headlessChecks, open: make(chan struct{})}
}

func (r *gatedRunner) Start(ctx context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	if slices.Contains(spec.Args, "-p") {
		r.mu.Lock()
		if r.waiting++; r.waiting == r.want {
			close(r.open)
		}
		r.mu.Unlock()
		select {
		case <-r.open:
		case <-ctx.Done():
			r.leave()
		// The bound only ends a wait the gate never opens, so a probe that runs its checks one at a time
		// fails the test and does not hang it. A start that leaves no longer counts as waiting.
		case <-time.After(10 * time.Second):
			r.leave()
		}
	}
	return r.ProcessRunner.Start(ctx, spec)
}

// leave takes a held start out of the count, unless the gate opened meanwhile.
func (r *gatedRunner) leave() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.opened() {
		r.waiting--
	}
}

// opened says every headless check asked for its session while the others were held.
func (r *gatedRunner) opened() bool {
	select {
	case <-r.open:
		return true
	default:
		return false
	}
}

// TestAProbeRunsItsChecksAtOnce is the probe's checks running side by side: each headless check's session
// is held at its start until all four have asked for theirs, which only checks that overlap can do, and
// the result is still every check's own.
func TestAProbeRunsItsChecksAtOnce(t *testing.T) {
	_, spec, _ := probeRig(t)
	// Every check here passes, so its waits bound only a failure, and a busy machine does not end one early.
	t.Cleanup(claudecode.ShortenProbeWaits(30 * time.Second))
	runner := newGatedRunner()
	driver := claudecode.New(runner, claudecode.Options{Env: spec.Env, Terminal: doubles.PrivateTmux(t), StopGrace: 2 * time.Second})
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.opened() {
		t.Errorf("the probe's headless checks never ran at once: no more than one asked for a session while the others were held")
	}
	if missing := notPassed(q); len(missing) != 0 {
		t.Errorf("checks that ran at once left %v not passed: %+v", missing, q.Checks)
	}
}

// The probe's sessions start as a lane's do, with the machine's own settings, and with nothing the
// design refuses.
func TestAProbeStartsNoSessionTheDesignRefuses(t *testing.T) {
	driver, spec, path := probeRig(t)
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	sessions, capped := 0, 0
	for _, seen := range doubles.ProbesSeenBy(t, doubles.BinDir(path)) {
		sessions++
		if at := slices.Index(seen.Args, "--max-budget-usd"); at >= 0 {
			capped++
			if at+1 >= len(seen.Args) || seen.Args[at+1] != "0.0001" {
				t.Errorf("the budget check's cap is not a fraction of a cent: %v", seen.Args)
			}
		}
		for _, refused := range []string{"--bare", "--dangerously-skip-permissions", "bypassPermissions"} {
			if slices.Contains(seen.Args, refused) {
				t.Errorf("a probe passed %s: %v", refused, seen.Args)
			}
		}
		if seen.Env[claudecode.EnvProbe] != "1" || seen.Env[lanes.EnvWorkerSocket] != "" {
			t.Errorf("a probe's session reads as a lane's: %v", seen.Env)
		}
	}
	if sessions != 5 {
		t.Errorf("the probe ran %d sessions, and it runs four headless and one pane", sessions)
	}
	if capped != 1 {
		t.Errorf("%d probe sessions started with a budget cap, and the budget check's is the only one", capped)
	}
}

// The budget check pays for a session on each probe, and a version whose flag does not bound a turn fails it each time
// while the check gates only a backstop. A probe of a version whose last result failed it does not run it again, and its
// result reads failed as before; a failure of another version's result says nothing of this one.
func TestAFailedBudgetCheckIsNotRunAgainForItsVersion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version string
		capped  int
	}{
		{name: "a result for the version", version: "2.1.399", capped: 0},
		{name: "a result for another version", version: "2.1.398", capped: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			driver, spec, path := probeRig(t)
			held := lanes.Qualification{
				Driver: claudecode.DriverName, Version: tt.version, Source: lanes.QualifiedByProbe,
				Checks: map[lanes.Surface]lanes.CheckResult{claudecode.SurfaceMaxBudget: lanes.CheckFailed},
			}
			driver.Qualify(held)
			q, err := driver.Probe(t.Context(), spec)
			if err != nil {
				t.Fatal(err)
			}
			capped := 0
			for _, seen := range doubles.ProbesSeenBy(t, doubles.BinDir(path)) {
				if slices.Contains(seen.Args, "--max-budget-usd") {
					capped++
				}
			}
			if capped != tt.capped {
				t.Errorf("%d probe sessions started with a budget cap, want %d", capped, tt.capped)
			}
			want := lanes.CheckPassed
			if tt.capped == 0 {
				want = lanes.CheckFailed
			}
			if got := q.Checks[claudecode.SurfaceMaxBudget]; got != want {
				t.Errorf("the budget check reads %s, want %s", got, want)
			}
		})
	}
}

// Every probe session, headless and in its pane, starts with a lane's isolation, so what the probe qualifies is what a
// lane runs, and a piece of the member's own setup that reaches the probe reached it past the same flags and is a leak
// (ADR-0040): no user or local settings, ropeshift's MCP configuration alone, and no auto memory.
func TestAProbeRunsWithALanesIsolation(t *testing.T) {
	driver, spec, path := probeRig(t)
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	seen := doubles.ProbesSeenBy(t, doubles.BinDir(path))
	if len(seen) != 5 {
		t.Fatalf("the probe ran %d sessions", len(seen))
	}
	for _, session := range seen {
		at := slices.Index(session.Args, "--setting-sources")
		if at < 0 || at+1 >= len(session.Args) || session.Args[at+1] != "project" || !slices.Contains(session.Args, "--strict-mcp-config") {
			t.Errorf("a probe session started without a lane's flags: %v", session.Args)
		}
		if memory := session.Env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"]; memory != "1" {
			t.Errorf("a probe session started with CLAUDE_CODE_DISABLE_AUTO_MEMORY=%q: %v", memory, session.Args)
		}
	}
}
