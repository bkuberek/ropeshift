package entry

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// A plan approval that removes a consolidated design repository does so in its transaction, which reaches no
// checkout, so the worker still keeps one for the removed record. The intake tick drops it before it reads any
// source, and from then on that checkout is offered to no lane, read by no intake, and listed in no report of the
// worker's checkouts. A repository that is still active keeps its checkout.
func TestTheIntakeTickForgetsTheCheckoutOfARemovedRepository(t *testing.T) {
	root := apitest.ShortDir(t)
	paths := api.Paths{StateDir: filepath.Join(root, "state"), RunDir: filepath.Join(root, "run"), WorktreeDir: filepath.Join(root, "worktrees")}
	if err := os.MkdirAll(paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	kept := soloCheckouts(paths)
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
	repositories := control.NewRepositories(store)
	intake := ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	clones := map[string]*gittest.Repo{}
	for id, folder := range map[string]string{"repo-api": "api", "repo-design": "design"} {
		clone := gittest.New(t)
		clone.Commit("README.md", folder+"\n")
		clones[id] = clone
		if _, err := repositories.Detect(t.Context(), soloScope, control.DetectedRepository{ID: id, DefaultBranch: "main", Directory: folder}, intake); err != nil {
			t.Fatal(err)
		}
		if err := kept.Keep(t.Context(), soloScope, id, clones[id].Dir); err != nil {
			t.Fatal(err)
		}
	}
	// The removal reaches no checkout, as a plan approval's does.
	if _, err := repositories.Remove(t.Context(), soloScope, "repo-design", nil, member); err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(registryOf(store, soloScope), soloScope, soloOptions{}, kept, questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "intake" })
	if at < 0 {
		t.Fatalf("ticks %+v", ticks)
	}
	if err := ticks[at].Run(t.Context()); err != nil {
		t.Fatalf("the intake tick: %v", err)
	}
	// Listed in no report of the worker's checkouts.
	all, err := kept.All(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Repository != "repo-api" {
		t.Errorf("the worker keeps %+v, want the active repository's checkout alone", all)
	}
	// Offered to no lane.
	lookup := laneCheckout(checkoutOf(soloOptions{}, kept))
	if _, err := lookup(t.Context(), string(soloScope), "repo-design"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a lane of the removed repository is offered a checkout: %v", err)
	}
	if path, err := lookup(t.Context(), string(soloScope), "repo-api"); err != nil || path != clones["repo-api"].Dir {
		t.Errorf("a lane of the active repository is offered %q: %v", path, err)
	}
	// Read by no intake.
	sources, err := projectSources(t.Context(), soloOptions{}, kept, soloScope, soloScope, recordsIn(store, soloScope))
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Errorf("intake reads %d sources, want the active repository's alone", len(sources))
	}
}
