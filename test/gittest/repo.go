// Package gittest makes throwaway git repositories. Every command runs with a
// configuration the test owns, so a test never reads the user's git settings and
// never touches a real repository.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Repo is one repository in a temporary directory.
type Repo struct {
	T    testing.TB
	Dir  string
	Home string
}

// Require skips a test on a machine with no git.
func Require(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
}

// New creates a repository with one branch, main, and no commits.
func New(t testing.TB) *Repo {
	t.Helper()
	return NewIn(t, t.TempDir())
}

// NewIn creates the same repository in a directory the caller owns.
func NewIn(t testing.TB, root string) *Repo {
	t.Helper()
	Require(t)
	home := filepath.Join(root, "home")
	dir := filepath.Join(root, "repository")
	for _, path := range []string{home, dir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := "[user]\n\tname = Test\n\temail = test@example.invalid\n[init]\n\tdefaultBranch = main\n[protocol \"file\"]\n\tallow = always\n"
	if err := os.WriteFile(filepath.Join(home, "gitconfig"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &Repo{T: t, Dir: dir, Home: home}
	r.Git("init", "--quiet")
	return r
}

// Env is the whole environment of a git command in this repository.
func (r *Repo) Env() []string {
	return []string{
		"HOME=" + r.Home,
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(r.Home, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	}
}

// Git runs one command and returns its output.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = r.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Commit writes one file and commits it, and returns the commit.
func (r *Repo) Commit(name, content string) string {
	r.T.Helper()
	path := filepath.Join(r.Dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.T.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		r.T.Fatal(err)
	}
	r.Git("add", "--", name)
	r.Git("commit", "--quiet", "-m", "change "+name)
	return r.Git("rev-parse", "HEAD")
}

// Rename moves one file and commits the move.
func (r *Repo) Rename(from, to string) {
	r.T.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(r.Dir, to)), 0o700); err != nil {
		r.T.Fatal(err)
	}
	r.Git("mv", "--", from, to)
	r.Git("commit", "--quiet", "-m", "move "+from)
}
