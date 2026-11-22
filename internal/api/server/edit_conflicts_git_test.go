package server_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/gittest"
)

// TestAConflictOnRealGitIsFoundAndClearsOnceTheLaneHoldsThePersonsFiles runs a conflict through real git from end to
// end: a lane's branch and a person's uncommitted file reach Needs you, the person runs the commands Keep both changes
// gives, the lane merges that branch keeping both changes, and the next reading finds no conflict.
func TestAConflictOnRealGitIsFoundAndClearsOnceTheLaneHoldsThePersonsFiles(t *testing.T) {
	t.Parallel()
	origin := gittest.New(t)
	origin.Commit("plans/plan.md", "the plan\n")
	clone := origin.Clone("registered")
	git := gitcli.Worktrees{Env: clone.Env()}
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return clone.Dir, nil }}
	d := apitest.StartWith(t, apitest.Options{Starts: starts, LaneBranchPrefix: "lane/", OutsideEdits: control.NewOutsideEdits(starts, git, "lane/")})
	w := apitest.NewWorld(t, d)
	w.Repository("repository-1", ledger.PersonMerges)
	w.Running("wo-1", "Retry within a budget")

	worktree := filepath.Join(t.TempDir(), "lane")
	clone.Git("worktree", "add", "--quiet", "-b", "lane/lane-wo-1", worktree, "origin/main")
	lane := &gittest.Repo{T: t, Dir: worktree, Home: clone.Home}
	lane.Commit("plans/plan.md", "the plan, as the lane wrote it\n")
	if err := os.WriteFile(filepath.Join(clone.Dir, "plans", "plan.md"), []byte("the plan, as the person wrote it\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	items := conflictItems(t, d)
	if len(items) != 1 || len(items[0].Conflict.Paths) != 1 || items[0].Conflict.Paths[0] != (api.ConflictPathView{Path: "plans/plan.md", Person: string(ledger.EditUncommitted)}) {
		t.Fatalf("Needs you holds %+v", items)
	}
	both := items[0].Conflict.Options[0]
	commit := exec.Command("sh", "-c", strings.Join(both.Commands[:len(both.Commands)-1], "\n"))
	commit.Dir, commit.Env = t.TempDir(), clone.Env()
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("the commands of Keep both changes failed: %v\n%s", err, out)
	}
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if again := conflictItems(t, d); len(again) != 1 {
		t.Fatalf("the conflict cleared before the lane held the person's files: %+v", again)
	}

	// The lane merges the branch and keeps both changes.
	if _, err := lane.Try("merge", "--no-edit", both.Branch); err == nil {
		t.Fatal("the two changes merged with no conflict")
	}
	if err := os.WriteFile(filepath.Join(worktree, "plans", "plan.md"), []byte("the plan, as the lane and the person wrote it\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lane.Git("add", "plans/plan.md")
	lane.Git("commit", "--quiet", "--no-edit")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Errorf("the conflict stays once the lane's branch holds the person's files: %+v", got)
	}
	if status := clone.Git("status", "--porcelain"); status != "M plans/plan.md" {
		t.Errorf("the person's clone changed: %q", status)
	}
}

// commitOn writes a file in a repository and commits it with a message.
func commitOn(t *testing.T, repo *gittest.Repo, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo.Dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	repo.Git("add", "--", name)
	repo.Git("commit", "--quiet", "-m", message)
	return repo.Git("rev-parse", "HEAD")
}

// workPersonMergesOnTheForge takes a work order to a pull request a person merges on the forge, and returns its lane. The
// lane finishes at head and the pull request opens at it, the review passes, and the worker's read of the merge is the
// ledger's mergedOnForge: the work ends with the merge commit the forge gave and with no range recorded.
func workPersonMergesOnTheForge(t *testing.T, d *apitest.Daemon, w *apitest.World, id, head, mergeCommit string) {
	t.Helper()
	lane := w.Running(id, "Landed on the forge")
	branch := "lane/lane-" + id
	lane.Finish(workorders.LaneResult{
		Report: workorders.Report{Outcome: workorders.OutcomeCompleted, Checks: []workorders.CheckResult{{Name: "go test", Passed: true, Criterion: "c1"}}},
		Branch: branch, Head: head,
	})
	open := ledger.PullRequest{
		Number: 7, URL: "https://forge.example/acme/app/pull/7", State: ledger.PullRequestOpen, Base: "main", Branch: branch, HeadCommit: head, Mergeable: "MERGEABLE",
	}
	if err := d.Store.Write(t.Context(), apitest.Scope, "test.pull_request", func(tx ports.WriteTx) error {
		if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{PullRequest: open, WorkOrder: id, Repository: "repository-1", Worker: apitest.WorldWorker}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.pull_request", Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"}, Subject: ledger.Subject{Kind: "work_order", ID: id}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	w.Reviewed(id, workorders.CriterionVerdict{Criterion: "c1", Verdict: workorders.VerdictPass, Reasons: "It works.", Citations: []string{"guide.md:1"}})
	merged := open
	merged.State, merged.MergeCommit, merged.MergedAt = ledger.PullRequestMerged, mergeCommit, "2026-11-20T10:00:00Z"
	lane.Upload(ledger.UploadPullRequestRead, ledger.PullRequestReport{WorkOrder: id, PullRequest: merged})
	var order workorders.WorkOrder
	if err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) (err error) {
		order, err = tx.WorkOrders().Get(id)
		return err
	}); err != nil || order.State != workorders.Done {
		t.Fatalf("the work order the person merged on the forge reads %s, want done: %v", order.State, err)
	}
}

// A work order a person merged on the forge lands commits with no trailer and with no range the ledger recorded, and its
// commits are no person's push: a squash is the one commit the pull request's merge commit names, and a rebase writes
// each of the pull request's commits. A person's own push still is a conflict once the lane's branch holds the base the
// merge landed in, on real git from end to end. While a rebase the ledger recorded no range for sits between the lane's
// branch and the base, the base's side is not shown, as nothing tells its commits from a person's.
func TestAWorkOrderAPersonMergedOnTheForgeIsNoConflictAndAPersonsPushLaterIs(t *testing.T) {
	t.Parallel()
	for _, method := range []ledger.MergeMethod{ledger.MergeSquash, ledger.MergeRebase} {
		t.Run(string(method), func(t *testing.T) {
			t.Parallel()
			origin := gittest.New(t)
			origin.Commit("docs/guide.md", "the guide\n")
			origin.Commit("docs/other.md", "other\n")
			clone := origin.Clone("registered")
			git := gitcli.Worktrees{Env: clone.Env()}
			starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return clone.Dir, nil }}
			d := apitest.StartWith(t, apitest.Options{Starts: starts, LaneBranchPrefix: "lane/", OutsideEdits: control.NewOutsideEdits(starts, git, "lane/")})
			w := apitest.NewWorld(t, d)
			w.Repository("repository-1", ledger.PersonMerges)
			target, binding := ledger.ReviewOnPullRequest, "github.com/acme/app"
			change := control.RepositoryChange{MergeMethod: &method, ReviewTarget: &target, ForgeBinding: &binding}
			if _, err := control.NewRepositories(d.Store).Change(t.Context(), apitest.Scope, "repository-1", change, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}); err != nil {
				t.Fatal(err)
			}
			w.Running("wo-1", "Retry within a budget")
			worktree := filepath.Join(t.TempDir(), "lane")
			clone.Git("worktree", "add", "--quiet", "-b", "lane/lane-wo-1", worktree, "origin/main")
			lane := &gittest.Repo{T: t, Dir: worktree, Home: clone.Home}
			lane.Commit("docs/guide.md", "the guide, as the lane wrote it\n")
			lane.Commit("docs/other.md", "other, as the lane wrote it\n")

			// Another work order's pull request lands on the forge, changing the same two files, and a person merges it.
			switch method {
			case ledger.MergeSquash:
				commitOn(t, origin, "docs/guide.md", "the guide, squashed on the forge\n", "Change the guide and the rest (#7)")
			case ledger.MergeRebase:
				commitOn(t, origin, "docs/guide.md", "the guide, rebased on the forge\n", "change the guide")
				commitOn(t, origin, "docs/other.md", "other, rebased on the forge\n", "change the rest")
			}
			workPersonMergesOnTheForge(t, d, w, "wo-landed", strings.Repeat("c", 40), origin.Git("rev-parse", "HEAD"))
			clone.Git("fetch", "--quiet", "origin")
			if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := conflictItems(t, d); len(got) != 0 {
				t.Fatalf("work a person merged on the forge by %s reads as a person's push: %+v", method, got[0].Conflict.Paths)
			}

			// A person pushes to the guide. Beside a squash it is a conflict at once; a rebase the ledger recorded no range for
			// hides the base's side while it stays between the lane's branch and the base.
			commitOn(t, origin, "docs/guide.md", "the guide, as a person wrote it\n", "a person's change")
			clone.Git("fetch", "--quiet", "origin")
			if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
				t.Fatal(err)
			}
			items := conflictItems(t, d)
			if method == ledger.MergeSquash && (len(items) != 1 || len(items[0].Conflict.Paths) != 1 || items[0].Conflict.Paths[0].Path != "docs/guide.md") {
				t.Fatalf("a person's push to a repository that squashes reads %+v", items)
			}
			if method == ledger.MergeRebase && len(items) != 0 {
				t.Fatalf("the base's side shows beside a rebase the ledger recorded no range for: %+v", items[0].Conflict.Paths)
			}

			// The lane's branch takes the base, and the person pushes again: nothing the ledger cannot name lies between them.
			lane.Git("merge", "--quiet", "-s", "ours", "--no-edit", "origin/main")
			commitOn(t, origin, "docs/other.md", "other, as a person wrote it\n", "another change of a person's")
			clone.Git("fetch", "--quiet", "origin")
			if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
				t.Fatal(err)
			}
			items = conflictItems(t, d)
			if len(items) != 1 || len(items[0].Conflict.Paths) != 1 || items[0].Conflict.Paths[0] != (api.ConflictPathView{Path: "docs/other.md", Person: ledger.PersonPushed}) {
				t.Fatalf("a person's push after the lane took the base reads %+v", items)
			}
		})
	}
}

// A rebase a worker made records the range it wrote, so its commits are no person's push whatever stands beside them,
// and a person's own push is still a conflict, on real git from end to end.
func TestARangeAWorkerRecordedLeavesItsCommitsOutOfAPersonsPushes(t *testing.T) {
	t.Parallel()
	origin := gittest.New(t)
	origin.Commit("docs/guide.md", "the guide\n")
	origin.Commit("docs/other.md", "other\n")
	clone := origin.Clone("registered")
	git := gitcli.Worktrees{Env: clone.Env()}
	starts := &control.Starts{Bases: git, Checkout: func(context.Context, ledger.WorkspaceID, string) (string, error) { return clone.Dir, nil }}
	d := apitest.StartWith(t, apitest.Options{Starts: starts, LaneBranchPrefix: "lane/", OutsideEdits: control.NewOutsideEdits(starts, git, "lane/")})
	w := apitest.NewWorld(t, d)
	w.Repository("repository-1", ledger.PersonMerges)
	rebase := ledger.MergeRebase
	if _, err := control.NewRepositories(d.Store).Change(t.Context(), apitest.Scope, "repository-1", control.RepositoryChange{MergeMethod: &rebase}, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}); err != nil {
		t.Fatal(err)
	}
	w.Running("wo-1", "Retry within a budget")
	worktree := filepath.Join(t.TempDir(), "lane")
	clone.Git("worktree", "add", "--quiet", "-b", "lane/lane-wo-1", worktree, "origin/main")
	lane := &gittest.Repo{T: t, Dir: worktree, Home: clone.Home}
	lane.Commit("docs/guide.md", "the guide, as the lane wrote it\n")
	lane.Commit("docs/other.md", "other, as the lane wrote it\n")

	w.Created("wo-landed", "Rebased on the forge")
	before := origin.Git("rev-parse", "HEAD")
	commitOn(t, origin, "docs/guide.md", "the guide, rebased on the forge\n", "change the guide")
	after := commitOn(t, origin, "docs/other.md", "other, rebased on the forge\n", "change the rest")
	if err := d.Store.Write(t.Context(), apitest.Scope, "test.merged", func(tx ports.WriteTx) error {
		if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
			WorkOrder: "wo-landed", Repository: "repository-1", Worker: apitest.WorldWorker,
			PullRequest: ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, Base: "main", Branch: "lane/x", HeadCommit: strings.Repeat("c", 40), MergeCommit: after, MergedAt: "2026-11-20T10:00:00Z"},
		}); err != nil {
			return err
		}
		data := `{"landed":{"before":"` + before + `","after":"` + after + `"}}`
		_, err := tx.Append(ledger.EventDraft{
			Type: ledger.EventIntegrationMerged, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"},
			Subject: ledger.Subject{Kind: ledger.SubjectIntegration, ID: "wo-landed"}, Data: []byte(data),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	clone.Git("fetch", "--quiet", "origin")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := conflictItems(t, d); len(got) != 0 {
		t.Fatalf("work a worker rebased reads as a person's push: %+v", got[0].Conflict.Paths)
	}
	commitOn(t, origin, "docs/guide.md", "the guide, as a person wrote it\n", "a person's change")
	clone.Git("fetch", "--quiet", "origin")
	if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
		t.Fatal(err)
	}
	items := conflictItems(t, d)
	if len(items) != 1 || len(items[0].Conflict.Paths) != 1 || items[0].Conflict.Paths[0] != (api.ConflictPathView{Path: "docs/guide.md", Person: ledger.PersonPushed}) {
		t.Fatalf("a person's push beside a range a worker recorded reads %+v", items)
	}
}

// A page that reads the clones often reads them again no sooner than the window Reusing set, and answers with the
// reading it has.
func TestAReadingIsReusedWithinItsWindow(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	d.Config.OutsideEdits.Reusing(time.Hour)
	first, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin"})
	again, err := d.Client.OutsideEdits(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if again.AsOf != first.AsOf || len(again.Clones[0].Edits) != len(first.Clones[0].Edits) {
		t.Errorf("a reading within its window read the clones again: %+v", again)
	}
	if _, err := d.Config.OutsideEdits.Read(t.Context(), d.Store, apitest.Scope, "default"); err != nil {
		t.Fatal(err)
	}
	if fresh, err := d.Client.OutsideEdits(t.Context()); err != nil || len(fresh.Clones[0].Edits) != 0 {
		t.Errorf("the page reuses an older reading than the newest: %+v, %v", fresh, err)
	}
}

// A member who reads again is answered with a reading made after the request, not the one the window holds, and the
// reading it makes is the one the next page reads.
func TestAFreshReadIgnoresTheReuseWindow(t *testing.T) {
	t.Parallel()
	d, _, edits := conflictWorld(t)
	d.Config.OutsideEdits.Reusing(time.Hour)
	first, err := d.Client.OutsideEdits(t.Context())
	if err != nil || len(first.Clones[0].Edits) == 0 {
		t.Fatalf("the first reading holds %+v: %v", first, err)
	}
	edits.Set("/srv/repository-1", ledger.CloneEdits{Branch: "main", Head: "abc", Remote: "origin"})
	if reused, err := d.Client.OutsideEdits(t.Context()); err != nil || len(reused.Clones[0].Edits) != len(first.Clones[0].Edits) {
		t.Fatalf("a read within the window read the clones again: %+v, %v", reused, err)
	}
	fresh, err := d.Client.FreshOutsideEdits(t.Context())
	if err != nil || len(fresh.Clones[0].Edits) != 0 || fresh.AsOf <= first.AsOf {
		t.Fatalf("a fresh read listed %d paths as of %d, want none as of a time after %d: %v", len(fresh.Clones[0].Edits), fresh.AsOf, first.AsOf, err)
	}
	if next, err := d.Client.OutsideEdits(t.Context()); err != nil || next.AsOf != fresh.AsOf {
		t.Errorf("the page after a fresh read answered %+v, want it", next)
	}
}

// heldClones is a machine whose clones answer only when a test lets them, and that counts the reads of a clone running at once.
type heldClones struct {
	ports.CloneEdits
	release chan struct{}
	now     atomic.Int64
	most    atomic.Int64
	reads   atomic.Int64
}

func (g *heldClones) Edits(ctx context.Context, clone string, limit int) (ledger.CloneEdits, error) {
	g.reads.Add(1)
	running := g.now.Add(1)
	defer g.now.Add(-1)
	for {
		if most := g.most.Load(); running <= most || g.most.CompareAndSwap(most, running) {
			break
		}
	}
	select {
	case <-g.release:
	case <-ctx.Done():
		return ledger.CloneEdits{}, ctx.Err()
	}
	return g.CloneEdits.Edits(ctx, clone, limit)
}

// A fresh read is bounded: one that waits on a clone ends once the bound the daemon holds is up, and a member never has
// more than one running for the project at a time, however many read again at once.
func TestAFreshReadIsBounded(t *testing.T) {
	t.Parallel()
	t.Run("in time", func(t *testing.T) {
		t.Parallel()
		var machine *heldClones
		d, _, _ := conflictWorldReading(t, apitest.Options{}, func(edits *fake.CloneEdits) ports.CloneEdits {
			machine = &heldClones{CloneEdits: edits, release: make(chan struct{})}
			return machine
		})
		d.Config.OutsideEdits.Bounding(100 * time.Millisecond)
		// The member's own request ends at ten seconds, which only a read with no bound of its own reaches.
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		_, err := d.Client.FreshOutsideEdits(ctx)
		if err == nil || ctx.Err() != nil {
			t.Fatalf("a fresh read of a clone that never answers, bounded at 100ms, ended with %v after the request's own %v", err, ctx.Err())
		}
	})
	t.Run("one at a time", func(t *testing.T) {
		t.Parallel()
		var machine *heldClones
		d, _, _ := conflictWorldReading(t, apitest.Options{}, func(edits *fake.CloneEdits) ports.CloneEdits {
			machine = &heldClones{CloneEdits: edits, release: make(chan struct{})}
			return machine
		})
		const asking = 6
		var wg sync.WaitGroup
		failures := make(chan error, asking)
		for range asking {
			wg.Go(func() {
				if _, err := d.Client.FreshOutsideEdits(t.Context()); err != nil {
					failures <- err
				}
			})
		}
		for machine.reads.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		close(machine.release)
		wg.Wait()
		close(failures)
		for err := range failures {
			t.Errorf("a fresh read failed: %v", err)
		}
		if machine.most.Load() != 1 {
			t.Errorf("%d readings of the clone ran at once, want one", machine.most.Load())
		}
		if reads := machine.reads.Load(); reads > asking {
			t.Errorf("%d fresh reads of %d asks ran", reads, asking)
		}
	})
}
