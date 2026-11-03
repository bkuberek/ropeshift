// Package doubles holds what stands in for the agent CLI. No test outside the
// realsession build runs the real `claude`: a test that needs an agent gets
// fakeclaude first on PATH, and every other test gets the canary, which fails the
// test if anything executes it.
package doubles

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/osexec"
	"ropeshift.dev/ropeshift/internal/adapters/tmux"
	"ropeshift.dev/ropeshift/internal/api/shimlink"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// Fixtures lists the scripted sessions by name, each with its text.
func Fixtures(t testing.TB) map[string][]byte {
	t.Helper()
	entries, err := fixtures.ReadDir("fixtures")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		raw, err := fixtures.ReadFile("fixtures/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[strings.TrimSuffix(entry.Name(), ".json")] = raw
	}
	return out
}

// The files fakeclaude reads and leaves beside itself.
const (
	FixtureFile = "fixture.json"
	SeenDir     = "seen"
)

// Seen is what one fakeclaude process saw, for a test to read.
type Seen struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
	// SystemPrompt is what the file `--append-system-prompt-file` names held at the start.
	SystemPrompt string `json:"system_prompt,omitempty"`
}

var built struct {
	once sync.Once
	dir  string
	path string
	err  error
}

var builtBinary struct {
	once sync.Once
	dir  string
	path string
	err  error
}

func moduleRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func buildFakeClaude() (string, error) {
	built.once.Do(func() {
		built.dir, built.err = os.MkdirTemp("", "rsfake")
		if built.err != nil {
			return
		}
		built.path = filepath.Join(built.dir, "fakeclaude")
		build := exec.Command("go", "build", "-o", built.path, "ropeshift.dev/ropeshift/test/doubles/fakeclaude")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			built.err = fmt.Errorf("build fakeclaude: %v\n%s", err, out)
		}
	})
	return built.path, built.err
}

// Ropeshift builds the real binary once per test process, because a session runs under
// the real shim whichever double stands for the agent.
func Ropeshift(t testing.TB) string {
	t.Helper()
	builtBinary.once.Do(func() {
		builtBinary.dir, builtBinary.err = os.MkdirTemp("", "rsbin")
		if builtBinary.err != nil {
			return
		}
		builtBinary.path = filepath.Join(builtBinary.dir, "ropeshift")
		build := exec.Command("go", "build", "-o", builtBinary.path, "ropeshift.dev/ropeshift/cmd/ropeshift")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			builtBinary.err = fmt.Errorf("build ropeshift: %v\n%s", err, out)
		}
	})
	if builtBinary.err != nil {
		t.Fatal(builtBinary.err)
	}
	return builtBinary.path
}

// Main runs a package's tests and removes the double it built.
func Main(m *testing.M) int {
	code := m.Run()
	for _, dir := range []string{built.dir, builtBinary.dir} {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
	}
	return code
}

// InstallFakeClaude puts a `claude` that plays one fixture in a new directory, and
// returns a PATH that finds it first and the canary second.
func InstallFakeClaude(t testing.TB, fixture string) string {
	t.Helper()
	path, err := buildFakeClaude()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fixtures.ReadFile("fixtures/" + fixture + ".json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Cleanup(func() { reapProbes(dir) })
	if err := os.Symlink(path, filepath.Join(dir, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FixtureFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + CanaryPath(t)
}

// reapProbes ends what a qualification probe of a killed daemon left of this double, before its
// directory goes: each session it recorded under its process ID, and only while that process still
// runs this double's binary, so a reused ID is left alone.
func reapProbes(dir string) {
	entries, err := os.ReadDir(filepath.Join(dir, SeenDir+"-probe"))
	if err != nil {
		return
	}
	var held []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil || pid <= 0 {
			continue
		}
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err != nil || !strings.Contains(string(out), dir) {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
		held = append(held, pid)
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		alive := slices.DeleteFunc(slices.Clone(held), func(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
		if len(alive) == 0 {
			return
		}
	}
}

// InstallFakeClaudeVersion is InstallFakeClaude with the version the double reports, so a test can
// stand for a version the matrix holds or one it does not.
func InstallFakeClaudeVersion(t testing.TB, fixture, version string) string {
	t.Helper()
	path := InstallFakeClaude(t, fixture)
	SetFakeClaudeVersion(t, path, version)
	return path
}

// SetFakeClaudeVersion changes the version an installed double reports from its next run, which is
// how a test stands for a CLI that updated itself under a running worker.
func SetFakeClaudeVersion(t testing.TB, path, version string) {
	t.Helper()
	dir, _, _ := strings.Cut(path, string(os.PathListSeparator))
	file := filepath.Join(dir, FixtureFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var script map[string]any
	if err := json.Unmarshal(raw, &script); err != nil {
		t.Fatal(err)
	}
	script["version"] = version
	raw, err = json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	// A rename, so a run that reads the fixture while it changes reads one whole version.
	next := file + ".next"
	if err := os.WriteFile(next, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, file); err != nil {
		t.Fatal(err)
	}
}

// ReapShims stops what a generation's shim started, when a test ends, by the exact IDs
// its state file names. A shim outlives its worker by design, so a test that fails with
// a session running would otherwise leave both behind.
func ReapShims(t testing.TB, configDir string) {
	t.Helper()
	t.Cleanup(func() {
		dirs, err := filepath.Glob(filepath.Join(configDir, "shim-*"))
		if err != nil {
			return
		}
		for _, dir := range dirs {
			state, err := shimlink.ReadState(dir)
			if err != nil {
				continue
			}
			for _, pid := range []int{state.ChildPID, state.ShimPID} {
				if pid > 0 {
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
		}
	})
}

// RequireTmux skips a test on a machine with no tmux, which the default suite tolerates
// because no other layer needs a terminal.
func RequireTmux(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath(tmux.Binary); err != nil {
		t.Skip("no tmux on this machine")
	}
}

// PrivateTmux returns a terminal on a server nothing else uses, and ends that server when
// the test ends. It uses the shortest directory a test can have, because a socket path
// has a 104-byte limit.
func PrivateTmux(t testing.TB) *tmux.Terminal {
	t.Helper()
	terminal, stop := NewTmux(t)
	t.Cleanup(stop)
	return terminal
}

// NewTmux returns a terminal and the function that ends its server.
func NewTmux(t testing.TB) (*tmux.Terminal, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "rsmux")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	terminal, err := tmux.New(osexec.Runner{}, tmux.Options{Socket: socket, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	return terminal, func() {
		binary, err := exec.LookPath(tmux.Binary)
		if err == nil {
			kill := exec.Command(binary, "-S", socket, "kill-server")
			kill.Env = env
			_ = kill.Run()
		}
		_ = os.RemoveAll(dir)
	}
}

// TmuxOn returns a terminal on a server something else started, and ends that server when
// the test ends, because a daemon's server outlives the daemon by design.
func TmuxOn(t testing.TB, socket string) *tmux.Terminal {
	t.Helper()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(socket)}
	terminal, err := tmux.New(osexec.Runner{}, tmux.Options{Socket: socket, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		binary, err := exec.LookPath(tmux.Binary)
		if err == nil {
			kill := exec.Command(binary, "-S", socket, "kill-server")
			kill.Env = env
			_ = kill.Run()
		}
	})
	return terminal
}

// EndTmuxOn ends the terminal server on a socket when a test ends, if one is running there, with no
// terminal of the test's own.
func EndTmuxOn(t testing.TB, socket string) {
	t.Helper()
	t.Cleanup(func() {
		binary, err := exec.LookPath(tmux.Binary)
		if err != nil {
			return
		}
		kill := exec.Command(binary, "-S", socket, "kill-server")
		kill.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Dir(socket)}
		_ = kill.Run()
	})
}

// KillTmux closes one pane when a test ends, which ends a server with no other pane.
func KillTmux(t testing.TB, terminal *tmux.Terminal, name string) {
	t.Helper()
	t.Cleanup(func() { _ = terminal.Close(context.Background(), name) })
}

// BinDir returns the directory InstallFakeClaude made, from the PATH it returned.
func BinDir(path string) string {
	dir, _, _ := strings.Cut(path, string(os.PathListSeparator))
	return dir
}

// CanaryPath returns a PATH that finds the canary first, and fails the test if the
// canary runs.
func CanaryPath(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(moduleRoot(), "test", "doubles", "canary")
	t.Cleanup(func() {
		if _, err := os.Stat(filepath.Join(dir, "tripped")); err == nil {
			t.Errorf("the claude canary ran during this test")
		}
	})
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// SeenBy reads what every fakeclaude process in a directory saw.
func SeenBy(t testing.TB, dir string) []Seen {
	t.Helper()
	return seenIn(t, filepath.Join(dir, SeenDir))
}

// ProbesSeenBy lists the qualification probe's sessions a double ran, which SeenBy leaves out.
func ProbesSeenBy(t testing.TB, dir string) []Seen {
	t.Helper()
	path := filepath.Join(dir, SeenDir+"-probe")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return seenIn(t, path)
}

func seenIn(t testing.TB, path string) []Seen {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	// A record is named for a process, and a name says nothing of order once IDs come round.
	written := func(entry os.DirEntry) time.Time {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		return info.ModTime()
	}
	slices.SortStableFunc(entries, func(a, b os.DirEntry) int { return written(a).Compare(written(b)) })
	var out []Seen
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var seen Seen
		if err := json.Unmarshal(raw, &seen); err != nil {
			t.Fatal(err)
		}
		out = append(out, seen)
	}
	return out
}

// SeenEnv returns the value of one variable a session saw, from the PATH that
// InstallFakeClaude returned.
func SeenEnv(t testing.TB, path, name string) string {
	t.Helper()
	for _, seen := range SeenBy(t, BinDir(path)) {
		if value, ok := seen.Env[name]; ok {
			return value
		}
	}
	return ""
}

// CanaryPathWithout is a PATH with no agent CLI on it at all, not even the canary, for a
// machine that has none.
func CanaryPathWithout(t testing.TB) string {
	t.Helper()
	return t.TempDir()
}
