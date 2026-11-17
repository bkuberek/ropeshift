package projectfolders_test

import (
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
	err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}, {Path: "plan/goal.md", Text: "goal"}})
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
	if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents"}}); err != nil {
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
	if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}}); err != nil {
		t.Fatal(err)
	}
	commit, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false)
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
	prepare := func(commit bool) string {
		t.Helper()
		folder := filepath.Join(t.TempDir(), "harbor")
		if err := f.Write(t.Context(), folder, []ports.FolderFile{{Path: "AGENTS.md", Text: "agents\n"}}); err != nil {
			t.Fatal(err)
		}
		git(folder, "init", "--quiet")
		if commit {
			git(folder, "add", "-A")
			git(folder, "commit", "--quiet", "-m", "planted")
		}
		return folder
	}
	for name, commit := range map[string]bool{"with no commit": false, "with a commit": true} {
		folder := prepare(commit)
		if id, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", false); err == nil || !strings.Contains(err.Error(), "did not make") {
			t.Errorf("a .git %s was taken: %q, %v", name, id, err)
		}
	}
	folder := prepare(false)
	if id, err := f.FirstCommit(t.Context(), folder, "chore: start harbor", true); err != nil || id == "" {
		t.Errorf("a resume that journaled its git init is refused: %q, %v", id, err)
	}
}
