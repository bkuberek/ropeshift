package projectfolders_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
)

// effectiveKeys are the settings the recording git reads, so a log shows which setting won.
var effectiveKeys = []string{"core.hooksPath", "core.fsmonitor", "core.quotePath", "commit.gpgsign", "test.third"}

// environmentSettings is git configuration the environment already holds, as a person's shell or a profile can pass it.
var environmentSettings = []string{
	"GIT_CONFIG_COUNT=2",
	"GIT_CONFIG_KEY_0=test.first", "GIT_CONFIG_VALUE_0=one",
	"GIT_CONFIG_KEY_1=test.second", "GIT_CONFIG_VALUE_1=two",
}

// recordGit returns env with settings added, and puts a git first on PATH, for this test and the commands it starts,
// that appends the environment of every command it runs, and the values of the settings git reads under it, to a log,
// and then runs the real git. It also returns the path of that log.
func recordGit(t *testing.T, env, settings []string) ([]string, string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "commands")
	script := "#!/bin/sh\n{ echo '=== command'; env; for key in " + strings.Join(effectiveKeys, " ") +
		"; do echo \"effective $key=$('" + real + "' config --get $key)\"; done; } >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { //nolint:gosec // a test shim must be executable
		t.Fatal(err)
	}
	// The process finds git through its own PATH and not through the command's environment, so the shim goes in both.
	path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	shimmed := slices.DeleteFunc(slices.Clone(env), func(entry string) bool { return strings.HasPrefix(entry, "PATH=") })
	return slices.Concat(shimmed, []string{"PATH=" + path}, settings), log
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

// The first commit adds its settings, which keep a hook and a signature out of it, after the ones the environment
// already carries, so the count grows and none of the environment's own is replaced.
func TestFirstCommitKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	f, root, _ := folders(t)
	folder := filepath.Join(root, "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "a.md", Text: "a"}})
	env, log := recordGit(t, f.Env, environmentSettings)
	if _, err := (projectfolders.Folders{Env: env}).FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"a.md"}}); err != nil {
		t.Fatal(err)
	}
	commands := commandSettings(t, log)
	if len(commands) == 0 {
		t.Fatal("no command ran")
	}
	for _, settings := range commands {
		for _, want := range []string{"test.first=one", "test.second=two", "core.hooksPath=" + os.DevNull, "commit.gpgsign=false"} {
			if !slices.Contains(settings, want) {
				t.Errorf("a command ran with the settings %q, which lack %q", settings, want)
			}
		}
	}
}
