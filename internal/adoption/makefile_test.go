package adoption_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/test/gittest"
)

// The Makefile an adoption writes does what it says under make and git: setup clones each repository that has no
// clone, naming the one with no remote, fetch and status read each clone, and a remote that a shell or make would
// read as more than a word is still one word.
func TestTheMakefileSetsUpFetchesAndShowsTheStatusOfEachRepository(t *testing.T) {
	gittest.Require(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	remote := gittest.New(t)
	remote.Commit("README.md", "api\n")
	spaced := gittest.NewIn(t, filepath.Join(t.TempDir(), "with space & more"))
	spaced.Commit("README.md", "docs\n")

	workspace := gittest.New(t)
	makefile := planning.RenderWorkspaceMakefile([]planning.WorkspaceRepository{
		{Name: "api", Remote: remote.Dir, Path: "repos/api"},
		{Name: "docs", Remote: spaced.Dir, Path: "repos/group/docs"},
		{Name: "local", Path: "repos/local"},
	})
	if err := os.WriteFile(filepath.Join(workspace.Dir, "Makefile"), []byte(makefile), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(target string) string {
		t.Helper()
		cmd := exec.Command("make", "--no-print-directory", "-C", workspace.Dir, target)
		cmd.Env = remote.Env()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make %s: %v\n%s", target, err, out)
		}
		return string(out)
	}

	setup := run("setup")
	if !strings.Contains(setup, "local: no remote, clone it into repos/local by hand") {
		t.Errorf("make setup says nothing of the repository with no remote:\n%s", setup)
	}
	for _, name := range []string{"repos/api", "repos/group/docs"} {
		if _, err := os.Stat(filepath.Join(workspace.Dir, name, "README.md")); err != nil {
			t.Errorf("make setup cloned nothing at %s: %v", name, err)
		}
	}
	if again := run("setup"); !strings.Contains(again, "api: present") || !strings.Contains(again, "docs: present") {
		t.Errorf("a second make setup does not leave the clones alone:\n%s", again)
	}
	fetch := run("fetch")
	for _, want := range []string{"workspace: no remote", "api: fetched", "docs: fetched", "local: missing, run make setup"} {
		if !strings.Contains(fetch, want) {
			t.Errorf("make fetch printed no %q:\n%s", want, fetch)
		}
	}
	status := run("status")
	for _, want := range []string{"workspace: ## ", "api: ## ", "docs: ## ", "local: missing"} {
		if !strings.Contains(status, want) {
			t.Errorf("make status printed no %q:\n%s", want, status)
		}
	}
}
