package entry

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
)

// countedEdits counts the clones a reading reads.
type countedEdits struct {
	*fake.CloneEdits
	reads atomic.Int32
}

func (c *countedEdits) Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error) {
	c.reads.Add(1)
	return c.CloneEdits.Edits(ctx, clone, limit)
}

// The clones are read on the intake tick in every active project, so a conflict between a lane and a person's edits
// reaches Needs you though nobody opens Repositories (FR-UI-14).
func TestTheOutsideEditsTickReadsEveryProjectsClones(t *testing.T) {
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: soloScope}, tickActor("solo")); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewRepositories(store).Detect(t.Context(), soloScope, control.DetectedRepository{ID: "repository-1", DefaultBranch: "main"}, tickActor("intake")); err != nil {
		t.Fatal(err)
	}
	edits := &countedEdits{CloneEdits: fake.NewCloneEdits()}
	starts := &control.Starts{Bases: fake.NewWorktrees(), Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/srv/repository-1", nil }}
	config := server.Config{OutsideEdits: control.NewOutsideEdits(starts, edits, "lane/")}
	tick := outsideEditsTick(config, registryOf(store, soloScope), 30*time.Second, time.Minute)
	if tick.Name != "outside-edits" || tick.Every != 30*time.Second {
		t.Errorf("the tick %s runs every %s", tick.Name, tick.Every)
	}
	if err := tick.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := edits.reads.Load(); got != 1 {
		t.Errorf("the tick read %d clones, want 1", got)
	}
}

// stuckEdits never answers a read of a clone until its context ends.
type stuckEdits struct{ *fake.CloneEdits }

func (stuckEdits) Edits(ctx context.Context, _ string, _ int) (ledger.CloneEdits, error) {
	<-ctx.Done()
	return ledger.CloneEdits{}, ctx.Err()
}

// Each project's read of its clones has a time of its own, so a clone git never answers holds no tick past it.
func TestAReadOfTheClonesStopsAtItsOwnTimeout(t *testing.T) {
	store, err := fake.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(t.Context(), ledger.Workspace{ID: soloScope}, tickActor("solo")); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewRepositories(store).Detect(t.Context(), soloScope, control.DetectedRepository{ID: "repository-1", DefaultBranch: "main"}, tickActor("intake")); err != nil {
		t.Fatal(err)
	}
	starts := &control.Starts{Bases: fake.NewWorktrees(), Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return "/srv/repository-1", nil }}
	config := server.Config{OutsideEdits: control.NewOutsideEdits(starts, stuckEdits{fake.NewCloneEdits()}, "lane/")}
	tick := outsideEditsTick(config, registryOf(store, soloScope), 30*time.Second, 50*time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- tick.Run(t.Context()) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a read that ran out of time answered as if it read everything")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the tick waited past its read's timeout")
	}
}
