package server_test

import (
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// The floor and `orchestrator show` say what keeps a worker's orchestrator from starting: at a usage limit, the window
// and its reset; and a member who presses Start then schedules the start for the reset, which both reads show, and can
// cancel it.
func TestAnOrchestratorAtAUsageLimitStartsAtTheReset(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	reset := time.Now().Add(2 * time.Hour).UnixMilli()
	w.ReportSignIn(ledger.SignInUsage{Limited: true, Window: ledger.UsageWindowSession, ResetsAtMs: reset})
	line := workerLine(t, d, apitest.WorldWorker)
	if line.OrchestratorHold == nil || line.OrchestratorHold.Reason != ledger.HoldUsageLimit || line.OrchestratorHold.ResetsAt != reset || line.OrchestratorHold.Window != ledger.UsageWindowSession {
		t.Fatalf("the floor says %+v of the worker's orchestrator", line.OrchestratorHold)
	}
	start, err := d.Client.ScheduleOrchestrator(t.Context(), apitest.WorldWorker)
	if err != nil {
		t.Fatal(err)
	}
	if start.StartsAt != reset {
		t.Errorf("the start reads %+v, want it at the reset", start)
	}
	if line := workerLine(t, d, apitest.WorldWorker); line.ScheduledStart == nil || line.ScheduledStart.StartsAt != reset {
		t.Errorf("the floor says %+v of the scheduled start", line.ScheduledStart)
	}
	if _, err := d.Client.SetOrchestrator(t.Context(), apitest.WorldWorker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning}); err != nil {
		t.Fatal(err)
	}
	shown, err := d.Client.Orchestrator(t.Context(), apitest.WorldWorker)
	if err != nil {
		t.Fatal(err)
	}
	if shown.Hold == nil || shown.Hold.Reason != ledger.HoldUsageLimit || shown.Scheduled == nil {
		t.Errorf("orchestrator show reads %+v", shown)
	}
	if err := d.Client.CancelScheduledStart(t.Context(), apitest.WorldWorker); err != nil {
		t.Fatal(err)
	}
	if line := workerLine(t, d, apitest.WorldWorker); line.ScheduledStart != nil {
		t.Errorf("a canceled start still reads %+v", line.ScheduledStart)
	}
	var refused *api.Error
	if err := d.Client.CancelScheduledStart(t.Context(), apitest.WorldWorker); !errors.As(err, &refused) || refused.Detail.Code != api.CodeNotFound {
		t.Errorf("a second cancel: %v", err)
	}
}

// A worker whose sign-in is not at its limit gets no scheduled start: a member starts its orchestrator now.
func TestAStartIsScheduledOnlyAtAUsageLimit(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.ReportSignIn(ledger.SignInUsage{Window: ledger.UsageWindowSession})
	var refused *api.Error
	if _, err := d.Client.ScheduleOrchestrator(t.Context(), apitest.WorldWorker); !errors.As(err, &refused) || refused.Detail.Code != api.CodeInvalid {
		t.Errorf("a start scheduled with no limit: %v", err)
	}
}

func workerLine(t *testing.T, d *apitest.Daemon, worker string) api.WorkerLine {
	t.Helper()
	for _, line := range readView[api.ShopFloor](t, d, api.ViewShopFloor, "").Workers {
		if line.Worker.ID == worker {
			return line
		}
	}
	t.Fatalf("the floor holds no worker %s", worker)
	return api.WorkerLine{}
}
