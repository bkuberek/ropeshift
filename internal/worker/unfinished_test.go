package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
)

// TestAWorkerNamesWhatItsWaitWaitsOn names each lane whose goroutine runs, so a daemon that gives up
// waiting on its worker can say what it left running, and names nothing once the wait is over.
func TestAWorkerNamesWhatItsWaitWaitsOn(t *testing.T) {
	t.Parallel()
	r, _, _, stop := runStoppable(t, "", func(*laneRig) []fake.DriverStep {
		return []fake.DriverStep{{Bound: true}, {Hold: true}}
	})
	r.awaitWorking(t, 20*time.Second)
	if got := r.subject.Unfinished(); !slices.Equal(got, []string{"lane lane-1"}) {
		t.Errorf("a worker running one lane names %q", got)
	}
	stop()
	if waited, stacks := r.waitedWithin(keptLaneStop); !waited {
		t.Fatalf("the worker still waits %s after its context ended; goroutines:\n%s", keptLaneStop, stacks)
	}
	if got := r.subject.Unfinished(); len(got) != 0 {
		t.Errorf("a worker whose wait is over names %q", got)
	}
}
