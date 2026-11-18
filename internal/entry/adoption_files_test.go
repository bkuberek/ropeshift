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
// clone, and fetch and status read each clone, for a remote that is a local path as for a URL, and for a clone the
// layout already holds at a nested path of the planning repository as for one moved to repos/<name>.
func TestTheMakefileAnAdoptionWritesSetsUpFetchesAndShowsTheStatusOfEachRepository(t *testing.T) {
	gittest.Require(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not on PATH")
	}
	remote := gittest.New(t)
	remote.Commit("README.md", "api\n")

	kept, layouts := fake.NewCheckouts(), fake.NewLayouts()
	kept.Place(ports.Clone{Path: "/work/ws", ID: "root-ws", DefaultBranch: "main"})
	layouts.Set("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md"}})
	kept.Place(ports.Clone{Path: "/work/api", ID: "root-api", Remote: remote.Dir, DefaultBranch: "main"})
	layouts.Set("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	kept.Place(ports.Clone{Path: "/work/ws/repos/group/deep", ID: "root-deep", Remote: remote.Dir, DefaultBranch: "main"})
	layouts.Set("/work/ws/repos/group/deep", fake.CloneLayout{Tracked: []string{"go.mod"}})
	known := func(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
		var out []planning.PlannedRepository
		for _, one := range clones {
			out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
		}
		return out, nil
	}
	plan, err := adoption.Plan(t.Context(), adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Depth: 2, Planning: "ws"}, kept, layouts, known)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.MakefileLeftOut) != 0 {
		t.Fatalf("the plan leaves %+v out of the Makefile", plan.MakefileLeftOut)
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
	withFake, record := fakeRopeshift(t)
	run := func(target string) string {
		t.Helper()
		cmd := exec.Command("make", "--no-print-directory", "-C", workspace.Dir, target)
		cmd.Env = append(remote.Env(), "PATH="+withFake)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make %s: %v\n%s", target, err, out)
		}
		return string(out)
	}
	run("setup")
	for _, clone := range []string{"repos/api", "repos/group/deep"} {
		if _, err := os.Stat(filepath.Join(workspace.Dir, filepath.FromSlash(clone), "README.md")); err != nil {
			t.Errorf("make setup cloned nothing at %s: %v", clone, err)
		}
	}
	if again := run("setup"); !strings.Contains(again, "api: present") || !strings.Contains(again, "deep: present") {
		t.Errorf("a second make setup does not leave the clones alone:\n%s", again)
	}
	if fetch := run("fetch"); !strings.Contains(fetch, "api: fetched") || !strings.Contains(fetch, "deep: fetched") {
		t.Errorf("make fetch printed no api: fetched and deep: fetched:\n%s", fetch)
	}
	// make register names each clone the Makefile lists by its own path, the nested one included, and no folder to search.
	run("register")
	calls, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if want := "repository\nadd\n.\nrepos/api\nrepos/group/deep\n--project\nshop\n--\n"; !strings.HasPrefix(string(calls), want) {
		t.Errorf("make register ran %q, want it to start with %q", calls, want)
	}
	if status := run("status"); !strings.Contains(status, "api: ## ") || !strings.Contains(status, "deep: ## ") || strings.Contains(status, "missing") {
		t.Errorf("make status printed no api: ## and deep: ## , or reads a clone as missing:\n%s", status)
	}
}

// fakeRopeshift puts a ropeshift on the PATH of a test that records its arguments, one per line and each command ended
// by a line of two dashes, so a Makefile's register target runs against no daemon and never reaches an installed
// binary. It returns the PATH to run make with and the file the commands are recorded in.
func fakeRopeshift(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nfor argument in \"$@\"; do printf '%s\\n' \"$argument\"; done >> " + record + "\nprintf -- '--\\n' >> " + record + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ropeshift"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH"), record
}
