package control_test

import (
	"errors"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// adoptionMachine is a worker's machine with the clones a test lays out on it.
type adoptionMachine struct {
	t         *testing.T
	checkouts *fake.Checkouts
	layouts   *fake.Layouts
}

func newAdoptionMachine(t *testing.T) *adoptionMachine {
	t.Helper()
	return &adoptionMachine{t: t, checkouts: fake.NewCheckouts(), layouts: fake.NewLayouts()}
}

// clone places a clone with its own first commit and remote, and what it holds.
func (m *adoptionMachine) clone(at string, layout fake.CloneLayout) {
	m.t.Helper()
	name := path.Base(at)
	m.checkouts.Place(ports.Clone{Path: at, ID: "root-" + name, Remote: "git@example.invalid:acme/" + name + ".git", DefaultBranch: "main"})
	m.layouts.Set(at, layout)
}

func (m *adoptionMachine) plan(in control.AdoptionRequest) (planning.AdoptionPlan, error) {
	m.t.Helper()
	store := ledgerOf(m.t, "acme")
	return control.NewAdoptions(control.NewRepositories(store)).Plan(m.t.Context(), "acme", in, m.checkouts, m.layouts, projectMember)
}

func (m *adoptionMachine) mustPlan(in control.AdoptionRequest) planning.AdoptionPlan {
	m.t.Helper()
	plan, err := m.plan(in)
	if err != nil {
		m.t.Fatalf("plan: %v", err)
	}
	return plan
}

func clonesOf(plan planning.AdoptionPlan) []string {
	var names []string
	for _, clone := range plan.Layout.Clones {
		names = append(names, clone.Name)
	}
	return names
}

func filesOf(plan planning.AdoptionPlan) []string {
	var files []string
	for _, file := range plan.Files {
		files = append(files, file.Action+" "+file.Path)
	}
	return files
}

func outcomesOf(plan planning.AdoptionPlan) map[string]string {
	out := map[string]string{}
	for _, repository := range plan.Repositories {
		out[repository.Path] = repository.Outcome
	}
	return out
}

// workspaceOf lays out a workspace with a code folder it ignores.
func workspaceOf(m *adoptionMachine, tracked ...string) {
	m.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: append([]string{".gitignore", "README.md"}, tracked...)})
}

const designTracked = ".gitignore README.md AGENTS.md docs/a.md docs/b.md plan/goal.md tools/go.mod tools/main.go"

func designOf(m *adoptionMachine, files int, extra ...string) {
	m.clone("/work/ws/repos/design", fake.CloneLayout{Plan: ports.PlanContent{Files: files}, Tracked: append(strings.Fields(designTracked), extra...)})
}

// A clone with no other clone found is a single repository, and restructuring offers nothing.
func TestAdoptionOfALoneCloneIsTheSingleShape(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Shape != planning.ShapeSingle || plan.Shape.Restructures() {
		t.Errorf("a lone clone is shape %s", plan.Shape)
	}
	if plan.Planning.Path != "/work/ws" || plan.Planning.Name != "ws" || plan.Design != nil || len(plan.Files) != 0 || len(plan.Moves) != 0 {
		t.Errorf("the single shape plans %+v", plan)
	}
	if got := outcomesOf(plan); !reflect.DeepEqual(got, map[string]string{"/work/ws": string(ledger.Registered)}) {
		t.Errorf("the registrations read %v", got)
	}
	if plan.Digest == "" || plan.Digest != plan.ComputeDigest() {
		t.Errorf("the digest reads %q", plan.Digest)
	}
}

// A clone that holds plan content, with clones found in a folder it ignores, is planned: it plans in itself,
// and the manifest is what restructuring offers, when none exists.
func TestAdoptionOfAClonePlannedAlreadyOffersTheManifest(t *testing.T) {
	m := newAdoptionMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 1}, Tracked: []string{"README.md", "plan/goal.md"}})
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Shape != planning.ShapePlanned || plan.Planning.Path != "/work/ws" {
		t.Fatalf("a planned clone with a clone below it is %s, planning in %+v", plan.Shape, plan.Planning)
	}
	if want := []string{"add plan/repositories.md"}; !reflect.DeepEqual(filesOf(plan), want) {
		t.Errorf("the planned shape adds %v, want %v", filesOf(plan), want)
	}
	if len(plan.Moves) != 0 {
		t.Errorf("the planned shape lists moves %+v", plan.Moves)
	}
	if want := []string{"ws", "api"}; !reflect.DeepEqual(clonesOf(plan), want) {
		t.Errorf("the layout holds %v, want %v", clonesOf(plan), want)
	}
	if plan.Layout.Clones[0].Where != planning.WhereHere || !plan.Layout.Clones[0].Plan || plan.Layout.Clones[1].Where != planning.WhereIgnored {
		t.Errorf("the clones read %+v", plan.Layout.Clones)
	}
	if want := []string{"/work/ws/repos"}; !reflect.DeepEqual(plan.Layout.Folders, want) {
		t.Errorf("the folders read %v, want %v", plan.Layout.Folders, want)
	}

	// With a manifest already, there is nothing to add.
	m.layouts.Set("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Plan: ports.PlanContent{Files: 2, Manifest: true}, Tracked: []string{"README.md", "plan/repositories.md"}})
	if again := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"}); again.Shape != planning.ShapePlanned || len(again.Files) != 0 {
		t.Errorf("a planned clone with a manifest is %s and adds %v", again.Shape, filesOf(again))
	}
}

// A workspace with no plan content whose ignored folder holds the design repository is consolidated: the plan
// lists the paths it takes, the colliding ones with the version kept, the code trees, and the files it adds, and
// leaves the design repository out of what it registers.
func TestAdoptionOfAWorkspaceWithADesignRepositoryConsolidates(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	designOf(m, 3)
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod", "main.go", "README.md"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Shape != planning.ShapeConsolidate || plan.Planning.Path != "/work/ws" {
		t.Fatalf("the shape is %s, planning in %+v", plan.Shape, plan.Planning)
	}
	if plan.Design == nil || plan.Design.Name != "design" || plan.Design.Root != "root-design" || !strings.Contains(plan.Design.Why, "plan content") {
		t.Errorf("the design repository reads %+v", plan.Design)
	}
	want := map[string]string{
		"/work/ws": string(ledger.Registered), "/work/ws/repos/api": string(ledger.Registered), "/work/ws/repos/design": planning.OutcomeNotRegistered,
	}
	if got := outcomesOf(plan); !reflect.DeepEqual(got, want) {
		t.Errorf("the registrations read %v, want %v", got, want)
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
	if want := []string{"add plan/repositories.md", "add Makefile"}; !reflect.DeepEqual(filesOf(plan), want) {
		t.Errorf("the plan adds %v, want %v", filesOf(plan), want)
	}
	if len(plan.Moves) != 0 {
		t.Errorf("clones in the code folder move nowhere: %+v", plan.Moves)
	}

	// Leaving a code tree out takes none of its paths, and the plan says so.
	left := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", LeaveOut: []string{"tools"}})
	if want := []string{"AGENTS.md", "docs/a.md", "docs/b.md", "plan/goal.md"}; !reflect.DeepEqual(left.Takes, want) {
		t.Errorf("the plan takes %v once tools is left out, want %v", left.Takes, want)
	}
	if len(left.CodeTrees) != 1 || !left.CodeTrees[0].LeftOut {
		t.Errorf("the code trees read %+v", left.CodeTrees)
	}
	if left.Digest == plan.Digest {
		t.Error("leaving a tree out leaves the digest as it was")
	}
}

// With no clone holding plan content, the design repository is the one with the most committed Markdown
// documents, and a tie names none and says how to name one.
func TestTheDesignRepositoryIsTheOneWithTheMostDocumentsAndATieNamesNone(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	m.clone("/work/ws/repos/notes", fake.CloneLayout{Tracked: []string{"a.md", "b.md", "c.md", "d.md"}})
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod", "README.md"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil || plan.Design.Name != "notes" || !strings.Contains(plan.Design.Why, "4") {
		t.Errorf("the repository with four documents reads %s, %+v", plan.Shape, plan.Design)
	}

	tied := newAdoptionMachine(t)
	workspaceOf(tied)
	tied.clone("/work/ws/repos/a", fake.CloneLayout{Tracked: []string{"x.md", "y.md", "z.md"}})
	tied.clone("/work/ws/repos/b", fake.CloneLayout{Tracked: []string{"x.md", "y.md", "z.md"}})
	plan = tied.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if plan.Design != nil || plan.Shape != planning.ShapeWorkspaceFiles || !slices.ContainsFunc(plan.Notes, func(note string) bool { return strings.Contains(note, "--design NAME") }) {
		t.Errorf("a tie reads shape %s, design %+v, notes %v", plan.Shape, plan.Design, plan.Notes)
	}
	named := tied.mustPlan(control.AdoptionRequest{Root: "/work/ws", Design: "b"})
	if named.Shape != planning.ShapeConsolidate || named.Design == nil || named.Design.Name != "b" || !strings.Contains(named.Design.Why, "--design") {
		t.Errorf("--design b reads shape %s, design %+v", named.Shape, named.Design)
	}
	if _, err := tied.plan(control.AdoptionRequest{Root: "/work/ws", Design: "missing"}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("--design naming no clone: %v", err)
	}
	// Naming the clone adopt runs in keeps the design repository out of the code folder.
	if root := tied.mustPlan(control.AdoptionRequest{Root: "/work/ws", Design: "ws"}); root.Shape != planning.ShapeWorkspaceFiles || root.Design == nil || root.Design.Name != "ws" {
		t.Errorf("--design naming the root reads %s, %+v", root.Shape, root.Design)
	}
}

// Any other layout with clones is the workspace files: clones named by paths beside the clone are listed to move
// into its folder of clones, the folder is ignored, and each file is added only when the clone lacks it.
func TestAdoptionOfClonesNamedByPathsIsTheWorkspaceFiles(t *testing.T) {
	m := newAdoptionMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", "Makefile"}})
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Paths: []string{"/work/api", "/work/web"}})
	if plan.Shape != planning.ShapeWorkspaceFiles || plan.Planning.Path != "/work/ws" {
		t.Fatalf("the shape is %s, planning in %+v", plan.Shape, plan.Planning)
	}
	if want := []string{"add plan/repositories.md", "append .gitignore"}; !reflect.DeepEqual(filesOf(plan), want) {
		t.Errorf("the plan adds %v, want %v (the Makefile exists)", filesOf(plan), want)
	}
	if plan.Files[1].Detail != "repos/" {
		t.Errorf("the appended line is %q", plan.Files[1].Detail)
	}
	wantMoves := []planning.Move{
		{Name: "api", From: "/work/api", To: "/work/ws/repos/api", Commands: []string{"mv /work/api /work/ws/repos/api", "ropeshift repository add /work/ws/repos/api"}},
		{Name: "web", From: "/work/web", To: "/work/ws/repos/web", Commands: []string{"mv /work/web /work/ws/repos/web", "ropeshift repository add /work/ws/repos/web"}},
	}
	if !reflect.DeepEqual(plan.Moves, wantMoves) {
		t.Errorf("the moves read %+v, want %+v", plan.Moves, wantMoves)
	}
	for _, clone := range plan.Layout.Clones {
		if clone.Name != "ws" && clone.Where != planning.WherePath {
			t.Errorf("clone %s was found %s", clone.Name, clone.Where)
		}
	}

	// A clone already inside the clone's folder of clones moves nowhere, and a folder already ignored is not
	// added to the ignore file again.
	inside := newAdoptionMachine(t)
	inside.clone("/work/ws", fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{"README.md"}})
	inside.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	inside.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	got := inside.mustPlan(control.AdoptionRequest{Root: "/work/ws", Paths: []string{"/work/web"}})
	if len(got.Moves) != 1 || got.Moves[0].Name != "web" {
		t.Errorf("only the clone beside moves: %+v", got.Moves)
	}
	if slices.Contains(filesOf(got), "append .gitignore") {
		t.Errorf("a folder already ignored is appended again: %v", filesOf(got))
	}
}

// Clones are searched for down to the depth, one level by default, and a path that holds no clone is refused.
func TestAdoptionSearchesAnIgnoredFolderToTheDepth(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	m.clone("/work/ws/repos/group/deep", fake.CloneLayout{Tracked: []string{"go.mod"}})
	if plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"}); plan.Shape != planning.ShapeSingle {
		t.Errorf("one level down finds %v and reads %s", clonesOf(plan), plan.Shape)
	}
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Depth: 2})
	if want := []string{"ws", "deep"}; !reflect.DeepEqual(clonesOf(plan), want) || plan.Shape == planning.ShapeSingle {
		t.Errorf("two levels down finds %v and reads %s", clonesOf(plan), plan.Shape)
	}
	nothing := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Paths: []string{"/work/nothing"}})
	if outcomesOf(nothing)["/work/nothing"] != string(ledger.RegistrationRefused) {
		t.Errorf("a path with no clone reads %v", outcomesOf(nothing))
	}
}

// The folder around the clone is never scanned: a clone beside it is seen only when a path names it.
func TestAdoptionNeverScansTheFolderAroundTheClone(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	m.clone("/work/beside", fake.CloneLayout{Tracked: []string{"go.mod"}})
	if plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Depth: 3}); slices.Contains(clonesOf(plan), "beside") {
		t.Errorf("the folder around the clone was scanned: %v", clonesOf(plan))
	}
}

// A build file at the top of the design repository makes the whole of it one code tree, which cannot be left
// out, and a tree left out must be one the design repository has, in a consolidation.
func TestLeavingOutNamesACodeTreeOfTheDesignRepository(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	designOf(m, 2, "go.mod")
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws"})
	if want := []planning.PlannedCodeTree{{Path: ".", BuildFile: "go.mod", Whole: true}}; !reflect.DeepEqual(plan.CodeTrees, want) {
		t.Errorf("the code trees read %+v, want %+v", plan.CodeTrees, want)
	}
	for name, leave := range map[string][]string{"the whole repository": {"."}, "a tree it lacks": {"web"}} {
		if _, err := m.plan(control.AdoptionRequest{Root: "/work/ws", LeaveOut: leave}); !errors.Is(err, ledger.ErrInvalidRepository) {
			t.Errorf("leaving out %s: %v", name, err)
		}
	}
	single := newAdoptionMachine(t)
	workspaceOf(single)
	if _, err := single.plan(control.AdoptionRequest{Root: "/work/ws", LeaveOut: []string{"tools"}}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("leaving a tree out of a layout that consolidates nothing: %v", err)
	}
}

// --planning keeps its meaning: it names the repository that holds the plan.
func TestPlanningNamesTheRepositoryThatHoldsThePlan(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Planning: "api"})
	if plan.Planning.Name != "api" || plan.Planning.Path != "/work/ws/repos/api" || !strings.Contains(plan.Planning.Why, "--planning") {
		t.Errorf("--planning api reads %+v", plan.Planning)
	}
	elsewhere := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Planning: "registered-earlier"})
	if elsewhere.Planning.Name != "registered-earlier" || elsewhere.Planning.Path != "" {
		t.Errorf("--planning naming no clone found reads %+v", elsewhere.Planning)
	}
}

// A dry run records nothing, and the digest is the same for the same layout and follows a clone added.
func TestAnAdoptionPlanRecordsNothingAndItsDigestFollowsTheLayout(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	designOf(m, 1)
	store := ledgerOf(t, "acme")
	adoptions := control.NewAdoptions(control.NewRepositories(store))
	events := func() int {
		var n int
		err := store.Read(t.Context(), "acme", func(tx ports.ReadTx) error {
			all, err := tx.Events(0, 1000)
			n = len(all)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := events()
	first, err := adoptions.Plan(t.Context(), "acme", control.AdoptionRequest{Root: "/work/ws"}, m.checkouts, m.layouts, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	second, err := adoptions.Plan(t.Context(), "acme", control.AdoptionRequest{Root: "/work/ws"}, m.checkouts, m.layouts, projectMember)
	if err != nil || first.Digest != second.Digest {
		t.Fatalf("two plans of one layout have digests %q and %q: %v", first.Digest, second.Digest, err)
	}
	if after := events(); after != before {
		t.Errorf("a plan appended %d events", after-before)
	}
	if kept, err := m.checkouts.Of(t.Context(), "acme"); err != nil || len(kept) != 0 {
		t.Errorf("a plan kept the checkouts %v: %v", kept, err)
	}
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	added, err := adoptions.Plan(t.Context(), "acme", control.AdoptionRequest{Root: "/work/ws"}, m.checkouts, m.layouts, projectMember)
	if err != nil || added.Digest == first.Digest {
		t.Errorf("a clone added leaves the digest %q: %v", added.Digest, err)
	}

	// Registering the clones changes what a registration says and not the digest.
	if _, err := control.NewRepositories(store).Add(t.Context(), "acme", control.RepositoryAdd{Paths: []string{"/work/ws", "/work/ws/repos/api"}}, m.checkouts, projectMember); err != nil {
		t.Fatal(err)
	}
	registered, err := adoptions.Plan(t.Context(), "acme", control.AdoptionRequest{Root: "/work/ws"}, m.checkouts, m.layouts, projectMember)
	if err != nil || registered.Digest != added.Digest {
		t.Errorf("a plan after the clones registered has the digest %q, want %q: %v", registered.Digest, added.Digest, err)
	}
	if got := outcomesOf(registered)["/work/ws/repos/api"]; got != string(ledger.AlreadyRegistered) {
		t.Errorf("a registered clone reads %q", got)
	}
}

// A project the adoption has yet to create has no records, so each repository reads as one that would
// register, and the plan says why.
func TestAnAdoptionPlanOfAProjectNotCreatedYetReadsNoRecords(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	adoptions := control.NewAdoptions(control.NewRepositories(nil))
	plan, err := adoptions.Plan(t.Context(), "", control.AdoptionRequest{Root: "/work/ws", NewProject: true}, m.checkouts, m.layouts, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	if got := outcomesOf(plan); got["/work/ws"] != string(ledger.Registered) || got["/work/ws/repos/api"] != string(ledger.Registered) {
		t.Errorf("the registrations read %v", got)
	}
	if !slices.ContainsFunc(plan.Notes, func(note string) bool { return strings.Contains(note, "does not exist yet") }) {
		t.Errorf("the notes read %v", plan.Notes)
	}
}

// Only a member reads paths on the machine.
func TestOnlyAMemberPlansAnAdoption(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "delegate-1"}
	_, err := control.NewAdoptions(control.NewRepositories(ledgerOf(t, "acme"))).Plan(t.Context(), "acme", control.AdoptionRequest{Root: "/work/ws"}, m.checkouts, m.layouts, delegate)
	if !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a delegate's plan: %v", err)
	}
	// A project not created yet reads no record, so the member check is the one that refuses a delegate.
	_, err = control.NewAdoptions(control.NewRepositories(nil)).Plan(t.Context(), "", control.AdoptionRequest{Root: "/work/ws", NewProject: true}, m.checkouts, m.layouts, delegate)
	if !errors.Is(err, workorders.ErrForbidden) {
		t.Errorf("a delegate's plan of a project not created yet: %v", err)
	}
	if _, err := m.plan(control.AdoptionRequest{Root: "relative"}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("a relative root: %v", err)
	}
	if _, err := m.plan(control.AdoptionRequest{Root: "/work/nowhere"}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("a root that is no clone: %v", err)
	}
}

// A root that is not itself the top of a clone is refused, even when exactly one clone sits below it.
func TestAnAdoptionRootThatHoldsOneCloneIsNotThatClone(t *testing.T) {
	m := newAdoptionMachine(t)
	m.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	if _, err := m.plan(control.AdoptionRequest{Root: "/work/ws/repos"}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("a folder holding one clone: %v", err)
	}
	if plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws/repos/api"}); plan.Layout.Root != "/work/ws/repos/api" {
		t.Errorf("the clone itself reads root %s", plan.Layout.Root)
	}
}

// The depth is bounded, so a request cannot walk a tree as deep as it likes.
func TestAnAdoptionDepthIsBounded(t *testing.T) {
	m := newAdoptionMachine(t)
	workspaceOf(m)
	if _, err := m.plan(control.AdoptionRequest{Root: "/work/ws", Depth: control.MaxAdoptionDepth}); err != nil {
		t.Errorf("the deepest depth: %v", err)
	}
	if _, err := m.plan(control.AdoptionRequest{Root: "/work/ws", Depth: control.MaxAdoptionDepth + 1}); !errors.Is(err, ledger.ErrInvalidRepository) {
		t.Errorf("a depth above the bound: %v", err)
	}
}

// A path with a space or a quote in it is quoted in the commands the plan lists, so they run as printed.
func TestTheMovesAreQuotedForAShell(t *testing.T) {
	m := newAdoptionMachine(t)
	m.clone("/work/ws", fake.CloneLayout{Tracked: []string{"README.md", "Makefile"}})
	m.clone("/work/my api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/it's", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/new\nline", fake.CloneLayout{Tracked: []string{"go.mod"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/ws", Paths: []string{"/work/my api", "/work/it's", "/work/new\nline"}})
	var commands []string
	for _, move := range plan.Moves {
		commands = append(commands, move.Commands...)
	}
	want := []string{
		`mv '/work/it'\''s' '/work/ws/repos/it'\''s'`, `ropeshift repository add '/work/ws/repos/it'\''s'`,
		`mv '/work/my api' '/work/ws/repos/my api'`, `ropeshift repository add '/work/ws/repos/my api'`,
		// Single quotes hold a newline as it is, so the command runs as it prints.
		"mv '/work/new\nline' '/work/ws/repos/new\nline'", "ropeshift repository add '/work/ws/repos/new\nline'",
	}
	if !slices.Equal(commands, want) {
		t.Errorf("the commands read %q, want %q", commands, want)
	}
}

// Run from a code clone with a design repository beside it, the plan lives in the design repository, and moves
// the code clone into it. --planning still decides.
func TestAdoptionFromACodeCloneWithTheDesignRepositoryBesidePlansThere(t *testing.T) {
	m := newAdoptionMachine(t)
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod", "main.go"}})
	m.clone("/work/design", fake.CloneLayout{Plan: ports.PlanContent{Files: 1}, Tracked: []string{"docs/a.md", "docs/b.md", "plan/goal.md"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/api", Paths: []string{"/work/design"}})
	if plan.Shape != planning.ShapeWorkspaceFiles || plan.Planning.Name != "design" || plan.Planning.Path != "/work/design" || plan.Planning.Undecided {
		t.Fatalf("the shape is %s, planning in %+v", plan.Shape, plan.Planning)
	}
	if plan.Design == nil || plan.Design.Name != "design" {
		t.Errorf("the design repository reads %+v", plan.Design)
	}
	if len(plan.Moves) != 1 || plan.Moves[0].Name != "api" || plan.Moves[0].To != "/work/design/repos/api" {
		t.Errorf("the code clone moves into the design repository: %+v", plan.Moves)
	}
	named := m.mustPlan(control.AdoptionRequest{Root: "/work/api", Paths: []string{"/work/design"}, Planning: "api"})
	if named.Planning.Name != "api" || named.Planning.Path != "/work/api" || !strings.Contains(named.Planning.Why, "--planning") {
		t.Errorf("--planning api reads %+v", named.Planning)
	}
}

// With no design repository beside the clone, or a tie between candidates, nothing settles where the plan lives,
// and the plan says so rather than choosing the clone adopt runs in. A folder the clone ignores that holds
// clones makes it a workspace, which plans in itself.
func TestAdoptionWithNoDesignRepositoryLeavesThePlanningRepositoryUndecided(t *testing.T) {
	m := newAdoptionMachine(t)
	m.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	m.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	plan := m.mustPlan(control.AdoptionRequest{Root: "/work/api", Paths: []string{"/work/web"}})
	if !plan.Planning.Undecided || plan.Planning.Name != "" || plan.Planning.Path != "" ||
		!slices.ContainsFunc(plan.Notes, func(note string) bool { return strings.Contains(note, "--planning NAME") }) {
		t.Errorf("no design repository reads planning %+v, notes %v", plan.Planning, plan.Notes)
	}

	tied := newAdoptionMachine(t)
	tied.clone("/work/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	tied.clone("/work/a", fake.CloneLayout{Tracked: []string{"x.md", "y.md"}})
	tied.clone("/work/b", fake.CloneLayout{Tracked: []string{"x.md", "y.md"}})
	if plan := tied.mustPlan(control.AdoptionRequest{Root: "/work/api", Paths: []string{"/work/a", "/work/b"}}); !plan.Planning.Undecided || plan.Design != nil {
		t.Errorf("a tie reads planning %+v, design %+v", plan.Planning, plan.Design)
	}
	if plan := tied.mustPlan(control.AdoptionRequest{Root: "/work/api", Paths: []string{"/work/a", "/work/b"}, Planning: "a"}); plan.Planning.Undecided || plan.Planning.Name != "a" {
		t.Errorf("--planning a reads %+v", plan.Planning)
	}

	workspace := newAdoptionMachine(t)
	workspaceOf(workspace)
	workspace.clone("/work/ws/repos/api", fake.CloneLayout{Tracked: []string{"go.mod"}})
	workspace.clone("/work/web", fake.CloneLayout{Tracked: []string{"package.json"}})
	if plan := workspace.mustPlan(control.AdoptionRequest{Root: "/work/ws", Paths: []string{"/work/web"}}); plan.Planning.Undecided || plan.Planning.Path != "/work/ws" {
		t.Errorf("a workspace reads planning %+v", plan.Planning)
	}
}
