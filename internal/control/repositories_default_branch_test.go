package control_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// A default branch git names that ropeshift takes as no branch, such as a remote HEAD that holds a character that is
// not seen or one that starts with a plus, is stored empty when a repository is detected, so its lanes wait for a base
// a member names. A later look that names a branch stores it.
func TestADetectedDefaultBranchThatIsNoBranchIsStoredEmpty(t *testing.T) {
	t.Parallel()
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
	for _, tc := range []struct{ named, stored string }{{"ma\u200bin", ""}, {"main", "main"}, {"+main", ""}} {
		found := control.DetectedRepository{ID: "repo-api", Remote: "https://example.com/acme/api.git", DefaultBranch: tc.named, Directory: "api"}
		record, err := repositories.Detect(ctx, "w", found, member)
		if err != nil || record.DefaultBranch != tc.stored {
			t.Errorf("a look that names %q stores %q (%v), want %q", tc.named, record.DefaultBranch, err, tc.stored)
		}
	}
}
