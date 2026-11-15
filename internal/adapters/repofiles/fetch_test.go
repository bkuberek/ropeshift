package repofiles

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/gittest"
)

// A claim's fetch of a base branch updates the same remote-tracking ref as intake's fetch of the remote, so
// intake can lose git's lock on it. That is a collision with the daemon's own work, not a remote that failed,
// and intake must not report it as one.
func TestIntakeFetchOutlastsAFetchThatHoldsTheRefsLock(t *testing.T) {
	origin := gittest.New(t)
	origin.Commit("README.txt", "one\n")
	clone := origin.Clone("clone")
	moved := origin.Advance("main")
	clone.HoldRefLock("refs/remotes/origin/main", 60*time.Millisecond)

	source := New(Config{Repository: clone.Dir, Env: clone.EnvWithoutRefLockWait()})
	batch, err := source.Poll(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	health, err := source.Health(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.SourceHealth{State: planning.HealthOK}); health != want {
		t.Errorf("intake reports %+v after a collision, want %+v", health, want)
	}
	if batch.Cursor != ports.Cursor(moved) {
		t.Errorf("intake read %s, want the remote's commit %s", batch.Cursor, moved)
	}
}
