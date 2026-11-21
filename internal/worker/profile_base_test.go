package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// writerHook is a profile layer a writer committed on its branch, whose hook would run on the worker when a lane's
// worktree is made, beside a gate.
const writerHook = "[agent]\ngates = [\"Bash(git tag *)\"]\n[hooks]\non_lane_created = [\"./scripts/setup.sh\"]\n"

// writerLane is a worker that leaves lane-1 to run under a lease, a commit of the base branch, and the tip of a writer's
// branch from that commit, whose profile layer holds writerHook where the base's holds none.
type writerLane struct {
	*worldRig
	source    *fake.ProfileSource
	hooks     *fake.HookRunner
	fork, tip string
}

// aLaneOn is a writerLane whose lease leaseOf gives for its two commits.
func aLaneOn(t *testing.T, leaseOf func(fork, tip string) ledger.DesiredLease) writerLane {
	t.Helper()
	w := newWorldRig(t)
	worktrees := w.config.Provisioner.Worktrees.(*fake.Worktrees)
	fork := worktrees.Grow("/repository", "main")
	worktrees.Point("/repository", "lane/writer", fork)
	tip := worktrees.Touch("/repository", "lane/writer", ".ropeshift/profile.toml")
	source := w.config.Provisioner.Profiles.(*fake.ProfileSource)
	source.PutAt("/repository", tip, writerHook)
	source.Hold("/repository", fork)
	lease := leaseOf(fork, tip)
	lease.WorkOrder, lease.ClaimID, lease.Fence, lease.State, lease.Lane = "wo-lane-1", "c-lane-1", 1, string(workorders.Claimed), "lane-1"
	if err := w.journal.SaveDesired(t.Context(), ledger.DesiredState{Rev: 1, Leases: []ledger.DesiredLease{lease}}); err != nil {
		t.Fatal(err)
	}
	w.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	return writerLane{worldRig: w, source: source, hooks: w.config.Provisioner.Hooks.(*fake.HookRunner), fork: fork, tip: tip}
}

// started waits until the lane's session has started, which its worktree and its hook come before.
func (w writerLane) started(t *testing.T) {
	t.Helper()
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for len(w.driver.Specs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the lane never started a session: %v", w.problems())
		}
		time.Sleep(time.Millisecond)
	}
}

// A profile layer a writer commits to its branch reaches no lane until it is merged: the review of that branch and a
// lane that continues it read the repository's layer at the commit the work began from, never at the commit the
// writer wrote, so the hook the writer committed runs on neither. A review of a lane that recorded no base has no such
// commit, so it reads the layer at the commit under review for its gates, which only narrow what it may do, and runs
// none of its hooks.
func TestAProfileHookAWriterCommittedRunsInNoReviewOrContinuedLane(t *testing.T) {
	t.Parallel()
	for name, leaseOf := range map[string]func(fork, tip string) ledger.DesiredLease{
		"its review": func(fork, tip string) ledger.DesiredLease {
			return ledger.DesiredLease{Kind: string(workorders.KindReview), BaseCommit: tip, Attempt: &ledger.DesiredAttempt{Commit: tip, BaseCommit: fork}}
		},
		"a lane that continues its branch": func(fork, tip string) ledger.DesiredLease {
			return ledger.DesiredLease{
				Kind: "change", BaseRef: "origin/main", BaseCommit: fork,
				Continues: "lane/writer", ContinuesRef: "origin/lane/writer", ContinuesCommit: tip,
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := aLaneOn(t, leaseOf)
			w.started(t)
			if runs := w.hooks.Runs(); len(runs) != 0 {
				t.Errorf("%s ran the writer's hook: %+v", name, runs)
			}
			loaded := w.source.Loaded()
			if slices.Contains(loaded, fake.Load{Repository: "/repository", Base: w.tip}) || !slices.Contains(loaded, fake.Load{Repository: "/repository", Base: w.fork}) {
				t.Errorf("%s read the repository's layer at %+v, want only at %s and never at the writer's %s", name, loaded, w.fork, w.tip)
			}
		})
	}
	t.Run("a review of a lane that recorded no base", func(t *testing.T) {
		t.Parallel()
		w := aLaneOn(t, func(_, tip string) ledger.DesiredLease {
			return ledger.DesiredLease{Kind: string(workorders.KindReview), BaseCommit: tip, Attempt: &ledger.DesiredAttempt{Commit: tip}}
		})
		w.started(t)
		if runs := w.hooks.Runs(); len(runs) != 0 {
			t.Errorf("the review ran the writer's hook: %+v", runs)
		}
		if loaded := w.source.Loaded(); !slices.Contains(loaded, fake.Load{Repository: "/repository", Base: w.tip}) {
			t.Errorf("a review with no base read the repository's layer at %+v, want at %s for its gates", loaded, w.tip)
		}
		if gates := w.driver.Specs()[0].Gates; !slices.Contains(gates, "Bash(git tag *)") {
			t.Errorf("a review with no base runs with the gates %v, want the repository's", gates)
		}
	})
}
