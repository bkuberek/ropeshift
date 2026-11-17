package server_test

import (
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
)

// orchestratorReads is what every read of one worker's orchestrator says holds its run: the shop floor, the
// orchestrator's own route, and the targets a member can talk to.
func (d *talkDaemon) orchestratorReads(t *testing.T) (floor, row, target string) {
	t.Helper()
	var view api.ShopFloor
	if err := d.client.ReadView(t.Context(), api.ViewShopFloor, "", &view); err != nil {
		t.Fatal(err)
	}
	for _, line := range view.Workers {
		if line.Worker.ID == d.auth.WorkerID && line.Orchestrator != nil {
			floor = line.Orchestrator.WaitingFor
		}
	}
	read, err := d.client.Orchestrator(t.Context(), d.auth.WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := d.client.ConversationTargets(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(targets.Orchestrators) != 1 {
		t.Fatalf("targets %+v", targets.Orchestrators)
	}
	return floor, read.WaitingFor, targets.Orchestrators[0].WaitingFor
}

// orchestratorSays reports the orchestrator's state and conditions, as its worker would.
func (d *talkDaemon) orchestratorSays(t *testing.T, state string, conditions ...lanes.Condition) {
	t.Helper()
	status := ledger.LaneStatus{ActualState: state, Liveness: "unknown"}
	for _, condition := range conditions {
		status.Conditions = append(status.Conditions, string(condition))
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	d.seq++
	reply, err := d.workers.Upload(t.Context(), ledger.UploadBatch{Auth: d.auth, Epoch: d.epoch, Events: []ledger.UploadEvent{{
		Epoch: d.epoch, Seq: d.seq, Type: ledger.UploadLaneStatus, Lane: "orchestrator-1", Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil || len(reply.Rejected) != 0 {
		t.Fatalf("upload: %+v %v", reply, err)
	}
}

// A worker says its orchestrator's run awaits a wake while there is nothing to judge, and an attended worker's
// orchestrator judges no new work with no run window open, so every read says the run waits for a window. With
// a window open, or at another level, the same wait is for work. A run a wake reached prepares its session,
// and no read says it waits, nor once the session started.
func TestAnAttendedOrchestratorWaitsForARunWindow(t *testing.T) {
	t.Parallel()
	d := startTalk(t)
	ctx := t.Context()
	if _, err := d.workers.SetOrchestrator(ctx, d.auth.WorkerID, ledger.OrchestratorRunning, talkMember); err != nil {
		t.Fatal(err)
	}
	d.orchestratorSays(t, "provisioning", lanes.AwaitingWake)
	if floor, row, target := d.orchestratorReads(t); floor != api.WaitingForRunWindow || row != api.WaitingForRunWindow || target != api.WaitingForRunWindow {
		t.Fatalf("an attended worker with no window: floor %q, orchestrator %q, target %q", floor, row, target)
	}
	if _, err := d.Client.OpenWindow(ctx, d.auth.WorkerID, api.OpenWindow{ID: "w1", LengthMs: 60_000}); err != nil {
		t.Fatal(err)
	}
	if floor, row, target := d.orchestratorReads(t); floor != api.WaitingForWork || row != api.WaitingForWork || target != api.WaitingForWork {
		t.Errorf("an open window: floor %q, orchestrator %q, target %q", floor, row, target)
	}
	if err := d.Client.CloseWindow(ctx, d.auth.WorkerID); err != nil {
		t.Fatal(err)
	}
	if err := d.Client.SetAutonomy(ctx, d.auth.WorkerID, api.SetAutonomy{Level: authority.LevelManual}); err != nil {
		t.Fatal(err)
	}
	if floor, row, target := d.orchestratorReads(t); floor != api.WaitingForWork || row != api.WaitingForWork || target != api.WaitingForWork {
		t.Errorf("a manual worker, whom no window lets claim: floor %q, orchestrator %q, target %q", floor, row, target)
	}
	if err := d.Client.SetAutonomy(ctx, d.auth.WorkerID, api.SetAutonomy{Level: authority.LevelAttended}); err != nil {
		t.Fatal(err)
	}
	d.orchestratorSays(t, "provisioning")
	if floor, row, target := d.orchestratorReads(t); floor != "" || row != "" || target != "" {
		t.Errorf("a run a wake reached, with no window open: floor %q, orchestrator %q, target %q", floor, row, target)
	}
	d.orchestratorSays(t, "idle")
	if floor, row, target := d.orchestratorReads(t); floor != "" || row != "" || target != "" {
		t.Errorf("a session that started: floor %q, orchestrator %q, target %q", floor, row, target)
	}
}

// A member who stopped the orchestrator waits for nothing, whatever its worker said last.
func TestAStoppedOrchestratorWaitsForNoWindow(t *testing.T) {
	t.Parallel()
	d := startTalk(t)
	if _, err := d.workers.SetOrchestrator(t.Context(), d.auth.WorkerID, ledger.OrchestratorRunning, talkMember); err != nil {
		t.Fatal(err)
	}
	d.orchestratorSays(t, "provisioning", lanes.AwaitingWake)
	if _, err := d.workers.SetOrchestrator(t.Context(), d.auth.WorkerID, ledger.OrchestratorStopped, talkMember); err != nil {
		t.Fatal(err)
	}
	read, err := d.client.Orchestrator(t.Context(), d.auth.WorkerID)
	if err != nil {
		t.Fatal(err)
	}
	if read.Actual != "provisioning" || read.WaitingFor != "" {
		t.Errorf("a stopped orchestrator reads %+v", read)
	}
}
