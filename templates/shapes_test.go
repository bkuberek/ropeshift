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
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/practicegit"
	"ropeshift.com/ropeshift/internal/practicerun"
	"ropeshift.com/ropeshift/internal/practices"
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
// the root. The practices check run on the commit with no forge binding and the default merge authority gives
// every practice met or not applicable and no finding (FR-PRAC-7).
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
				requireNoFinding(t, repo, set.data.Project)
			})
		}
	}
}

// requireNoFinding runs the practices check on the repository's committed base branch, with real adapters, no
// forge binding, and the default merge authority, and fails for any practice that is not met or not applicable.
func requireNoFinding(t *testing.T, repo *gittest.Repo, name string) {
	t.Helper()
	var forge fake.Practices
	result, err := practicerun.Run(t.Context(), practicerun.Sources{
		Snapshots: practicegit.Snapshots{Env: repo.Env()},
		Secrets:   practicegit.SecretScanner{Env: repo.Env()},
		Forge:     &forge,
		Clock:     fake.NewClock(time.Date(2026, 11, 18, 9, 30, 0, 0, time.UTC)),
	}, practicerun.Request{Repository: name, Dir: repo.Dir, BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range result.Findings {
		t.Errorf("finding %s: %s", f.ID, f.Detail)
	}
	for _, p := range result.Practices {
		if p.Status != practices.StatusMet && p.Status != practices.StatusNotApplicable {
			t.Errorf("practice %s is %s %q", p.ID, p.Status, p.Reason)
		}
	}
	if len(result.Practices) != len(practices.Table()) {
		t.Errorf("the result holds %d practices, want %d", len(result.Practices), len(practices.Table()))
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
