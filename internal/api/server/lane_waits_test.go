package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// workLine finds a work order's line in the in-progress view.
func workLine(t *testing.T, d *apitest.Daemon, id string) api.WorkLine {
	t.Helper()
	for _, item := range readView[api.InProgress](t, d, api.ViewInProgress, "").Items {
		if item.WorkOrder.ID == id {
			return item
		}
	}
	t.Fatalf("%s is not in progress", id)
	return api.WorkLine{}
}

// TestALaneShowsItWaitsOnTheOrchestrator holds that a lane's blocking question, while it climbs the ladder, shows
// on its work as a wait on the orchestrator, since the lane asked it, and that a question the lane can go on
// without shows no wait (FR-ORCH-1).
func TestALaneShowsItWaitsOnTheOrchestrator(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	lane := w.Running("wo-asks", "Ask what blocks")
	ask := func(id string, blocking bool) {
		t.Helper()
		_, err := w.Questions.Ask(t.Context(), apitest.Scope, questions.Ask{
			ID: id, Text: "Which queue carries retries, " + id + "?", Categories: []string{questions.CategoryArchitecture},
			Blocking: blocking, Lane: lane.ID, Generation: lane.Generation(), SubjectKind: workorders.SubjectKind, SubjectID: "wo-asks",
		}, ledger.Actor{Kind: ledger.ActorLane, ID: lane.ID})
		if err != nil {
			t.Fatal(err)
		}
	}
	ask("q-goes-on", false)
	if waits := workLine(t, d, "wo-asks").WaitsOn; waits != nil {
		t.Errorf("a lane with a question it goes on without waits on %+v", waits)
	}
	ask("q-blocks", true)
	waits := workLine(t, d, "wo-asks").WaitsOn
	if waits == nil || waits.Who != api.WaitsOnOrchestrator || waits.Question != "q-blocks" || waits.Since == 0 {
		t.Errorf("a lane with a blocking question waits on %+v, want the orchestrator, for q-blocks, with a time", waits)
	}
}
