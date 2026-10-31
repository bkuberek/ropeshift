package rules

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCrashStepsCompileToNothing reads the bytes of two builds. A normal build
// holds no crash step, and the chaos build does, which proves the check can fail.
func TestCrashStepsCompileToNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries")
	}
	build := func(tags ...string) []byte {
		out := filepath.Join(t.TempDir(), "probe")
		args := append([]string{"build", "-o", out}, tags...)
		args = append(args, "./test/rules/testdata/stepprobe")
		cmd := exec.Command("go", args...)
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %v: %v\n%s", tags, err, output)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	marker := []byte("before-commit")
	if bytes.Contains(build(), marker) {
		t.Error("a normal build holds a crash step")
	}
	if !bytes.Contains(build("-tags", "chaos"), marker) {
		t.Error("the chaos build holds no crash step, so this check cannot fail")
	}
}

// TestSessionOverrideCompilesToNothing reads the bytes of two builds. A normal build
// holds no way to change what a session starts with, and the realsession build does,
// which proves the check can fail.
func TestSessionOverrideCompilesToNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two binaries")
	}
	build := func(tags ...string) []byte {
		out := filepath.Join(t.TempDir(), "probe")
		args := append([]string{"build", "-o", out}, tags...)
		args = append(args, "./test/rules/testdata/overrideprobe")
		cmd := exec.Command("go", args...)
		cmd.Dir = repoRoot(t)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build %v: %v\n%s", tags, err, output)
		}
		raw, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	marker := []byte("refused argument")
	if bytes.Contains(build(), marker) {
		t.Error("a normal build holds the session override")
	}
	if !bytes.Contains(build("-tags", "realsession"), marker) {
		t.Error("the realsession build holds no override, so this check cannot fail")
	}
}
