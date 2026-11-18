package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/cli"
	domain "ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// TestNewAdoptAndInitRefuseAGitBelowTheMinimumBeforeTheirFirstGitRead holds each command that reads git in this
// process to the doctor's own words, with exit 1, and to its order: the refusal comes before the command reads
// git, writes a file, or starts a daemon. A build with no check refuses nothing.
func TestNewAdoptAndInitRefuseAGitBelowTheMinimumBeforeTheirFirstGitRead(t *testing.T) {
	old := domain.CheckGit(domain.GitVersion{Major: 2, Minor: 25, Patch: 1}, nil)
	folder := filepath.Join(t.TempDir(), "acme")
	notRead := func(what string) {
		t.Helper()
		t.Errorf("%s ran before the git check refused", what)
	}
	options := cli.Options{
		Git:    func(context.Context) domain.GitReport { return old },
		Doctor: func(context.Context) (ledger.Manifest, error) { notRead("the doctor"); return ledger.Manifest{}, nil },
		StartDaemon: func(context.Context) (func() error, error) {
			notRead("the daemon start")
			return nil, nil
		},
		StateDir:       t.TempDir(),
		RepositoryRoot: func(context.Context) (string, error) { notRead("the repository root"); return "", nil },
		RepositoryHere: func(context.Context) (string, error) { notRead("the repository identity"); return "", nil },
		CloneTop:       func(context.Context, string) (string, error) { notRead("the clone top"); return "", nil },
		CloneIgnores:   func(context.Context, string, string) (bool, error) { notRead("the ignore check"); return false, nil },
	}
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"new monorepo", []string{"new", "monorepo", folder}},
		{"new workspace", []string{"new", "workspace", folder}},
		{"new repository", []string{"new", "repository", folder}},
		{"adopt", []string{"adopt", "--dry-run"}},
		{"init", []string{"init"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(tt.args, &stdout, &stderr, options)
			if want := "ropeshift " + tt.name + ": " + old.Problem + "\n"; code != cli.ExitError || stderr.String() != want {
				t.Errorf("exit %d, stderr %q, want %d and %q", code, stderr.String(), cli.ExitError, want)
			}
			if _, err := os.Stat(folder); !os.IsNotExist(err) {
				t.Errorf("the folder was written despite the refusal: %v", err)
			}
		})
	}

	t.Run("a command line that is wrong is still a usage error", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		if code := cli.Run([]string{"new", "monorepo"}, &stdout, &stderr, options); code != cli.ExitUsage {
			t.Errorf("exit %d, stderr %q, want %d", code, stderr.String(), cli.ExitUsage)
		}
	})

	t.Run("a usable git is let through", func(t *testing.T) {
		usable := options
		usable.Git = func(context.Context) domain.GitReport { return domain.CheckGit(domain.MinimumGit, nil) }
		usable.CloneTop = func(context.Context, string) (string, error) { return "", nil }
		var stdout, stderr bytes.Buffer
		if code := cli.Run([]string{"new", "monorepo", folder}, &stdout, &stderr, usable); code != cli.ExitOK || !strings.Contains(stdout.String(), "wrote\t") {
			t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
		}
	})
}
