package worker_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
)

// A usage limit a StopFailure spooled while the worker was down says is dated at that hook, because the wait for a limit
// with no reset counts from when it was found. One whose backoff already ended has nothing left to wait for, and parks no
// lane. One inside its backoff waits for what is left of it, until its hook's time plus the backoff.
func TestAnUnheardLimitIsDatedAtItsHook(t *testing.T) {
	t.Parallel()
	backoff := lanes.DefaultUsageBackoff
	tests := []struct {
		name    string
		age     time.Duration
		waiting bool
	}{
		{name: "a failure older than the backoff", age: backoff + time.Minute},
		{name: "a failure inside the backoff", age: time.Minute, waiting: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.desire("lane-1")
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.holdsToken(t)
			at := w.clock.Now().Add(-tt.age).UnixMilli()
			w.spooledWhileDown(t, lanes.HookEvent{Event: "StopFailure", Detail: lanes.FailureUsageLimit, AtMs: at})
			state, err := w.journal.SignIn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if state.Limited != tt.waiting {
				t.Fatalf("a failure %v old left the sign-in %+v, want limited: %v", tt.age, state, tt.waiting)
			}
			if tt.waiting && state.ProbeAtMs != at+backoff.Milliseconds() {
				t.Errorf("the wait ends at %d, want its hook's time %d plus the backoff", state.ProbeAtMs, at)
			}
			if tt.waiting {
				w.passUntil(t, "the lane never parked", func() bool { return w.lane("lane-1").Parking != nil })
				return
			}
			w.passesOnceAdopted(t)
			if record := w.lane("lane-1"); record.Parking != nil || record.State != lanes.StateRunning {
				t.Errorf("a limit that ended before it was heard parked the lane: %+v", record)
			}
		})
	}
}
