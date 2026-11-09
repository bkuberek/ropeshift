package server_test

import (
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

func orchestratorStatus(state string, conditions ...string) ledger.LaneStatus {
	return ledger.LaneStatus{ActualState: state, Conditions: conditions, Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable"}
}

func stuckOrchestrators(t *testing.T, d *apitest.Daemon) []api.StuckItem {
	t.Helper()
	var stuck api.Stuck
	if err := d.Client.ReadView(t.Context(), api.ViewStuck, "", &stuck); err != nil {
		t.Fatal(err)
	}
	return stuck.Items
}

func TestATrippedOrchestratorAsksAPersonAndAStartResetsIt(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Orchestrator(orchestratorStatus("tripped"))
	items := stuckOrchestrators(t, d)
	if len(items) != 1 {
		t.Fatalf("stuck %+v", items)
	}
	item := items[0]
	if item.Reason != api.ReasonTripped || item.Orchestrator == nil || item.Orchestrator.WorkerID != apitest.WorldWorker || item.Since == 0 {
		t.Errorf("item %+v", item)
	}
	if !slices.Equal(item.Actions, []string{api.ActStartOrchestrator}) {
		t.Errorf("a tripped orchestrator offers %v", item.Actions)
	}
	var needs api.NeedsYou
	if err := d.Client.ReadView(t.Context(), api.ViewNeedsYou, "", &needs); err != nil {
		t.Fatal(err)
	}
	if len(needs.Items) != 1 || needs.Items[0].Work == nil || needs.Items[0].Work.Orchestrator == nil {
		t.Errorf("needs you %+v", needs.Items)
	}
	row, err := d.Client.SetOrchestrator(t.Context(), apitest.WorldWorker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning})
	if err != nil {
		t.Fatal(err)
	}
	if row.Resets != 1 || row.Run != 1 {
		t.Errorf("the start left %+v", row)
	}
}

// A stalled session has no breaker to reset, so a start replaces it. The worker stops a run that is no
// longer the one asked for, which leaves the work of the old run to nobody.
func TestAStalledOrchestratorAsksAPersonAndAStartReplacesIt(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Orchestrator(orchestratorStatus("working", "stalled"))
	items := stuckOrchestrators(t, d)
	if len(items) != 1 || items[0].Reason != api.ReasonStalled || !slices.Equal(items[0].Actions, []string{api.ActStartOrchestrator}) {
		t.Fatalf("stuck %+v", items)
	}
	row, err := d.Client.SetOrchestrator(t.Context(), apitest.WorldWorker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning})
	if err != nil {
		t.Fatal(err)
	}
	if row.Run != 2 || row.Resets != 0 || len(row.Conditions) != 0 {
		t.Errorf("the start left %+v", row)
	}
	if items := stuckOrchestrators(t, d); len(items) != 0 {
		t.Errorf("a replaced session is still stuck: %+v", items)
	}
}

func TestAnOrchestratorAtWorkOrStoppedIsNotStuck(t *testing.T) {
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Orchestrator(orchestratorStatus("working"))
	if items := stuckOrchestrators(t, d); len(items) != 0 {
		t.Errorf("at work: %+v", items)
	}
	w.OrchestratorStatus(orchestratorStatus("tripped"))
	if _, err := d.Client.SetOrchestrator(t.Context(), apitest.WorldWorker, api.SetOrchestrator{Desired: ledger.OrchestratorStopped}); err != nil {
		t.Fatal(err)
	}
	if items := stuckOrchestrators(t, d); len(items) != 0 {
		t.Errorf("stopped: %+v", items)
	}
}

func TestAWebPageReachesAnOrchestrator(t *testing.T) {
	if !slices.ContainsFunc(api.WebRoutes(), func(route api.WebRoute) bool {
		return route.Pattern == "/v1/workers/{id}/orchestrator" && route.Command == "orchestrator start"
	}) {
		t.Error("the web listener has no route to an orchestrator")
	}
}
