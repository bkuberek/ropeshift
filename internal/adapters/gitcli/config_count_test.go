package gitcli_test

import (
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

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
			t.Fatalf("GIT_CONFIG_COUNT = %q in %q", env["GIT_CONFIG_COUNT"], record)
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

// keepsTheEnvironmentsSettings checks that no command lost the settings the environment holds, and that a command
// held them beside every setting the call site adds.
func keepsTheEnvironmentsSettings(t *testing.T, log string, site ...string) {
	t.Helper()
	commands := commandSettings(t, log)
	if len(commands) == 0 {
		t.Fatal("no command ran")
	}
	together := false
	lacking := map[string]bool{}
	for _, settings := range commands {
		for _, want := range []string{"test.first=one", "test.second=two"} {
			if !slices.Contains(settings, want) {
				lacking[strconv.Quote(want)+" is missing from "+strings.Join(settings, " ")] = true
			}
		}
		together = together || (slices.Contains(settings, "test.first=one") && slices.Contains(settings, "test.second=two") &&
			!slices.ContainsFunc(site, func(want string) bool { return !slices.Contains(settings, want) }))
	}
	for _, report := range slices.Sorted(maps.Keys(lacking)) {
		t.Errorf("the environment's setting %s", report)
	}
	if !together {
		t.Errorf("no command held the environment's settings beside %q: %q", site, commands)
	}
}

func TestCommitFilesKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	r := newGuardRig(t)
	var log string
	r.worktrees.Env, log = recordGit(t, r.repo.Env(), environmentSettings)
	if _, err := r.worktrees.CommitFiles(t.Context(), r.path, []ports.TreeFile{{Path: "Makefile", Text: "setup:\n"}}, workspaceSubject); err != nil {
		t.Fatal(err)
	}
	keepsTheEnvironmentsSettings(t, log, "core.hooksPath="+os.DevNull, "commit.gpgsign=false")
}

func TestCommitsKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	r := newGuardRig(t)
	head := r.inWorktree("rev-parse", "HEAD")
	var log string
	r.worktrees.Env, log = recordGit(t, r.repo.Env(), environmentSettings)
	if _, err := r.worktrees.Commits(t.Context(), r.path, head); err != nil {
		t.Fatal(err)
	}
	keepsTheEnvironmentsSettings(t, log, "core.quotePath=false")
}

func TestBranchCommitsAndCommitPathsKeepTheConfigurationTheEnvironmentHolds(t *testing.T) {
	r := newImportRig(t)
	var log string
	r.worktrees.Env, log = recordGit(t, r.workspace.Env(), environmentSettings)
	if _, err := r.worktrees.BranchCommits(t.Context(), r.workspace.Dir, "", r.base); err != nil {
		t.Fatal(err)
	}
	keepsTheEnvironmentsSettings(t, log, "core.quotePath=false")
	var paths string
	r.worktrees.Env, paths = recordGit(t, r.workspace.Env(), environmentSettings)
	if _, _, err := r.worktrees.CommitPaths(t.Context(), r.workspace.Dir, r.base, 10); err != nil {
		t.Fatal(err)
	}
	keepsTheEnvironmentsSettings(t, paths, "core.quotePath=false")
}

func TestImportHistoryKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	r := newImportRig(t)
	var log string
	r.worktrees.Env, log = recordGit(t, r.workspace.Env(), environmentSettings)
	if _, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request()); err != nil {
		t.Fatal(err)
	}
	keepsTheEnvironmentsSettings(t, log, "core.hooksPath="+os.DevNull, "commit.gpgsign=false")
}

func TestMergeBranchKeepsTheConfigurationTheEnvironmentHolds(t *testing.T) {
	for _, remote := range []bool{false, true} {
		r := newMergeRig(t, remote)
		var log string
		r.merger.Env, log = recordGit(t, r.repo.Env(), environmentSettings)
		r.merge("merge")
		keepsTheEnvironmentsSettings(t, log, "core.hooksPath="+os.DevNull)
	}
}
