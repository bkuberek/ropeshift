package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
)

// A registration from another clone of a repository whose kept checkout still holds it keeps that checkout, and
// records it again, so a sync that dropped it in the meantime does not leave the repository with none.
func TestKeepingTheCheckoutOfARepositoryRecordsItAgain(t *testing.T) {
	ctx := t.Context()
	kept := fake.NewCheckouts()
	kept.Place(ports.Clone{Path: "/work/a", ID: "repo-1", DefaultBranch: "main"})
	kept.Place(ports.Clone{Path: "/work/b", ID: "repo-1", DefaultBranch: "main"})
	// The registration read /work/a as the kept checkout, and a sync has dropped it since.
	held := map[string]string{"repo-1": "/work/a"}
	path, err := keepCheckout(ctx, kept, "w", held, ports.Clone{Path: "/work/b", ID: "repo-1", DefaultBranch: "main"}, "repo-1", false)
	if err != nil || path != "/work/a" {
		t.Fatalf("the registration keeps %q: %v", path, err)
	}
	if now, err := kept.Of(ctx, "w"); err != nil || now["repo-1"] != "/work/a" {
		t.Errorf("the worker keeps %v: %v", now, err)
	}
	// A dry run records nothing.
	dry := fake.NewCheckouts()
	dry.Place(ports.Clone{Path: "/work/a", ID: "repo-1", DefaultBranch: "main"})
	if _, err := keepCheckout(ctx, dry, "w", map[string]string{"repo-1": "/work/a"}, ports.Clone{Path: "/work/b", ID: "repo-1"}, "repo-1", true); err != nil {
		t.Fatal(err)
	}
	if now, err := dry.Of(ctx, "w"); err != nil || len(now) != 0 {
		t.Errorf("a dry run left the worker keeping %v: %v", now, err)
	}
}
