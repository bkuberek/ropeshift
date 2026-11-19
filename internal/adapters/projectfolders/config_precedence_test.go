package projectfolders_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
)

// hooksThatRecord makes a folder of hooks of every kind git runs around a commit, a merge, and a push, each of which
// writes to a marker, and returns the folder and the marker.
func hooksThatRecord(t *testing.T) (string, string) {
	t.Helper()
	dir, marker := t.TempDir(), filepath.Join(t.TempDir(), "hook-ran")
	for _, name := range []string{"pre-commit", "prepare-commit-msg", "commit-msg", "post-commit", "pre-merge-commit", "post-merge", "pre-push", "reference-transaction"} {
		script := "#!/bin/sh\necho " + name + " >> '" + marker + "'\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { //nolint:gosec // a test hook must be executable
			t.Fatal(err)
		}
	}
	return dir, marker
}

// precedenceSettings is git configuration the environment holds that a call site's own settings must beat: a hooks
// path with real hooks and a file system monitor, through git's count variables and through GIT_CONFIG_PARAMETERS,
// which `git -c` leaves in the environment of a child and which git reads after the count variables. It also holds
// one setting of each kind that the call site never sets.
func precedenceSettings(hooks string) []string {
	return []string{
		"GIT_CONFIG_COUNT=4",
		"GIT_CONFIG_KEY_0=test.first", "GIT_CONFIG_VALUE_0=one",
		"GIT_CONFIG_KEY_1=test.second", "GIT_CONFIG_VALUE_1=two",
		"GIT_CONFIG_KEY_2=core.hooksPath", "GIT_CONFIG_VALUE_2=" + hooks,
		"GIT_CONFIG_KEY_3=core.fsmonitor", "GIT_CONFIG_VALUE_3=/bin/true",
		"GIT_CONFIG_PARAMETERS='core.hooksPath'='" + hooks + "' 'core.fsmonitor'='/bin/true' 'test.third'='three'",
	}
}

// effectiveSettings reads, for each recorded command, the values git read for the settings of effectiveKeys.
func effectiveSettings(t *testing.T, log string) []map[string]string {
	t.Helper()
	text, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("no command ran: %v", err)
	}
	var commands []map[string]string
	for _, record := range strings.Split(string(text), "=== command\n") {
		if readsDefaultBranch(record) {
			continue
		}
		read := map[string]string{}
		for _, line := range strings.Split(record, "\n") {
			if key, value, ok := strings.Cut(strings.TrimPrefix(line, "effective "), "="); ok && strings.HasPrefix(line, "effective ") {
				read[key] = value
			}
		}
		if len(read) > 0 {
			commands = append(commands, read)
		}
	}
	if len(commands) == 0 {
		t.Fatal("no command ran")
	}
	return commands
}

// everyCommandReads checks that every command read these values, and that the environment's setting that the call
// site never sets reached each one.
func everyCommandReads(t *testing.T, log string, want map[string]string) {
	t.Helper()
	want["test.third"] = "three"
	for _, read := range effectiveSettings(t, log) {
		for key, value := range want {
			if read[key] != value {
				t.Errorf("a command read %s = %q, want %q", key, read[key], value)
			}
		}
	}
}

// noHookRan checks that no hook of the folder ran.
func noHookRan(t *testing.T, marker string) {
	t.Helper()
	if ran, err := os.ReadFile(marker); err == nil {
		t.Errorf("hooks the environment names ran: %s", strings.Fields(string(ran)))
	}
}

// The first commit runs with no hook and no signature, though the environment names a hooks path through the count
// variables and through GIT_CONFIG_PARAMETERS.
func TestFirstCommitRunsNoHookWhateverTheEnvironmentNames(t *testing.T) {
	f, root, _ := folders(t)
	folder := filepath.Join(root, "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "a.md", Text: "a"}})
	hooks, marker := hooksThatRecord(t)
	env, log := recordGit(t, f.Env, precedenceSettings(hooks))
	if _, err := (projectfolders.Folders{Env: env}).FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"a.md"}}); err != nil {
		t.Fatal(err)
	}
	noHookRan(t, marker)
	everyCommandReads(t, log, map[string]string{"core.hooksPath": os.DevNull, "commit.gpgsign": "false"})
}
