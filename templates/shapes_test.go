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
