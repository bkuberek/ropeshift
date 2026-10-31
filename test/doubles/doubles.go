// Package doubles holds what stands in for the agent CLI. No test outside the
// realsession build runs the real `claude`: a test that needs an agent gets
// fakeclaude first on PATH, and every other test gets the canary, which fails the
// test if anything executes it.
package doubles

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"

	"ropeshift.dev/ropeshift/internal/api/shimlink"
)

//go:embed fixtures/*.json
var fixtures embed.FS

// The files fakeclaude reads and leaves beside itself.
const (
	FixtureFile = "fixture.json"
	SeenDir     = "seen"
)

// Seen is what one fakeclaude process saw, for a test to read.
type Seen struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
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
	if err := os.Symlink(path, filepath.Join(dir, "claude")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FixtureFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + CanaryPath(t)
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
	entries, err := os.ReadDir(filepath.Join(dir, SeenDir))
	if err != nil {
		t.Fatal(err)
	}
	var out []Seen
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, SeenDir, entry.Name()))
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
