package planning

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func aLayoutPlan() AdoptionPlan {
	return AdoptionPlan{
		Layout: DetectedLayout{
			Root:    "/work/ws",
			Folders: []string{"/work/ws/repos"},
			Clones: []LayoutClone{
				{Name: "ws", Path: "/work/ws", Where: WhereHere, Documents: 1},
				{Name: "api", Path: "/work/ws/repos/api", Where: WhereIgnored, Documents: 2},
			},
		},
		Shape:    ShapeWorkspaceFiles,
		Planning: PlanningRepository{Name: "ws", Path: "/work/ws", Why: "the clone adopt runs in"},
		Repositories: []PlannedRepository{
			{Name: "ws", Path: "/work/ws", Outcome: string(ledger.Registered)},
			{Name: "api", Path: "/work/ws/repos/api", Outcome: string(ledger.Registered)},
		},
		Files: []PlannedFile{{Path: "plan/repositories.md", Action: ActionAdd, Detail: "the manifest of the repositories"}},
	}
}

// The digest is the SHA-256 of the plan's JSON with no digest, so it is the same for the same plan and differs
// when a clone is added.
func TestThePlanDigestIsStableAndFollowsTheLayout(t *testing.T) {
	plan := aLayoutPlan()
	first, second := plan.WithDigest(), aLayoutPlan().WithDigest()
	if first.Digest == "" || !strings.HasPrefix(first.Digest, "sha256:") || len(first.Digest) != len("sha256:")+64 {
		t.Fatalf("the digest reads %q", first.Digest)
	}
	if first.Digest != second.Digest {
		t.Errorf("two plans of one layout have digests %s and %s", first.Digest, second.Digest)
	}
	if again := first.WithDigest(); again.Digest != first.Digest {
		t.Errorf("a plan with a digest has another when digested again: %s and %s", first.Digest, again.Digest)
	}

	added := aLayoutPlan()
	added.Layout.Clones = append(added.Layout.Clones, LayoutClone{Name: "web", Path: "/work/ws/repos/web", Where: WhereIgnored})
	added.Repositories = append(added.Repositories, PlannedRepository{Name: "web", Path: "/work/ws/repos/web", Outcome: string(ledger.Registered)})
	if got := added.WithDigest(); got.Digest == first.Digest {
		t.Errorf("a plan with one more clone has the digest %s of the one before", got.Digest)
	}
}

// A digest a member's work order stored keeps meaning what it meant, so the digest of a fixed plan is pinned: a
// change to how a plan is digested must move this value on purpose.
func TestThePlanDigestIsPinned(t *testing.T) {
	const pinned = "sha256:8c41d80c84e723c84b3f02f0df7a474b75a4079e251d45f79fe5f8f778c31fb9"
	if got := aLayoutPlan().WithDigest().Digest; got != pinned {
		t.Errorf("the digest of the fixed plan reads %s, want %s", got, pinned)
	}
}

// What a registration says depends on the project's records, which the registration itself changes, so the
// digest holds the repositories a plan registers and not what became of each.
func TestThePlanDigestIgnoresWhatARegistrationSaid(t *testing.T) {
	before := aLayoutPlan().WithDigest()
	after := aLayoutPlan()
	for i := range after.Repositories {
		after.Repositories[i].Outcome = string(ledger.AlreadyRegistered)
		after.Repositories[i].Reason = "matched by its earlier identity"
		after.Repositories[i].ID = "abc"
	}
	after.Notes = []string{"The project does not exist yet, so no record was read."}
	if got := after.WithDigest(); got.Digest != before.Digest {
		t.Errorf("a plan whose clones registered has the digest %s, want %s", got.Digest, before.Digest)
	}
	left := aLayoutPlan()
	left.Repositories[0].Outcome = OutcomeNotRegistered
	if got := left.WithDigest(); got.Digest == before.Digest {
		t.Error("a plan that leaves a repository unregistered has the digest of one that registers it")
	}
}

// A restructuring a member chooses holds a shape that offers one, the digest of the plan it was chosen from,
// and for a consolidation the design repository and the code trees to leave out.
func TestARestructuringIsCheckedBeforeItIsKept(t *testing.T) {
	digest := aLayoutPlan().WithDigest().Digest
	ok := Restructure{Shape: ShapeConsolidate, Digest: digest, DesignPath: "/work/ws/repos/design", DesignRoot: strings.Repeat("a", 40), LeaveOut: []string{"tools"}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a consolidation with its design repository: %v", err)
	}
	if err := (Restructure{Shape: ShapeWorkspaceFiles, Digest: digest}).Validate(); err != nil {
		t.Errorf("the workspace files with a digest: %v", err)
	}
	for name, bad := range map[string]Restructure{
		"no shape":                         {Digest: digest},
		"the single shape":                 {Shape: ShapeSingle, Digest: digest},
		"an unknown shape":                 {Shape: "other", Digest: digest},
		"no digest":                        {Shape: ShapePlanned},
		"a digest that is no digest":       {Shape: ShapePlanned, Digest: "sha256:abc"},
		"a consolidation with no design":   {Shape: ShapeConsolidate, Digest: digest, DesignRoot: "abc"},
		"a consolidation with no root":     {Shape: ShapeConsolidate, Digest: digest, DesignPath: "/work/design"},
		"a relative design path":           {Shape: ShapeConsolidate, Digest: digest, DesignPath: "design", DesignRoot: "abc"},
		"a design outside a consolidation": {Shape: ShapePlanned, Digest: digest, DesignPath: "/work/design"},
		"a tree left out of the files":     {Shape: ShapeWorkspaceFiles, Digest: digest, LeaveOut: []string{"tools"}},
		"the whole repository left out":    {Shape: ShapeConsolidate, Digest: digest, DesignPath: "/work/design", DesignRoot: "abc", LeaveOut: []string{"."}},
		"a path above the repository":      {Shape: ShapeConsolidate, Digest: digest, DesignPath: "/work/design", DesignRoot: "abc", LeaveOut: []string{"../x"}},
		"an absolute tree":                 {Shape: ShapeConsolidate, Digest: digest, DesignPath: "/work/design", DesignRoot: "abc", LeaveOut: []string{"/tools"}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
}

// A restructuring a work order kept before it held the request still reads: its fields are all optional, so
// nothing a stored one lacks makes it invalid. What it lacks is named, so a worker can say why it cannot read
// the layout again.
func TestAnOldRestructuringStillReadsAndNamesWhatItLacks(t *testing.T) {
	digest := aLayoutPlan().WithDigest().Digest
	old := `{"shape":"workspace_files","digest":"` + digest + `"}`
	var kept Restructure
	if err := json.Unmarshal([]byte(old), &kept); err != nil {
		t.Fatal(err)
	}
	if err := kept.Validate(); err != nil {
		t.Fatalf("a restructuring kept before the request: %v", err)
	}
	if got := kept.Missing(); !reflect.DeepEqual(got, []string{"root", "parts"}) {
		t.Errorf("an old restructuring lacks %v, want the root and the parts", got)
	}
	whole := Restructure{Shape: ShapeWorkspaceFiles, Digest: digest, Root: "/work/ws", Parts: aLayoutPlan().Parts()}
	if got := whole.Missing(); len(got) != 0 {
		t.Errorf("a restructuring with its root and parts lacks %v", got)
	}
	if got := (Restructure{Shape: ShapePlanned, Digest: digest, Root: "/work/ws"}).Missing(); !reflect.DeepEqual(got, []string{"parts"}) {
		t.Errorf("a restructuring with no parts lacks %v", got)
	}
}

// The request a plan was read with is kept so a worker reads the layout again the way the plan did, and it is
// checked before it is kept.
func TestTheRequestAPlanWasReadWithIsCheckedBeforeItIsKept(t *testing.T) {
	plan := aLayoutPlan().WithDigest()
	ok := Restructure{
		Shape: ShapeWorkspaceFiles, Digest: plan.Digest, Root: "/work/ws", Paths: []string{"/work/libs"}, Depth: 2,
		Planning: "ws", Design: "docs", Parts: plan.Parts(),
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a restructuring with its request: %v", err)
	}
	for name, bad := range map[string]func(*Restructure){
		"a relative root":             func(r *Restructure) { r.Root = "ws" },
		"an unclean root":             func(r *Restructure) { r.Root = "/work/../ws" },
		"a relative path":             func(r *Restructure) { r.Paths = []string{"libs"} },
		"a negative depth":            func(r *Restructure) { r.Depth = -1 },
		"a depth past the bound":      func(r *Restructure) { r.Depth = MaxLayoutDepth + 1 },
		"a planning name with a path": func(r *Restructure) { r.Planning = "a/b" },
		"a design name with a path":   func(r *Restructure) { r.Design = "../x" },
		"a part nothing digests":      func(r *Restructure) { r.Parts = map[string]string{"colors": plan.Digest} },
		"a part with no digest":       func(r *Restructure) { r.Parts = map[string]string{PartFiles: "sha256:abc"} },
		"a request with no root":      func(r *Restructure) { r.Root = "" },
	} {
		chosen := ok
		chosen.Paths = slices.Clone(ok.Paths)
		chosen.Parts = maps.Clone(ok.Parts)
		bad(&chosen)
		if err := chosen.Validate(); err == nil {
			t.Errorf("%s was kept", name)
		}
	}
}

// Each part of a plan has a digest of its own, so a refusal can name what differs, and it holds only that part.
func TestThePartsOfAPlanNameWhatDiffers(t *testing.T) {
	plan := aLayoutPlan().WithDigest()
	parts := plan.Parts()
	for _, name := range PlanParts {
		if got := parts[name]; !strings.HasPrefix(got, DigestSHA256) || len(got) != len(DigestSHA256)+64 {
			t.Errorf("part %s reads %q", name, got)
		}
	}
	if len(parts) != len(PlanParts) {
		t.Errorf("a plan has parts %v, and names %v", slices.Sorted(maps.Keys(parts)), PlanParts)
	}
	if again := aLayoutPlan().WithDigest().Parts(); !reflect.DeepEqual(again, parts) {
		t.Errorf("two plans of one layout have parts %v and %v", parts, again)
	}
	if got := plan.DiffersIn(parts); len(got) != 0 {
		t.Errorf("a plan differs from its own parts in %v", got)
	}

	added := aLayoutPlan()
	added.Layout.Clones = append(added.Layout.Clones, LayoutClone{Name: "web", Path: "/work/ws/repos/web", Where: WhereIgnored})
	added.Repositories = append(added.Repositories, PlannedRepository{Name: "web", Path: "/work/ws/repos/web", Outcome: string(ledger.Registered)})
	added.Moves = []Move{{Name: "web", From: "/work/web", To: "/work/ws/repos/web", Commands: []string{"mv /work/web /work/ws/repos/web"}}}
	if got := added.DiffersIn(parts); !reflect.DeepEqual(got, []string{PartLayout, PartRepositories, PartMoves}) {
		t.Errorf("a plan with one more clone differs in %v", got)
	}
	files := aLayoutPlan()
	files.Files = nil
	if got := files.DiffersIn(parts); !reflect.DeepEqual(got, []string{PartFiles}) {
		t.Errorf("a plan with no file to add differs in %v", got)
	}
	// A part the stored parts do not hold reads as different, and a registration's outcome as the same.
	if got := plan.DiffersIn(map[string]string{}); len(got) != len(PlanParts) {
		t.Errorf("a plan differs from no parts in %v", got)
	}
	registered := aLayoutPlan()
	registered.Repositories[0].Outcome = string(ledger.AlreadyRegistered)
	registered.Notes = []string{"a note"}
	if got := registered.DiffersIn(parts); len(got) != 0 {
		t.Errorf("a plan whose clones registered differs in %v", got)
	}
	// A part with nothing in it is the same whether the list is nil or empty.
	empty := aLayoutPlan()
	empty.Moves, empty.Takes = []Move{}, []string{}
	if got := empty.DiffersIn(parts); len(got) != 0 {
		t.Errorf("a plan with empty lists differs in %v", got)
	}
}

func TestAShapeKnowsWhatItOffers(t *testing.T) {
	for shape, offers := range map[LayoutShape]bool{ShapeSingle: false, ShapePlanned: true, ShapeConsolidate: true, ShapeWorkspaceFiles: true} {
		if got := shape.Restructures(); got != offers {
			t.Errorf("shape %s offers a restructuring: %v, want %v", shape, got, offers)
		}
	}
	if LayoutShape("other").Valid() || !ShapeConsolidate.Valid() {
		t.Error("a shape is valid when the plan names it")
	}
}
