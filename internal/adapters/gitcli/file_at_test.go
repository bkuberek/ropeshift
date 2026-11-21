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
