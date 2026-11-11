package entry

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/profiles"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/contextpacks"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/mcpserver"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/questions"
	buildversion "ropeshift.com/ropeshift/internal/version"
)

func TestDispatchOnStartedName(t *testing.T) {
	tests := []struct {
		name string
		arg0 string
		want string
	}{
		{"the CLI by bare name", "ropeshift", "ropeshift"},
		{"the CLI by path", "/opt/ropeshift/ropeshift", "ropeshift"},
		{"the daemon by link", "/opt/ropeshift/ropeshiftd", "ropeshiftd"},
		{"a renamed binary acts as the CLI", "/tmp/rs", "ropeshift"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Main([]string{tt.arg0, "version", "--json"}, &stdout, &stderr)
			if code != exitOK {
				t.Fatalf("exit %d: %s", code, stderr.String())
			}
			var got versionInfo
			if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not JSON: %v: %q", err, stdout.String())
			}
			if got.Command != tt.want {
				t.Errorf("command %q, want %q", got.Command, tt.want)
			}
			if got.Go != runtime.Version() || got.OS != runtime.GOOS || got.Arch != runtime.GOARCH {
				t.Errorf("build fields do not describe this binary: %+v", got)
			}
		})
	}
}

func TestVersionPlainText(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Main([]string{"ropeshift", "version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "ropeshift ") {
		t.Errorf("stdout %q", stdout.String())
	}
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no program name", nil},
		{"no command", []string{"ropeshift"}},
		{"unknown command", []string{"ropeshift", "launch"}},
		{"unknown flag", []string{"ropeshift", "version", "--xml"}},
		{"stray argument", []string{"ropeshift", "version", "now"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Main(tt.args, &stdout, &stderr); code != exitUsage {
				t.Errorf("exit %d, want %d", code, exitUsage)
			}
			if stderr.Len() == 0 {
				t.Error("a usage error wrote nothing to stderr")
			}
		})
	}
}

// buildCLI builds the CLI with no cgo into dir, with the given linker flags.
func buildCLI(t *testing.T, dir string, ldflags string) string {
	t.Helper()
	binary := filepath.Join(dir, "ropeshift")
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", binary, "ropeshift.com/ropeshift/cmd/ropeshift")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

func runVersionJSON(t *testing.T, path string) versionInfo {
	t.Helper()
	out, err := exec.Command(path, "version", "--json").Output()
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var got versionInfo
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s: %v: %q", path, err, out)
	}
	return got
}

// TestOneBinaryTwoNames builds the CLI with no cgo and the stamp a release build sets, and starts it
// through a link, as a release package does. Both names, and the MCP server it serves, report the
// stamp.
func TestOneBinaryTwoNames(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real binary")
	}
	const (
		stamped  = "ropeshift.com/ropeshift/internal/version"
		release  = "v0.9.0-rc.2"
		revision = "0123456789abcdef0123456789abcdef01234567"
		built    = "2026-11-08T10:00:00Z"
	)
	dir := t.TempDir()
	binary := buildCLI(t, dir, strings.Join([]string{
		"-X", stamped + ".version=" + release, "-X", stamped + ".commit=" + revision,
		"-X", stamped + ".date=" + built, "-X", stamped + ".modified=false",
	}, " "))
	link := filepath.Join(dir, "ropeshiftd")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{binary: "ropeshift", link: "ropeshiftd"} {
		got := runVersionJSON(t, path)
		if got.Command != want {
			t.Errorf("started as %s, ran as %s", want, got.Command)
		}
		if got.Version != release || got.Commit != revision || got.Date != built || got.Modified {
			t.Errorf("%s does not report its stamp: %+v", want, got)
		}
	}
	mcp := exec.Command(binary, "mcp")
	mcp.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	out, err := mcp.Output()
	if err != nil {
		t.Fatalf("mcp: %v", err)
	}
	if got := serverVersion(t, out); got != release {
		t.Errorf("the MCP server reports version %q, want %q", got, release)
	}
}

// A build with no release stamp at a commit, as go build and make do, is a development build, even
// though the toolchain records a pseudo-version for it.
func TestAPlainBuildReportsADevelopmentVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real binary")
	}
	got := runVersionJSON(t, buildCLI(t, t.TempDir(), ""))
	if got.Version != buildversion.Development {
		t.Errorf("a plain build reports version %q, want %q", got.Version, buildversion.Development)
	}
}

func TestTheMCPServerReportsTheBinaryVersion(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	if err := mcpserver.Serve(in, &out, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := serverVersion(t, out.Bytes()), buildversion.Read("ropeshift").Version; got != want {
		t.Errorf("serverInfo.version %q, want %q", got, want)
	}
}

func serverVersion(t *testing.T, line []byte) string {
	t.Helper()
	var reply struct {
		Result struct {
			ServerInfo struct {
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		t.Fatalf("%v: %q", err, line)
	}
	return reply.Result.ServerInfo.Version
}

// A wake with a timer comes due with nothing to touch it, so the daemon has to run this tick.
func TestTheDaemonRunsTheWakeTick(t *testing.T) {
	opts, ok := parseSolo([]string{"--wake-tick", "250ms"}, io.Discard)
	if !ok {
		t.Fatal("the flag did not parse")
	}
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: soloScope}, tickActor("solo")); err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(registryOf(store, soloScope), soloScope, opts, fake.NewCheckouts(), questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "wakes" })
	if at < 0 {
		t.Fatalf("ticks %+v", ticks)
	}
	if ticks[at].Every != 250*time.Millisecond {
		t.Errorf("every %v", ticks[at].Every)
	}
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Errorf("an empty tick: %v", err)
	}
}

// TestTheDaemonExpiresMessages holds the tick that tells a sender a message never got through,
// without which a lost one stays `handed_off` for good.
func TestTheDaemonExpiresMessages(t *testing.T) {
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: soloScope}, tickActor("solo")); err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(registryOf(store, soloScope), soloScope, soloOptions{wakeTick: time.Second}, fake.NewCheckouts(), questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "messages" })
	if at < 0 {
		t.Fatalf("ticks %+v", ticks)
	}
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Errorf("an empty tick: %v", err)
	}
}

func TestMessageWindowsComeFromTheUserLayer(t *testing.T) {
	dir := t.TempDir()
	paths := api.Paths{StateDir: dir}
	layer := "[messages.instruction]\nstale_after = \"1m\"\nexpire_after = \"3m\"\n"
	if err := os.WriteFile(filepath.Join(dir, profiles.UserFile), []byte(layer), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := soloWindows(paths).For(messages.KindInstruction); got.ExpireAfter != 3*time.Minute || got.StaleAfter != time.Minute {
		t.Errorf("window %+v", got)
	}
	if got := soloWindows(paths).For(messages.KindAnswer); got != messages.DefaultWindow {
		t.Errorf("window %+v", got)
	}
}

func TestAPackBudgetComesFromTheUserLayer(t *testing.T) {
	dir := t.TempDir()
	paths := api.Paths{StateDir: dir}
	if got := soloPackTokens(paths); got != contextpacks.DefaultBudgetTokens {
		t.Errorf("no layer gives a budget of %d", got)
	}
	if err := os.WriteFile(filepath.Join(dir, profiles.UserFile), []byte("[context]\npack_budget_tokens = 1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := soloPackTokens(paths); got != 1234 {
		t.Errorf("a budget of %d", got)
	}
	if err := os.WriteFile(filepath.Join(dir, profiles.UserFile), []byte("not toml ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := soloPackTokens(paths); got != 0 {
		t.Errorf("a layer that does not parse gives %d, and the default is the control plane's", got)
	}
}

func TestBatchLimitsComeFromTheUserLayer(t *testing.T) {
	dir := t.TempDir()
	paths := api.Paths{StateDir: dir}
	tests := []struct {
		name    string
		profile string
		want    questions.BatchPolicy
	}{
		{"no layer leaves the built-in limits", "", questions.BatchPolicy{WindowMs: 15 * 60_000, MaxWaitMs: 5 * 60_000}},
		{"the user layer sets them", "[lifecycle]\nhuman_batch_window = \"1h\"\nhuman_batch_max_wait = \"10m\"\n", questions.BatchPolicy{WindowMs: 3_600_000, MaxWaitMs: 600_000}},
		{"a layer that does not parse leaves zero, which means the defaults", "[lifecycle", questions.BatchPolicy{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, profiles.UserFile), []byte(tt.profile), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := soloBatches(paths); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTheDefaultLimitsLetABlockingQuestionHurry holds the rule the two limits exist for: a
// fixed window leaves a blocking question nothing to shorten unless its wait is the shorter
// limit.
func TestTheDefaultLimitsLetABlockingQuestionHurry(t *testing.T) {
	limits := soloBatches(api.Paths{StateDir: t.TempDir()}).Filled()
	if limits.MaxWaitMs >= limits.WindowMs {
		t.Fatalf("limits %+v: a blocking question could never close a batch early", limits)
	}
}
