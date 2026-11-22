package worker_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/lanes"
)

// failedHook lays down one StopFailure as a hook left it in the journal while no worker listened.
func (w *worldRig) failedHook(t *testing.T, detail string, atMs int64) {
	t.Helper()
	err := w.journal.MergeHook(t.Context(), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "StopFailure", Subject: "s", Detail: detail, AtMs: atMs})
	if err != nil {
		t.Fatal(err)
	}
}

// A StopFailure in the journal is a failure of a past turn. Reading it again when a worker adopts the
// session says nothing of the sign-in now, so it starts no limit and parks no lane.
func TestAReplayedStopFailureChangesNoSignIn(t *testing.T) {
	t.Parallel()
	for _, detail := range []string{lanes.FailureUsageLimit, lanes.FailureSignIn} {
		t.Run(detail, func(t *testing.T) {
			t.Parallel()
			w := newWorldRig(t)
			w.driver.SetSignInStatus(lanes.SignInUnknown)
			w.adoptedAt(t, lanes.Working, lanes.Idle)
			w.failedHook(t, detail, 10)
			w.passesOnceAdopted(t)
			state, err := w.journal.SignIn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if state.Limited || state.SignedOut || state.Probe != "" {
				t.Errorf("an old failure moved the sign-in: %+v", state)
			}
		})
	}
}

// A clear while no worker listened is a generation, though the cleared session compacted after it.
func TestAClearBeforeACompactionSurvivesAReplay(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	w.adoptedAt(t, lanes.Idle, lanes.Working)
	for at, detail := range []string{lanes.SourceClear, "compact"} {
		event := lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "SessionStart", Subject: "s2", SessionID: "s2", Detail: detail, AtMs: int64(10 + at)}
		if err := w.journal.MergeHook(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	w.passesOnceAdopted(t)
	if got := w.lane("lane-1").Generation; got != 2 {
		t.Errorf("the lane reads generation %d after a clear", got)
	}
}
