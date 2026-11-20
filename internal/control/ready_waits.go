package control

import (
	"errors"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// ReadyWait says why no worker claims a ready work order now (FR-UI-13): its orchestrator, or for work a wake made
// ready, the worker itself. The control plane reads it
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
	// CapRespawns is the bound on the generations a day of a woken lane, on a worker below unattended.
	CapRespawns = "respawns"
)

// ReadyWaitFact is a ready work order's wait, with the worker it is one worker's wait on, the cap a cap's wait
// names, and the mode a fleet's wait is in.
type ReadyWaitFact struct {
	Wait   ReadyWait
	Worker ledger.Worker
	Cap    string
	Fleet  ledger.FleetMode
}

// readyWorker is what one worker's claim of any work order reads, once for every work order. fresh is the wait of work no
// wake made ready, which its orchestrator claims; capped says the worker holds as many lanes as it may; and respawns is
// the bound on a woken lane's generations a day, where zero means none.
type readyWorker struct {
	record   ledger.WorkerRecord
	fresh    ReadyWait
	capped   bool
	respawns int64
}

// ReadyWaits reads, for each ready work order of these that no worker will claim, why not. It holds the checks a claim
// no member made is held to, in their order: the fleet, the project's spend, the workspace's caps, and then for each
// worker that can run the work, its level, its orchestrator, its run window, and its cap. Work a wake made ready is
// claimed by the worker that holds its lane, with nobody there and no orchestrator, so the level, the window, and the
// orchestrator hold none of it, and the bound on its lane's generations a day does (holdToAutonomy). With several
// workers it names the one nearest to claiming, so what a member does about it lets that one claim.
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
	now, err := tx.Now()
	if err != nil {
		return nil, err
	}
	workers, err := readyWorkers(tx, workspace, onWorker, now)
	if err != nil {
		return nil, err
	}
	for _, order := range ready {
		fact, err := readyWaitOf(tx, order, workers, now)
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
func readyWorkers(tx ports.ReadTx, workspace ledger.Workspace, onWorker map[string]int64, now int64) ([]readyWorker, error) {
	list, err := tx.Workers().List()
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
		limit := tighter(tighter(workspace.WorkerLimit, record.LaneCap()), facts.lanes())
		line := readyWorker{record: record, capped: limit != 0 && onWorker[worker.ID] >= limit, respawns: facts.respawns()}
		switch {
		case !facts.standing.MayWork():
			line.fresh = ReadyManual
		case !running:
			line.fresh = ReadyNoOrchestrator
		case !facts.standing.MayClaim():
			line.fresh = ReadyNoRunWindow
		case line.capped:
			line.fresh = ReadyCapReached
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

// readyWaitOf is the wait of the worker nearest to claiming a work order, among those that can run it. A lane the ledger
// does not hold admits no worker, so work that resumes one waits for a worker and fails no read.
func readyWaitOf(tx ports.ReadTx, order workorders.WorkOrder, workers []readyWorker, now int64) (ReadyWaitFact, error) {
	nearest := ReadyWaitFact{Wait: ReadyNoWorker}
	today := int64(-1)
	for _, line := range workers {
		if runs, _ := line.record.Manifest.Runs(order.Needs); !runs {
			continue
		}
		admitted, err := admitsResume(tx.Lanes(), order, line.record.ID)
		switch {
		case errors.Is(err, ports.ErrNotFound):
			continue
		case err != nil:
			return ReadyWaitFact{}, err
		case !admitted:
			continue
		}
		wait, capName := line.fresh, CapWorker
		if order.ResumeLaneID != "" {
			if today < 0 {
				if today, err = generationsToday(tx.Lanes(), order.ResumeLaneID, now); err != nil {
					return ReadyWaitFact{}, err
				}
			}
			wait = ReadyClaims
			switch {
			case line.respawns != 0 && today >= line.respawns:
				wait, capName = ReadyCapReached, CapRespawns
			case line.capped:
				wait = ReadyCapReached
			}
		}
		if wait >= nearest.Wait {
			continue
		}
		nearest = ReadyWaitFact{Wait: wait, Worker: line.record.Worker}
		if wait == ReadyCapReached {
			nearest.Cap = capName
		}
	}
	return nearest, nil
}

// generationsToday counts the generations a lane started in the day before a time, as the bound on respawns does.
func generationsToday(lanes ports.LaneReader, lane string, now int64) (int64, error) {
	generations, err := lanes.Generations(lane)
	if errors.Is(err, ports.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var today int64
	for _, generation := range generations {
		if now-generation.StartedAt < dayMs {
			today++
		}
	}
	return today, nil
}
