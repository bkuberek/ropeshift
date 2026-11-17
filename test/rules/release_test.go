package rules

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestTheFormulaTellsTheDaemonThatBrewRunsIt renders the Homebrew formula from made-up checksums and reads
// its service block as text. The daemon takes its restart advice from this variable, so a formula that lost
// it would send a Homebrew user to the wrong command.
func TestTheFormulaTellsTheDaemonThatBrewRunsIt(t *testing.T) {
	root := repoRoot(t)
	var checksums strings.Builder
	for _, platform := range []string{"darwin_arm64", "darwin_amd64", "linux_arm64", "linux_amd64"} {
		checksums.WriteString(strings.Repeat("ab", 32) + "  ropeshift_0.1.0_" + platform + ".tar.gz\n")
	}
	dir := t.TempDir()
	sums, formula := filepath.Join(dir, "checksums.txt"), filepath.Join(dir, "ropeshift.rb")
	if err := os.WriteFile(sums, []byte(checksums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "scripts", "homebrew-formula.sh"), sums, "file:///dist", formula)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("render the formula: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(formula)
	if err != nil {
		t.Fatal(err)
	}
	_, service, found := strings.Cut(string(raw), "  service do\n")
	if !found {
		t.Fatalf("the formula has no service block:\n%s", raw)
	}
	service, _, _ = strings.Cut(service, "\n  end\n")
	const want = `environment_variables PATH: std_service_path_env, ROPESHIFT_SERVICE_MANAGER: "brew"`
	if !strings.Contains(service, want) {
		t.Errorf("the service block holds no %q:\n%s", want, service)
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

// TestScreenshotsBuildNamesTheNearestReleaseTag holds the screenshots script to stamping its binary with the
// release tag nearest the commit, which the status bar of every image shows, and never with the commit
// counts, hash, or dirty marker that `git describe` appends. Only this script changes: a release build is
// stamped by GoReleaser from the tag it releases.
func TestScreenshotsBuildNamesTheNearestReleaseTag(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "screenshots.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var versions int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "version=") {
			continue
		}
		versions++
		if !strings.Contains(line, "git describe --tags --abbrev=0") {
			t.Errorf("%q does not take the nearest release tag with git describe --tags --abbrev=0", line)
		}
		if strings.Contains(line, "--dirty") || strings.Contains(line, "--always") {
			t.Errorf("%q adds a commit count, hash, or dirty marker to the release tag", line)
		}
	}
	if versions == 0 {
		t.Error("the script assigns no version, so this check cannot fail")
	}
	if !strings.Contains(string(raw), "-X $stamp.version=$version") {
		t.Error("the screenshots build is not stamped with the version the script chose")
	}
}

// TestScreenshotMachineCompilesToNothing reads the bytes of two builds of the binary. A normal build holds
// neither the fixed machine nor the fixed worker ID that the images of the web UI show, and the screenshots
// build, which make screenshots captures, holds both, which proves the check can fail.
func TestScreenshotMachineCompilesToNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary twice")
	}
	build := func(tags ...string) []byte {
		out := filepath.Join(t.TempDir(), "ropeshift")
		args := append([]string{"build", "-trimpath", "-o", out}, tags...)
		args = append(args, "./cmd/ropeshift")
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
	// The free disk the fixed machine is written with, and the worker ID, in
	// internal/adapters/machine/figures_screenshots.go and internal/worker/identity_screenshots.go.
	markers := []string{"412 GiB", "3f9a2c7e5b1d4086a4e2c8f06d9b1e57"}
	release, screenshots := build(), build("-tags", "screenshots")
	for _, marker := range markers {
		if bytes.Contains(release, []byte(marker)) {
			t.Errorf("a normal build holds %q of the screenshots build", marker)
		}
		if !bytes.Contains(screenshots, []byte(marker)) {
			t.Errorf("the screenshots build holds no %q, so this check cannot fail", marker)
		}
	}
}
