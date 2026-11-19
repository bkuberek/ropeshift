package gitcli

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

var (
	errLockedGone = errors.New("git worktree add --quiet --detach /w/r1 HEAD: exit status 128: fatal: could not open '.git/worktrees/r1/locked' for writing: No such file or directory")
	errGitdirGone = errors.New("git worktree add --quiet /w/r2 main: exit status 128: fatal: could not open '/repo/.git/worktrees/r2/gitdir' for writing: No such file or directory")
	errNotARepo   = errors.New("git worktree add --quiet /w/r3 main: exit status 128: fatal: not a git repository: /repo/.git/worktrees/r3")
	errNoRoom     = errors.New("git worktree add --quiet /w/r4 main: exit status 128: fatal: could not create directory of '/w/r4/file': No space left on device")
	// A prune that removes the registration after the add wrote its files, but before the add read it back.
	errCreatedGone = errors.New("git worktree add --quiet --detach /w/r5 HEAD: exit status 255: error: could not find created worktree 'r5'")
	// macOS fails the open of a file in a directory a prune is removing with EINVAL, where Linux has ENOENT.
	errLockedInvalid = errors.New("git worktree add --quiet --detach /w/r6 HEAD: exit status 128: fatal: could not open '.git/worktrees/r6/locked' for writing: Invalid argument")
	// A prune that found no registration left removes .git/worktrees itself, between the add making it and
	// making the registration in it.
	errWorktreesGone = errors.New("git worktree add --quiet --detach /w/r7 HEAD: exit status 128: fatal: could not create directory of '/repo/.git/worktrees/r7': No such file or directory")
	// The checkout the add runs reads the registration through the worktree's .git file, which then points at
	// nothing.
	errGitfileBroken = errors.New("git worktree add --quiet /w/r8 main: exit status 128: fatal: gitfile does not point to a valid repository: /w/r8/.git")
)

func TestALostRegistrationIsRecognizedByWhatGitPrints(t *testing.T) {
	errFoundLost := fmt.Errorf("%w: /w/r9", errRegistrationLost)
	for _, err := range []error{errLockedGone, errGitdirGone, errNotARepo, errCreatedGone, errLockedInvalid, errWorktreesGone, errGitfileBroken, errFoundLost} {
		if !lostRegistration(err) {
			t.Errorf("%v is not recognized as a lost registration", err)
		}
	}
	for _, err := range []error{nil, errNoRoom, errOther, errLockLost} {
		if lostRegistration(err) {
			t.Errorf("%v is recognized as a lost registration", err)
		}
	}
}

func TestALostRegistrationTwiceIsTriedAThirdTime(t *testing.T) {
	step, runs := script(errLockedGone, errGitdirGone)
	if err := retryLostRegistration(t.Context(), step); err != nil || *runs != 3 {
		t.Errorf("after %d runs the step gave %v, want success on the third", *runs, err)
	}
}

func TestALostRegistrationEveryTimeStopsAtThreeAttempts(t *testing.T) {
	step, runs := script(errLockedGone, errNotARepo, errLockedGone, errLockedGone)
	if err := retryLostRegistration(t.Context(), step); !errors.Is(err, errLockedGone) || *runs != 3 {
		t.Errorf("after %d runs the step gave %v, want the lost registration after three", *runs, err)
	}
}

func TestAnUnrelatedFailureOfAStepIsNotTriedAgain(t *testing.T) {
	step, runs := script(errNoRoom, errLockedGone)
	if err := retryLostRegistration(t.Context(), step); !errors.Is(err, errNoRoom) || *runs != 1 {
		t.Errorf("after %d runs the step gave %v, want the first failure after one", *runs, err)
	}
}

func TestALostRegistrationAfterTheContextEndedIsNotTriedAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	step, runs := script(errLockedGone, errLockedGone)
	if err := retryLostRegistration(ctx, step); !errors.Is(err, errLockedGone) || *runs != 1 {
		t.Errorf("after %d runs the step gave %v, want the lost registration after one", *runs, err)
	}
}
