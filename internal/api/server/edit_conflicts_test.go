package server_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

func conflictItems(t *testing.T, d *apitest.Daemon) []api.NeedItem {
	t.Helper()
	var out []api.NeedItem
	for _, item := range readView[api.NeedsYou](t, d, api.ViewNeedsYou, "").Items {
		if item.Kind == api.NeedConflict {
			out = append(out, item)
		}
	}
	return out
}

// conflictWorld runs the work order wo-1 on a lane whose branch changed plans/plan.md and plans/lane.md, in the clone of
// repository-1 where a person left plans/plan.md uncommitted and pushed a change to docs/guide.md, which the lane
// changed too.
func conflictWorld(t *testing.T) (*apitest.Daemon, *apitest.World, *fake.CloneEdits) {
	t.Helper()
	return conflictWorldWith(t, apitest.Options{})
}

func conflictWorldWith(t *testing.T, options apitest.Options) (*apitest.Daemon, *apitest.World, *fake.CloneEdits) {
	t.Helper()
	return conflictWorldReading(t, options, func(edits *fake.CloneEdits) ports.CloneEdits { return edits })
}

// conflictWorldReading is conflictWorld whose daemon reads the clones through what wrap makes of the fake.
func conflictWorldReading(t *testing.T, options apitest.Options, wrap func(*fake.CloneEdits) ports.CloneEdits) (*apitest.Daemon, *apitest.World, *fake.CloneEdits) {
	t.Helper()
	edits := fake.NewCloneEdits()
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin", Edits: []ledger.CloneEdit{
		{Path: "plans/plan.md", State: ledger.EditUncommitted}, {Path: "notes.md", State: ledger.EditUntracked},
	}})
	edits.Diverge("/srv/repository-1", "lane/lane-wo-1", ledger.Divergence{
		Fork: "fork", Base: "origin/main", Lane: []string{"docs/guide.md", "plans/lane.md", "plans/plan.md"}, Pushed: []string{"docs/guide.md", "docs/other.md"},
	})
	d, w := outsideWorldWith(t, options, wrap(edits), "repository-1")
	w.Repository("repository-1", ledger.PersonMerges)
	w.Running("wo-1", "Retry within a budget")
	return d, w, edits
}

// FR-UI-14: a lane's change to a file a person changed too, in their clone or in a push, is a conflict in Needs you
// for a member to settle, naming both sides and the commands of each way to settle it.
func TestALanesChangeToAFileAPersonChangedIsAConflictInNeedsYou(t *testing.T) {
	t.Parallel()
	d, _, _ := conflictWorld(t)
	if got := conflictItems(t, d); len(got) != 0 {
		t.Fatalf("Needs you holds a conflict no reading found: %+v", got)
	}
	list, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Conflicts) != 1 || list.Conflicts[0].WorkOrder != "wo-1" {
		t.Fatalf("the list's conflicts %+v", list.Conflicts)
	}
	items := conflictItems(t, d)
	if len(items) != 1 || items[0].ID != "wo-1" || items[0].Conflict == nil {
		t.Fatalf("Needs you holds %+v", items)
	}
	need := items[0].Conflict
	if need.Lane != "lane-wo-1" || need.Branch != "lane/lane-wo-1" || need.Base != "origin/main" || need.Clone != "/srv/repository-1" || need.Title != "Retry within a budget" {
		t.Errorf("the conflict %+v", need)
	}
	want := []api.ConflictPathView{{Path: "docs/guide.md", Person: ledger.PersonPushed}, {Path: "plans/plan.md", Person: string(ledger.EditUncommitted)}}
	if !slices.Equal(need.Paths, want) {
		t.Errorf("the paths %+v, want %+v", need.Paths, want)
	}
	kinds := []string{}
	for _, option := range need.Options {
		kinds = append(kinds, option.Kind)
	}
	if !slices.Equal(kinds, []string{ledger.SettleKeepBoth, ledger.SettleKeepPerson, ledger.SettleStop}) {
		t.Fatalf("the options %v", kinds)
	}
	both := strings.Join(need.Options[0].Commands, "\n")
	if !strings.Contains(both, "git update-index --add --remove -- plans/plan.md &&") || !strings.Contains(both, "ropeshift message send lane-wo-1 --project default") {
		t.Errorf("keeping both runs:\n%s", both)
	}
}

// A conflict leaves Needs you once its work order ends, before any reading, and once a reading no longer finds it.
func TestAConflictLeavesNeedsYouOnceItIsSettled(t *testing.T) {
	t.Parallel()
	d, w, edits := conflictWorld(t)
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin"})
	edits.Diverge("/srv/repository-1", "lane/lane-wo-1", ledger.Divergence{Fork: "fork", Base: "origin/main", Lane: []string{"plans/plan.md"}})
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Errorf("a conflict a reading no longer finds stays: %+v", got)
	}

	other, w2, _ := conflictWorld(t)
	if _, err := other.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	w2.Cancel("wo-1")
	if got := conflictItems(t, other); len(got) != 0 {
		t.Errorf("the conflict of canceled work stays: %+v", got)
	}
	_ = w
}

// A reading keeps the time it first found a conflict, so the conflict keeps its place in Needs you.
func TestAConflictKeepsTheTimeItWasFirstFound(t *testing.T) {
	t.Parallel()
	d, _, _ := conflictWorld(t)
	first, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := conflictItems(t, d)
	if len(found) != 1 {
		t.Fatalf("Needs you holds %+v", found)
	}
	since := found[0].Since
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again := conflictItems(t, d); len(again) != 1 || again[0].Since != since || since != first.AsOf {
		t.Errorf("found at %d, then %+v, read at %d", since, again, first.AsOf)
	}
}

// TestGoldenConflictNeed writes what Needs you reads of a conflict, from a real ledger: a lane whose branch changed a file
// a person left uncommitted and a file a person pushed, with the three ways to settle it.
func TestGoldenConflictNeed(t *testing.T) {
	t.Parallel()
	clock := apitest.NewClock()
	d, _, _ := conflictWorldWith(t, apitest.Options{Clock: clock.Dialect})
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	view := readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")
	view.Items = conflictItems(t, d)
	if len(view.Items) != 1 {
		t.Fatalf("Needs you holds %d conflicts", len(view.Items))
	}
	holdsGolden(t, "conflict-needs-you", steadyTimes(steady(pretty(t, view))))
}

// A reading that cannot read a clone, or a lane's branch in it, keeps the conflict it found before, with the time it was
// first found, so a conflict is never dropped for a read that failed. A branch the clone no longer holds is no conflict.
func TestAReadingThatCannotReadTheCloneKeepsTheConflict(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	found := conflictItems(t, d)
	if len(found) != 1 {
		t.Fatalf("Needs you holds %+v", found)
	}
	edits.FailDiverge("/srv/repository-1", "lane/lane-wo-1", errors.New("fatal: unable to read tree"))
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if kept := conflictItems(t, d); len(kept) != 1 || kept[0].Since != found[0].Since {
		t.Errorf("a branch git could not read dropped the conflict: %+v", kept)
	}
	edits.Fail("/srv/repository-1", errors.New("fatal: index file corrupt"))
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if kept := conflictItems(t, d); len(kept) != 1 || kept[0].Since != found[0].Since {
		t.Errorf("a clone git could not read dropped the conflict: %+v", kept)
	}
}

// A repository whose merge method rewrites commits lands a work order's change in commits that carry no trailer, on
// the forge or by a local rebase, so its base's side reads as nobody's: only the clone's side of a conflict is shown.
func TestARepositoryThatRewritesCommitsShowsNoPushedSide(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	squash := ledger.MergeSquash
	if _, err := control.NewRepositories(d.Store).Change(t.Context(), apitest.Scope, "repository-1", control.RepositoryChange{MergeMethod: &squash}, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	items := conflictItems(t, d)
	if len(items) != 1 || !slices.Equal(items[0].Conflict.Paths, []api.ConflictPathView{{Path: "plans/plan.md", Person: string(ledger.EditUncommitted)}}) {
		t.Fatalf("Needs you holds %+v", items)
	}
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin"})
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Errorf("a squash-landed change reads as a person's push: %+v", got)
	}
}

// A commit the ledger records as the forge's merge of a work order, and its head, are no person's push in a repository
// that merges with merge commits either.
func TestACommitTheForgeLandedIsGivenAsLandedWork(t *testing.T) {
	t.Parallel()
	d, w, edits := conflictWorld(t)
	w.Created("wo-landed", "Landed on the forge")
	if err := d.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
		if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
			WorkOrder: "wo-landed", Repository: "repository-1", Worker: apitest.WorldWorker,
			PullRequest: ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, Base: "main", Branch: "lane/x", HeadCommit: "head-of-7", MergeCommit: "squash-of-7", MergedAt: "2026-11-20T10:00:00Z"},
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventPullRequestUpdated, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}, Subject: ledger.Subject{Kind: ledger.SubjectPullRequest, ID: "wo-landed"},
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	landed := edits.Landed()
	if len(landed) == 0 || !slices.Contains(landed[len(landed)-1], "squash-of-7") || !slices.Contains(landed[len(landed)-1], "head-of-7") {
		t.Errorf("the read of the lane's branch was given %v as landed work", landed)
	}
}

// A lane's branch that is really gone ends its conflict: the lane's work left the clone.
func TestAConflictWhoseBranchIsGoneLeavesNeedsYou(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	edits.Undiverge("/srv/repository-1", "lane/lane-wo-1")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Errorf("the conflict of a branch that is gone stays: %+v", got)
	}
}

// Keep both changes ends the conflict once the lane's branch holds the branch the person's files were committed on,
// which is named the same at every reading while the files stay as they are. The card says so (FR-UI-13).
func TestKeepingBothChangesEndsTheConflictOnceTheLaneHoldsTheirBranch(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	edits.Diverge("/srv/repository-1", "lane/lane-wo-1", ledger.Divergence{Fork: "fork", Base: "origin/main", Lane: []string{"plans/plan.md"}})
	edits.SetContents("/srv/repository-1", "plans/plan.md", "blob-1")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	first := conflictItems(t, d)
	if len(first) != 1 {
		t.Fatalf("Needs you holds %+v", first)
	}
	branch := first[0].Conflict.Options[0].Branch
	if branch != ledger.EditsBranch("wo-1", map[string]string{"plans/plan.md": "blob-1"}) {
		t.Errorf("keep both commits on %q", branch)
	}
	if want := "This clears when the lane's branch holds " + branch + " or no longer changes these files, or when the lane's work is merged or stopped."; first[0].Conflict.Clears != want {
		t.Errorf("the card says %q, want %q", first[0].Conflict.Clears, want)
	}
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again := conflictItems(t, d); len(again) != 1 || again[0].Conflict.Options[0].Branch != branch {
		t.Errorf("a later reading names another branch: %+v", again)
	}
	edits.Hold("/srv/repository-1", "lane/lane-wo-1", branch)
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Errorf("the conflict stays once the lane's branch holds the person's: %+v", got)
	}
	edits.SetContents("/srv/repository-1", "plans/plan.md", "blob-2")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 1 || got[0].Conflict.Options[0].Branch == branch {
		t.Errorf("a person's later change is no conflict: %+v", got)
	}
}

// stopping reads a clone as the fake does, and once armed it ends the reading's context at its next read of a clone and
// finds nothing in it, as a reading that stopped part way would.
type stopping struct {
	*fake.CloneEdits
	stop atomic.Pointer[context.CancelFunc]
}

func (s *stopping) Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error) {
	if stop := s.stop.Swap(nil); stop != nil {
		(*stop)()
		return ledger.CloneEdits{Edits: []ledger.CloneEdit{}}, nil
	}
	return s.CloneEdits.Edits(ctx, clone, limit)
}

// A reading whose context ends part way keeps nothing it found, so the conflict an earlier reading found stays.
func TestAReadingWhoseContextEndedKeepsTheConflict(t *testing.T) {
	t.Parallel()
	reader := &stopping{}
	d, _, edits := conflictWorldReading(t, apitest.Options{}, func(edits *fake.CloneEdits) ports.CloneEdits {
		reader.CloneEdits = edits
		return reader
	})
	edits.Diverge("/srv/repository-1", "lane/lane-wo-1", ledger.Divergence{Fork: "fork", Base: "origin/main", Lane: []string{"plans/plan.md"}})
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	found := conflictItems(t, d)
	if len(found) != 1 {
		t.Fatalf("Needs you holds %+v", found)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	reader.stop.Store(&cancel)
	if _, err := d.Config.OutsideEdits.Read(ctx, d.Store, apitest.Scope, "default"); err == nil {
		t.Error("a reading whose context ended answered as if it read everything")
	}
	if kept := conflictItems(t, d); len(kept) != 1 || kept[0].Since != found[0].Since {
		t.Errorf("a reading that stopped part way dropped the conflict: %+v", kept)
	}
}

// gated reads a clone as the fake does, and once armed it holds its next read of a clone until released, then finds
// nothing in it.
type gated struct {
	*fake.CloneEdits
	held     atomic.Bool
	reached  chan struct{}
	released chan struct{}
}

func (g *gated) Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error) {
	if g.held.CompareAndSwap(true, false) {
		close(g.reached)
		<-g.released
		return ledger.CloneEdits{Edits: []ledger.CloneEdit{}}, nil
	}
	return g.CloneEdits.Edits(ctx, clone, limit)
}

// A reading that began before another and ends after it replaces nothing the newer reading found.
func TestAnOlderReadingNeverReplacesANewerOne(t *testing.T) {
	t.Parallel()
	reader := &gated{reached: make(chan struct{}), released: make(chan struct{})}
	d, _, edits := conflictWorldReading(t, apitest.Options{}, func(edits *fake.CloneEdits) ports.CloneEdits {
		reader.CloneEdits = edits
		return reader
	})
	edits.Diverge("/srv/repository-1", "lane/lane-wo-1", ledger.Divergence{Fork: "fork", Base: "origin/main", Lane: []string{"plans/plan.md"}})
	readings := d.Config.OutsideEdits
	reader.held.Store(true)
	older := make(chan error, 1)
	go func() {
		_, err := readings.Read(t.Context(), d.Store, apitest.Scope, "default")
		older <- err
	}()
	<-reader.reached
	if _, err := readings.Read(t.Context(), d.Store, apitest.Scope, "default"); err != nil {
		t.Fatal(err)
	}
	close(reader.released)
	if err := <-older; err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 1 {
		t.Errorf("an older reading replaced the conflict a newer one found: %+v", got)
	}
}
