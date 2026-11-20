package server

import (
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// readyReasons names each wait a read finds in the API's words.
var readyReasons = map[control.ReadyWait]string{
	control.ReadyFleetHeld:      api.ReadyWaitsForFleet,
	control.ReadySpendHeld:      api.ReadyWaitsForSpend,
	control.ReadyCapReached:     api.ReadyWaitsForCap,
	control.ReadyNoRunWindow:    api.ReadyWaitsForRunWindow,
	control.ReadyNoOrchestrator: api.ReadyWaitsForOrchestrator,
	control.ReadyManual:         api.ReadyWaitsForManual,
	control.ReadyNoWorker:       api.ReadyWaitsForWorker,
}

// readyCaps names each cap a wait names by the name a fleet's caps carry.
var readyCaps = map[string]string{
	control.CapWorkInProgress: api.CapWorkInProgress,
	control.CapRepository:     api.CapLanesPerRepository,
	control.CapWorker:         api.CapLanesPerWorker,
}

// readyWaits says why each ready work order that no worker's orchestrator will claim waits, and holds nothing when
// every one will be claimed.
func (h *handler) readyWaits(tx ports.ReadTx, orders []workorders.WorkOrder) (map[string]api.ReadyWait, error) {
	facts, err := h.workers.ReadyWaits(tx, orders)
	if err != nil || len(facts) == 0 {
		return nil, err
	}
	out := make(map[string]api.ReadyWait, len(facts))
	for id, fact := range facts {
		out[id] = api.ReadyWait{
			Reason: readyReasons[fact.Wait], Worker: fact.Worker.ID, WorkerName: fact.Worker.Name,
			Cap: readyCaps[fact.Cap], Fleet: fact.Fleet,
		}
	}
	return out, nil
}
