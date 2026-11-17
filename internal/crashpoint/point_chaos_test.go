//go:build chaos

package crashpoint

import (
	"testing"
	"time"
)

func mustAwait(t *testing.T, spec string) func(op, step string) {
	t.Helper()
	fn, err := awaiting(spec)
	if err != nil {
		t.Fatal(err)
	}
	return fn
}

func TestAwaitHoldsTheStepUntilTheReleaseStepIsHit(t *testing.T) {
	hit := mustAwait(t, "sweep/before-lapse=lease.renew/decided,1m")
	done := make(chan struct{})
	go func() {
		hit("sweep", "before-lapse")
		close(done)
	}()
	hit("lease.renew", "committed")
	select {
	case <-done:
		t.Fatal("the step returned before the release step was hit")
	case <-time.After(50 * time.Millisecond):
	}
	// The waiter may not have arrived at the first hit, and a hit before it arrives
	// does not release it, so the release step is hit until the waiter returns.
	deadline := time.After(10 * time.Second)
	for {
		hit("lease.renew", "decided")
		select {
		case <-done:
			return
		case <-deadline:
			t.Fatal("the step still waits after the release step was hit")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestAwaitReturnsAtTheBoundWhenTheReleaseStepIsNeverHit(t *testing.T) {
	hit := mustAwait(t, "sweep/before-lapse=lease.renew/decided,100ms")
	start := time.Now()
	hit("sweep", "before-lapse")
	if waited := time.Since(start); waited < 100*time.Millisecond || waited > 10*time.Second {
		t.Fatalf("the step waited %v for a bound of 100ms", waited)
	}
}

func TestAwaitIsNotReleasedByAHitBeforeItArrived(t *testing.T) {
	hit := mustAwait(t, "sweep/before-lapse=lease.renew/decided,100ms")
	hit("lease.renew", "decided")
	start := time.Now()
	hit("sweep", "before-lapse")
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Fatalf("an earlier hit of the release step released the step after %v", waited)
	}
}

func TestAwaitPassesOtherStepsThrough(t *testing.T) {
	hit := mustAwait(t, "sweep/before-lapse=lease.renew/decided,1m")
	start := time.Now()
	hit("sweep", "candidates-read")
	hit("lapse", "begun")
	if waited := time.Since(start); waited > time.Second {
		t.Fatalf("other steps waited %v", waited)
	}
}

func TestAwaitRefusesAMalformedSpec(t *testing.T) {
	for _, spec := range []string{"", "sweep/before-lapse", "sweep/before-lapse=lease.renew/decided", "sweep/before-lapse=lease.renew/decided,soon"} {
		if _, err := awaiting(spec); err == nil {
			t.Errorf("%q was accepted", spec)
		}
	}
}
