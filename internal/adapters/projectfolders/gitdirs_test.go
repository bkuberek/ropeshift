package projectfolders_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
)

// A walk marks a .git at any depth, a folder or a file, and does not look inside one.
func TestWalkMarksEveryGitUnderTheFolderAndLooksInsideNone(t *testing.T) {
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
	var got []string
	if err := f.Walk(t.Context(), root, func(path string, git bool) bool {
		if git {
			got = append(got, path)
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if want := []string{".git", "repos/x/.git", "vendor/lib/.git"}; !slices.Equal(got, want) {
		t.Errorf("a walk finds the repositories %v, want %v", got, want)
	}
}

// A walk stops the moment visit says so, and reads nothing further of the folder.
func TestWalkStopsWhenVisitSaysSo(t *testing.T) {
	f := projectfolders.Folders{}
	root := filepath.Join(t.TempDir(), "harbor")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	visited := 0
	if err := f.Walk(t.Context(), root, func(string, bool) bool {
		visited++
		return visited < 2
	}); err != nil {
		t.Fatal(err)
	}
	if visited != 2 {
		t.Errorf("a walk that was told to stop at the second entry visited %d", visited)
	}
}
