package gitcli_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/gitcli"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// lockHeld is how long another fetch holds a remote-tracking ref in these tests. It outlasts the first attempt
// and the pause after it, so the fetch must retry, and it ends before the third attempt starts, so the third
// attempt finds the ref free, with git's own wait for the lock turned off.
const lockHeld = 60 * time.Millisecond

// A fetch of a base branch and a fetch of every branch both update the remote-tracking ref of the base, so
// when both see the remote move, one of them loses git's lock on the ref. The claim that asks for the base's
// tip must not take that loss for a base it cannot read, because the lane then starts without the worktree
// of the work it builds on.

func TestBaseTipOutlastsAFetchThatHoldsTheRefsLock(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	moved := origin.Advance("main")
	clone.HoldRefLock("refs/remotes/origin/main", lockHeld)

	tip, err := gitcli.Worktrees{Env: clone.EnvWithoutRefLockWait()}.BaseTip(t.Context(), clone.Dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.BaseTip{Ref: "origin/main", Commit: moved}); tip != want {
		t.Errorf("the tip reads %+v, want %+v", tip, want)
	}
}

func TestBranchOutlastsAFetchThatHoldsTheRefsLock(t *testing.T) {
	origin := gittest.New(t)
	origin.Branch("feature", origin.Commit("README.txt", "one\n"))
	clone := origin.Clone("clone")
	moved := origin.Advance("feature")
	clone.HoldRefLock("refs/remotes/origin/feature", lockHeld)

	tip, err := gitcli.Worktrees{Env: clone.EnvWithoutRefLockWait()}.Branch(t.Context(), clone.Dir, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.BaseTip{Ref: "origin/feature", Commit: moved}); tip != want {
		t.Errorf("the branch reads %+v, want %+v", tip, want)
	}
}

// A lock that is never released is a real failure, and it still reaches the caller as one: the retry is
// bounded, and the error keeps git's own words.
func TestBaseTipFailsWhenTheRefsLockIsNeverReleased(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	origin.Advance("main")
	clone.HoldRefLock("refs/remotes/origin/main", time.Minute)

	started := time.Now()
	_, err := gitcli.Worktrees{Env: clone.EnvWithoutRefLockWait()}.BaseTip(t.Context(), clone.Dir, "main")
	if !errors.Is(err, ports.ErrBaseUnavailable) {
		t.Fatalf("a lock that stays held gave %v, want ErrBaseUnavailable", err)
	}
	if got := err.Error(); !strings.Contains(got, "cannot lock ref") || !strings.Contains(got, "refs/remotes/origin/main") {
		t.Errorf("the error lost git's words: %v", err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("a held lock kept the claim for %s", took)
	}
}

// The fetch that brings a lane's branch and snapshots from the remote names each ref as its own destination,
// which also moves the ref's remote-tracking copy, as intake's fetch of the clone does. It can lose git's lock
// on that ref to intake's fetch, and a lane that comes back to a machine must not take that loss for a
// branch the remote does not hold.
func TestFetchOfALanesRefsOutlastsAFetchThatHoldsTheRefsLock(t *testing.T) {
	origin := gittest.New(t)
	seed := origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	origin.Branch("lane/one", seed)
	lane := origin.Advance("lane/one")
	clone.HoldRefLock("refs/remotes/origin/lane/one", lockHeld)

	worktrees := gitcli.Worktrees{Env: clone.EnvWithoutRefLockWait()}
	if err := worktrees.Fetch(t.Context(), clone.Dir, []string{"refs/heads/lane/one"}); err != nil {
		t.Fatal(err)
	}
	if got := clone.Git("rev-parse", "refs/heads/lane/one"); got != lane {
		t.Errorf("the branch reads %s, want %s", got, lane)
	}
	if got := clone.Git("rev-parse", "refs/remotes/origin/lane/one"); got != lane {
		t.Errorf("the remote-tracking ref reads %s, want %s", got, lane)
	}
}

// A lock that is never released is still a failure the caller sees, in git's own words, after the bounded
// retry.
func TestFetchOfALanesRefsFailsWhenTheRefsLockIsNeverReleased(t *testing.T) {
	origin := gittest.New(t)
	seed := origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	origin.Branch("lane/one", seed)
	origin.Advance("lane/one")
	clone.HoldRefLock("refs/remotes/origin/lane/one", time.Minute)

	started := time.Now()
	err := gitcli.Worktrees{Env: clone.EnvWithoutRefLockWait()}.Fetch(t.Context(), clone.Dir, []string{"refs/heads/lane/one"})
	if err == nil || !strings.Contains(err.Error(), "cannot lock ref") {
		t.Fatalf("a lock that stays held gave %v, want git's cannot lock ref", err)
	}
	if took := time.Since(started); took > 10*time.Second {
		t.Errorf("a held lock kept the fetch for %s", took)
	}
}
