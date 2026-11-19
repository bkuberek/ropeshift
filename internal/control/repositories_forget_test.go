package control_test

import (
	"context"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
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

// registersAgainAsItForgets registers a removed repository again, from the clone it was kept at, in the moment between
// the sync's read of the record and its drop of the checkout.
type registersAgainAsItForgets struct {
	*fake.Checkouts
	store ports.Store
}

func (c registersAgainAsItForgets) ForgetAt(ctx context.Context, project ledger.WorkspaceID, repository, path string) (bool, error) {
	err := c.store.Write(ctx, project, "test.register", func(tx ports.WriteTx) error {
		if err := tx.Repositories().Restore(repository); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"},
			Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: repository}, Data: []byte("{}"),
		})
		return err
	})
	if err != nil {
		return false, err
	}
	return c.Checkouts.ForgetAt(ctx, project, repository, path)
}

// A repository registered again from the clone it was kept at, while the sync drops its checkout, keeps that
// checkout.
func TestForgetRemovedKeepsTheCheckoutOfARepositoryRegisteredAgainFromTheSameClone(t *testing.T) {
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
	if err := kept.Keep(ctx, "w", "repo-design", "/work/design"); err != nil {
		t.Fatal(err)
	}
	forgot, err := repositories.ForgetRemoved(ctx, "w", registersAgainAsItForgets{Checkouts: kept, store: store})
	if err != nil {
		t.Fatal(err)
	}
	held, err := kept.Of(ctx, "w")
	if err != nil || held["repo-design"] != "/work/design" || len(forgot) != 0 {
		t.Errorf("after the repository was registered again the worker keeps %v and forgot %v: %v", held, forgot, err)
	}
}

// keepsAnotherCloneAfterTheDrop registers a removed repository again from another clone just after the sync drops
// its checkout, and keeps that clone.
type keepsAnotherCloneAfterTheDrop struct {
	*fake.Checkouts
	store ports.Store
	other string
}

func (c keepsAnotherCloneAfterTheDrop) ForgetAt(ctx context.Context, project ledger.WorkspaceID, repository, path string) (bool, error) {
	dropped, err := c.Checkouts.ForgetAt(ctx, project, repository, path)
	if err != nil || !dropped {
		return dropped, err
	}
	err = c.store.Write(ctx, project, "test.register", func(tx ports.WriteTx) error {
		if err := tx.Repositories().Restore(repository); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventRepositoryDetected, Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"},
			Subject: ledger.Subject{Kind: ledger.SubjectRepository, ID: repository}, Data: []byte("{}"),
		})
		return err
	})
	if err != nil {
		return false, err
	}
	return true, c.Checkouts.Keep(ctx, project, repository, c.other)
}

// A repository registered again from another clone, whose checkout is kept just after the sync drops the old one,
// keeps the new checkout: the sync keeps its own path again only when nothing is kept.
func TestForgetRemovedKeepsTheCheckoutRegisteredFromAnotherCloneAfterTheDrop(t *testing.T) {
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
	if err := kept.Keep(ctx, "w", "repo-design", "/work/old-design"); err != nil {
		t.Fatal(err)
	}
	forgot, err := repositories.ForgetRemoved(ctx, "w", keepsAnotherCloneAfterTheDrop{Checkouts: kept, store: store, other: "/work/new-design"})
	if err != nil {
		t.Fatal(err)
	}
	held, err := kept.Of(ctx, "w")
	if err != nil || held["repo-design"] != "/work/new-design" || len(forgot) != 0 {
		t.Errorf("after the sync the worker keeps %v and forgot %v: %v", held, forgot, err)
	}
}
