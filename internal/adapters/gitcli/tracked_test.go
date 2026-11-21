package gitcli_test

import (
	"errors"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
)

// TestTracksReadsAPathLiterallyAndAFolderWhole says what the index holds at a path or under a folder, and reads a
// name git would expand as only itself.
func TestTracksReadsAPathLiterallyAndAFolderWhole(t *testing.T) {
	r := newGuardRig(t)
	r.commit("src/main.go", "package main\n")
	r.write("cache/index", "c\n")
	for name, want := range map[string]bool{
		"src/": true, "src": true, "src/main.go": true, "README.txt": true, "cache/": false, "cache/index": false, "*": false, "s*": false,
	} {
		got, err := r.worktrees.Tracks(t.Context(), r.path, name)
		if err != nil || got != want {
			t.Errorf("%q tracked %v (%v), want %v", name, got, err, want)
		}
	}
	if _, err := r.worktrees.Tracks(t.Context(), filepath.Join(r.path, "gone"), "src/"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a worktree that is gone: %v", err)
	}
}
