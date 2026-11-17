package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// A workspace that ignores its folder of clones, with a design repository and a code repository in it.
var (
	workspaceClone = ports.Clone{Path: "/work/ws", ID: "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d", Remote: "https://example.invalid/acme/ws.git", DefaultBranch: "main"}
	designClone    = ports.Clone{Path: "/work/ws/repos/design", ID: "2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e", Remote: "https://example.invalid/acme/design.git", DefaultBranch: "main"}
	codeClone      = ports.Clone{Path: "/work/ws/repos/api", ID: "3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f", Remote: "https://example.invalid/acme/api.git", DefaultBranch: "main"}
)

// aWorkspace lays the workspace out on the worker's machine.
func (a *adoption) aWorkspace() {
	a.t.Helper()
	for _, clone := range []ports.Clone{workspaceClone, designClone, codeClone} {
		a.in.Checkouts.Place(clone)
	}
	a.in.Layouts.Set(workspaceClone.Path, fake.CloneLayout{Ignored: []string{"repos"}, Tracked: []string{".gitignore", "README.md"}})
	a.in.Layouts.Set(designClone.Path, fake.CloneLayout{
		Plan: ports.PlanContent{Files: 1}, Tracked: []string{".gitignore", "AGENTS.md", "README.md", "docs/a.md", "plan/goal.md", "tools/go.mod", "tools/main.go"},
	})
	a.in.Layouts.Set(codeClone.Path, fake.CloneLayout{Tracked: []string{"go.mod", "main.go"}})
}

// plan runs adopt --dry-run --json and reads the plan it prints.
func (a *adoption) plan(clone ports.Clone, args ...string) planning.AdoptionPlan {
	a.t.Helper()
	code, stdout, stderr := a.runIn(clone, "", append([]string{"adopt", "--dry-run", "--json"}, args...)...)
	if code != cli.ExitOK {
		a.t.Fatalf("adopt --dry-run exit %d: %s", code, stderr)
	}
	var plan planning.AdoptionPlan
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		a.t.Fatalf("adopt --dry-run --json printed %q: %v", stdout, err)
	}
	return plan
}

// A dry run lists the layout, the shape, and every repository it would register, and records nothing: no
// project, no record, no checkout, and no work.
func TestAdoptDryRunListsTheLayoutAndRecordsNothing(t *testing.T) {
	a := newAdoption(t)
	code, stdout, stderr := a.runIn(shopClone, "", "adopt", "--dry-run", "/work/libs")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{"workspace_files", "would register\tShop_App", "would register\tapi", "would register\tweb", "mv /work/libs/api /work/Shop_App/repos/api", "append\t.gitignore\trepos/", "sha256:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the dry run printed no %q:\n%s", want, stdout)
		}
	}
	if _, made := a.projects()["shop-app"]; made {
		t.Error("a dry run made the project")
	}
	if kept, err := a.in.Checkouts.Every(t.Context()); err != nil || len(kept) != 0 {
		t.Errorf("a dry run kept checkouts %v: %v", kept, err)
	}
	if len(a.projects()) != 1 {
		t.Errorf("a dry run left projects %v", a.projects())
	}
}

// The plan is the same for the same layout, whether the project exists or not and whether the clones are
// registered or not, and it reads the project's records when it has them.
func TestAdoptDryRunIsTheSameBeforeAndAfterTheCloneRegisters(t *testing.T) {
	a := newAdoption(t)
	before := a.plan(shopClone, "/work/libs")
	if before.Shape != planning.ShapeWorkspaceFiles || before.Planning.Name != "Shop_App" || len(before.Moves) != 2 || before.Digest == "" {
		t.Fatalf("the plan reads shape %s, planning %+v, moves %+v", before.Shape, before.Planning, before.Moves)
	}
	if again := a.plan(shopClone, "/work/libs"); again.Digest != before.Digest {
		t.Errorf("two dry runs have digests %s and %s", before.Digest, again.Digest)
	}
	if !slices.ContainsFunc(before.Notes, func(note string) bool { return strings.Contains(note, "does not exist yet") }) {
		t.Errorf("a plan of a project not created yet says nothing of it: %v", before.Notes)
	}
	if code, _, stderr := a.runIn(shopClone, "", "adopt", "/work/libs", "--worker", "worker-a", "--no-attach"); code != cli.ExitOK {
		t.Fatalf("adopt: exit %d: %s", code, stderr)
	}
	after := a.plan(shopClone, "/work/libs")
	if after.Digest != before.Digest {
		t.Errorf("registering the clones changed the digest from %s to %s", before.Digest, after.Digest)
	}
	for _, repository := range after.Repositories {
		if repository.Outcome != string(ledger.AlreadyRegistered) {
			t.Errorf("after adopting, %s reads %q", repository.Path, repository.Outcome)
		}
	}
}

// A workspace whose ignored folder holds a design repository is a consolidation: the dry run lists what it
// takes and where the two repositories collide, and the design repository is not registered.
func TestAdoptDryRunOfAWorkspaceListsTheConsolidation(t *testing.T) {
	a := newAdoption(t)
	a.aWorkspace()
	code, stdout, stderr := a.runIn(workspaceClone, "", "adopt", "--dry-run", "--leave-out", "tools")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		"consolidate", "Design repository\tdesign", "not registered\tdesign", "would register\tapi", "AGENTS.md", "docs/a.md",
		"README.md\tworkspace", ".gitignore\tunion", "tools\ttools/go.mod\tleft out", "add\tplan/repositories.md", "add\tMakefile",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the dry run printed no %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "tools/main.go") {
		t.Errorf("a tree left out is listed as taken:\n%s", stdout)
	}
	plan := a.plan(workspaceClone, "--leave-out", "tools")
	if plan.Shape != planning.ShapeConsolidate || plan.Design == nil || plan.Design.Root != designClone.ID {
		t.Errorf("the JSON plan reads shape %s, design %+v", plan.Shape, plan.Design)
	}
}

// A ropeshift adopt names no restructuring by default, and one that asks for it in a layout with nothing to
// restructure is a usage error naming the shape, before anything is made.
func TestAdoptRestructureOfASingleRepositoryIsAUsageError(t *testing.T) {
	a := newAdoption(t)
	for _, args := range [][]string{{"adopt", "--restructure"}, {"adopt", "--dry-run", "--restructure"}} {
		code, _, stderr := a.runIn(shopClone, "", append(args, "--worker", "worker-a", "--no-attach")...)
		if code != cli.ExitUsage || !strings.Contains(stderr, "single") || !strings.Contains(stderr, "--restructure") {
			t.Errorf("%v: exit %d: %s", args, code, stderr)
		}
	}
	if len(a.projects()) != 1 {
		t.Errorf("a refused restructuring made projects: %v", a.projects())
	}
}

// With --restructure the adopt work carries the plan's digest and the choices, and the repositories register
// as they do without it.
func TestAdoptRestructureSendsTheDigestAndTheChoices(t *testing.T) {
	a := newAdoption(t)
	a.aWorkspace()
	planned := a.plan(workspaceClone, "--leave-out", "tools")
	code, _, stderr := a.runIn(workspaceClone, "", "adopt", "--restructure", "--leave-out", "tools", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	orders := a.workOrders("ws")
	if len(orders) != 1 || !orders[0].Adopt || orders[0].Restructure == nil {
		t.Fatalf("the adopt work is %+v", orders)
	}
	got := orders[0].Restructure
	want := planning.Restructure{Shape: planning.ShapeConsolidate, Digest: planned.Digest, DesignPath: designClone.Path, DesignRoot: designClone.ID, LeaveOut: []string{"tools"}}
	if got.Shape != want.Shape || got.Digest != want.Digest || got.DesignPath != want.DesignPath || got.DesignRoot != want.DesignRoot || !slices.Equal(got.LeaveOut, want.LeaveOut) {
		t.Errorf("the work carries %+v, want %+v", got, want)
	}
	held := a.repositories("ws")
	if _, registered := held["design"]; registered || held["ws"].ID == "" || held["api"].ID == "" {
		t.Errorf("the project holds %v", held)
	}
	if !held["ws"].Planning {
		t.Errorf("the workspace is not the planning repository: %+v", held["ws"])
	}
}

// Without --restructure the adopt work carries none, and the detection still decides which clones register and
// where the plan lives.
func TestAdoptWithoutRestructureRegistersTheDetectedClonesAndPlansInTheClone(t *testing.T) {
	a := newAdoption(t)
	a.aWorkspace()
	code, stdout, stderr := a.runIn(workspaceClone, "", "adopt", "--worker", "worker-a", "--no-attach")
	if code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	orders := a.workOrders("ws")
	if len(orders) != 1 || orders[0].Restructure != nil || orders[0].RepositoryID != workspaceClone.ID {
		t.Errorf("the adopt work is %+v", orders)
	}
	held := a.repositories("ws")
	if _, registered := held["design"]; registered || held["api"].ID == "" || !held["ws"].Planning {
		t.Errorf("the project holds %+v", held)
	}
	if !strings.Contains(stdout, "consolidate") {
		t.Errorf("adopt says nothing of the shape it read:\n%s", stdout)
	}
}

// Clones are searched for to --depth levels below each folder the clone ignores.
func TestAdoptSearchesToTheDepthItIsGiven(t *testing.T) {
	a := newAdoption(t)
	a.aWorkspace()
	deep := ports.Clone{Path: "/work/ws/repos/group/deep", ID: "4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70", Remote: "https://example.invalid/acme/deep.git", DefaultBranch: "main"}
	a.in.Checkouts.Place(deep)
	if plan := a.plan(workspaceClone); slices.ContainsFunc(plan.Layout.Clones, func(c planning.LayoutClone) bool { return c.Name == "deep" }) {
		t.Error("one level down found a clone two levels down")
	}
	if plan := a.plan(workspaceClone, "--depth", "2"); !slices.ContainsFunc(plan.Layout.Clones, func(c planning.LayoutClone) bool { return c.Name == "deep" }) {
		t.Errorf("--depth 2 found %+v", plan.Layout.Clones)
	}
}
