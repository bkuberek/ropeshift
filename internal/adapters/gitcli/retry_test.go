package gitcli

import (
	"context"
	"errors"
	"testing"
)

var (
	errLockLost = errors.New("git fetch: exit status 1: error: cannot lock ref 'refs/remotes/origin/main': is at a but expected b")
	errOther    = errors.New("git fetch: exit status 128: fatal: unable to access the remote")
)

// script returns an attempt that fails with each error in turn, then succeeds, and counts its runs.
func script(failures ...error) (attempt func() error, runs *int) {
	runs = new(int)
	return func() error {
		defer func() { *runs++ }()
		if *runs < len(failures) {
			return failures[*runs]
		}
		return nil
	}, runs
}

func TestARefLockLostTwiceIsTriedAThirdTime(t *testing.T) {
	attempt, runs := script(errLockLost, errLockLost)
	if err := retryRefLock(t.Context(), attempt); err != nil || *runs != 3 {
		t.Errorf("after %d runs the fetch gave %v, want success on the third", *runs, err)
	}
}

func TestARefLockLostEveryTimeStopsAtThreeAttempts(t *testing.T) {
	attempt, runs := script(errLockLost, errLockLost, errLockLost, errLockLost)
	if err := retryRefLock(t.Context(), attempt); !errors.Is(err, errLockLost) || *runs != 3 {
		t.Errorf("after %d runs the fetch gave %v, want the lock failure after three", *runs, err)
	}
}

func TestAnyOtherFailureIsNotTriedAgain(t *testing.T) {
	attempt, runs := script(errOther, errLockLost)
	if err := retryRefLock(t.Context(), attempt); !errors.Is(err, errOther) || *runs != 1 {
		t.Errorf("after %d runs the fetch gave %v, want the first failure after one", *runs, err)
	}
}

func TestARefLockLostAfterTheContextEndedIsNotTriedAgain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	attempt, runs := script(errLockLost, errLockLost)
	if err := retryRefLock(ctx, attempt); !errors.Is(err, errLockLost) || *runs != 1 {
		t.Errorf("after %d runs the fetch gave %v, want the lock failure after one", *runs, err)
	}
}
