package gitcli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/gitcli"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/test/gittest"
)

// guardRig is one repository with a lane's worktree, on real git.
type guardRig struct {
	t         *testing.T
	repo      *gittest.Repo
	worktrees gitcli.Worktrees
	path      string
	branch    string
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	repo := gittest.New(t)
	repo.Commit("README.txt", "seed\n")
	r := &guardRig{
		t: t, repo: repo, worktrees: gitcli.Worktrees{Env: repo.Env()}, branch: "lane/one",
		path: filepath.Join(filepath.Dir(repo.Dir), "lanes", "one"),
	}
	_, err := r.worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: repo.Dir, Path: r.path, Branch: r.branch})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// remote gives the repository a bare remote holding what it has so far.
func (r *guardRig) remote() string {
	r.t.Helper()
	bare := filepath.Join(filepath.Dir(r.repo.Dir), "remote.git")
	r.repo.Git("init", "--quiet", "--bare", bare)
	r.repo.Git("remote", "add", "origin", bare)
	r.repo.Git("push", "--quiet", "origin", "HEAD")
	r.repo.Git("fetch", "--quiet", "origin")
	return bare
}

func (r *guardRig) inWorktree(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Env = r.path, r.repo.Env()
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *guardRig) write(name, content string) {
	r.t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(r.path, name)), 0o700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.path, name), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

func (r *guardRig) commit(name, content string) {
	r.t.Helper()
	r.write(name, content)
	r.inWorktree("add", "--", name)
	r.inWorktree("commit", "--quiet", "-m", "change "+name)
}

func (r *guardRig) survey() lanes.WorktreeFindings {
	r.t.Helper()
	found, err := r.worktrees.Survey(r.t.Context(), r.path)
	if err != nil {
		r.t.Fatal(err)
	}
	return found
}

// TestFetchBringsWhatAnotherMachinePushed is a lane that comes back on a machine that never ran
// it: the branch and the snapshot arrive under their own names, and a worktree goes on from them.
func TestFetchBringsWhatAnotherMachinePushed(t *testing.T) {
	r := newGuardRig(t)
	bare := r.remote()
	work := filepath.Join(r.path, "work.txt")
	if err := os.WriteFile(work, []byte("work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, r.repo.Env()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(r.path, "add", "--", "work.txt")
	git(r.path, "commit", "--quiet", "-m", "lane work")
	head := git(r.path, "rev-parse", "HEAD")
	const ref = "refs/ropeshift/checkpoints/one/1"
	if err := os.WriteFile(filepath.Join(r.path, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.worktrees.Snapshot(t.Context(), r.path, ref)
	if err != nil {
		t.Fatal(err)
	}
	for _, pushed := range []string{lanes.HeadsRef(r.branch), ref} {
		if err := r.worktrees.Push(t.Context(), r.path, "origin", pushed); err != nil {
			t.Fatal(err)
		}
	}
	second := filepath.Join(filepath.Dir(r.repo.Dir), "second")
	git(filepath.Dir(r.repo.Dir), "clone", "--quiet", bare, second)
	refs := []string{lanes.HeadsRef(r.branch), ref, lanes.HeadsRef("lane/nothing-pushed")}
	if err := r.worktrees.Fetch(t.Context(), second, refs); err != nil {
		t.Fatal(err)
	}
	if got := git(second, "rev-parse", lanes.HeadsRef(r.branch)); got != head {
		t.Errorf("the branch reads %s, want %s", got, head)
	}
	if got := git(second, "rev-parse", ref); got != snapshot {
		t.Errorf("the snapshot reads %s, want %s", got, snapshot)
	}
	path := filepath.Join(filepath.Dir(r.repo.Dir), "second-lanes", "one")
	created, err := r.worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: second, Path: path, Branch: r.branch})
	if err != nil {
		t.Fatal(err)
	}
	if created.Head != head {
		t.Errorf("a worktree started at %s, want the work at %s", created.Head, head)
	}
}

func TestFetchWithNoRemoteIsDone(t *testing.T) {
	r := newGuardRig(t)
	if err := r.worktrees.Fetch(t.Context(), r.repo.Dir, []string{lanes.HeadsRef(r.branch)}); err != nil {
		t.Fatal(err)
	}
}

func TestSurvey(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*guardRig)
		dirty   bool
		ahead   int64
		remote  string
	}{
		{name: "clean, on the remote", arrange: func(r *guardRig) { r.remote() }, remote: "origin"},
		{name: "commits not pushed", arrange: func(r *guardRig) { r.remote(); r.commit("a.txt", "a\n"); r.commit("b.txt", "b\n") }, ahead: 2, remote: "origin"},
		{name: "a modified file", arrange: func(r *guardRig) { r.remote(); r.write("README.txt", "changed\n") }, dirty: true, remote: "origin"},
		{name: "an untracked file", arrange: func(r *guardRig) { r.remote(); r.write("new.txt", "new\n") }, dirty: true, remote: "origin"},
		{name: "a staged file", arrange: func(r *guardRig) {
			r.remote()
			r.write("staged.txt", "new\n")
			r.inWorktree("add", "--", "staged.txt")
		}, dirty: true, remote: "origin"},
		{name: "no remote, nothing of its own", arrange: func(*guardRig) {}},
		{name: "no remote, commits of its own", arrange: func(r *guardRig) { r.commit("a.txt", "a\n") }, ahead: 1},
		{name: "commits a push already took", arrange: func(r *guardRig) {
			r.remote()
			r.commit("a.txt", "a\n")
			r.inWorktree("push", "--quiet", "origin", "lane/one")
		}, remote: "origin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newGuardRig(t)
			tt.arrange(r)
			got := r.survey()
			if got.Dirty != tt.dirty || got.Ahead != tt.ahead || got.Remote != tt.remote || got.Branch != r.branch {
				t.Errorf("findings %+v", got)
			}
		})
	}
}

// A handoff names what changed, so each thing git can say about a file reads as a path.
func TestSurveyNamesWhatChanged(t *testing.T) {
	r := newGuardRig(t)
	r.commit("moved.txt", "moved\n")
	r.write("README.txt", "changed\n")
	r.write("deep/new file.txt", "new\n")
	r.write("staged.txt", "new\n")
	r.inWorktree("add", "--", "staged.txt")
	r.inWorktree("mv", "moved.txt", "renamed.txt")
	got := r.survey().DirtyPaths
	slices.Sort(got)
	want := []string{"README.txt", "deep/", "renamed.txt", "staged.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("paths %q, want %q", got, want)
	}
}

func TestSurveyOfNoWorktreeIsNotFound(t *testing.T) {
	r := newGuardRig(t)
	_, err := r.worktrees.Survey(t.Context(), filepath.Join(r.path, "absent"))
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestSurveyOfADetachedHead(t *testing.T) {
	r := newGuardRig(t)
	r.remote()
	r.commit("a.txt", "a\n")
	r.inWorktree("checkout", "--quiet", "--detach")
	got := r.survey()
	if got.Branch != "" || got.Ahead != 1 {
		t.Errorf("findings %+v", got)
	}
}

// indexBytes reads the index a lane's worktree really uses.
func (r *guardRig) indexBytes() []byte {
	r.t.Helper()
	raw, err := os.ReadFile(r.inWorktree("rev-parse", "--path-format=absolute", "--git-path", "index"))
	if err != nil {
		r.t.Fatal(err)
	}
	return raw
}

func TestSnapshotHoldsUntrackedFilesAndLeavesTheIndexAlone(t *testing.T) {
	r := newGuardRig(t)
	r.write("staged.txt", "staged\n")
	r.inWorktree("add", "--", "staged.txt")
	r.write("README.txt", "changed\n")
	r.write("deep/untracked.txt", "untracked\n")
	r.write(".gitignore", "ignored.txt\n")
	r.write("ignored.txt", "ignored\n")
	head := r.inWorktree("rev-parse", "HEAD")
	branch := r.inWorktree("rev-parse", "refs/heads/lane/one")
	statusBefore := r.inWorktree("status", "--porcelain")
	index := r.indexBytes()

	const ref = "refs/ropeshift/checkpoints/one/1"
	commit, err := r.worktrees.Snapshot(t.Context(), r.path, ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.inWorktree("rev-parse", ref); got != commit {
		t.Errorf("ref %s, commit %s", got, commit)
	}
	for name, want := range map[string]string{
		"deep/untracked.txt": "untracked", "staged.txt": "staged", "README.txt": "changed",
	} {
		if got := r.inWorktree("show", commit+":"+name); got != want {
			t.Errorf("%s holds %q", name, got)
		}
	}
	if listed := r.inWorktree("ls-tree", "-r", "--name-only", commit); strings.Contains(listed, "ignored.txt\n") {
		t.Errorf("an ignored file went into the snapshot: %s", listed)
	}
	if got := r.inWorktree("rev-parse", commit+"^"); got != head {
		t.Errorf("parent %s, want %s", got, head)
	}
	if !bytes.Equal(r.indexBytes(), index) {
		t.Error("the lane's index changed")
	}
	if got := r.inWorktree("rev-parse", "refs/heads/lane/one"); got != branch {
		t.Errorf("the branch moved to %s", got)
	}
	if got := r.inWorktree("rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved to %s", got)
	}
	if got := r.inWorktree("status", "--porcelain"); got != statusBefore {
		t.Errorf("status %q, want %q", got, statusBefore)
	}
}

func TestSnapshotWithNoIdentityStillSaves(t *testing.T) {
	r := newGuardRig(t)
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=" + filepath.Join(home, "absent"), "GIT_CONFIG_NOSYSTEM=1"}
	r.write("work.txt", "work\n")
	if _, err := (gitcli.Worktrees{Env: env}).Snapshot(t.Context(), r.path, "refs/ropeshift/checkpoints/one/1"); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRepeatsToTheSameCommit(t *testing.T) {
	r := newGuardRig(t)
	r.write("work.txt", "work\n")
	const ref = "refs/ropeshift/checkpoints/one/1"
	first, err := r.worktrees.Snapshot(t.Context(), r.path, ref)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.worktrees.Snapshot(t.Context(), r.path, ref)
	if err != nil || second != first {
		t.Errorf("a repeat made %s after %s: %v", second, first, err)
	}
}

func TestALaterSnapshotKeepsTheEarlierOneReachable(t *testing.T) {
	r := newGuardRig(t)
	r.write("work.txt", "first\n")
	const ref = "refs/ropeshift/checkpoints/one/1"
	first, err := r.worktrees.Snapshot(t.Context(), r.path, ref)
	if err != nil {
		t.Fatal(err)
	}
	r.write("work.txt", "second\n")
	second, err := r.worktrees.Snapshot(t.Context(), r.path, ref)
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("a change made no new snapshot")
	}
	r.inWorktree("merge-base", "--is-ancestor", first, ref)
}

func TestPushTakesARefAndNeverForces(t *testing.T) {
	r := newGuardRig(t)
	bare := r.remote()
	r.commit("a.txt", "a\n")
	if err := r.worktrees.Push(t.Context(), r.path, "origin", lanes.HeadsRef(r.branch)); err != nil {
		t.Fatal(err)
	}
	if got := r.survey(); got.Ahead != 0 {
		t.Errorf("a pushed branch reads %+v", got)
	}
	r.inWorktree("commit", "--quiet", "--amend", "-m", "rewritten")
	err := r.worktrees.Push(t.Context(), r.path, "origin", lanes.HeadsRef(r.branch))
	if err == nil {
		t.Error("a rewritten branch went over the remote's")
	}
	if got := r.survey(); got.Ahead == 0 {
		t.Errorf("a refused push reads as pushed: %+v", got)
	}
	_ = bare
}

func TestDiscardRemovesADirtyWorktreeAndKeepsItsBranch(t *testing.T) {
	r := newGuardRig(t)
	r.commit("a.txt", "a\n")
	r.write("dirty.txt", "dirty\n")
	ctx := context.Background()
	if err := r.worktrees.Remove(ctx, r.repo.Dir, r.path); !errors.Is(err, ports.ErrWorktreeDirty) {
		t.Fatalf("a plain removal: %v", err)
	}
	if err := r.worktrees.Discard(ctx, r.repo.Dir, r.path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree stands: %v", err)
	}
	if r.repo.Git("rev-parse", "--verify", "--quiet", "refs/heads/lane/one") == "" {
		t.Error("the branch went with it")
	}
	if err := r.worktrees.Discard(ctx, r.repo.Dir, r.path); err != nil {
		t.Errorf("a repeat: %v", err)
	}
}
