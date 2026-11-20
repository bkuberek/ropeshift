package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReadyWait says why no worker's orchestrator claims a ready work order now (FR-UI-13). The control plane reads it
// at the time of a read from what a claim reads, and no event holds it. The values after ReadyClaims run from the
// wait nearest to a claim to the farthest, which is the order a read compares workers in.
type ReadyWait int

// The values of ReadyWait.
const (
	// ReadyClaims is work a worker's orchestrator claims when its next wake comes, which waits on nobody.
	ReadyClaims ReadyWait = iota
	// ReadyFleetHeld is a fleet a member paused or set to drain.
	ReadyFleetHeld
	// ReadySpendHeld is a project past its daily or weekly spend limit.
	ReadySpendHeld
	// ReadyCapReached is a cap a claim would pass: work in progress, lanes in the work's repository, or the lanes
	// of the worker nearest to claiming it.
	ReadyCapReached
	// ReadyNoRunWindow is an attended worker with no run window open.
	ReadyNoRunWindow
	// ReadyNoOrchestrator is a worker whose orchestrator no member wants running.
	ReadyNoOrchestrator
	// ReadyManual is a manual worker, whose orchestrator claims nothing.
	ReadyManual
	// ReadyNoWorker is work whose needs or lane no worker meets.
	ReadyNoWorker
)

// The caps ReadyWaitFact.Cap names.
const (
	CapWorkInProgress = "work_in_progress"
	CapRepository     = "repository"
	CapWorker         = "worker"
)

// ReadyWaitFact is a ready work order's wait, with the worker it is one worker's wait on, the cap a cap's wait
// names, and the mode a fleet's wait is in.
type ReadyWaitFact struct {
	Wait   ReadyWait
	Worker ledger.Worker
	Cap    string
	Fleet  ledger.FleetMode
}

// readyWorker is what one worker's claim of any work order reads, once for every work order.
type readyWorker struct {
	record ledger.WorkerRecord
	wait   ReadyWait
	limit  int64
}

// ReadyWaits reads, for each ready work order of these that no worker's orchestrator will claim, why not. It holds
// the checks a claim no member made is held to, in their order: the fleet, the project's spend, the workspace's
// caps, and then for each worker that can run the work, its level, its orchestrator, its run window, and its cap.
// With several workers it names the one nearest to claiming, so what a member does about it lets that one claim.
func (w *Workers) ReadyWaits(tx ports.ReadTx, orders []workorders.WorkOrder) (map[string]ReadyWaitFact, error) {
	var ready []workorders.WorkOrder
	inProgress, inRepository, onWorker := int64(0), map[string]int64{}, map[string]int64{}
	for _, order := range orders {
		switch order.State {
		case workorders.Ready:
			ready = append(ready, order)
		case workorders.Claimed, workorders.Running, workorders.Lapsed:
			inProgress++
			inRepository[order.RepositoryID]++
			onWorker[order.LeaseWorker]++
		}
	}
	out := map[string]ReadyWaitFact{}
	if len(ready) == 0 {
		return out, nil
	}
	workspace, err := tx.Workspace()
	if err != nil {
		return nil, err
	}
	if workspace.FleetMode != ledger.FleetRun {
		for _, order := range ready {
			out[order.ID] = ReadyWaitFact{Wait: ReadyFleetHeld, Fleet: workspace.FleetMode}
		}
		return out, nil
	}
	hold, _, err := spendReadsOf(tx).projectHold()
	if err != nil {
		return nil, err
	}
	if hold != nil {
		for _, order := range ready {
			out[order.ID] = ReadyWaitFact{Wait: ReadySpendHeld}
		}
		return out, nil
	}
	workers, err := readyWorkers(tx, workspace, onWorker)
	if err != nil {
		return nil, err
	}
	for _, order := range ready {
		fact, err := readyWaitOf(tx, order, workers)
		if err != nil {
			return nil, err
		}
		switch {
		case workspace.WIPLimit != 0 && inProgress >= workspace.WIPLimit:
			fact = ReadyWaitFact{Wait: ReadyCapReached, Cap: CapWorkInProgress}
		case order.RepositoryID != "" && workspace.RepositoryLimit != 0 && inRepository[order.RepositoryID] >= workspace.RepositoryLimit:
			fact = ReadyWaitFact{Wait: ReadyCapReached, Cap: CapRepository}
		}
		if fact.Wait != ReadyClaims {
			out[order.ID] = fact
		}
	}
	return out, nil
}

// readyWorkers reads each worker's standing, orchestrator, and cap once.
func readyWorkers(tx ports.ReadTx, workspace ledger.Workspace, onWorker map[string]int64) ([]readyWorker, error) {
	list, err := tx.Workers().List()
	if err != nil {
		return nil, err
	}
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	out := make([]readyWorker, 0, len(list))
	for _, worker := range list {
		record, err := tx.Workers().Find(worker.ID)
		if err != nil {
			return nil, err
		}
		facts, err := readAutonomy(tx.Policies(), tx.Workers(), record, now)
		if err != nil {
			return nil, err
		}
		running, err := orchestratorWanted(tx, worker.ID)
		if err != nil {
			return nil, err
		}
		line := readyWorker{record: record, limit: tighter(tighter(workspace.WorkerLimit, record.LaneCap()), facts.lanes())}
		switch {
		case !facts.standing.MayWork():
			line.wait = ReadyManual
		case !running:
			line.wait = ReadyNoOrchestrator
		case !facts.standing.MayClaim():
			line.wait = ReadyNoRunWindow
		case line.limit != 0 && onWorker[worker.ID] >= line.limit:
			line.wait = ReadyCapReached
		}
		out = append(out, line)
	}
	return out, nil
}

// orchestratorWanted says a member wants this worker's orchestrator running.
func orchestratorWanted(tx ports.ReadTx, worker string) (bool, error) {
	row, err := tx.Workers().Orchestrator(worker)
	switch {
	case err == nil:
		return row.Desired == ledger.OrchestratorRunning, nil
	case errors.Is(err, ports.ErrNotFound):
		return false, nil
	}
	return false, err
}

// readyWaitOf is the wait of the worker nearest to claiming a work order, among those that can run it.
func readyWaitOf(tx ports.ReadTx, order workorders.WorkOrder, workers []readyWorker) (ReadyWaitFact, error) {
	nearest := ReadyWaitFact{Wait: ReadyNoWorker}
	for _, line := range workers {
		if runs, _ := line.record.Manifest.Runs(order.Needs); !runs {
			continue
		}
		admitted, err := admitsResume(tx.Lanes(), order, line.record.ID)
		if err != nil {
			return ReadyWaitFact{}, err
		}
		if !admitted || line.wait >= nearest.Wait {
			continue
		}
		nearest = ReadyWaitFact{Wait: line.wait, Worker: line.record.Worker}
		if line.wait == ReadyCapReached {
			nearest.Cap = CapWorker
		}
	}
	return nearest, nil
}
