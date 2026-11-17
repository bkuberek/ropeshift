package planning

import (
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
