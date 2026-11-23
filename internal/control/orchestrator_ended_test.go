package control_test

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// saysOfRun uploads the worker's word on a run of its orchestrator.
func (r *planeRig) saysOfRun(seq int64, lane, kind string, payload any) {
	r.t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		r.t.Fatal(err)
	}
	reply, err := r.plane.Upload(r.t.Context(), ledger.UploadBatch{Auth: r.auth, Epoch: r.epoch, Events: []ledger.UploadEvent{{
		Epoch: r.epoch, Seq: seq, Type: kind, Lane: lane, Generation: 1, Payload: raw, OccurredAt: seq,
	}}})
	if err != nil || len(reply.Rejected) != 0 {
		r.t.Fatalf("upload %s: %+v, %v", kind, reply, err)
	}
}

// endedRun starts the orchestrator and has its worker end that run.
func (r *planeRig) endedRun() ledger.Orchestrator {
	r.t.Helper()
	row, err := r.plane.SetOrchestrator(r.t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember)
	if err != nil {
		r.t.Fatal(err)
	}
	r.saysOfRun(1, row.Lane(), ledger.UploadLaneEnded, workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonUnknownScreen}})
	row, err = r.plane.Orchestrator(r.t.Context(), "worker-1")
	if err != nil {
		r.t.Fatal(err)
	}
	return row
}

// A run's end keeps the reason its worker gave, and the cause beside it as plain text with no credential, which Needs
// you shows. The next run says nothing of the end before it.
func TestAnEndedOrchestratorKeepsWhyItEnded(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	row, err := r.plane.SetOrchestrator(t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	r.saysOfRun(1, row.Lane(), ledger.UploadLaneEnded, workorders.LaneResult{Report: workorders.Report{
		Outcome: workorders.OutcomeFailed, Reason: workorders.ReasonSpawnFailed, Detail: "open pane:\x1b[31m token ghp_0123456789abcdefghijklmnopqrstuvwxyzAB",
	}})
	ended, err := r.plane.Orchestrator(t.Context(), "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if ended.Actual != "ended" || ended.EndReason != workorders.ReasonSpawnFailed {
		t.Fatalf("the ended run reads %+v, want its reason", ended)
	}
	if want := ledger.SanitizeCIText("open pane:\x1b[31m token ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"); ended.EndDetail != want || strings.Contains(ended.EndDetail, "ghp_") {
		t.Errorf("the ended run keeps %q as its cause, want %q", ended.EndDetail, want)
	}
	next, err := r.plane.SetOrchestrator(t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	if next.Run != 2 || next.EndReason != "" || next.EndDetail != "" {
		t.Errorf("the next run reads %+v, want nothing of the end before it", next)
	}
}

// A member's start of a run that ended makes the next run, once however many starts race, and a start of that live run
// asks for nothing new.
func TestAStartOfAnEndedOrchestratorMakesOneNextRun(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	if row := r.endedRun(); row.Desired != ledger.OrchestratorRunning || row.Run != 1 || row.Actual != "ended" {
		t.Fatalf("the ended run reads %+v", row)
	}
	var starts sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		starts.Go(func() {
			if _, err := r.plane.SetOrchestrator(t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember); err != nil {
				failures <- err
			}
		})
	}
	starts.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	row, err := r.plane.Orchestrator(t.Context(), "worker-1")
	if err != nil || row.Run != 2 || row.Actual != "" || row.Resets != 0 {
		t.Fatalf("8 starts of an ended run left %+v, %v, want run 2 and nothing said of it", row, err)
	}
	if desired := r.desired(); desired.Orchestrator == nil || desired.Orchestrator.Lane != row.Lane() {
		t.Errorf("the worker is asked for %+v, want lane %s", desired.Orchestrator, row.Lane())
	}
	for seq, state := range []string{"provisioning", "idle", "backoff"} {
		r.saysOfRun(int64(seq+2), row.Lane(), ledger.UploadLaneStatus, ledger.LaneStatus{ActualState: state})
		if again, err := r.plane.SetOrchestrator(t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember); err != nil || again.Run != 2 {
			t.Errorf("a start of a run that reads %s left %+v, %v", state, again, err)
		}
	}
}

// A member's stop of an ended orchestrator stands across the worker's next session, until a member starts it again.
func TestAStopOfAnEndedOrchestratorStands(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	enrollMember(t, r)
	r.endedRun()
	if _, err := r.plane.SetOrchestrator(ctx, "worker-1", ledger.OrchestratorStopped, projectMember); err != nil {
		t.Fatal(err)
	}
	starting := control.NewPlane(r.state, r.state.Installation, "pa", control.WorkerOptions{StartsOrchestrator: true}, nil)
	if _, err := starting.OpenSession(ctx, ledger.SessionRequest{Auth: r.auth, Protocol: ledger.ProtocolVersion, Manifest: apiKeyManifest}); err != nil {
		t.Fatal(err)
	}
	if row, err := starting.Orchestrator(ctx, "worker-1"); err != nil || row.Desired != ledger.OrchestratorStopped || row.Run != 1 {
		t.Fatalf("a stopped orchestrator came back: %+v, %v", row, err)
	}
	if row, err := starting.SetOrchestrator(ctx, "worker-1", ledger.OrchestratorRunning, projectMember); err != nil || row.Run != 2 {
		t.Errorf("a start after the stop left %+v, %v", row, err)
	}
}

// forcedInterruptions counts the forced acts a project's measures read.
func (r *planeRig) forcedInterruptions() int {
	r.t.Helper()
	forced := 0
	err := r.stores["pa"].Read(r.t.Context(), "pa", func(tx ports.ReadTx) error {
		history, err := control.HistoryIn(tx)
		for _, occasion := range history.Interruptions {
			if occasion.Kind == measures.InterruptionForced {
				forced++
			}
		}
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return forced
}

// A start of a run that ended makes the next run, as a start of a stopped one does, so it counts as no forced act.
// Replacing a run that stalled is one.
func TestAStartOfAnEndedOrchestratorIsNoForcedInterruption(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ended := r.endedRun()
	next, err := r.plane.SetOrchestrator(t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember)
	if err != nil {
		t.Fatal(err)
	}
	if forced := r.forcedInterruptions(); forced != 0 {
		t.Errorf("a start of run %d, which ended, counted %d forced interruptions, want none", ended.Run, forced)
	}
	r.saysOfRun(2, next.Lane(), ledger.UploadLaneStatus, ledger.LaneStatus{ActualState: "working", Conditions: []string{"stalled"}})
	if _, err := r.plane.SetOrchestrator(t.Context(), "worker-1", ledger.OrchestratorRunning, projectMember); err != nil {
		t.Fatal(err)
	}
	if forced := r.forcedInterruptions(); forced != 1 {
		t.Errorf("replacing a run that stalled counted %d forced interruptions, want 1", forced)
	}
}
