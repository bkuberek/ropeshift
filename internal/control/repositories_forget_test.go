package control_test

import (
	"context"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// readBeforeReAdd is this worker's checkouts as a sync read them, a moment before a member registered the removed
// repository again from another clone: Of answers what the sync read, and the checkouts themselves hold the new path.
type readBeforeReAdd struct {
	*fake.Checkouts
	read map[string]string
}

func (c readBeforeReAdd) Of(context.Context, ledger.WorkspaceID) (map[string]string, error) {
	return c.read, nil
}

// The sync drops a removed repository's checkout only at the path it read, so a repository registered again from
// another clone in the meantime keeps the checkout that registration kept.
func TestForgetRemovedKeepsACheckoutKeptAgainAtAnotherPath(t *testing.T) {
	ctx := t.Context()
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "w"}, member); err != nil {
		t.Fatal(err)
	}
	repositories := control.NewRepositories(store)
	if _, err := repositories.Detect(ctx, "w", control.DetectedRepository{ID: "repo-design", DefaultBranch: "main", Directory: "design"}, member); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Remove(ctx, "w", "repo-design", nil, member); err != nil {
		t.Fatal(err)
	}
	kept := fake.NewCheckouts()
	if err := kept.Keep(ctx, "w", "repo-design", "/work/new-design"); err != nil {
		t.Fatal(err)
	}
	sync := readBeforeReAdd{Checkouts: kept, read: map[string]string{"repo-design": "/work/old-design"}}
	forgot, err := repositories.ForgetRemoved(ctx, "w", sync)
	if err != nil {
		t.Fatal(err)
	}
	held, err := kept.Of(ctx, "w")
	if err != nil || held["repo-design"] != "/work/new-design" || len(forgot) != 0 {
		t.Errorf("after the sync the worker keeps %v and forgot %v: %v", held, forgot, err)
	}
	// At the path the sync read, the removed repository's checkout is dropped.
	forgot, err = repositories.ForgetRemoved(ctx, "w", kept)
	if held, _ := kept.Of(ctx, "w"); err != nil || len(held) != 0 || len(forgot) != 1 {
		t.Errorf("the removed repository's checkout reads %v, forgot %v: %v", held, forgot, err)
	}
}
