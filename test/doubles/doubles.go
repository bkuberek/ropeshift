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
	"testing"
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

// Main runs a package's tests and removes the double it built.
func Main(m *testing.M) int {
	code := m.Run()
	if built.dir != "" {
		_ = os.RemoveAll(built.dir)
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
