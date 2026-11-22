package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/test/apitest"
)

// A lane whose pane shows something only a person can answer, such as a dialog no request covers, waits in Needs you,
// though the call that dialog holds keeps it from reading stalled. A person answers it in the pane, so it offers no
// act on the work.
func TestALaneThatWaitsInItsPaneReachesNeedsYou(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Running("wo-dialog", "Read the configuration").Status(ledger.LaneStatus{
		ActualState: "working", Conditions: []string{"waiting_external", "waiting_in_pane"}, Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable",
	})
	var stuck api.Stuck
	if err := d.Client.ReadView(t.Context(), api.ViewStuck, "", &stuck); err != nil {
		t.Fatal(err)
	}
	if len(stuck.Items) != 1 {
		t.Fatalf("stuck %+v, want the lane that waits in its pane", stuck.Items)
	}
	item := stuck.Items[0]
	if item.ID != "wo-dialog" || item.Reason != api.ReasonWaitingInPane || item.Lane == nil || len(item.Actions) != 0 || item.Since == 0 {
		t.Errorf("item %+v", item)
	}
	var needs api.NeedsYou
	if err := d.Client.ReadView(t.Context(), api.ViewNeedsYou, "", &needs); err != nil {
		t.Fatal(err)
	}
	if len(needs.Items) != 1 || needs.Items[0].Work == nil || needs.Items[0].Work.Reason != api.ReasonWaitingInPane {
		t.Errorf("needs you %+v", needs.Items)
	}
}

// An orchestrator's pane can show a dialog too, and a start would replace the session that shows it, so its item
// offers nothing to press.
func TestAnOrchestratorThatWaitsInItsPaneReachesNeedsYou(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Orchestrator(orchestratorStatus("working", "waiting_external", "waiting_in_pane"))
	items := stuckOrchestrators(t, d)
	if len(items) != 1 || items[0].Reason != api.ReasonWaitingInPane || items[0].Orchestrator == nil || len(items[0].Actions) != 0 {
		t.Fatalf("stuck %+v", items)
	}
}
