package claudecode_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/apitest"
	"ropeshift.dev/ropeshift/test/doubles"
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
				return s != claudecode.SurfaceCommandLifecycle && s != claudecode.SurfacePeerMessageHold
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

// The probe's sessions start as a lane's do, with the machine's own settings, and with nothing the
// design refuses.
func TestAProbeStartsNoSessionTheDesignRefuses(t *testing.T) {
	driver, spec, path := probeRig(t)
	if _, err := driver.Probe(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	sessions := 0
	for _, seen := range doubles.ProbesSeenBy(t, doubles.BinDir(path)) {
		sessions++
		for _, refused := range []string{"--bare", "--dangerously-skip-permissions", "bypassPermissions"} {
			if slices.Contains(seen.Args, refused) {
				t.Errorf("a probe passed %s: %v", refused, seen.Args)
			}
		}
		if seen.Env[claudecode.EnvProbe] != "1" || seen.Env[lanes.EnvWorkerSocket] != "" {
			t.Errorf("a probe's session reads as a lane's: %v", seen.Env)
		}
	}
	if sessions != 4 {
		t.Errorf("the probe ran %d sessions, and it runs three headless and one pane", sessions)
	}
}
