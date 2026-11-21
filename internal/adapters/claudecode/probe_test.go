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
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
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
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: env, Terminal: doubles.PrivateTmux(t), StopGrace: 2 * time.Second})
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

// A login check made on an API key cannot read a subscription's login, so it stays unobserved.
func TestAProbeOnAnAPIKeyLeavesTheLoginUnobserved(t *testing.T) {
	driver, spec, _ := probeRig(t)
	spec.SignIn = ports.SignInAPIKey
	q, err := driver.Probe(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Checks[claudecode.SurfacePrintKeepsLogin] != lanes.CheckUnobserved || q.Degraded {
		t.Errorf("qualification %+v", q)
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
