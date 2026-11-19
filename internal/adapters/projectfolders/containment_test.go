package projectfolders_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/projectfolders"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

const identity = "[user]\n\tname = Test\n\temail = test@example.invalid\n"

// gitEnv is the whole environment of a git command whose global configuration is the text given, with no system
// configuration, so a test owns every setting the user's own git would add.
func gitEnv(t *testing.T, config string, more []string) []string {
	t.Helper()
	home := t.TempDir()
	file := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(file, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return append([]string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=" + file, "GIT_CONFIG_NOSYSTEM=1"}, more...)
}

// put writes a folder's files and returns the folder it wrote.
func put(t *testing.T, f projectfolders.Folders, folder string, files []ports.FolderFile) ports.FolderID {
	t.Helper()
	id, err := f.Write(t.Context(), folder, files, "")
	if err != nil || id == "" {
		t.Fatalf("a write names the folder %q (%v)", id, err)
	}
	return id
}

// A write that is told which folder it wrote before finds it again, and refuses a folder that is another one.
func TestWriteRefusesAFolderThatIsNotTheOneItWrote(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, identity, nil)}
	root := t.TempDir()
	folder := filepath.Join(root, "harbor")
	files := []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}}
	id := put(t, f, folder, files)
	if again, err := f.Write(t.Context(), folder, files, id); err != nil || again != id {
		t.Fatalf("a write of the same folder says %q (%v), want %q", again, err, id)
	}
	other := filepath.Join(root, "other")
	if _, err := f.Write(t.Context(), other, files, id); !errors.Is(err, ports.ErrFolderChanged) {
		t.Errorf("a write of a folder that is not the one it wrote: %v", err)
	}
}

// A folder swapped for a link to a sibling between the worker's write and its first commit is not committed in: git is
// never run there, and the refusal says the folder changed.
func TestFirstCommitRefusesAFolderSwappedForALinkToASibling(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, identity, nil)}
	root := t.TempDir()
	folder := filepath.Join(root, "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
	moved, sibling := filepath.Join(root, "moved"), filepath.Join(root, "sibling")
	for _, step := range []func() error{
		func() error { return os.Rename(folder, moved) },
		func() error { return os.Mkdir(sibling, 0o755) },
		func() error { return os.Symlink(sibling, folder) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	for _, resumed := range []bool{false, true} {
		commit, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", resumed, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}})
		if !errors.Is(err, ports.ErrFolderChanged) || commit != "" {
			t.Errorf("resumed %v: the first commit is %q (%v), want a refusal that the folder changed", resumed, commit, err)
		}
	}
	for _, dir := range []string{sibling, moved} {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			t.Errorf("git ran in %s", dir)
		}
	}
	if _, err := f.FirstCommit(t.Context(), moved, "chore: start harbor", false, "", ports.CommitSpec{Paths: []string{"AGENTS.md"}}); !errors.Is(err, ports.ErrFolderChanged) {
		t.Errorf("a first commit with no folder named: %v", err)
	}
}

func script(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

// A link planted at a folder inside the one being written is not followed out of it: the write fails and nothing lands
// where the link leads.
func TestWriteDoesNotFollowALinkPlantedAtANestedFolder(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, identity, nil)}
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	folder := filepath.Join(root, "harbor")
	for _, dir := range []string{outside, folder} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(folder, "plan")); err != nil {
		t.Fatal(err)
	}
	_, err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}, {Path: "plan/goal.md", Text: "goal"}}, "")
	if err == nil {
		t.Fatal("a write went through a link planted at a nested folder")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the link's target holds %v", entries)
	}
}

// A link planted at a file's own name is not written through either.
func TestWriteDoesNotFollowALinkPlantedAtAFile(t *testing.T) {
	gittest.Require(t)
	f := projectfolders.Folders{Env: gitEnv(t, identity, nil)}
	root := t.TempDir()
	folder := filepath.Join(root, "harbor")
	target := filepath.Join(root, "target.txt")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(folder, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}}, ""); err != nil {
		t.Fatalf("a link at a file's name is a file that exists, and is left: %v", err)
	}
	if text, _ := os.ReadFile(target); string(text) != "mine" {
		t.Errorf("the link's target holds %q", text)
	}
}

// The first commit runs under the user's global git configuration, which holds a hooks path, an init template with a hook,
// and signing, and none of them takes effect: no hook runs, the repository has none, and the commit is unsigned.
func TestFirstCommitRunsNoHookAndSignsNothing(t *testing.T) {
	gittest.Require(t)
	scratch := t.TempDir()
	hooks, templates := filepath.Join(scratch, "hooks"), filepath.Join(scratch, "template")
	markers := map[string]string{
		"hooksPath":    filepath.Join(scratch, "ran-hooks-path"),
		"template":     filepath.Join(scratch, "ran-template"),
		"preCommitMsg": filepath.Join(scratch, "ran-commit-msg"),
	}
	script(t, filepath.Join(hooks, "pre-commit"), markers["hooksPath"])
	script(t, filepath.Join(hooks, "commit-msg"), markers["preCommitMsg"])
	script(t, filepath.Join(templates, "hooks", "post-commit"), markers["template"])
	config := identity + "[core]\n\thooksPath = " + hooks + "\n[init]\n\ttemplateDir = " + templates + "\n[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = /bin/false\n"
	f := projectfolders.Folders{Env: gitEnv(t, config, nil)}
	folder := filepath.Join(t.TempDir(), "harbor")
	id := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
	commit, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, id, ports.CommitSpec{Paths: []string{"AGENTS.md"}})
	if err != nil || commit == "" {
		t.Fatalf("the first commit is %q (%v)", commit, err)
	}
	for name, marker := range markers {
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("the %s hook ran", name)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(folder, ".git", "hooks")); len(entries) != 0 {
		t.Errorf("git init copied %d files of the user's template into .git/hooks", len(entries))
	}
	show := exec.Command("git", "-C", folder, "cat-file", "-p", commit)
	show.Env = gitEnv(t, identity, nil)
	out, err := show.CombinedOutput()
	if err != nil || strings.Contains(string(out), "gpgsig") {
		t.Errorf("the commit is signed or unreadable: %v\n%s", err, out)
	}
}

// A repository the worker did not make is not taken as its own: a .git with no commit, or one that holds a commit, is
// refused unless the worker's journal says it ran git init, which is how a resume finds the repository its crash left.
func TestFirstCommitRefusesARepositoryTheWorkerDidNotMake(t *testing.T) {
	gittest.Require(t)
	env := gitEnv(t, identity, nil)
	f := projectfolders.Folders{Env: env}
	git := func(dir string, args ...string) {
		t.Helper()
		run := exec.Command("git", args...)
		run.Dir, run.Env = dir, env
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	prepare := func(commit bool) (string, ports.FolderID) {
		t.Helper()
		folder := filepath.Join(t.TempDir(), "harbor")
		made := put(t, f, folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}})
		git(folder, "init", "--quiet")
		if commit {
			git(folder, "add", "-A")
			git(folder, "commit", "--quiet", "-m", "planted")
		}
		return folder, made
	}
	for name, commit := range map[string]bool{"with no commit": false, "with a commit": true} {
		folder, made := prepare(commit)
		if id, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false, made, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err == nil || !strings.Contains(err.Error(), "did not make") {
			t.Errorf("a .git %s was taken: %q, %v", name, id, err)
		}
	}
	folder, made := prepare(false)
	if id, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", true, made, ports.CommitSpec{Paths: []string{"AGENTS.md"}}); err != nil || id == "" {
		t.Errorf("a resume that journaled its git init is refused: %q, %v", id, err)
	}
}
