package claudecode_test

import (
	"encoding/json"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/claudecode"
	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/secrets"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
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
	}
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
	driver, path, spec := session(t, "unqualified-version", func(spec *ports.SessionSpec) { spec.TempDir = "/tmp/rs-private" })
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

func TestAQualifiedVersionGetsItsPrivateDirectory(t *testing.T) {
	driver, path, spec := session(t, "two-turns", func(spec *ports.SessionSpec) { spec.TempDir = "/tmp/rs-private" })
	got, err := driver.Capabilities(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Degraded {
		t.Errorf("capabilities %+v", got)
	}
	play(t, driver, spec)
	if value := doubles.SeenEnv(t, path, "CLAUDE_CODE_TMPDIR"); value != "/tmp/rs-private" {
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
		if name != "PATH" && name != "ROPESHIFT_LANE_ID" && name != "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS" {
			t.Errorf("%s reached the session", name)
		}
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

// TestGeneratedMCPNamesTheToken holds both halves: the server is told about the lane
// token by name, and the value reaches no file.
func TestGeneratedMCPNamesTheToken(t *testing.T) {
	const token = "the-lane-token-value"
	driver, _, spec := session(t, "two-turns", func(spec *ports.SessionSpec) {
		spec.Binary = "/state/bin/ropeshift"
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
	const stable = "/state dir/bin/ropeshift"
	driver, _, spec := session(t, "two-turns", func(spec *ports.SessionSpec) {
		spec.Binary, spec.Tools, spec.Credential = stable, []string{"lane_report"}, binding
	})
	play(t, driver, spec)
	settings, raw := readSettings(t, spec)
	if !slices.Equal(settings.Permissions.Allow, []string{"mcp__ropeshift__lane_report"}) {
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
