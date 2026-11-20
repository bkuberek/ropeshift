package control

import (
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// goneLanes is a ledger that holds no lane at all.
type goneLanes struct{ ports.LaneReader }

func (goneLanes) Row(string) (ledger.LaneRow, error) { return ledger.LaneRow{}, ports.ErrNotFound }

func (goneLanes) Generations(string) ([]ledger.Generation, error) { return nil, ports.ErrNotFound }

// laneless is a read that reaches only those lanes.
type laneless struct{ ports.ReadTx }

func (laneless) Lanes() ports.LaneReader { return goneLanes{} }

// Woken work whose lane the ledger does not hold resumes on no worker, which is a wait like any other: it does not fail
// the read of every other work order.
func TestWorkThatResumesALaneTheLedgerLacksWaitsForAWorker(t *testing.T) {
	t.Parallel()
	order := workorders.WorkOrder{ID: "wo-woken", State: workorders.Ready, ResumeLaneID: "lane-gone"}
	workers := []readyWorker{{record: ledger.WorkerRecord{Worker: ledger.Worker{ID: "worker-a", Name: "solo"}}, fresh: ReadyNoRunWindow}}
	got, err := readyWaitOf(laneless{}, order, workers, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got.Wait != ReadyNoWorker || got.Worker.ID != "" {
		t.Errorf("wait %+v, want no worker", got)
	}
}
