package control_test

import (
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// TestAnOrchestratorStartsWithItsWorker holds that a worker's session asks for its orchestrator when no member has
// said otherwise, so it runs with no member's Start, and that a member's Stop holds across the worker's next session
// (FR-ORCH-1). A plane that holds the orchestrator, as a test rig's does, asks for none.
func TestAnOrchestratorStartsWithItsWorker(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	if _, err := r.plane.Orchestrator(ctx, "worker-1"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("a plane that holds the orchestrator asked for one: %v", err)
	}
	starting := control.NewPlane(r.state, r.state.Installation, "pa", control.WorkerOptions{StartsOrchestrator: true}, nil)
	open := func() {
		t.Helper()
		if _, err := starting.OpenSession(ctx, ledger.SessionRequest{Auth: r.auth, Protocol: ledger.ProtocolVersion, Manifest: apiKeyManifest}); err != nil {
			t.Fatal(err)
		}
	}
	open()
	row, err := starting.Orchestrator(ctx, "worker-1")
	if err != nil || row.Desired != ledger.OrchestratorRunning || row.Run != 1 {
		t.Fatalf("after the worker's session opened, its orchestrator is %+v, %v, want running on run 1", row, err)
	}
	if _, err := starting.SetOrchestrator(ctx, "worker-1", ledger.OrchestratorStopped, projectMember); err != nil {
		t.Fatal(err)
	}
	open()
	if row, err = starting.Orchestrator(ctx, "worker-1"); err != nil || row.Desired != ledger.OrchestratorStopped {
		t.Errorf("after a member's stop and the next session, the orchestrator is %+v, %v, want stopped", row, err)
	}
}
