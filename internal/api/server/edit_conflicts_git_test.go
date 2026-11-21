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

// In a repository that squashes or rebases, a work order the forge landed is no person's push, and a person's own push
// still is: a squash names the work order in its trailers, and a rebase's commits are named by the range the ledger
// recorded for the merge, on real git from end to end.
func TestAForgeLandedWorkOrderIsNoConflictAndAPersonsPushIs(t *testing.T) {
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
			if _, err := control.NewRepositories(d.Store).Change(t.Context(), apitest.Scope, "repository-1", control.RepositoryChange{MergeMethod: &method}, ledger.Actor{Kind: ledger.ActorMember, ID: "m"}); err != nil {
				t.Fatal(err)
			}
			w.Running("wo-1", "Retry within a budget")
			worktree := filepath.Join(t.TempDir(), "lane")
			clone.Git("worktree", "add", "--quiet", "-b", "lane/lane-wo-1", worktree, "origin/main")
			lane := &gittest.Repo{T: t, Dir: worktree, Home: clone.Home}
			lane.Commit("docs/guide.md", "the guide, as the lane wrote it\n")
			lane.Commit("docs/other.md", "other, as the lane wrote it\n")

			// Another work order lands on the forge in two commits that touch the same files.
			w.Created("wo-landed", "Landed on the forge")
			var span *ledger.LandedRange
			before := origin.Git("rev-parse", "HEAD")
			switch method {
			case ledger.MergeSquash:
				trailers := ledger.MergeTrailers("wo-landed", strings.Repeat("c", 40))
				commitOn(t, origin, "docs/guide.md", "the guide, squashed on the forge\n", "Change the guide and the rest (#7)\n\n"+trailers)
			case ledger.MergeRebase:
				commitOn(t, origin, "docs/guide.md", "the guide, rebased on the forge\n", "change the guide")
				commitOn(t, origin, "docs/other.md", "other, rebased on the forge\n", "change the rest")
				span = &ledger.LandedRange{Before: before, After: origin.Git("rev-parse", "HEAD")}
			}
			if span != nil {
				if err := d.Store.Write(t.Context(), apitest.Scope, "test.merged", func(tx ports.WriteTx) error {
					if err := tx.Forge().SavePullRequest(ledger.WorkPullRequest{
						WorkOrder: "wo-landed", Repository: "repository-1", Worker: apitest.WorldWorker,
						PullRequest: ledger.PullRequest{Number: 7, State: ledger.PullRequestMerged, Base: "main", Branch: "lane/x", HeadCommit: strings.Repeat("c", 40), MergeCommit: span.After, MergedAt: "2026-11-20T10:00:00Z"},
					}); err != nil {
						return err
					}
					data := `{"landed":{"before":"` + span.Before + `","after":"` + span.After + `"}}`
					_, err := tx.Append(ledger.EventDraft{
						Type: ledger.EventIntegrationMerged, Actor: ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"},
						Subject: ledger.Subject{Kind: ledger.SubjectIntegration, ID: "wo-landed"}, Data: []byte(data),
					})
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			clone.Git("fetch", "--quiet", "origin")
			if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
				t.Fatal(err)
			}
			if got := conflictItems(t, d); len(got) != 0 {
				t.Fatalf("work the forge landed by %s reads as a person's push: %+v", method, got[0].Conflict.Paths)
			}

			// A person pushes to the same file.
			commitOn(t, origin, "docs/guide.md", "the guide, as a person wrote it\n", "a person's change")
			clone.Git("fetch", "--quiet", "origin")
			if _, err := d.Client.OutsideEdits(t.Context()); err != nil {
				t.Fatal(err)
			}
			items := conflictItems(t, d)
			if len(items) != 1 || len(items[0].Conflict.Paths) != 1 || items[0].Conflict.Paths[0] != (api.ConflictPathView{Path: "docs/guide.md", Person: ledger.PersonPushed}) {
				t.Fatalf("a person's push to a repository that rewrites commits reads %+v", items)
			}
		})
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
