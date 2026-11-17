package worker_test

import (
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/workorders"
)

// FR-SPEND-1: the park a long `lane_wait` asks for is in the worker's journal before the lane is told to park, so
// a worker that dies between the call and the park does not leave a lane live and idle with no timer. The next
// worker finishes the park, with the wake the lane asked for.
func TestAWorkerThatDiesAfterALongWaitStillParksTheLane(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, waitProfile)
	r.clockAtDatabaseTime()
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	at := r.clock.Now().Add(30 * time.Minute).UnixMilli()
	if reply := r.wait(t, 30); !reply.OK || !reply.Parking {
		t.Fatalf("a 30 minute wait answers %+v", reply)
	}
	if record, err := r.journal.Lane(t.Context(), "lane-1"); err != nil || record.WaitParkAt != at {
		t.Fatalf("the journal holds wait_park_at %d, want %d: %v", record.WaitParkAt, at, err)
	}

	r.stopWorker()
	g.free()
	r.driver.StopEverySession()
	r.subject.Wait()

	next := r.nextWorker(t)
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	next.Wait()
	if err := next.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Parked {
		t.Fatalf("a long wait kept in the journal did not finish parking: %+v", row)
	}
	if wake := r.wakeOf(t, 1); wake.Expr != lanes.WaitWake(at) || !wake.Open() {
		t.Errorf("wake %+v, want %s", wake, lanes.WaitWake(at))
	}
	if record, err := r.journal.Lane(t.Context(), "lane-1"); err != nil || record.WaitParkAt != 0 {
		t.Errorf("the request outlived the park: %d, %v", record.WaitParkAt, err)
	}
}
