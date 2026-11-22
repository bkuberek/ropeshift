package gitcli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// recordedCommand is one git command a recording git ran: its arguments and the configuration it started with.
type recordedCommand struct {
	args     string
	settings []string
}

// recordCommands puts a git first on PATH that appends each command's arguments and the configuration in its
// environment to a log, and then runs the real git. It returns env for the code under test and the log's path.
func recordCommands(t *testing.T, env []string) ([]string, string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "commands")
	script := "#!/bin/sh\n{ echo '=== command'; echo \"ARGS $*\"; env; } >> '" + log + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil { //nolint:gosec // a test shim must be executable
		t.Fatal(err)
	}
	path := bin + string(os.PathListSeparator) + os.Getenv("PATH")
	t.Setenv("PATH", path)
	shimmed := slices.DeleteFunc(slices.Clone(env), func(entry string) bool { return strings.HasPrefix(entry, "PATH=") })
	return append(shimmed, "PATH="+path), log
}

// commandsRecorded reads the commands a recording git ran, in order.
func commandsRecorded(t *testing.T, log string) []recordedCommand {
	t.Helper()
	text, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no command ran: %v", err)
	}
	var commands []recordedCommand
	for _, record := range strings.Split(string(text), "=== command\n") {
		env := map[string]string{}
		var args string
		for _, line := range strings.Split(record, "\n") {
			if rest, ok := strings.CutPrefix(line, "ARGS "); ok && args == "" {
				args = rest
				continue
			}
			if key, value, ok := strings.Cut(line, "="); ok {
				env[key] = value
			}
		}
		if args == "" {
			continue
		}
		count, _ := strconv.Atoi(env["GIT_CONFIG_COUNT"])
		var settings []string
		for at := range count {
			n := strconv.Itoa(at)
			settings = append(settings, env["GIT_CONFIG_KEY_"+n]+"="+env["GIT_CONFIG_VALUE_"+n])
		}
		commands = append(commands, recordedCommand{args: args, settings: settings})
	}
	return commands
}

// configuredHookIsOff checks that every command a recording git ran, but the reads that find the hooks the
// configuration names, ran with the hook the test configured disabled, and that commands of each named kind ran.
func configuredHookIsOff(t *testing.T, log string, ran ...string) {
	t.Helper()
	seen := map[string]bool{}
	for _, command := range commandsRecorded(t, log) {
		if strings.HasPrefix(command.args, "config --name-only --get-regexp") {
			continue
		}
		if !slices.Contains(command.settings, "hook.format.enabled=false") {
			t.Errorf("git %s ran with the settings %q, which leave the configured hook on", command.args, command.settings)
		}
		first, _, _ := strings.Cut(command.args, " ")
		seen[first] = true
	}
	for _, verb := range ran {
		if !seen[verb] {
			t.Errorf("no git %s ran, among %v", verb, seen)
		}
	}
}

func TestAMergeDisablesEveryConfiguredHook(t *testing.T) {
	for name, tt := range map[string]struct {
		method ledger.MergeMethod
		ran    []string
	}{
		"a merge commit": {ledger.MergeCommit, []string{"merge", "worktree", "push"}},
		"a squash":       {ledger.MergeSquash, []string{"merge", "commit", "worktree", "push"}},
		"a rebase":       {ledger.MergeRebase, []string{"cherry-pick", "commit", "worktree", "push"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := newMergeRig(t, true)
			r.repo.Git("config", "hook.format.command", "true")
			var log string
			r.merger.Env, log = recordCommands(t, r.repo.Env())
			if result := r.merge(tt.method); result.Outcome != ledger.MergeMerged {
				t.Fatalf("the merge ended %+v", result)
			}
			configuredHookIsOff(t, log, tt.ran...)
		})
	}
}

func TestCommitFilesDisablesEveryConfiguredHook(t *testing.T) {
	r := newGuardRig(t)
	r.repo.Git("config", "hook.format.command", "true")
	var log string
	r.worktrees.Env, log = recordCommands(t, r.repo.Env())
	if _, err := r.worktrees.CommitFiles(t.Context(), r.path, []ports.TreeFile{{Path: "Makefile", Text: "setup:\n"}}, workspaceSubject); err != nil {
		t.Fatal(err)
	}
	configuredHookIsOff(t, log, "add", "commit")
}

func TestImportHistoryDisablesEveryConfiguredHook(t *testing.T) {
	r := newImportRig(t)
	r.workspace.Git("config", "hook.format.command", "true")
	var log string
	r.worktrees.Env, log = recordCommands(t, r.workspace.Env())
	if _, err := r.worktrees.ImportHistory(t.Context(), r.path, r.request()); err != nil {
		t.Fatal(err)
	}
	configuredHookIsOff(t, log, "fetch", "merge", "commit")
}
