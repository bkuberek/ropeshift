package entry

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
	"ropeshift.com/ropeshift/templates"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// The worker writes the files a restructuring adds and shows the starting instructions with the renderer the entry
// package hands it, which is the one `ropeshift new` writes with, because the worker never imports the templates.
func TestTheLaneRuntimeCarriesTheRendererForTheFilesAnAdoptionWrites(t *testing.T) {
	root := apitest.ShortDir(t)
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "worktrees")}
	runtime, _, err := soloLanes(paths, soloOptions{}, soloCheckouts(paths), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Render == nil {
		t.Fatal("the lane runtime has no renderer for the files an adoption writes")
	}
	data := projectfiles.Data{Project: "orchid", Repositories: []projectfiles.CodeRepository{{Name: "orchid-api", Remote: "https://example.invalid/orchid/orchid-api.git"}}}
	for _, name := range []string{"workspace/agents.md", "monorepo/agents.md", "claude.md", "workspace/makefile", "workspace/gitignore"} {
		got, err := runtime.Render(name, data)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		want, err := templates.Render(name, templates.Data{Project: "orchid", Repositories: []templates.Repository{{Name: "orchid-api", Remote: "https://example.invalid/orchid/orchid-api.git"}}})
		if err != nil || got != want {
			t.Errorf("%s renders %q, want what the templates render with starting data: %v", name, got, err)
		}
	}
}

// The files a restructuring writes are the templates `ropeshift new` writes, over the starting data of the project and the
// repositories found: AGENTS.md and the Makefile name each repository, CLAUDE.md imports AGENTS.md, and the ignore
// file leaves out the folder of clones and the folder a lane's worktree goes in.
func TestTheFilesAnAdoptionWritesAreTheTemplatesOverTheProjectAndItsRepositories(t *testing.T) {
	kept, layouts := fake.NewCheckouts(), fake.NewLayouts()
	place := func(at string, tracked ...string) {
		kept.Place(ports.Clone{Path: at, ID: "root-" + path.Base(at), Remote: "git@example.invalid:orchid/" + path.Base(at) + ".git", DefaultBranch: "main"})
		layouts.Set(at, fake.CloneLayout{Tracked: tracked})
	}
	place("/work/ws", "README.md")
	place("/work/orchid-api", "go.mod")
	known := func(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
		var out []planning.PlannedRepository
		for _, one := range clones {
			out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
		}
		return out, nil
	}
	plan, err := adoption.Plan(t.Context(), adoption.Request{Root: "/work/ws", Paths: []string{"/work/orchid-api"}, Planning: "ws"}, kept, layouts, known)
	if err != nil {
		t.Fatal(err)
	}
	files, err := adoption.Files(t.Context(), plan, "orchid", kept, renderProjectTemplate)
	if err != nil {
		t.Fatal(err)
	}
	text := map[string]string{}
	for _, file := range files {
		text[file.Path] = file.Text
	}
	for file, wants := range map[string][]string{
		"AGENTS.md":            {"# AGENTS.md", "the orchid project", "| `orchid-api` |"},
		"CLAUDE.md":            {"@AGENTS.md"},
		"Makefile":             {"PROJECT := orchid", "orchid-api=git@example.invalid:orchid/orchid-api.git", "setup:", "fetch:", "status:"},
		".gitignore":           {"/repos/\n", "/.ropeshift/worktrees/\n"},
		"plan/repositories.md": {`name = "orchid-api"`, `path = "repos/orchid-api"`},
	} {
		for _, want := range wants {
			if !strings.Contains(text[file], want) {
				t.Errorf("%s holds no %q:\n%s", file, want, text[file])
			}
		}
	}
}

// The Makefile an adoption writes does what it says under make and git: setup clones each repository that has no
// clone, and fetch and status read each clone, for a remote that is a local path as for a URL.
func TestTheMakefileAnAdoptionWritesSetsUpFetchesAndShowsTheStatusOfEachRepository(t *testing.T) {
	gittest.Require(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	remote := gittest.New(t)
	remote.Commit("README.md", "api\n")

	kept, layouts := fake.NewCheckouts(), fake.NewLayouts()
	kept.Place(ports.Clone{Path: "/work/ws", ID: "root-ws", DefaultBranch: "main"})
	layouts.Set("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
	kept.Place(ports.Clone{Path: "/work/api", ID: "root-api", Remote: remote.Dir, DefaultBranch: "main"})
	layouts.Set("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	known := func(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
		var out []planning.PlannedRepository
		for _, one := range clones {
			out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
		}
		return out, nil
	}
	plan, err := adoption.Plan(t.Context(), adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Planning: "ws"}, kept, layouts, known)
	if err != nil {
		t.Fatal(err)
	}
	files, err := adoption.Files(t.Context(), plan, "shop", kept, renderProjectTemplate)
	if err != nil {
		t.Fatal(err)
	}
	workspace := gittest.New(t)
	for _, file := range files {
		if file.Path == "Makefile" {
			if err := os.WriteFile(filepath.Join(workspace.Dir, "Makefile"), []byte(file.Text), 0o600); err != nil {
				t.Fatal(err)
			}
		}
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
	if _, err := os.Stat(filepath.Join(workspace.Dir, "repos", "api", "README.md")); err != nil {
		t.Errorf("make setup cloned nothing at repos/api: %v", err)
	}
	if again := run("setup"); !strings.Contains(again, "api: present") {
		t.Errorf("a second make setup does not leave the clone alone:\n%s", again)
	}
	if fetch := run("fetch"); !strings.Contains(fetch, "api: fetched") {
		t.Errorf("make fetch printed no api: fetched:\n%s", fetch)
	}
	if status := run("status"); !strings.Contains(status, "api: ## ") {
		t.Errorf("make status printed no api: ## :\n%s", status)
	}
}
