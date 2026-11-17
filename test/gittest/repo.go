// Package gittest makes throwaway git repositories. Every command runs with a
// configuration the test owns, so a test never reads the user's git settings and
// never touches a real repository.
package gittest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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

// EnvWithoutRefLockWait is Env with git's own wait for a ref's lock turned off. By default git retries a
// lock for 100 ms before it gives up, which would hide a lock held for about as long, so a test that holds
// one for a known time turns the wait off and leaves the retry to the code under test. The setting goes in the
// configuration file Env names, not in the environment, because a command that sets GIT_CONFIG_COUNT itself,
// as a merge does to run no hooks, replaces what the environment holds.
func (r *Repo) EnvWithoutRefLockWait() []string {
	r.T.Helper()
	file, err := os.OpenFile(filepath.Join(r.Home, "gitconfig"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		r.T.Fatal(err)
	}
	if _, err := file.WriteString("[core]\n\tfilesRefLockTimeout = 0\n"); err != nil {
		r.T.Fatal(err)
	}
	if err := file.Close(); err != nil {
		r.T.Fatal(err)
	}
	return r.Env()
}

// HoldRefLock creates the lock file git takes to update a ref, as a fetch in another process holds it, and
// removes it after hold. The lock is in place when this returns. The test's cleanup removes it early and
// waits for the removal, so no goroutine outlives the test.
func (r *Repo) HoldRefLock(ref string, hold time.Duration) {
	r.T.Helper()
	path := r.Git("rev-parse", "--git-path", ref+".lock")
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.Dir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		r.T.Fatal(err)
	}
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		r.T.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		r.T.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-time.After(hold):
		case <-stop:
		}
		_ = os.Remove(path)
	}()
	r.T.Cleanup(func() {
		close(stop)
		<-done
	})
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

// Advance adds an empty commit on top of a branch without checking the branch out, and returns it.
// The branch moves and no working tree does, which is what a remote's branch does under a clone.
func (r *Repo) Advance(branch string) string {
	r.T.Helper()
	tip := r.Git("rev-parse", "refs/heads/"+branch)
	tree := r.Git("rev-parse", tip+"^{tree}")
	commit := r.Git("commit-tree", tree, "-p", tip, "-m", "advance "+branch)
	r.Git("update-ref", "refs/heads/"+branch, commit, tip)
	return commit
}

// Branch points a new branch at a commit.
func (r *Repo) Branch(name, at string) {
	r.T.Helper()
	r.Git("branch", name, at)
}

// Merge merges a branch into the checked-out one with a merge commit and returns the result.
func (r *Repo) Merge(branch string) string {
	r.T.Helper()
	r.Git("merge", "--quiet", "--no-ff", "-m", "merge "+branch, branch)
	return r.Git("rev-parse", "HEAD")
}

// Roots lists every commit with no parent that a ref reaches, sorted as the commit IDs are.
func (r *Repo) Roots(ref string) []string {
	r.T.Helper()
	roots := strings.Fields(r.Git("rev-list", "--max-parents=0", ref))
	slices.Sort(roots)
	return roots
}

// MergeUnrelated merges the history of another repository's main branch into the checked-out branch, as a
// person does who brings a repository into a workspace, and returns the merge commit. The two histories
// share no commit, so the branch gains a second root.
func (r *Repo) MergeUnrelated(other *Repo) string {
	r.T.Helper()
	r.Git("fetch", "--quiet", other.Dir, "main")
	r.Git("merge", "--quiet", "--no-ff", "--allow-unrelated-histories", "-m", "merge an unrelated history", "FETCH_HEAD")
	return r.Git("rev-parse", "HEAD")
}

// NewUnrelatedPair creates two repositories that share no commit and holds each one commit. The first one's
// first commit sorts after the second's, so merging the second into the first gives a history whose lowest
// root is not the first-parent root.
func NewUnrelatedPair(t testing.TB) (higher, lower *Repo) {
	t.Helper()
	higher, lower = New(t), New(t)
	// The files differ, so the two first commits differ however close in time they are made.
	higher.Commit("README.md", "a workspace\n")
	lower.Commit("DESIGN.md", "a design\n")
	if higher.Roots("HEAD")[0] < lower.Roots("HEAD")[0] {
		higher, lower = lower, higher
	}
	return higher, lower
}

// NewWithRootBelow creates a repository with one commit whose first commit sorts below every root given, so
// merging it into a history that holds those roots gives that history a new lowest root.
func NewWithRootBelow(t testing.TB, roots ...string) *Repo {
	t.Helper()
	for attempt := range 200 {
		repo := New(t)
		repo.Commit("LOWER.md", fmt.Sprintf("a history that sorts lower, attempt %d\n", attempt))
		if root := repo.Roots("HEAD")[0]; root < slices.Min(roots) {
			return repo
		}
	}
	t.Fatalf("no first commit sorted below %v in 200 attempts", roots)
	return nil
}

// Clone makes a repository beside this one that fetches from it, with this one as its origin.
func (r *Repo) Clone(name string) *Repo {
	r.T.Helper()
	dir := filepath.Join(filepath.Dir(r.Dir), name)
	r.Git("clone", "--quiet", r.Dir, dir)
	return &Repo{T: r.T, Dir: dir, Home: r.Home}
}

// RemoteHead reads the commit a remote holds for a ref, or nothing when it holds none.
func (r *Repo) RemoteHead(remote, ref string) string {
	r.T.Helper()
	fields := strings.Fields(r.Git("ls-remote", remote, ref))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
