package projectfolders_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
)

// GitDirs finds a .git at any depth, a folder or a file, and does not look inside one.
func TestGitDirsListsEveryGitUnderTheFolderAndNoneBeyondIt(t *testing.T) {
	f := projectfolders.Folders{}
	root := filepath.Join(t.TempDir(), "harbor")
	for _, dir := range []string{".git/objects", "repos/x/.git/refs", "vendor/lib", "plan"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"vendor/lib/.git", "plan/goal.md", ".git/objects/.git"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("gitdir: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := f.GitDirs(t.Context(), root)
	if want := []string{".git", "repos/x/.git", "vendor/lib/.git"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("GitDirs reads %v (%v), want %v", got, err, want)
	}
	if got, err := f.GitDirs(t.Context(), filepath.Join(root, "nothing")); err != nil || len(got) != 0 {
		t.Errorf("a folder that is not there holds %v (%v)", got, err)
	}
}
