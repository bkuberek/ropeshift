package gitcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"ropeshift.com/ropeshift/internal/ports"
)

// Tracks says the worktree's index holds a file at name, or under it when it names a folder. The name is read
// literally, so a pattern git would expand matches only a file of that name.
func (w Worktrees) Tracks(ctx context.Context, worktree, name string) (bool, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("worktree %s: %w", worktree, ports.ErrNotFound)
		}
		return false, err
	}
	if name == "" {
		return false, errors.New("a tracked path names something")
	}
	out, err := w.git(worktree).Output(ctx, "--literal-pathspecs", "ls-files", "-z", "--cached", "--", name)
	return len(out) != 0, err
}
