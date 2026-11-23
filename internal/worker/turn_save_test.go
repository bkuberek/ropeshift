package worker_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// stoppedAtRest stops the worker's run as a lane's move to rest is kept, which is the gap between that move and the save of
// what the turn's end read.
type stoppedAtRest struct {
	ports.Journal
	stop  func()
	armed atomic.Bool
	once  sync.Once
}

func (j *stoppedAtRest) SaveActual(ctx context.Context, lane string, state, from lanes.ActualState) error {
	err := j.Journal.SaveActual(ctx, lane, state, from)
	if err == nil && state == lanes.Idle && j.armed.Load() {
		j.once.Do(j.stop)
	}
	return err
}

// A worker that stops after a turn's end moved its lane to rest, and before the progress that end read was saved, still
// keeps that progress: the worker that takes the lane over reads the background work the turn left running from the
// journal, and rests on it. The save is not part of what the stop cancels.
func TestAStopBetweenARestAndTheProgressSaveKeepsTheBackgroundWork(t *testing.T) {
	t.Parallel()
	base := newRig(t)
	journal := &stoppedAtRest{Journal: base.journal}
	base.workerJournal = journal
	r := newLaneRigFor(t, base).dispatched()
	journal.stop = func() { r.stopWorker() }
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)}))
	r.start()
	r.reach(t, stopped)
	r.stopAt(t, r.clock.Now().UnixMilli(), 1)
	journal.armed.Store(true)
	stopped.free()

	deadline := time.Now().Add(5 * time.Second)
	for {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		if err == nil && record.Progress.Background == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a worker stopped as the lane came to rest left the journal's background count at %d (%v), want 1", record.Progress.Background, err)
		}
		time.Sleep(time.Millisecond)
	}
}
