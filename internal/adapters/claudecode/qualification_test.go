package claudecode_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/doubles"
)

// probed is a probe's result for a version with every surface passed but those named failed.
func probed(version string, failed ...lanes.Surface) lanes.Qualification {
	q := lanes.Qualification{
		Driver: claudecode.DriverName, Version: version, Source: lanes.QualifiedByProbe, SignIn: ports.SignInSubscription,
		Checks: map[lanes.Surface]lanes.CheckResult{},
	}
	for _, surface := range claudecode.Surfaces {
		q.Checks[surface] = lanes.CheckPassed
		if slices.Contains(failed, surface) {
			q.Checks[surface] = lanes.CheckFailed
		}
	}
	return q
}

// TestAFailedCheckRemovesExactlyItsSurface is the gating table: for each check in the qualification
// matrix, a failed check takes away the one use it guards, and leaves every other use alone. The
// two surfaces the design only notes take nothing away, and the start screens take nothing beyond
// the start check's own rule, which parks a lane on a screen it does not know.
func TestAFailedCheckRemovesExactlyItsSurface(t *testing.T) {
	all := claudecode.UseOf(probed("9.9.9"))
	if all != (claudecode.Use{PeerInjection: true, Receipts: true, PrivateSockets: true, WindowFigures: true, HeadlessOnSubscription: true}) {
		t.Fatalf("every check passed and a use is missing: %+v", all)
	}
	for _, tt := range []struct {
		surface  lanes.Surface
		removed  func(*claudecode.Use)
		degrades bool
	}{
		{claudecode.SurfaceSocketLine, func(u *claudecode.Use) { u.PeerInjection = false }, true},
		{claudecode.SurfaceReceipts, func(u *claudecode.Use) { u.Receipts = false }, true},
		{claudecode.SurfaceSocketDir, func(u *claudecode.Use) { u.PrivateSockets = false }, true},
		{claudecode.SurfaceUnifiedWindows, func(u *claudecode.Use) { u.WindowFigures = false }, true},
		{claudecode.SurfacePrintKeepsLogin, func(u *claudecode.Use) { u.HeadlessOnSubscription = false }, true},
		{claudecode.SurfaceStartScreens, func(*claudecode.Use) {}, true},
		{claudecode.SurfaceCommandLifecycle, func(*claudecode.Use) {}, false},
		{claudecode.SurfacePeerMessageHold, func(*claudecode.Use) {}, false},
	} {
		t.Run(string(tt.surface), func(t *testing.T) {
			want := all
			tt.removed(&want)
			q := probed("9.9.9", tt.surface)
			if got := claudecode.UseOf(q); got != want {
				t.Errorf("a failed %s leaves %+v, want %+v", tt.surface, got, want)
			}
			q.Checks[tt.surface] = lanes.CheckUnobserved
			if got := claudecode.UseOf(q); got != want {
				t.Errorf("an unobserved %s leaves %+v, want %+v", tt.surface, got, want)
			}
			if got := claudecode.Judge(probed("9.9.9", tt.surface), true).Degraded; got != tt.degrades {
				t.Errorf("degraded %v, want %v", got, tt.degrades)
			}
		})
	}
	if len(claudecode.Surfaces) != 8 {
		t.Errorf("the matrix has 8 checks and the table covers 8, but the driver lists %d", len(claudecode.Surfaces))
	}
}

// A login check made on an API key reads nothing of a subscription, so it degrades nothing, and a
// driver with no terminal never matches a screen.
func TestASurfaceOnlyCountsWhereItIsUsed(t *testing.T) {
	q := probed("9.9.9", claudecode.SurfacePrintKeepsLogin)
	q.SignIn = ports.SignInAPIKey
	if claudecode.Judge(q, true).Degraded {
		t.Error("an API key's probe cannot see a subscription's login, and its result degraded the version")
	}
	if claudecode.Judge(probed("9.9.9", claudecode.SurfaceStartScreens), false).Degraded {
		t.Error("a driver with no terminal matches no screen, and the screens degraded it")
	}
	unknown := probed("9.9.9", claudecode.SurfacePrintKeepsLogin)
	unknown.SignIn = ""
	if !claudecode.Judge(unknown, true).Degraded {
		t.Error("a sign-in nobody could read is a subscription, and its lost login degraded nothing")
	}
}

func TestTheShippedMatrixIsWellFormed(t *testing.T) {
	raw, err := os.ReadFile("matrix.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := claudecode.CheckMatrix(raw); err != nil {
		t.Fatal(err)
	}
	// 2.1.316 is the spikes' version, and RS14 qualified 2.1.317 and 2.1.319 through the production
	// probe.
	for _, version := range []string{"2.1.316", "2.1.317", "2.1.319"} {
		if !slices.Contains(claudecode.MatrixVersions(), version) || claudecode.MatrixRow(version).Degraded {
			t.Errorf("the matrix does not qualify %s: %v", version, claudecode.MatrixVersions())
		}
	}
	for _, broken := range []string{
		`{"driver":"claude-code","versions":[{"version":"1","checks":{}}]}`,
		`{"driver":"claude-code","versions":[{"version":"1","checks":{"socket_line":"maybe"}}]}`,
		`{"driver":"claude-code","versions":[{"version":"1","checks":{"no_such":"passed"}}]}`,
		`{"driver":"codex","versions":[]}`,
	} {
		if claudecode.CheckMatrix([]byte(broken)) == nil {
			t.Errorf("a broken matrix passed: %s", broken)
		}
	}
}

// A row qualifies only its own version, and nothing in it extends to the next one.
func TestAMatrixRowQualifiesOnlyItsVersion(t *testing.T) {
	if claudecode.MatrixRow("2.1.316").Degraded {
		t.Error("2.1.316 passed every check in the spikes")
	}
	next := claudecode.MatrixRow("2.1.399")
	if !next.Degraded || next.Passed(claudecode.SurfaceSocketLine) {
		t.Errorf("a version the matrix does not hold read %+v", next)
	}
}

// TestASelfUpdateNeedsAProbeEitherWay is the rule for a version that changes under a worker: once
// a worker hands the driver a probe's result, only that result counts, and only for its version. A
// newer version is unqualified until its own probe ends, whether the last one passed or failed, and
// the shipped matrix does not stand in for the machine's probe.
func TestASelfUpdateNeedsAProbeEitherWay(t *testing.T) {
	for _, tt := range []struct {
		name      string
		installed string
		probe     lanes.Qualification
		degraded  bool
	}{
		{"before any probe the shipped row stands", "2.1.316", lanes.Qualification{}, false},
		{"a probe's pass holds for its version", "2.1.316", probed("2.1.316"), false},
		{"a probe's failure holds for its version, whatever the matrix says", "2.1.316", probed("2.1.316", claudecode.SurfaceSocketLine), true},
		{"a qualified version that updated is unqualified", "2.1.399", probed("2.1.316"), true},
		{"a degraded version that updated is still unqualified", "2.1.399", probed("2.1.316", claudecode.SurfaceSocketLine), true},
		{"the update's own probe decides", "2.1.399", probed("2.1.399"), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := doubles.InstallFakeClaudeVersion(t, "lane-completed", tt.installed)
			driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}})
			if tt.probe.Version != "" {
				driver.Qualify(tt.probe)
			}
			got, err := driver.Capabilities(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != tt.installed || got.Degraded != tt.degraded {
				t.Errorf("capabilities %s degraded %v, want %s degraded %v", got.Version, got.Degraded, tt.installed, tt.degraded)
			}
			want := ports.InjectionBetweenTools
			if tt.degraded {
				want = ports.InjectionNone
			}
			if got.Injection != want {
				t.Errorf("injection %q, want %q", got.Injection, want)
			}
			report, err := driver.Doctor(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if report.Qualified == tt.degraded {
				t.Errorf("the doctor says qualified %v of a version that is degraded %v", report.Qualified, tt.degraded)
			}
		})
	}
}

// TestAnAdoptedSessionKeepsTheVersionItStartedOn is a self-update under a worker that then restarts:
// the session's process still runs the version it started on, so a new driver qualified for the new
// version finds it degraded and gives it no surface the old version's checks refused.
func TestAnAdoptedSessionKeepsTheVersionItStartedOn(t *testing.T) {
	first, path, spec := session(t, "lane-survives", nil)
	doubles.SetFakeClaudeVersion(t, path, "2.1.399")
	first.Qualify(probed("2.1.399", claudecode.SurfaceSocketLine))
	handle, err := first.Start(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if handle.Version != "2.1.399" || !handle.Degraded {
		t.Fatalf("handle %+v", handle)
	}
	events, err := first.Events(t.Context(), handle)
	if err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event.Kind == ports.EventSessionBound {
			break
		}
	}
	doubles.SetFakeClaudeVersion(t, path, "2.1.400")
	next := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, StopGrace: 2 * time.Second})
	next.Qualify(probed("2.1.400"))
	located, err := next.Find(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if located.Handle.Version != "2.1.399" || !located.Handle.Degraded {
		t.Errorf("found %+v, which is the installed version's and not the process's", located.Handle)
	}
	// The session's socket is known before anything is read, as its SessionStart hook would name it.
	sockets, err := filepath.Glob(filepath.Join(spec.TempDir, "cc-socks", "[0-9]*.sock"))
	if err != nil || len(sockets) != 1 {
		t.Fatalf("sockets %v, %v", sockets, err)
	}
	hooks := []lanes.HookRecord{{HookEvent: lanes.HookEvent{Lane: spec.Lane, Generation: spec.Generation, Event: "SessionStart", Socket: sockets[0]}}}
	if err := next.Adopt(t.Context(), located.Handle, ports.AdoptFrom{Spec: spec, Hooks: hooks}); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Send(t.Context(), located.Handle, ports.Input{Text: "from a peer", Peer: true}); !errors.Is(err, ports.ErrNoChannel) {
		t.Errorf("an adopted session on a version whose socket failed took a peer's message: %v", err)
	}
	if err := os.WriteFile(filepath.Join(doubles.BinDir(path), "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := next.Stop(t.Context(), located.Handle, ports.StopNow); err != nil {
		t.Fatal(err)
	}
}

// A subscription whose `-p` lost its login has no headless lane, and the tmux fallback in ADR-0002
// is what is left. An API key's lanes keep it.
func TestALostLoginOnPrintRemovesHeadlessForASubscription(t *testing.T) {
	path := doubles.InstallFakeClaudeVersion(t, "lane-completed", "2.1.399")
	driver := claudecode.New(osexec.Runner{}, claudecode.Options{Env: []string{"PATH=" + path}, Terminal: fake.NewTerminal()})
	driver.Qualify(probed("2.1.399", claudecode.SurfacePrintKeepsLogin))
	got, err := driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(got.RunModes, ports.RunModeHeadless) || !slices.Contains(got.RunModes, ports.RunModeInteractive) {
		t.Errorf("run modes %v", got.RunModes)
	}
	unknown := probed("2.1.399", claudecode.SurfacePrintKeepsLogin)
	unknown.SignIn = ""
	driver.Qualify(unknown)
	if got, _ := driver.Capabilities(t.Context()); slices.Contains(got.RunModes, ports.RunModeHeadless) {
		t.Errorf("a sign-in nobody could read kept headless lanes after its login check failed: %v", got.RunModes)
	}
	key := probed("2.1.399", claudecode.SurfacePrintKeepsLogin)
	key.SignIn = ports.SignInAPIKey
	driver.Qualify(key)
	if got, _ := driver.Capabilities(t.Context()); !slices.Contains(got.RunModes, ports.RunModeHeadless) {
		t.Errorf("an API key lost its headless lanes: %v", got.RunModes)
	}
	driver.Qualify(lanes.Qualification{})
	if got, _ := driver.Capabilities(t.Context()); !slices.Contains(got.RunModes, ports.RunModeHeadless) {
		t.Errorf("a version no probe has checked lost its headless lanes: %v", got.RunModes)
	}
}
