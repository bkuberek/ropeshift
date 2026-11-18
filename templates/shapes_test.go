package templates_test

import (
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/gittest"
)

// shapeFiles is what a shape of a new project writes for the starting data given, as `ropeshift new` writes
// it: each file's path in the folder, and its text. The manifest joins a workspace that names a repository.
func shapeFiles(t *testing.T, shape projectfiles.Shape, data templates.Data) map[string]string {
	t.Helper()
	starting := projectfiles.Data{Project: data.Project}
	for _, r := range data.Repositories {
		starting.Repositories = append(starting.Repositories, projectfiles.CodeRepository{Name: r.Name, Remote: r.Remote})
	}
	// Files renders over the data it is given, which is data in the projectfiles form.
	files, err := projectfiles.Files(shape, starting, func(name string, _ projectfiles.Data) (string, error) {
		return templates.Render(name, data)
	})
	if err != nil {
		t.Fatal(err)
	}
	written := map[string]string{}
	for _, file := range files {
		written[file.Path] = file.Text
	}
	return written
}

// isIgnored asks git, as the practices check does, whether the repository's own .gitignore files ignore a path.
func isIgnored(t *testing.T, repo *gittest.Repo, path string) bool {
	t.Helper()
	command := exec.Command("git", "check-ignore", "-q", "--", path)
	command.Dir = repo.Dir
	command.Env = repo.Env()
	err := command.Run()
	if err == nil {
		return true
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git check-ignore %s: %v", path, err)
	return false
}

// Each shape's files, committed to a repository, hold the practices that FR-PRAC-3 reads from files: the
// repository's own .gitignore ignores a lane environment's files and keeps .env.example, none is tracked, and
// a SECURITY.md, a CODEOWNERS file, and, for a repository that holds code, a pre-commit configuration are in
// the root. Once the practices check exists, this test runs it on the commit with no forge binding and
// expects no finding (FR-PRAC-7).
func TestEachShapesFilesCommitWithTheirPractices(t *testing.T) {
	for _, shape := range projectfiles.Shapes() {
		for _, set := range datasets[1:] {
			if len(set.data.Repositories) > 0 && shape != projectfiles.Workspace {
				continue
			}
			t.Run(string(shape)+" with "+set.name, func(t *testing.T) {
				written := shapeFiles(t, shape, set.data)
				if _, ok := written["plan/repositories.md"]; ok != (len(set.data.Repositories) > 0) {
					t.Errorf("the manifest is written %v for %d repositories", ok, len(set.data.Repositories))
				}
				repo := gittest.New(t)
				for path, text := range written {
					full := filepath.Join(repo.Dir, filepath.FromSlash(path))
					if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(full, []byte(text), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				repo.Git("add", "-A")
				repo.Git("commit", "--quiet", "-m", "chore: start "+set.data.Project)
				tracked := strings.Split(repo.Git("ls-files"), "\n")
				want := slices.Sorted(maps.Keys(written))
				if !slices.Equal(tracked, want) {
					t.Errorf("the commit holds %v, want %v", tracked, want)
				}
				for _, path := range []string{".env", ".env.local", ".env.production", "app/.env"} {
					if !isIgnored(t, repo, path) {
						t.Errorf("%s is not ignored", path)
					}
				}
				if isIgnored(t, repo, ".env.example") {
					t.Error(".env.example is ignored")
				}
				for _, path := range []string{"SECURITY.md", "CODEOWNERS"} {
					if !slices.Contains(tracked, path) {
						t.Errorf("%s is not in the commit", path)
					}
				}
			})
		}
	}
}

// A workspace ignores its folder for lane worktrees, so a lane worktree there is never shown as untracked content
// or committed by mistake, and it keeps its own profile layer at .ropeshift/profile.toml tracked (FR-PROF-3).
func TestAWorkspaceIgnoresItsLaneWorktreesAndNotItsProfileLayer(t *testing.T) {
	repo := gittest.New(t)
	if err := os.WriteFile(filepath.Join(repo.Dir, ".gitignore"), []byte(render(t, "workspace/gitignore", templates.Data{Project: "orchid"})), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{".ropeshift/worktrees/", ".ropeshift/worktrees/orchid-api/lane-1"} {
		if !isIgnored(t, repo, path) {
			t.Errorf("%s is not ignored", path)
		}
	}
	for _, path := range []string{".ropeshift/profile.toml", ".ropeshift/worktrees-notes.md"} {
		if isIgnored(t, repo, path) {
			t.Errorf("%s is ignored", path)
		}
	}
}

// The workspace Makefile's setup warns when the workspace does not ignore the folder for lane worktrees, as it
// warns for the folder of clones, and its other targets keep to the folder of clones and the manifest's names.
func TestTheWorkspaceMakefileWarnsWhenWorktreesAreNotIgnored(t *testing.T) {
	makefile := render(t, "workspace/makefile", templates.Data{Project: "orchid"})
	if !strings.Contains(makefile, `git check-ignore -q ".ropeshift/worktrees/"`) || !strings.Contains(makefile, "does not ignore .ropeshift/worktrees/") {
		t.Error("make setup does not warn about an unignored .ropeshift/worktrees/")
	}
	if strings.Contains(makefile, "$(CODE_DIR)/.ropeshift") {
		t.Error("a target loops over the workspace's worktree folder")
	}
}

// A workspace ignores the folder of clones, so a clone there is never read as part of the plan.
func TestAWorkspaceIgnoresItsClones(t *testing.T) {
	repo := gittest.New(t)
	if err := os.WriteFile(filepath.Join(repo.Dir, ".gitignore"), []byte(render(t, "workspace/gitignore", templates.Data{Project: "orchid"})), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isIgnored(t, repo, "repos/orchid-api") {
		t.Error("repos/ is not ignored")
	}
}

// A repository that names the path of its clone is listed as name=remote=path, and one that names none is listed as
// name=remote with the clone at repos/<name>.
func TestARepositoryThatNamesItsPathIsListedWithIt(t *testing.T) {
	makefile := render(t, "workspace/makefile", templates.Data{Project: "orchid", Repositories: []templates.Repository{
		{Name: "api", Remote: "git@example.invalid:orchid/api.git"},
		{Name: "deep", Remote: "git@example.invalid:orchid/deep.git", Path: "repos/group/deep"},
		{Name: "local", Path: "services/local"},
	}})
	_, after, _ := strings.Cut(makefile, "REPOSITORIES :=")
	list, _, _ := strings.Cut(after, "\n\n")
	want := []string{"api=git@example.invalid:orchid/api.git", "deep=git@example.invalid:orchid/deep.git=repos/group/deep", "local==services/local"}
	if fields := strings.Fields(strings.ReplaceAll(list, "\\\n", " ")); !slices.Equal(fields, want) {
		t.Errorf("the Makefile's REPOSITORIES is %q, want %v", fields, want)
	}
}

// The workspace Makefile's setup, fetch, and status work on a repository at a nested path, and on one at repos/<name>
// beside it, under make and git: setup clones into the path and makes the folders above it, and the other two read
// the clone there.
func TestTheWorkspaceMakefileSetsUpFetchesAndShowsTheStatusOfANestedRepository(t *testing.T) {
	gittest.Require(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	remote := gittest.New(t)
	remote.Commit("README.md", "remote\n")
	workspace := gittest.New(t)
	text := render(t, "workspace/makefile", templates.Data{Project: "orchid", Repositories: []templates.Repository{
		{Name: "api", Remote: "file://" + remote.Dir},
		{Name: "deep", Remote: "file://" + remote.Dir, Path: "repos/group/deep"},
		{Name: "elsewhere", Remote: "file://" + remote.Dir, Path: "services/a.b/elsewhere"},
	}})
	if err := os.WriteFile(filepath.Join(workspace.Dir, "Makefile"), []byte(text), 0o600); err != nil {
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
	run("setup")
	for _, clone := range []string{"repos/api", "repos/group/deep", "services/a.b/elsewhere"} {
		if _, err := os.Stat(filepath.Join(workspace.Dir, filepath.FromSlash(clone), "README.md")); err != nil {
			t.Errorf("make setup cloned nothing at %s: %v", clone, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace.Dir, "repos", "deep")); err == nil {
		t.Error("make setup cloned the nested repository to repos/deep")
	}
	if again := run("setup"); strings.Count(again, ": present") != 3 {
		t.Errorf("a second make setup does not leave the three clones alone:\n%s", again)
	}
	fetch := run("fetch")
	for _, want := range []string{"api: fetched", "deep: fetched", "elsewhere: fetched"} {
		if !strings.Contains(fetch, want) {
			t.Errorf("make fetch printed no %q:\n%s", want, fetch)
		}
	}
	status := run("status")
	for _, want := range []string{"api: ## ", "deep: ## ", "elsewhere: ## "} {
		if !strings.Contains(status, want) {
			t.Errorf("make status printed no %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "missing") {
		t.Errorf("make status reads a clone as missing:\n%s", status)
	}
}
