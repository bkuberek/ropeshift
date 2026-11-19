package practicegit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/test/gittest"
)

// environmentSettings is git configuration the environment already holds, as a person's shell or a profile can pass it.
var environmentSettings = []string{
	"GIT_CONFIG_COUNT=2",
	"GIT_CONFIG_KEY_0=test.first", "GIT_CONFIG_VALUE_0=one",
	"GIT_CONFIG_KEY_1=test.second", "GIT_CONFIG_VALUE_1=two",
}

// recordGit returns env with the environmentSettings added, and puts a git first on PATH, for this test and the
// commands it starts, that appends the environment of every command it runs to a log, and then runs the real git. It
// also returns the path of that log.
func recordGit(t *testing.T, env []string) ([]string, string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "commands")
	script := "#!/bin/sh\n{ echo '=== command'; env; } >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { //nolint:gosec // a test shim must be executable
		t.Fatal(err)
	}
	// The process finds git through its own PATH and not through the command's environment, so the shim goes in both.
	path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	shimmed := slices.DeleteFunc(slices.Clone(env), func(entry string) bool { return strings.HasPrefix(entry, "PATH=") })
	return slices.Concat(shimmed, []string{"PATH=" + path}, environmentSettings), log
}

// commandSettings reads the git configuration each recorded command was started with, as "key=value" entries.
func commandSettings(t *testing.T, log string) [][]string {
	t.Helper()
	text, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no command ran: %v", err)
	}
	var commands [][]string
	for _, record := range strings.Split(string(text), "=== command\n") {
		env := map[string]string{}
		for _, line := range strings.Split(record, "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				env[key] = value
			}
		}
		if len(env) == 0 {
			continue
		}
		count, err := strconv.Atoi(env["GIT_CONFIG_COUNT"])
		if err != nil {
			t.Fatalf("GIT_CONFIG_COUNT = %q", env["GIT_CONFIG_COUNT"])
		}
		var settings []string
		for at := range count {
			n := strconv.Itoa(at)
			settings = append(settings, env["GIT_CONFIG_KEY_"+n]+"="+env["GIT_CONFIG_VALUE_"+n])
		}
		commands = append(commands, settings)
	}
	return commands
}

// A scan adds its settings after the ones the environment already carries, so the count grows and none of the
// environment's own is replaced.
func TestSecretScanKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	repo := gittest.New(t)
	tip := repo.Commit("a.txt", "a\n")
	env, log := recordGit(t, repo.Env())
	if _, err := (practicegit.SecretScanner{Env: env}).Scan(t.Context(), repo.Dir, tip, ""); err != nil {
		t.Fatal(err)
	}
	commands := commandSettings(t, log)
	if len(commands) == 0 {
		t.Fatal("no command ran")
	}
	for _, settings := range commands {
		for _, want := range []string{"test.first=one", "test.second=two", "core.quotePath=false", "core.fsmonitor=false"} {
			if !slices.Contains(settings, want) {
				t.Errorf("a command ran with the settings %q, which lack %q", settings, want)
			}
		}
	}
}
