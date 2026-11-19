package worker

import (
	"context"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/clock"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/projectfiles"
)

// silentForge never answers a read of the sign-in until the caller gives up.
type silentForge struct{ *fake.ForgeAccounts }

func (silentForge) Account(ctx context.Context, _ string) (ports.ForgeAccount, error) {
	<-ctx.Done()
	return ports.ForgeAccount{}, ctx.Err()
}

// A forge that does not answer within the check's limit refuses the check, and the caller's own context is not the one
// that ended.
func TestACreationCheckGitHubDoesNotAnswerIsRefused(t *testing.T) {
	t.Parallel()
	folders, kept := fake.NewProjectFolders(), fake.NewCheckouts()
	folders.Dir("/work/tt/repos")
	render := func(name string, _ projectfiles.Data) (string, error) { return name + "\n", nil }
	subject := New(nil, nil, clock.System{}, Config{Name: "atlas", Templates: &TemplateRuntime{
		Folders: folders, Checkouts: kept, Forge: silentForge{fake.NewForgeAccounts()}, Render: render, StateDir: t.TempDir(),
	}})
	creator := &repositoryCreator{w: subject, limit: 10 * time.Millisecond}
	job := ledger.RepositoryCreation{
		Key: ledger.CreationKey("approval-1", "api"), Project: "tt", Name: "api", Path: "/work/tt/repos/api",
		Bound: "/work", PlanningClone: "/work/tt", Create: ledger.RemoteCreate{Forge: ledger.ForgeGitHub, Owner: "test"}, ForgeName: "api",
	}
	checked, err := creator.Check(t.Context(), job)
	if err != nil || checked.Refusal == nil {
		t.Fatalf("the check is %+v (%v), want refused", checked, err)
	}
	if want := "GitHub did not answer within 30 seconds; read the preview again"; checked.Refusal.Detail != want {
		t.Errorf("the refusal says %q, want %q", checked.Refusal.Detail, want)
	}
}
