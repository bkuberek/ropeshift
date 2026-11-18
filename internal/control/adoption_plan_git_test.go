package control_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/checkouts"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// A layout is a folder of real git clones, each made from a repository of its own, so the detection reads what
// git says of them.
type layout struct {
	t      *testing.T
	base   string
	env    []string
	ledger *testLedger
}

func newLayout(t *testing.T) *layout {
	t.Helper()
	return newLayoutOn(t, &testLedger{t: t})
}

func newLayoutOn(t *testing.T, ledger *testLedger) *layout {
	t.Helper()
	gittest.Require(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &layout{t: t, base: base, ledger: ledger}
}

// fresh is a second layout for the same test, in a folder of its own and with the ledger of this one.
func (l *layout) fresh() *layout {
	l.t.Helper()
	return newLayoutOn(l.t, l.ledger)
}

// clone makes a clone at a path below the layout, holding the files, and returns where it is. Its origin holds
// the files in one commit, because the detection reads the files git lists and not how they came to be.
func (l *layout) clone(rel string, files map[string]string) string {
	l.t.Helper()
	origin := gittest.NewIn(l.t, filepath.Join(l.base, ".origins", strings.ReplaceAll(rel, "/", "-")))
	if l.env == nil {
		l.env = origin.Env()
	}
	names := slices.Sorted(maps.Keys(files))
	for _, name := range names {
		file := filepath.Join(origin.Dir, name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			l.t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(files[name]), 0o600); err != nil {
			l.t.Fatal(err)
		}
	}
	if len(names) > 0 {
		origin.Git(append([]string{"add", "--"}, names...)...)
		origin.Git("commit", "--quiet", "-m", "files of "+rel)
	}
	dest := filepath.Join(l.base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		l.t.Fatal(err)
	}
	origin.Git("clone", "--quiet", origin.Dir, dest)
	return dest
}

func (l *layout) path(rel string) string { return filepath.Join(l.base, filepath.FromSlash(rel)) }

// plan reads the layout with the real adapter, against an empty project.
func (l *layout) plan(in control.AdoptionRequest) (planning.AdoptionPlan, error) {
	l.t.Helper()
	machine := &checkouts.File{Path: filepath.Join(l.t.TempDir(), checkouts.FileName), Env: l.env}
	return control.NewAdoptions(control.NewRepositories(l.ledger.get())).Plan(l.t.Context(), "acme", in, machine, machine, projectMember)
}

func (l *layout) mustPlan(in control.AdoptionRequest) planning.AdoptionPlan {
	l.t.Helper()
	plan, err := l.plan(in)
	if err != nil {
		l.t.Fatalf("plan: %v", err)
	}
	return plan
}

const (
	goalDocument     = "+++\nkind = \"goal\"\nid = \"g\"\ntitle = \"A goal\"\nsuccess_measures = [\"one\"]\n+++\n\nWhy.\n"
	manifestDocument = "+++\nkind = \"repositories\"\nid = \"platform\"\n\n[[repository]]\nname = \"api\"\npath = \"repos/api\"\n+++\n"
)

// workspaceFiles are what a workspace commits: a README and an ignore file for the folder of clones and the
// folder a lane's worktree goes in.
func workspaceFiles(extra map[string]string) map[string]string {
	files := map[string]string{"README.md": "a workspace\n", ".gitignore": "repos/\n/.ropeshift/worktrees/\n"}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

// A clone with nothing else around it reads as the single shape.
func TestGitLayoutOfALoneCloneIsSingle(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(map[string]string{"AGENTS.md": "rules\n"}))
	plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")})
	if plan.Shape != planning.ShapeSingle || plan.Offers() || len(plan.Layout.Clones) != 1 || len(plan.Files) != 0 {
		t.Errorf("a lone clone reads %s with %+v", plan.Shape, plan.Layout.Clones)
	}
}

// A clone that commits a plan file, with a clone in the folder it ignores, reads as planned.
func TestGitLayoutOfAPlannedCloneIsPlanned(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(map[string]string{"plan/goal.md": goalDocument, "AGENTS.md": "rules\n"}))
	l.clone("ws/repos/api", map[string]string{"go.mod": "module api\n"})
	plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")})
	if plan.Shape != planning.ShapePlanned || !plan.Layout.Clones[0].Plan {
		t.Fatalf("a planned clone with a clone below it reads %s with %+v", plan.Shape, plan.Layout.Clones)
	}
	if got := filesOf(plan); !reflect.DeepEqual(got, []string{"add plan/repositories.md", "add Makefile"}) {
		t.Errorf("the planned shape adds %v", got)
	}
	if want := []string{l.path("ws/repos")}; !reflect.DeepEqual(plan.Layout.Folders, want) {
		t.Errorf("the folders read %v, want %v", plan.Layout.Folders, want)
	}

	// A manifest the clone commits leaves nothing to add.
	l = l.fresh()
	l.clone("ws", workspaceFiles(map[string]string{"plan/goal.md": goalDocument, "plan/repositories.md": manifestDocument, "Makefile": "all:\n", "AGENTS.md": "rules\n"}))
	l.clone("ws/repos/api", map[string]string{"go.mod": "module api\n"})
	if again := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")}); again.Shape != planning.ShapePlanned || len(again.Files) != 0 {
		t.Errorf("a planned clone with a manifest reads %s and adds %v", again.Shape, filesOf(again))
	}
}

// A workspace with no plan content, whose ignored folder holds a design repository and a code repository, reads
// as a consolidation, and lists what it takes, what collides, and the code trees.
func TestGitLayoutOfAWorkspaceWithADesignRepositoryConsolidates(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(nil))
	l.clone("ws/repos/design", map[string]string{
		"README.md": "the design\n", ".gitignore": "scratch/\n", "AGENTS.md": "rules\n", "plan/goal.md": goalDocument,
		"docs/a.md": "a\n", "docs/b.md": "b\n", "tools/go.mod": "module tools\n", "tools/main.go": "package main\n",
	})
	l.clone("ws/repos/api", map[string]string{"go.mod": "module api\n", "main.go": "package main\n"})
	plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")})
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil || plan.Design.Name != "design" || plan.Design.Path != l.path("ws/repos/design") {
		t.Fatalf("the layout reads %s with design %+v", plan.Shape, plan.Design)
	}
	if len(plan.Design.Root) != 40 {
		t.Errorf("the design repository's first-parent root reads %q", plan.Design.Root)
	}
	if want := []string{"AGENTS.md", "docs/a.md", "docs/b.md", "plan/goal.md", "tools/go.mod", "tools/main.go"}; !reflect.DeepEqual(plan.Takes, want) {
		t.Errorf("the plan takes %v, want %v", plan.Takes, want)
	}
	wantCollisions := []planning.Collision{{Path: ".gitignore", Kept: planning.KeptUnion}, {Path: "README.md", Kept: planning.KeptWorkspace}}
	if !reflect.DeepEqual(plan.Collisions, wantCollisions) {
		t.Errorf("the collisions read %+v, want %+v", plan.Collisions, wantCollisions)
	}
	if want := []planning.PlannedCodeTree{{Path: "tools", BuildFile: "tools/go.mod"}}; !reflect.DeepEqual(plan.CodeTrees, want) {
		t.Errorf("the code trees read %+v, want %+v", plan.CodeTrees, want)
	}
	if got := outcomesOf(plan); got[l.path("ws/repos/design")] != planning.OutcomeNotRegistered || got[l.path("ws/repos/api")] != string(ledger.Registered) {
		t.Errorf("the registrations read %v", got)
	}
	if !plan.Layout.Clones[1].Instructions && !plan.Layout.Clones[2].Instructions {
		t.Errorf("no clone reads as holding an instruction file: %+v", plan.Layout.Clones)
	}
}

// With no design repository to import, clones named by paths beside the clone are listed to move into its
// folder of clones, and the plan adds the manifest, the ignore line, and a Makefile.
func TestGitLayoutOfClonesBesideAWorkspaceIsTheWorkspaceFiles(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", map[string]string{"README.md": "a workspace\n"})
	l.clone("api", map[string]string{"go.mod": "module api\n", "README.md": "api\n"})
	l.clone("web", map[string]string{"package.json": "{}\n", "README.md": "web\n"})
	plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws"), Paths: []string{l.path("api"), l.path("web")}})
	if plan.Shape != planning.ShapeWorkspaceFiles || !plan.Planning.Undecided {
		t.Fatalf("clones beside a workspace read %s, planning in %+v", plan.Shape, plan.Planning)
	}
	if named := l.mustPlan(control.AdoptionRequest{Root: l.path("ws"), Paths: []string{l.path("api"), l.path("web")}, Planning: "ws"}); named.Planning.Path != l.path("ws") {
		t.Fatalf("--planning ws reads %+v", named.Planning)
	}
	if want := []string{"add plan/repositories.md", "add .gitignore", "add Makefile", "add AGENTS.md", "add CLAUDE.md"}; !reflect.DeepEqual(filesOf(plan), want) {
		t.Errorf("the plan adds %v, want %v", filesOf(plan), want)
	}
	if len(plan.Moves) != 2 || plan.Moves[0].To != l.path("ws/repos/api") || plan.Moves[0].Commands[0] != "mv "+l.path("api")+" "+l.path("ws/repos/api") {
		t.Errorf("the moves read %+v", plan.Moves)
	}
	if plan.Design != nil || !slices.ContainsFunc(plan.Notes, func(note string) bool { return strings.Contains(note, "--design NAME") }) {
		t.Errorf("a tie between design candidates reads design %+v and notes %v", plan.Design, plan.Notes)
	}
}

// The line that ignores the folder a lane's worktree goes in is listed when git does not ignore that folder, and
// git is asked, so a rule in the clone's own exclude file counts as much as one in its ignore file, and a rule
// that merely mentions the folder does not.
func TestGitLayoutAsksGitWhetherTheWorktreeFolderIsIgnored(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		ignore  string
		exclude string
		want    []string
	}{
		{name: "nothing ignores it", ignore: "repos/\n", want: []string{planning.WorktreesLine}},
		{name: "the ignore file does", ignore: "repos/\n/.ropeshift/worktrees/\n"},
		{name: "a folder above it is ignored", ignore: "repos/\n/.ropeshift/\n"},
		{name: "the exclude file does", ignore: "repos/\n", exclude: "/.ropeshift/worktrees/\n"},
		{name: "a comment names it and nothing ignores it", ignore: "repos/\n# /.ropeshift/worktrees/\n", want: []string{planning.WorktreesLine}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			l := newLayout(t)
			ws := l.clone("ws", workspaceFiles(map[string]string{
				"plan/goal.md": goalDocument, "plan/repositories.md": manifestDocument, ".gitignore": tt.ignore,
			}))
			l.clone("ws/repos/api", map[string]string{"go.mod": "module api\n"})
			if tt.exclude != "" {
				if err := os.WriteFile(filepath.Join(ws, ".git", "info", "exclude"), []byte(tt.exclude), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			plan := l.mustPlan(control.AdoptionRequest{Root: ws})
			if plan.Shape != planning.ShapePlanned {
				t.Fatalf("the layout reads %s", plan.Shape)
			}
			if got := ignoreLinesOf(plan); !slices.Equal(got, tt.want) {
				t.Errorf("the plan appends %v to the ignore file, want %v", got, tt.want)
			}
		})
	}
}

// Clones two levels down are found only at depth two, and a clone beside the one adopt runs in is seen only when
// a path names it.
func TestGitLayoutSearchesToTheDepthAndNoFurther(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(nil))
	l.clone("ws/repos/group/deep", map[string]string{"go.mod": "module deep\n"})
	l.clone("beside", map[string]string{"go.mod": "module beside\n"})
	if plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")}); plan.Shape != planning.ShapeSingle {
		t.Errorf("one level down reads %s with %+v", plan.Shape, plan.Layout.Clones)
	}
	deep := l.mustPlan(control.AdoptionRequest{Root: l.path("ws"), Depth: 2})
	if names := clonesOf(deep); !reflect.DeepEqual(names, []string{"ws", "deep"}) {
		t.Errorf("two levels down finds %v", names)
	}
	named := l.mustPlan(control.AdoptionRequest{Root: l.path("ws"), Depth: 2, Paths: []string{l.path("beside")}})
	if names := clonesOf(named); !slices.Contains(names, "beside") || len(names) != 3 {
		t.Errorf("a clone named by a path is found: %v", names)
	}
}

// Two candidates with the same number of documents name no design repository, and a build file at the top of
// the design repository makes the whole of it one code tree that cannot be left out.
func TestGitLayoutDesignChoicesAndTheRootBuildFile(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(nil))
	l.clone("ws/repos/one", map[string]string{"a.md": "a\n", "b.md": "b\n"})
	l.clone("ws/repos/two", map[string]string{"a.md": "a\n", "b.md": "b\n"})
	if plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")}); plan.Design != nil || plan.Shape != planning.ShapeWorkspaceFiles {
		t.Errorf("a tie reads design %+v and shape %s", plan.Design, plan.Shape)
	}
	if plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws"), Design: "two"}); plan.Design == nil || plan.Design.Name != "two" || plan.Shape != planning.ShapeConsolidate {
		t.Errorf("--design two reads design %+v and shape %s", plan.Design, plan.Shape)
	}

	l = l.fresh()
	l.clone("ws", workspaceFiles(nil))
	l.clone("ws/repos/design", map[string]string{"plan/goal.md": goalDocument, "go.mod": "module design\n", "main.go": "package main\n"})
	plan := l.mustPlan(control.AdoptionRequest{Root: l.path("ws")})
	if want := []planning.PlannedCodeTree{{Path: ".", BuildFile: "go.mod", Whole: true}}; !reflect.DeepEqual(plan.CodeTrees, want) {
		t.Errorf("the code trees read %+v, want %+v", plan.CodeTrees, want)
	}
	if _, err := l.plan(control.AdoptionRequest{Root: l.path("ws"), LeaveOut: []string{"."}}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("leaving out the whole design repository: %v", err)
	}
}

// A plan records nothing: no event, no record, and no checkout file. It is the same for the same layout, and it
// changes when a clone is added.
func TestGitLayoutPlanRecordsNothingAndItsDigestIsStable(t *testing.T) {
	t.Parallel()
	l := newLayout(t)
	l.clone("ws", workspaceFiles(nil))
	l.clone("ws/repos/design", map[string]string{"plan/goal.md": goalDocument, "docs/a.md": "a\n"})
	// The ledger every plan of this test reads, so the events compared are those of the one the plans used.
	store := l.ledger.get()
	checkoutFile := filepath.Join(t.TempDir(), checkouts.FileName)
	machine := &checkouts.File{Path: checkoutFile, Env: l.env}
	adoptions := control.NewAdoptions(control.NewRepositories(store))
	events := func() int {
		var n int
		if err := store.Read(t.Context(), "acme", func(tx ports.ReadTx) error {
			all, err := tx.Events(0, 1000)
			n = len(all)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return n
	}
	request := control.AdoptionRequest{Root: l.path("ws")}
	before := events()
	first, err := adoptions.Plan(t.Context(), "acme", request, machine, machine, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adoptions.Plan(t.Context(), "acme", request, machine, machine, projectMember)
	if err != nil || first.Digest != second.Digest || !reflect.DeepEqual(first, second) {
		t.Fatalf("two plans of one layout differ: %v", err)
	}
	if after := events(); after != before {
		t.Errorf("a plan appended %d events", after-before)
	}
	if _, err := os.Stat(checkoutFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a plan wrote the checkouts file: %v", err)
	}
	l.clone("ws/repos/api", map[string]string{"go.mod": "module api\n"})
	added, err := adoptions.Plan(t.Context(), "acme", request, machine, machine, projectMember)
	if err != nil || added.Digest == first.Digest {
		t.Errorf("a clone added leaves the digest %q: %v", added.Digest, err)
	}
}
