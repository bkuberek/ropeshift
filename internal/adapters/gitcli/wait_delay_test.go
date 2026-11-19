package gitcli_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/test/gittest"
)

// A command whose helper keeps its output open after git is stopped returns when its context ends, not when the helper does:
// ssh under an unreachable origin can hold the pipe long past the deadline of the step that ran it.
func TestACommandReturnsAtItsDeadlineWhileAHelperHoldsItsOutputOpen(t *testing.T) {
	repo := gittest.New(t)
	helper := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec sleep 8\n"), 0o700); err != nil { //nolint:gosec // a test helper must be executable
		t.Fatal(err)
	}
	git := gitcli.Git{Dir: repo.Dir, Env: append(repo.Env(), "GIT_SSH_COMMAND="+helper)}
	for name, run := range map[string]func(ctx context.Context) error{
		"Output": func(ctx context.Context) error {
			_, err := git.Output(ctx, "ls-remote", "--", "ssh://example.invalid/a.git")
			return err
		},
		"Feed": func(ctx context.Context) error {
			_, err := git.Feed(ctx, nil, "ls-remote", "--", "ssh://example.invalid/a.git")
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
		start := time.Now()
		err := run(ctx)
		cancel()
		if err == nil {
			t.Errorf("%s: the command succeeded", name)
		}
		if took := time.Since(start); took > 4*time.Second {
			t.Errorf("%s returned after %v, want about its 300ms deadline", name, took)
		}
	}
}
