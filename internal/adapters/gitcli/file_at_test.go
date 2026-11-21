package gitcli_test

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/test/gittest"
)

const settingsPath = ".claude/settings.json"

// FileAt reads what a commit holds at a path, not a later commit's copy and not the working tree's, and a path the
// commit does not hold is none.
func TestFileAtReadsTheCommitsCopy(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	base := repo.Commit(settingsPath, "base\n")
	repo.Commit(settingsPath, "later\n")
	if err := os.WriteFile(filepath.Join(repo.Dir, settingsPath), []byte("worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}
	if raw, found, err := git.FileAt(t.Context(), base, settingsPath, 1<<20); err != nil || !found || string(raw) != "base\n" {
		t.Errorf("FileAt read %q, %t, %v", raw, found, err)
	}
	for _, path := range []string{".claude/absent.json", "absent/settings.json", settingsPath + "/inner"} {
		if raw, found, err := git.FileAt(t.Context(), base, path, 1<<20); err != nil || found || raw != nil {
			t.Errorf("FileAt read %q at the absent path %s: %t, %v", raw, path, found, err)
		}
	}
	for _, commit := range []string{"HEAD", "--output=x", base[:12]} {
		if _, _, err := git.FileAt(t.Context(), commit, settingsPath, 1<<20); !errors.Is(err, gitcli.ErrNotAFullHash) {
			t.Errorf("FileAt at %q gave %v", commit, err)
		}
	}
}

// BlobID names a file's content as git names its blob, in the object format a commit's hash has, so a worktree's file
// is told apart from the commit's without git, and a commit that is no full hash names no format.
func TestBlobIDNamesContentAsGitDoes(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	base := repo.Commit(settingsPath, "base\n")
	if got, ok := gitcli.BlobID(base, []byte("base\n")); !ok || got != repo.Git("rev-parse", base+":"+settingsPath) {
		t.Errorf("BlobID names %q, %t", got, ok)
	}
	if got, ok := gitcli.BlobID(base, []byte("other\n")); !ok || got == repo.Git("rev-parse", base+":"+settingsPath) {
		t.Errorf("another content is named %q, %t", got, ok)
	}
	if _, ok := gitcli.BlobID("HEAD", []byte("base\n")); ok {
		t.Error("a commit that is no full hash names a format")
	}
}

// A replace ref names another object in place of the commit's, and FileAt reads the commit's own.
func TestFileAtFollowsNoReplaceRef(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	base := repo.Commit(settingsPath, "base\n")
	other := filepath.Join(t.TempDir(), "other")
	if err := os.WriteFile(other, []byte("replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo.Git("replace", repo.Git("rev-parse", base+":"+settingsPath), repo.Git("hash-object", "-w", other))
	if got := repo.Git("cat-file", "blob", base+":"+settingsPath); got != "replaced" {
		t.Fatalf("the replace ref did not take: %q", got)
	}
	raw, found, err := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}.FileAt(t.Context(), base, settingsPath, 1<<20)
	if err != nil || !found || string(raw) != "base\n" {
		t.Errorf("FileAt read %q, %t, %v", raw, found, err)
	}
}

// A loose object written over the real one, whether the commit, a tree, or the file, does not hash to its name, and
// FileAt refuses it rather than read it.
func TestFileAtRefusesAnObjectThatDoesNotHashToItsName(t *testing.T) {
	t.Parallel()
	for name, object := range map[string]func(repo *gittest.Repo, base string) (kind, oid string){
		"the commit": func(_ *gittest.Repo, base string) (string, string) { return "commit", base },
		"the root tree": func(repo *gittest.Repo, base string) (string, string) {
			return "tree", repo.Git("rev-parse", base+"^{tree}")
		},
		"a directory's tree": func(repo *gittest.Repo, base string) (string, string) {
			return "tree", repo.Git("rev-parse", base+":.claude")
		},
		"the file": func(repo *gittest.Repo, base string) (string, string) {
			return "blob", repo.Git("rev-parse", base+":"+settingsPath)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := gittest.New(t)
			base := repo.Commit(settingsPath, "base\n")
			kind, oid := object(repo, base)
			forgeLoose(t, filepath.Join(repo.Dir, ".git", "objects"), kind, oid, "forged\n")
			raw, _, err := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}.FileAt(t.Context(), base, settingsPath, 1<<20)
			if !errors.Is(err, gitcli.ErrObjectMismatch) || raw != nil {
				t.Errorf("FileAt read %q, %v", raw, err)
			}
		})
	}
}

// A repository that names its objects by SHA-256 is read the same way.
func TestFileAtReadsASHA256Repository(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = repo.Env()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
		return string(bytes.TrimSpace(out))
	}
	run("init", "--quiet", "--object-format=sha256")
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, settingsPath), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "--all")
	run("commit", "--quiet", "-m", "base")
	base := run("rev-parse", "HEAD")
	raw, found, err := gitcli.Git{Dir: dir, Env: repo.Env()}.FileAt(t.Context(), base, settingsPath, 1<<20)
	if len(base) != 64 || err != nil || !found || string(raw) != "base\n" {
		t.Errorf("FileAt at %s read %q, %t, %v", base, raw, found, err)
	}
}

// forgeLoose writes body as the loose object of a kind named oid, which git then reads by that name although it does
// not hash to it.
func forgeLoose(t *testing.T, objects, kind, oid, body string) {
	t.Helper()
	var packed bytes.Buffer
	w := zlib.NewWriter(&packed)
	if _, err := fmt.Fprintf(w, "%s %d\x00%s", kind, len(body), body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(objects, oid[:2], oid[2:])
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, packed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// FilesUnder lists the regular files below a directory at any depth, as the commit holds them and not as the working
// tree does, and leaves out a link and a submodule. BlobOf reads each one's content.
func TestFilesUnderListsTheCommitsFilesBelowADirectory(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	repo.Commit(".claude/agents/review/reviewer.md", "reviewer\n")
	repo.Commit("README.md", "a repository\n")
	if err := os.Symlink("agents/review/reviewer.md", filepath.Join(repo.Dir, ".claude", "linked.md")); err != nil {
		t.Skip("no symbolic links here")
	}
	repo.Git("add", "--", ".claude/linked.md")
	repo.Git("update-index", "--add", "--cacheinfo", "160000,"+repo.Git("rev-parse", "HEAD")+",.claude/module")
	base := repo.Commit(".claude/settings.json", "{}\n")
	if err := os.WriteFile(filepath.Join(repo.Dir, ".claude", "worktree.md"), []byte("worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}
	files, err := git.FilesUnder(t.Context(), base, ".claude", 10)
	if err != nil || len(files) != 2 || files[0].Path != "agents/review/reviewer.md" || files[1].Path != "settings.json" {
		t.Fatalf("FilesUnder listed %+v, %v", files, err)
	}
	if raw, err := git.BlobOf(t.Context(), files[0].Blob, 1<<20); err != nil || string(raw) != "reviewer\n" {
		t.Errorf("BlobOf read %q, %v", raw, err)
	}
	if _, err := git.FilesUnder(t.Context(), base, ".claude", 1); !errors.Is(err, gitcli.ErrTooManyFiles) {
		t.Errorf("a listing over its limit gave %v", err)
	}
	for _, dir := range []string{"absent", "README.md", ".claude/settings.json"} {
		if files, err := git.FilesUnder(t.Context(), base, dir, 10); err != nil || files != nil {
			t.Errorf("FilesUnder %s listed %+v, %v", dir, files, err)
		}
	}
}

// A listing checks each tree it reads, and BlobOf each file, as FileAt does.
func TestFilesUnderRefusesAnObjectThatDoesNotHashToItsName(t *testing.T) {
	t.Parallel()
	repo := gittest.New(t)
	base := repo.Commit(".claude/agents/reviewer.md", "reviewer\n")
	git := gitcli.Git{Dir: repo.Dir, Env: repo.Env()}
	files, err := git.FilesUnder(t.Context(), base, ".claude", 10)
	if err != nil || len(files) != 1 {
		t.Fatalf("FilesUnder listed %+v, %v", files, err)
	}
	objects := filepath.Join(repo.Dir, ".git", "objects")
	forgeLoose(t, objects, "blob", files[0].Blob, "forged\n")
	if raw, err := git.BlobOf(t.Context(), files[0].Blob, 1<<20); !errors.Is(err, gitcli.ErrObjectMismatch) || raw != nil {
		t.Errorf("BlobOf read %q, %v", raw, err)
	}
	forgeLoose(t, objects, "tree", repo.Git("rev-parse", base+":.claude/agents"), "forged\n")
	if files, err := git.FilesUnder(t.Context(), base, ".claude", 10); !errors.Is(err, gitcli.ErrObjectMismatch) || files != nil {
		t.Errorf("FilesUnder listed %+v, %v", files, err)
	}
}
