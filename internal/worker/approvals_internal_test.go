package worker

import (
	"os"
	"path/filepath"
	"testing"
)

// A delegated path is read where it lands. A link a lane made inside a covered directory must not
// carry a decision outside it.
func TestAPathIsReadWhereItLands(t *testing.T) {
	root := t.TempDir()
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
	tests := []struct {
		name, target, want string
	}{
		{"a file that does not exist yet", filepath.Join(worktree, "docs", "a.md"), "docs/a.md"},
		{"a relative path", "docs/a.md", "docs/a.md"},
		{"a way out that comes back", filepath.Join(worktree, "docs", "..", "docs", "a.md"), "docs/a.md"},
		{"through a link", filepath.Join(worktree, "docs", "link", "keys"), filepath.Join(worktree, "docs", "link", "keys")},
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
