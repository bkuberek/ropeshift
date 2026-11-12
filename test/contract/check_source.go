package contract

import (
	"context"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// CheckSourceRig is one source of checks and the way a check tells it what ran.
type CheckSourceRig struct {
	Source ports.CheckSource
	Scope  ledger.WorkspaceID
	// Set says what ran on a commit in a workspace.
	Set func(scope ledger.WorkspaceID, request ports.ForgeRequest, checks ...ports.ForgeCheck)
}

// RunCheckSource runs one contract on one source of checks. Only the fake exists, so a source that
// arrives later must pass the same checks.
func RunCheckSource(t *testing.T, fresh func(*testing.T) CheckSourceRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, CheckSourceRig)
	}{
		{"a commit nothing ran on lists nothing", checkForgeEmpty},
		{"checks come back by name", checkForgeOrder},
		{"a check keeps its criterion, status, and details", checkForgeFields},
		{"another commit's checks are not listed", checkForgeCommit},
		{"another repository's checks are not listed", checkForgeRepository},
		{"another workspace's checks are not listed", checkForgeScope},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			check.run(t, t.Context(), fresh(t))
		})
	}
}

var forgeCommit = ports.ForgeRequest{Repository: "repo-a", Commit: "abc"}

func listForge(t *testing.T, ctx context.Context, rig CheckSourceRig, scope ledger.WorkspaceID, request ports.ForgeRequest) []ports.ForgeCheck {
	t.Helper()
	checks, err := rig.Source.Checks(ctx, scope, request)
	if err != nil {
		t.Fatal(err)
	}
	return checks
}

func checkForgeEmpty(t *testing.T, ctx context.Context, rig CheckSourceRig) {
	if got := listForge(t, ctx, rig, rig.Scope, forgeCommit); len(got) != 0 {
		t.Errorf("listed %+v", got)
	}
}

func checkForgeOrder(t *testing.T, ctx context.Context, rig CheckSourceRig) {
	rig.Set(rig.Scope, forgeCommit, ports.ForgeCheck{Name: "unit"}, ports.ForgeCheck{Name: "lint"})
	got := listForge(t, ctx, rig, rig.Scope, forgeCommit)
	if len(got) != 2 || got[0].Name != "lint" || got[1].Name != "unit" {
		t.Errorf("listed %+v", got)
	}
}

func checkForgeFields(t *testing.T, ctx context.Context, rig CheckSourceRig) {
	want := ports.ForgeCheck{Name: "unit", Criterion: "c1", Status: workorders.CheckFailed, DetailsURI: "https://ci.example/1"}
	rig.Set(rig.Scope, forgeCommit, want)
	if got := listForge(t, ctx, rig, rig.Scope, forgeCommit); len(got) != 1 || got[0] != want {
		t.Errorf("listed %+v", got)
	}
}

func checkForgeCommit(t *testing.T, ctx context.Context, rig CheckSourceRig) {
	rig.Set(rig.Scope, forgeCommit, ports.ForgeCheck{Name: "unit"})
	other := forgeCommit
	other.Commit = "def"
	if got := listForge(t, ctx, rig, rig.Scope, other); len(got) != 0 {
		t.Errorf("listed %+v", got)
	}
}

func checkForgeRepository(t *testing.T, ctx context.Context, rig CheckSourceRig) {
	rig.Set(rig.Scope, forgeCommit, ports.ForgeCheck{Name: "unit"})
	other := forgeCommit
	other.Repository = "repo-b"
	if got := listForge(t, ctx, rig, rig.Scope, other); len(got) != 0 {
		t.Errorf("listed %+v", got)
	}
}

func checkForgeScope(t *testing.T, ctx context.Context, rig CheckSourceRig) {
	rig.Set(rig.Scope, forgeCommit, ports.ForgeCheck{Name: "unit"})
	if got := listForge(t, ctx, rig, "workspace-b", forgeCommit); len(got) != 0 {
		t.Errorf("listed %+v", got)
	}
}
