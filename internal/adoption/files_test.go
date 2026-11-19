package adoption_test

import (
	"context"
	"fmt"
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
	"ropeshift.com/ropeshift/internal/projectfiles"
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

// render stands in for the templates, which the shared logic never imports: it says which template was asked for, the
// project, and the repositories it was given, so a test reads what the files were written over.
func render(name string, data projectfiles.Data) (string, error) {
	var listed []string
	for _, repository := range data.Repositories {
		entry := repository.Name + "=" + repository.Remote
		if repository.Path != "" {
			entry += "=" + repository.Path
		}
		listed = append(listed, entry)
	}
	return fmt.Sprintf("%s for %s [%s]\n", name, data.Project, strings.Join(listed, " ")), nil
}

func filesByPath(files []ports.TreeFile) map[string]ports.TreeFile {
	out := map[string]ports.TreeFile{}
	for _, file := range files {
		out[file.Path] = file
	}
	return out
}

// A workspace with clones beside it gets the files in the order the plan lists them: the manifest of the clones as
// they sit once the moves are made, the ignore lines, the Makefile over the same list, and the instruction files,
// each written from the template the plan names.
func TestTheFilesOfAWorkspaceWithClonesBesideItAreTheManifestTheIgnoreLinesTheMakefileAndTheInstructions(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", ".gitignore"}})
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api", "/work/web"}, Planning: "ws"}, registered)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	if want := []string{"plan/repositories.md", ".gitignore", "Makefile", "AGENTS.md", "CLAUDE.md"}; !reflect.DeepEqual(paths, want) {
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
	const repositories = "api=git@example.invalid:acme/api.git web=git@example.invalid:acme/web.git"
	for i, want := range map[int]string{
		2: "workspace/makefile for shop [" + repositories + "]\n",
		3: "workspace/agents.md for shop [" + repositories + "]\n",
		4: "claude.md for shop [" + repositories + "]\n",
	} {
		if files[i].Text != want {
			t.Errorf("%s reads %q, want %q", files[i].Path, files[i].Text, want)
		}
	}
	if strings.Contains(files[0].Text, `name = "ws"`) {
		t.Errorf("the manifest lists the planning repository:\n%s", files[0].Text)
	}
}

// A file the plan does not list is not written: a Makefile, an ignore file, and instruction files the clone commits
// stay as they are, and the manifest and the ignore lines are all that is added.
func TestFilesTheCloneCommitsAreNotInTheFiles(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", "Makefile", "CLAUDE.md"}})
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Planning: "ws"}, registered)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	if want := []string{"plan/repositories.md", ".gitignore"}; !slices.Equal(paths, want) {
		t.Errorf("a workspace with a Makefile and an instruction file gets the files %v, want %v", paths, want)
	}
}

// A clone that tracks a Makefile, an ignore file, or an instruction file under another case holds it: no second file
// is written beside it, and the ignore lines are appended to the file under the spelling the clone tracks.
func TestFilesUnderAnotherCaseAreHeldAndNotWrittenBesideThemselves(t *testing.T) {
	t.Parallel()
	for name, tracked := range map[string][]string{
		"lowercase":   {"README.md", "makefile", ".gitignore", "agents.md"},
		"uppercase":   {"README.md", "MAKEFILE", ".GITIGNORE", "AGENTS.MD"},
		"the GNU one": {"README.md", "GNUmakefile", ".gitignore", "claude.md"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := newMachine(t)
			m.clone("/work/ws", fake.CloneLayout{Tracked: tracked})
			m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
			plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Planning: "ws"}, registered)
			files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render)
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, file := range files {
				paths = append(paths, file.Path)
			}
			ignore := tracked[2]
			if want := []string{"plan/repositories.md", ignore}; !slices.Equal(paths, want) {
				t.Errorf("a workspace that tracks %v gets the files %v, want %v", tracked[1:], paths, want)
			}
		})
	}
}

// A clone with no ignore file gets one from the template whole, with no lines to append to it, and the plan of a
// single repository writes its instruction files from the monorepo template, which names no code repository.
func TestAnIgnoreFileTheCloneLacksIsWrittenWholeAndASingleRepositoryGetsTheMonorepoInstructions(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Planning: "ws"}, registered)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render)
	if err != nil {
		t.Fatal(err)
	}
	ignore := filesByPath(files)[".gitignore"]
	if want := "workspace/gitignore for shop [api=git@example.invalid:acme/api.git]\n"; ignore.Text != want || len(ignore.Lines) != 0 {
		t.Errorf("the ignore file is %+v, want the text %q", ignore, want)
	}

	alone := newMachine(t)
	alone.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md"}})
	single := alone.plan(adoption.Request{Root: "/work/ws"}, registered)
	got, err := adoption.Files(t.Context(), single, "shop", alone.checkouts, render)
	if err != nil {
		t.Fatal(err)
	}
	want := []ports.TreeFile{{Path: "AGENTS.md", Text: "monorepo/agents.md for shop []\n"}, {Path: "CLAUDE.md", Text: "claude.md for shop []\n"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("a single repository gets %+v, want %+v", got, want)
	}
}

// The Makefile lists each repository whose name and path it can carry, at any depth: a name of letters, digits, and
// . _ - only, and a path of such segments, and each one it leaves out is in the plan with the reason. The remote is
// kept when a Makefile can carry it, a local path as a file URL, and left empty otherwise. The manifest lists every
// repository, as it always did.
func TestTheMakefileListsEachRepositoryItCanCarryAtAnyDepthAndThePlanNamesTheRestWithWhy(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md", ".gitignore", "AGENTS.md"}})
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/group/deep", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/with space", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/a$b", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/ws/repos/we ird/tool", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.checkouts.Place(ports.Clone{Path: "/work/ws/repos/odd", ID: "root-odd", Remote: "https://example.invalid/$(touch x)#.git", DefaultBranch: "main"})
	m.layouts.Set("/work/ws/repos/odd", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.checkouts.Place(ports.Clone{Path: "/work/ws/repos/local", ID: "root-local", Remote: "/srv/git/local", DefaultBranch: "main"})
	m.layouts.Set("/work/ws/repos/local", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.checkouts.Place(ports.Clone{Path: "/work/ws/repos/spaced", ID: "root-spaced", Remote: "/srv/git/with space", DefaultBranch: "main"})
	m.layouts.Set("/work/ws/repos/spaced", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.plan(adoption.Request{Root: "/work/ws", Depth: 2}, registered)
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render)
	if err != nil {
		t.Fatal(err)
	}
	const want = "workspace/makefile for shop [api=git@example.invalid:acme/api.git deep=git@example.invalid:acme/deep.git=repos/group/deep " +
		"local=file:///srv/git/local odd= spaced=]\n"
	if got := filesByPath(files)["Makefile"].Text; got != want {
		t.Errorf("the Makefile reads %q, want %q", got, want)
	}
	type left struct{ name, path, reason string }
	var got []left
	for _, one := range plan.MakefileLeftOut {
		got = append(got, left{one.Name, one.Path, one.Reason})
	}
	wantLeft := []left{
		{"a$b", "repos/a$b", "its name"}, {"tool", "repos/we ird/tool", "its path"}, {"with space", "repos/with space", "its name"},
	}
	if len(got) != len(wantLeft) {
		t.Fatalf("the plan leaves out %+v, want %d repositories", got, len(wantLeft))
	}
	for i, one := range wantLeft {
		if got[i].name != one.name || got[i].path != one.path || !strings.HasPrefix(got[i].reason, one.reason) {
			t.Errorf("the plan leaves out %+v, want %+v with a reason that starts %q", got[i], one.name+" at "+one.path, one.reason)
		}
	}
	manifest := filesByPath(files)["plan/repositories.md"].Text
	for _, want := range []string{`path = "repos/group/deep"`, `path = "repos/with space"`, `path = "repos/we ird/tool"`, `name = "a$b"`, `name = "odd"`} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the manifest holds no %q:\n%s", want, manifest)
		}
	}
}

// A plan lists nothing left out of a Makefile the repository holds, and nothing when every repository is carried.
func TestThePlanLeavesNothingOutOfAMakefileItDoesNotWrite(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md", ".gitignore", "AGENTS.md", "Makefile"}})
	m.clone("/work/ws/repos/with space", fake.CloneLayout{Tracked: []string{"go.mod"}})
	if plan := m.plan(adoption.Request{Root: "/work/ws"}, registered); len(plan.MakefileLeftOut) != 0 {
		t.Errorf("a plan that writes no Makefile leaves out %+v", plan.MakefileLeftOut)
	}
	other := newMachine(t)
	other.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md", ".gitignore", "AGENTS.md"}})
	other.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	if plan := other.plan(adoption.Request{Root: "/work/ws"}, registered); len(plan.MakefileLeftOut) != 0 {
		t.Errorf("a plan whose repositories are all carried leaves out %+v", plan.MakefileLeftOut)
	}
}

// A plan that names a template no renderer was given, or one the renderer refuses, writes nothing.
func TestAFileWhoseTemplateCannotBeRenderedRefusesTheWholePlan(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	plan := planning.AdoptionPlan{Files: []planning.PlannedFile{{Path: "AGENTS.md", Action: planning.ActionAdd, Template: "workspace/agents.md"}}}
	if _, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, nil); err == nil || !strings.Contains(err.Error(), "workspace/agents.md") {
		t.Errorf("a plan with no renderer: %v", err)
	}
	refuse := func(name string, _ projectfiles.Data) (string, error) { return "", fmt.Errorf("no template %s", name) }
	if _, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, refuse); err == nil || !strings.Contains(err.Error(), "no template workspace/agents.md") {
		t.Errorf("a renderer that refuses: %v", err)
	}
}

// The ignore file a restructuring appends to is the one named exactly .gitignore when the clone tracks it beside a
// case variant, whichever comes first, and a case variant only when the exact name is not tracked.
func TestTheIgnoreFileAppendedToIsTheExactNameWhenACaseVariantIsTrackedToo(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tracked []string
		want    string
	}{
		{"the variant first", []string{"README.md", ".GITIGNORE", ".gitignore"}, ".gitignore"},
		{"the exact name first", []string{"README.md", ".gitignore", ".GITIGNORE"}, ".gitignore"},
		{"only a variant", []string{"README.md", ".GitIgnore"}, ".GitIgnore"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := newMachine(t)
			m.clone("/work/ws", fake.CloneLayout{Tracked: tc.tracked})
			m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
			plan := m.plan(adoption.Request{Root: "/work/ws", Paths: []string{"/work/api"}, Planning: "ws"}, registered)
			var appended []string
			for _, listed := range plan.Files {
				if listed.Action == planning.ActionAppend {
					appended = append(appended, listed.Path)
				}
			}
			if !slices.Equal(appended, []string{tc.want}) {
				t.Errorf("the plan appends to %v, want %q alone", appended, tc.want)
			}
		})
	}
}

// A clone inside the planning clone is listed where it is, a clone the project refused is not listed, and a
// manifest the clone commits is left alone.
func TestACloneInsideThePlanningCloneIsListedWhereItIsAndARefusedOneNotAtAll(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md", "Makefile", "AGENTS.md", ".gitignore"}})
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
	files, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render)
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
	held.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 2, Manifest: true}, Tracked: []string{"README.md", "plan/repositories.md", "Makefile", ".gitignore", "AGENTS.md"}})
	held.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	held.checkouts.Ignore("/work/ws", planning.WorktreesFolder)
	planned := held.plan(adoption.Request{Root: "/work/ws"}, registered)
	if files, err := adoption.Files(t.Context(), planned, "shop", held.checkouts, render); err != nil || len(files) != 0 {
		t.Errorf("a clone that holds a manifest and ignores both folders gets the files %+v: %v", files, err)
	}
}

// A plan that lists a file no worker writes is refused, rather than half written.
func TestAFileNoWorkerWritesRefusesTheWholePlan(t *testing.T) {
	t.Parallel()
	m := newMachine(t)
	plan := planning.AdoptionPlan{Files: []planning.PlannedFile{{Path: "LICENSE", Action: planning.ActionAdd}}}
	if _, err := adoption.Files(t.Context(), plan, "shop", m.checkouts, render); err == nil || !strings.Contains(err.Error(), "LICENSE") {
		t.Errorf("a plan that adds a LICENSE: %v", err)
	}
}
