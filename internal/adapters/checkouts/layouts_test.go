package checkouts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

const (
	goalFile = "+++\nkind = \"goal\"\nid = \"g\"\ntitle = \"A goal\"\nsuccess_measures = [\"one\"]\n+++\n\nWhy.\n"
	// manifestFile lists one repository.
	manifestFile = "+++\nkind = \"repositories\"\nid = \"platform\"\n\n[[repository]]\nname = \"api\"\npath = \"repos/api\"\n+++\n"
)

func layoutsOf(repo *gittest.Repo) *File {
	return &File{Path: filepath.Join(repo.Home, FileName), Env: repo.Env()}
}

// A clone's ignored directories are the ones its rules leave out of the working tree, found with git itself, and
// a directory inside an ignored one is not listed.
func TestIgnoredListsTheDirectoriesACloneIgnores(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit(".gitignore", "repos/\nnode_modules/\n*.log\n.env\n")
	repo.Commit("README.md", "a workspace\n")
	for _, dir := range []string{"repos/api/src", "node_modules/left-pad", "docs", "build"} {
		if err := os.MkdirAll(filepath.Join(repo.Dir, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"debug.log", ".env", "docs/a.md", "build/out.bin"} {
		if err := os.WriteFile(filepath.Join(repo.Dir, file), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := layoutsOf(repo).Ignored(t.Context(), repo.Dir)
	if want := []string{"node_modules", "repos"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ignored directories read %v, want %v: %v", got, want, err)
	}
}

// A clone holds plan content when a committed file reads with a kind the format knows. A document with no front
// matter, or with front matter of another tool, is not plan content, and an uncommitted file is not read.
func TestPlanReadsTheCommittedFilesOfTheDefaultBranch(t *testing.T) {
	plain := gittest.New(t)
	plain.Commit("README.md", "# A design\n")
	plain.Commit("docs/other.md", "+++\ntitle = \"someone else's\"\n+++\n\nText.\n")
	if got, err := layoutsOf(plain).Plan(t.Context(), plain.Dir); err != nil || got.Held() || got.Manifest {
		t.Errorf("a repository of plain documents reads %+v: %v", got, err)
	}

	planned := gittest.New(t)
	planned.Commit("README.md", "# A design\n")
	planned.Commit("plan/goal.md", goalFile)
	if got, err := layoutsOf(planned).Plan(t.Context(), planned.Dir); err != nil || got.Files != 1 || got.Manifest {
		t.Errorf("a repository with a goal reads %+v: %v", got, err)
	}
	planned.Commit("plan/repositories.md", manifestFile)
	if got, err := layoutsOf(planned).Plan(t.Context(), planned.Dir); err != nil || got.Files != 2 || !got.Manifest {
		t.Errorf("a repository with a manifest reads %+v: %v", got, err)
	}

	uncommitted := gittest.New(t)
	uncommitted.Commit("README.md", "# A design\n")
	if err := os.WriteFile(filepath.Join(uncommitted.Dir, "goal.md"), []byte(goalFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := layoutsOf(uncommitted).Plan(t.Context(), uncommitted.Dir); err != nil || got.Held() {
		t.Errorf("an uncommitted goal reads %+v: %v", got, err)
	}
}

// The tip, the tracked paths, the Markdown count, the code trees, and the instruction file are all read from
// the default branch's committed tree.
func TestACloneIsReadAtItsDefaultBranchTip(t *testing.T) {
	repo := gittest.New(t)
	repo.Commit("README.md", "a\n")
	repo.Commit("docs/design.md", "b\n")
	repo.Commit("services/api/go.mod", "module api\n")
	repo.Commit("web/package.json", "{}\n")
	tip := repo.Commit("AGENTS.md", "rules\n")
	if err := os.WriteFile(filepath.Join(repo.Dir, "CLAUDE.md"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine, ctx := layoutsOf(repo), t.Context()

	if got, err := machine.Tip(ctx, repo.Dir); err != nil || got != tip {
		t.Errorf("tip reads %q, want %q: %v", got, tip, err)
	}
	tracked, err := machine.Tracked(ctx, repo.Dir, tip)
	if want := []string{"AGENTS.md", "README.md", "docs/design.md", "services/api/go.mod", "web/package.json"}; err != nil || !reflect.DeepEqual(tracked, want) {
		t.Errorf("tracked paths read %v, want %v: %v", tracked, want, err)
	}
	if got, err := machine.Documents(ctx, repo.Dir); err != nil || got != 3 {
		t.Errorf("documents count %d, want 3 (README.md, docs/design.md, AGENTS.md): %v", got, err)
	}
	wantTrees := []ports.CodeTree{{Path: "services/api", BuildFile: "services/api/go.mod"}, {Path: "web", BuildFile: "web/package.json"}}
	if got, err := machine.CodeTrees(ctx, repo.Dir); err != nil || !reflect.DeepEqual(got, wantTrees) {
		t.Errorf("code trees read %+v, want %+v: %v", got, wantTrees, err)
	}
	if got, err := machine.Instructions(ctx, repo.Dir); err != nil || !got {
		t.Errorf("instructions read %v: %v", got, err)
	}

	bare := gittest.New(t)
	bare.Commit("README.md", "a\n")
	if got, err := layoutsOf(bare).Instructions(ctx, bare.Dir); err != nil || got {
		t.Errorf("a clone with no instruction file reads %v: %v", got, err)
	}
	if _, err := machine.Tracked(ctx, repo.Dir, "0000000000000000000000000000000000000000"); err == nil {
		t.Error("a commit the clone does not hold listed its paths")
	}
}
