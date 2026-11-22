package server_test

import (
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// A start through the route of a run that ended makes the next run, and answers with it.
func TestAStartThroughTheRouteRunsAnEndedOrchestratorAgain(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	worker := d.auth.WorkerID
	if _, err := d.workers.SetOrchestrator(ctx, worker, ledger.OrchestratorRunning, talkMember); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonUnknownScreen}})
	if err != nil {
		t.Fatal(err)
	}
	d.seq++
	uploaded, err := d.workers.Upload(ctx, ledger.UploadBatch{Auth: d.auth, Epoch: d.epoch, Events: []ledger.UploadEvent{{
		Epoch: d.epoch, Seq: d.seq, Type: ledger.UploadLaneEnded, Lane: "orchestrator-1", Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil || len(uploaded.Rejected) != 0 {
		t.Fatalf("upload: %+v %v", uploaded, err)
	}
	if read, err := d.client.Orchestrator(ctx, worker); err != nil || read.Actual != "ended" {
		t.Fatalf("the route reads %+v, %v", read, err)
	}
	started, err := d.client.SetOrchestrator(ctx, worker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning})
	if err != nil || started.Run != 2 || started.Actual != "" {
		t.Fatalf("the start answered %+v, %v, want run 2", started, err)
	}
	if again, err := d.client.SetOrchestrator(ctx, worker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning}); err != nil || again.Run != 2 {
		t.Errorf("a second start answered %+v, %v", again, err)
	}
}
