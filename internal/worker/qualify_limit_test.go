package worker_test

import (
	"context"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/worker"
)

// A probe runs real sessions on the sign-in, and one that meets a usage limit or a sign-in that is out fails the checks it
// could not run and replaces a pass with them. So no probe starts while the sign-in is at a limit or signed out, whether
// the worker's own start or a later look meets it, and the first look after it ends starts the probe.
func TestNoProbeStartsAtAKnownUsageLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state func(now time.Time) lanes.SignInState
	}{
		{name: "a usage limit", state: func(now time.Time) lanes.SignInState {
			return lanes.SignInState{Limited: true, ResetAtMs: now.Add(30 * time.Second).UnixMilli()}
		}},
		{name: "a sign-in that is out", state: func(time.Time) lanes.SignInState { return lanes.SignInState{SignedOut: true} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := newRig(t)
			if err := base.journal.SaveSignIn(t.Context(), tt.state(base.clock.Now())); err != nil {
				t.Fatal(err)
			}
			driver := fake.NewAgentDriver()
			subject := worker.New(base.link, base.journal, base.clock, worker.Config{
				Name:      "test",
				JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
				Lanes:     &worker.LaneRuntime{Driver: driver},
			})
			if err := subject.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			subject.Wait()
			if n := len(driver.Probes()); n != 0 {
				t.Fatalf("%d probes started as the worker began on %s", n, tt.name)
			}
			look := func(after time.Duration) {
				t.Helper()
				base.clock.Advance(after)
				if _, err := subject.Reconcile(t.Context()); err != nil {
					t.Fatal(err)
				}
				subject.Wait()
			}
			// This pass looks at the version while the sign-in is still out, and then ends what is out.
			look(2 * time.Minute)
			if n := len(driver.Probes()); n != 0 {
				t.Fatalf("%d probes started at a look made at %s", n, tt.name)
			}
			look(2 * time.Minute)
			if n := len(driver.Probes()); n != 1 {
				t.Errorf("%d probes after %s ended and the version was looked at, want one", n, tt.name)
			}
		})
	}
}
