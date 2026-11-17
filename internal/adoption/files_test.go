package adoption_test

import (
	"context"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adoption"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// machine is a worker's machine with the clones a test lays out on it.
type machine struct {
	t         *testing.T
	checkouts *fake.Checkouts
	layouts   *fake.Layouts
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	return &machine{t: t, checkouts: fake.NewCheckouts(), layouts: fake.NewLayouts()}
}

func (m *machine) clone(at string, layout fake.CloneLayout) {
	m.t.Helper()
	name := path.Base(at)
	m.checkouts.Place(ports.Clone{Path: at, ID: "root-" + name, Remote: "git@example.invalid:acme/" + name + ".git", DefaultBranch: "main"})
	m.layouts.Set(at, layout)
}

// registered says every clone of the project is registered, as the worker knows the project's records.
func registered(_ context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
	var out []planning.PlannedRepository
	for _, one := range clones {
		out = append(out, planning.PlannedRepository{Name: one.Name, Path: one.Path, ID: one.Identity, Outcome: string(ledger.AlreadyRegistered)})
	}
	return out, nil
}

func (m *machine) plan(request adoption.Request, register adoption.Register) planning.AdoptionPlan {
	m.t.Helper()
	plan, err := adoption.Plan(m.t.Context(), request, m.checkouts, m.layouts, register)
	if err != nil {
		m.t.Fatalf("plan: %v", err)
	}
	return plan
}

func filesByPath(files []ports.TreeFile) map[string]ports.TreeFile {
	out := map[string]ports.TreeFile{}
	for _, file := range files {
		out[file.Path] = file
	}
	return out
}

// A workspace with clones beside it gets the three files in the order the plan lists them: the manifest of the
// clones as they sit once the moves are made, the ignore lines, and the Makefile over the same list.
func TestTheFilesOfAWorkspaceWithClonesBesideItAreTheManifestTheIgnoreLinesAndTheMakefile(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api", "/work/web"}, Planning: "ws"}, registered)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	if want := []string{"plan/repositories.md", ".gitignore", "Makefile"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("the files are %v, want %v", paths, want)
	}
	listed := []planning.WorkspaceRepository{
		{Name: "api", Remote: "git@example.invalid:acme/api.git", Path: "repos/api"},
		{Name: "web", Remote: "git@example.invalid:acme/web.git", Path: "repos/web"},
	}
	if want := planning.RenderRepositoriesFile("shop", listed); files[0].Text != want {
		t.Errorf("the manifest reads:\n%s\nwant:\n%s", files[0].Text, want)
	}
	if want := []string{planning.CodeFolderLine, planning.WorktreesLine}; !slices.Equal(files[1].Lines, want) || files[1].Text != "" {
		t.Errorf("the ignore file gains %+v, want the lines %v", files[1], want)
	}
	if want := planning.RenderWorkspaceMakefile(listed); files[2].Text != want {
		t.Errorf("the Makefile reads:\n%s\nwant:\n%s", files[2].Text, want)
	}
	if strings.Contains(files[0].Text, `name = "ws"`) {
		t.Errorf("the manifest lists the planning repository:\n%s", files[0].Text)
	}
}

// A file the plan does not list is not written: a Makefile the clone commits stays as it is.
func TestAMakefileTheCloneCommitsIsNotInTheFiles(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", "Makefile"}})
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Planning: "ws"}, registered)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts)
	if err != nil {
		t.Fatal(err)
	}
	if _, has := filesByPath(files)["Makefile"]; has || len(files) != 2 {
		t.Errorf("a workspace with a Makefile gets the files %+v", files)
	}
}

// A clone inside the planning clone is listed where it is, a clone the project refused is not listed, and a
// manifest the clone commits is left alone.
func TestACloneInsideThePlanningCloneIsListedWhereItIsAndARefusedOneNotAtAll(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md", "Makefile"}})
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/group/deep", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/held", fake.CloneLayout{Tracked: []string{"go.mod"}})
	register := func(ctx context.Context, clones []adoption.Found) ([]planning.PlannedRepository, error) {
		out, err := registered(ctx, clones)
		for i := range out {
			if out[i].Name == "held" {
				out[i].Outcome = string(ledger.RegistrationRefused)
			}
		}
		return out, err
	}
	plan := m.plan(adoption.Request{Root: "/work/ws", Depth: 2}, register)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filesByPath(files)["plan/repositories.md"].Text
	for _, want := range []string{`path = "repos/api"`, `path = "repos/group/deep"`} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the manifest holds no %q:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "held") {
		t.Errorf("the manifest lists a repository the project refused:\n%s", manifest)
	}
	if lines := filesByPath(files)[".gitignore"].Lines; !slices.Equal(lines, []string{planning.WorktreesLine}) {
		t.Errorf("the ignore file gains %v, and the folder of clones is ignored already", lines)
	}

	held := newMachine(t)
	held.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 2, Manifest: true}, Tracked: []string{"README.md", "plan/repositories.md"}})
	held.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	held.checkouts.Ignore("/work/ws", planning.WorktreesFolder)
	planned := held.plan(adoption.Request{Root: "/work/ws"}, registered)
	if files, err := adoption.Files(t.Context(), planned, "shop", held.checkouts); err != nil || len(files) != 0 {
		t.Errorf("a clone that holds a manifest and ignores both folders gets the files %+v: %v", files, err)
	}
}

// A plan that lists a file no worker writes is refused, rather than half written.
func TestAFileNoWorkerWritesRefusesTheWholePlan(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	plan := planning.AdoptionPlan{Files: []planning.PlannedFile{{Path: "LICENSE", Action: planning.ActionAdd}}}
	if _, err := adoption.Files(t.Context(), plan, "shop", m.checkouts); err == nil || !strings.Contains(err.Error(), "LICENSE") {
		t.Errorf("a plan that adds a LICENSE: %v", err)
	}
}
