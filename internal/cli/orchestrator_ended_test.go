package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
)

// `orchestrator start` of a run that ended starts the next, and prints it as `orchestrator show` does.
func TestOrchestratorStartRunsAnEndedOrchestratorAgain(t *testing.T) {
	s := newTalking(t)
	ctx, worker := t.Context(), s.auth.WorkerID
	if _, err := s.workers.SetOrchestrator(ctx, worker, ledger.OrchestratorRunning, ledger.Actor{Kind: ledger.ActorMember, ID: "member"}); err != nil {
		t.Fatal(err)
	}
	session, err := s.workers.OpenSession(ctx, ledger.SessionRequest{Auth: s.auth, Protocol: ledger.ProtocolVersion})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeBlocked, Reason: workorders.ReasonUnknownScreen}})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := s.workers.Upload(ctx, ledger.UploadBatch{Auth: s.auth, Epoch: session.Epoch, Events: []ledger.UploadEvent{{
		Epoch: session.Epoch, Seq: 1, Type: ledger.UploadLaneEnded, Lane: "orchestrator-1", Generation: 1, Payload: raw, OccurredAt: 1,
	}}})
	if err != nil || len(reply.Rejected) != 0 {
		t.Fatalf("upload: %+v %v", reply, err)
	}
	if show := s.must("orchestrator", "show", worker); !strings.Contains(show, "reads ended") {
		t.Fatalf("show %q", show)
	}
	if start, show := s.must("orchestrator", "start", worker), s.must("orchestrator", "show", worker); start != show || !strings.Contains(start, "asked to be running, reads no status yet") {
		t.Errorf("start %q and show %q", start, show)
	}
	if row, err := s.workers.Orchestrator(ctx, worker); err != nil || row.Run != 2 {
		t.Errorf("the start left %+v, %v", row, err)
	}
}
