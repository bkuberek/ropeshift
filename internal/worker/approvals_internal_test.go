package worker

import (
	"os"
	"path/filepath"
	"testing"
)

// A delegated path is read where it lands. A link a lane made inside a covered directory must not
// carry a decision outside it.
func TestAPathIsReadWhereItLands(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(root, "worktree")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{filepath.Join(worktree, "docs"), outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "docs", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "nothing-yet"), filepath.Join(worktree, "docs", "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(worktree, "docs", "loop"), filepath.Join(worktree, "docs", "loop")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.md", filepath.Join(worktree, "docs", "inside")); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(worktree, "docs", "shared.md"), filepath.Join(worktree, "single.md")} {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(filepath.Join(worktree, "docs", "shared.md"), filepath.Join(outside, "other-name")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, target, want string
	}{
		{"a file that does not exist yet", filepath.Join(worktree, "docs", "a.md"), "docs/a.md"},
		{"a relative path", "docs/a.md", "docs/a.md"},
		{"a file with one name", "single.md", "single.md"},
		{"a link that stays inside", filepath.Join(worktree, "docs", "inside"), "docs/a.md"},
		{"a way out that comes back", filepath.Join(worktree, "docs", "..", "docs", "a.md"), "docs/a.md"},
		{"through a link", filepath.Join(worktree, "docs", "link", "keys"), filepath.Join(outside, "keys")},
		{"a link to nothing yet", filepath.Join(worktree, "docs", "dangling"), filepath.Join(outside, "nothing-yet")},
		{"a link to itself", filepath.Join(worktree, "docs", "loop"), filepath.Join(worktree, "docs", "loop")},
		{"a file with a second name elsewhere", filepath.Join(worktree, "docs", "shared.md"), filepath.Join(worktree, "docs", "shared.md")},
		{"a climb through a link", worktree + "/docs/link/../keys", filepath.Join(root, "keys")},
		{"a climb that reads the same on paper", worktree + "/docs/../docs/a.md", filepath.Join(worktree, "docs", "a.md")},
		{"a relative climb", "docs/../../keys", filepath.Join(root, "keys")},
		{"outside the worktree", filepath.Join(outside, "keys"), filepath.Join(outside, "keys")},
		{"no path", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := insideWorktree(worktree, tt.target); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
