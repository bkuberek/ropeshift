package projectfolders_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
)

// A hook the user's own configuration names by `hook.<name>.command` runs under any hooks path since git 2.54, so the
// first commit disables it by name, from the init on, though the folder was no repository when the names were read.
func TestFirstCommitDisablesEveryConfiguredHook(t *testing.T) {
	f, root, _ := folders(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte(identity+"[hook \"format\"]\n\tcommand = true\n\tevent = pre-commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(root, "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "a.md", Text: "a"}})
	env, log := recordGit(t, f.Env, []string{"GIT_CONFIG_GLOBAL=" + global})
	if _, err := (projectfolders.Folders{Env: env}).FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"a.md"}}); err != nil {
		t.Fatal(err)
	}
	commands := commandSettings(t, log)
	if len(commands) == 0 {
		t.Fatal("no command ran")
	}
	for _, settings := range commands {
		if !slices.Contains(settings, "hook.format.enabled=false") {
			t.Errorf("a command ran with the settings %q, which leave the configured hook on", settings)
		}
	}
}
