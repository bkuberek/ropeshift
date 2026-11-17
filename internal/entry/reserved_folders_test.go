package entry

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// The folders a template write may not be at or inside are the ones the daemon's own checkouts reserve, each as spelled and as it
// resolves when a link is in the way.
func TestTheReservedFoldersAreTheDaemonsOwnPathsAsSpelledAndAsResolved(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, run := filepath.Join(root, "state"), filepath.Join(root, "run")
	for _, dir := range []string{state, run} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(state, alias); err != nil {
		t.Fatal(err)
	}
	plain := api.Paths{StateDir: state, RunDir: run, WorktreeDir: filepath.Join(root, "worktrees")}
	if got, want := reservedFolders(plain), soloCheckouts(plain).Reserved; !slices.Equal(got, want) {
		t.Errorf("the reserved folders are %v, and the checkouts reserve %v", got, want)
	}
	linked := api.Paths{StateDir: alias, RunDir: run}
	got := reservedFolders(linked)
	for _, want := range []string{alias, state, run} {
		if !slices.Contains(got, want) {
			t.Errorf("the reserved folders %v lack %s", got, want)
		}
	}
}
