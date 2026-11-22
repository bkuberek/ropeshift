package gitcli_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// blobOf is the blob git stores for a file or a link in the index, which stages it.
func blobOf(t *testing.T, repo *gittest.Repo, path string) string {
	t.Helper()
	repo.Git("add", "--", path)
	return repo.Git("rev-parse", ":"+path)
}

// A symlink is read as git stores it, the blob of its link text, and what it points at is never opened: a link to a FIFO
// or to /dev/zero answers at once, where opening it would wait for a writer or read for ever.
func TestASymlinkIsReadAsItsLinkTextAndItsTargetIsNeverOpened(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	links := map[string]string{"to-fifo": fifo, "to-zero": "/dev/zero", "to-file": "a.md", "to-nothing": "nowhere/at all"}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(repo.Dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	// A read that opens the FIFO waits for a writer: bounded here, so a failure fails the test and does not stall it.
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	objects := repo.Git("count-objects")
	got, err := gitcli.Worktrees{Env: repo.Env()}.Contents(ctx, repo.Dir, []string{"to-fifo", "to-zero", "to-file", "to-nothing", "a.md", "gone.md"})
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if repo.Git("count-objects") != objects {
		t.Error("reading the contents wrote an object")
	}
	for name := range links {
		if want := blobOf(t, repo, name); got[name] != want {
			t.Errorf("%s reads as %q, want the blob of its link text %s", name, got[name], want)
		}
	}
	if want := blobOf(t, repo, "a.md"); got["a.md"] != want || got["gone.md"] != "" {
		t.Errorf("a.md reads as %q, want %s, and gone.md as %q", got["a.md"], want, got["gone.md"])
	}
}

// A FIFO that is no link is never opened either: git cannot store it, and a read of it would wait for a writer.
func TestAFIFOInPlaceOfAFileIsNeverOpened(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	if err := os.Remove(filepath.Join(repo.Dir, "a.md")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(repo.Dir, "a.md"), 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := (gitcli.Worktrees{Env: repo.Env()}).Contents(ctx, repo.Dir, []string{"a.md"}); err != nil {
		t.Fatalf("contents: %v", err)
	}
}

// A submodule's path holds a directory that no blob stands for: it reads as the commit the index records for it, and
// a read that names it fails no other path's.
func TestAGitlinkIsReadAsItsRecordedCommit(t *testing.T) {
	inner := gittest.New(t)
	recorded := inner.Commit("inner.md", "inner\n")
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	repo.Git("submodule", "--quiet", "add", inner.Dir, "sub")
	if err := os.WriteFile(filepath.Join(repo.Dir, "b.md"), []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := gitcli.Worktrees{Env: repo.Env()}.Contents(t.Context(), repo.Dir, []string{"sub", "b.md", "a.md"})
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if got["sub"] != recorded {
		t.Errorf("the submodule reads as %q, want its recorded commit %s", got["sub"], recorded)
	}
	if want := repo.Git("hash-object", "b.md"); got["b.md"] != want {
		t.Errorf("b.md reads as %q, want %s", got["b.md"], want)
	}
}

// A path that holds a newline, a carriage return, or begins with a quote is read as it is, and does not fail the paths
// beside it.
func TestAPathThatHoldsANewlineIsHashedOnItsOwn(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	names := []string{"new\nline.md", "carriage\rreturn.md", "\"quoted.md", "-dash.md", "plain.md", " space.md"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(repo.Dir, name), []byte("contents of "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := gitcli.Worktrees{Env: repo.Env()}.Contents(t.Context(), repo.Dir, append(slices.Clone(names), "a.md"))
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	for _, name := range append(slices.Clone(names), "a.md") {
		if want := blobOf(t, repo, name); got[name] != want {
			t.Errorf("%q reads as %q, want %s", name, got[name], want)
		}
	}
}

// A path beneath a directory that is a link is read as gone, whatever the link points at: what lies outside the clone is
// never opened, hashed, or listed, for a file, a file named with a line break, a link, and a submodule alike. A link that
// is the path itself still reads as its link text.
func TestAPathBeneathALinkedDirectoryIsReadAsGone(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	outside := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(outside, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outside, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("secret.md", "a file outside the clone\n")
	write("new\nline.md", "a file with a line break in its name\n")
	if err := os.Symlink("secret.md", filepath.Join(outside, "inner-link")); err != nil {
		t.Fatal(err)
	}
	inner := gittest.NewIn(t, filepath.Join(outside, "submodule-root"))
	inner.Commit("inner.md", "inner\n")
	if err := os.Rename(inner.Dir, filepath.Join(outside, "subrepo")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo.Dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo.Dir, "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo.Dir, "deep", "linked2")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.md", filepath.Join(repo.Dir, "leaf-link")); err != nil {
		t.Fatal(err)
	}
	beneath := []string{"linked/secret.md", "linked/new\nline.md", "linked/inner-link", "linked/subrepo", "linked/subrepo/", "deep/linked2/secret.md"}
	runs := gittest.RecordGit(t)
	got, err := gitcli.Worktrees{Env: repo.Env()}.Contents(t.Context(), repo.Dir, append(slices.Clone(beneath), "a.md", "leaf-link", "linked"))
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	for _, path := range beneath {
		if got[path] != "" {
			t.Errorf("%q beneath a linked directory reads as %q, want gone", path, got[path])
		}
	}
	for _, link := range []string{"leaf-link", "linked"} {
		if want := blobOf(t, repo, link); got[link] != want {
			t.Errorf("the link %s reads %q, want the blob of its link text %s", link, got[link], want)
		}
	}
	if want := blobOf(t, repo, "a.md"); got["a.md"] != want {
		t.Errorf("a.md reads as %q, want %s", got["a.md"], want)
	}
	for _, run := range runs("hash-object") {
		if strings.Contains(run, "linked") {
			t.Errorf("git hashed a path beneath a link: %q", run)
		}
	}
	for _, run := range runs("--literal-pathspecs") {
		if strings.Contains(run, "linked") {
			t.Errorf("git listed a path beneath a link: %q", run)
		}
	}
}

// pathOf puts a git on PATH that answers the commands whose first argument match as a script says, and runs the real one
// for the rest. The command a read runs is found on this process's PATH.
func pathOf(t *testing.T, answer string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	bin := t.TempDir()
	shim := "#!/bin/sh\n" + answer + "\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

// A git that refuses to read a clone, as for a clone another user owns, is a read that failed: the clone is not
// missing, so what an earlier reading found of it is kept. Only a path git says is no repository holds no clone.
func TestAGitThatRefusesTheCloneIsAFailedReadNotAMissingClone(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("a.md", "a\n")
	pathOf(t, `for a in "$@"; do case "$a" in --git-dir) echo "fatal: detected dubious ownership in repository at '/srv/clone'" >&2; exit 128;; esac; done`)
	_, err := gitcli.Worktrees{Env: repo.Env()}.Edits(t.Context(), repo.Dir, 10)
	if err == nil || errors.Is(err, ports.ErrNotFound) || !strings.Contains(err.Error(), "dubious ownership") {
		t.Errorf("a git that refused the clone reads %v", err)
	}
}

func TestADirectoryThatIsNoRepositoryHoldsNoClone(t *testing.T) {
	gittest.Require(t)
	repo := gittest.New(t)
	plain := t.TempDir()
	if _, err := (gitcli.Worktrees{Env: repo.Env()}).Edits(t.Context(), plain, 10); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a directory that is no repository reads %v", err)
	}
}

// A ref or a fork point git fails to read, as a git does that stops part way with exit 128, is an error and never a
// branch that is gone, which a reading would drop its conflict for. Only what git says is missing is ErrNotFound.
func TestAFailedRefOrForkPointReadIsNeverReadAsMissing(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("branch", "lane/lane-1")
	tests := []struct {
		name, shim string
		// diverged and held say the read of the lane's branch and of what it holds is a missing ref or history.
		diverged, held bool
	}{
		{"a rev-parse that exits 128", `case "$*" in *rev-parse*) echo "fatal: unable to read tree" >&2; exit 128;; esac`, false, false},
		{"a merge-base that exits 128", `case "$*" in *merge-base*) echo "fatal: unable to read tree" >&2; exit 128;; esac`, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pathOf(t, tt.shim)
			worktrees := gitcli.Worktrees{Env: clone.Env()}
			if _, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{}, 100); err == nil || errors.Is(err, ports.ErrNotFound) != tt.diverged {
				t.Errorf("Diverged reads %v, want an error that is not-found %v", err, tt.diverged)
			}
			if _, err := worktrees.Holds(t.Context(), clone.Dir, "lane/lane-1", "lane/lane-1"); err == nil || errors.Is(err, ports.ErrNotFound) != tt.held {
				t.Errorf("Holds reads %v, want an error that is not-found %v", err, tt.held)
			}
		})
	}
	t.Run("what git says is missing", func(t *testing.T) {
		pathOf(t, `case "$*" in *merge-base*) exit 1;; esac`)
		if _, err := (gitcli.Worktrees{Env: clone.Env()}).Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{}, 100); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("two refs that share no history read %v", err)
		}
		pathOf(t, `case "$*" in *rev-parse*) exit 1;; esac`)
		if _, err := (gitcli.Worktrees{Env: clone.Env()}).Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{}, 100); !errors.Is(err, ports.ErrNotFound) {
			t.Errorf("a ref git says is missing reads %v", err)
		}
	})
}

// A clone's file system monitor never runs, so a read of a clone runs no program the person's configuration names.
func TestReadingAClonePassesNoFileSystemMonitor(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("branch", "lane/lane-1")
	marker := filepath.Join(t.TempDir(), "monitor-ran")
	hook := filepath.Join(t.TempDir(), "monitor")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho ran >> '"+marker+"'\nprintf '\\0'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	clone.Git("config", "core.fsmonitor", hook)
	if err := os.WriteFile(filepath.Join(clone.Dir, "a.md"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The configuration does run the monitor for the person's own git, or this test shows nothing.
	clone.Git("status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Skipf("this git does not run the file system monitor for status: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	worktrees := gitcli.Worktrees{Env: clone.Env()}
	if _, err := worktrees.Edits(t.Context(), clone.Dir, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := worktrees.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{}, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := worktrees.Contents(t.Context(), clone.Dir, []string{"a.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := worktrees.Holds(t.Context(), clone.Dir, "lane/lane-1", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a read of the clone ran the file system monitor the person's configuration names")
	}
}

// commitsOnto writes count commits onto a branch of a repository with fast-import, each changing a file of its own.
func commitsOnto(t *testing.T, repo *gittest.Repo, branch string, count int) {
	t.Helper()
	tip := repo.Git("rev-parse", "refs/heads/"+branch)
	var stream strings.Builder
	for i := range count {
		message := "change " + strconv.Itoa(i)
		fmt.Fprintf(&stream, "commit refs/heads/%s\ncommitter T <t@example.invalid> %d +0000\ndata %d\n%s\n", branch, 1700000000+i, len(message), message)
		if i == 0 {
			fmt.Fprintf(&stream, "from %s\n", tip)
		}
		content := "file " + strconv.Itoa(i) + "\n"
		fmt.Fprintf(&stream, "M 644 inline many/f%04d.md\ndata %d\n%s\n", i, len(content), content)
	}
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Env, cmd.Stdin = repo.Dir, repo.Env(), strings.NewReader(stream.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fast-import: %v\n%s", err, out)
	}
}

// The base's side of a lane's branch lists at most 1000 commits, asked for as one more than that, and says it cut when
// git held more.
func TestTheBasesSideIsCutAtAThousandCommitsAndSaysSo(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	clone.Git("branch", "lane/lane-1")
	commitsOnto(t, origin, "main", 1005)
	clone.Git("fetch", "--quiet", "origin")
	runs := gittest.RecordGit(t)
	got, err := gitcli.Worktrees{Env: clone.Env()}.Diverged(t.Context(), clone.Dir, "lane/lane-1", "main", ledger.Landed{}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Cut || len(got.Pushed) != 1000 {
		t.Errorf("the base's side lists %d paths, cut %v, want 1000 and a cut", len(got.Pushed), got.Cut)
	}
	if !slices.ContainsFunc(runs("--no-optional-locks"), func(run string) bool {
		return strings.Contains(run, " log ") && strings.Contains(run, " --max-count=1001 ")
	}) {
		t.Errorf("no log asked for 1001 commits among %q", runs("--no-optional-locks"))
	}
}

// A clone's unpushed commits are counted up to one more than the 1000 a read walks, and a clone with more says it cut.
func TestAClonesUnpushedCommitsAreCutAtAThousandAndSaySo(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("a.md", "a\n")
	clone := origin.Clone("registered")
	commitsOnto(t, clone, "main", 1005)
	clone.Git("reset", "--quiet", "--hard")
	runs := gittest.RecordGit(t)
	got, err := gitcli.Worktrees{Env: clone.Env()}.Edits(t.Context(), clone.Dir, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Cut || len(got.Edits) != 1000 {
		t.Errorf("the clone lists %d paths, cut %v, want 1000 and a cut", len(got.Edits), got.Cut)
	}
	if !slices.ContainsFunc(runs("--no-optional-locks"), func(run string) bool {
		return strings.Contains(run, " rev-list ") && strings.Contains(run, " --max-count=1001 ")
	}) {
		t.Errorf("no count asked for 1001 commits among %q", runs("--no-optional-locks"))
	}
}
