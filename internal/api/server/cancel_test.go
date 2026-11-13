package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

func TestAMemberCancelsRunningWork(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo", "Drop the old plan")
	done, err := d.Client.CancelWork(t.Context(), "wo", api.CancelWork{Reason: "The plan changed"})
	if err != nil {
		t.Fatal(err)
	}
	if done.State != workorders.Canceled {
		t.Errorf("answered %+v", done)
	}
	row, err := d.Client.WorkOrder(t.Context(), "wo")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Canceled || row.LeaseWorker != "" {
		t.Errorf("the record reads %+v", row)
	}
	lane.End(workorders.OutcomeFailed, workorders.ReasonFenced)
	view, err := d.Client.Lane(t.Context(), lane.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.ActualState != ledger.StateEnded {
		t.Errorf("the lane reads %s after its worker ended it", view.ActualState)
	}
	var completed api.Completed
	if err := d.Client.ReadView(t.Context(), api.ViewCompleted, "", &completed); err != nil {
		t.Fatal(err)
	}
	if len(completed.Items) != 1 || completed.Items[0].Reason != "The plan changed" {
		t.Errorf("completed %+v", completed.Items)
	}
}

func TestACancelNeedsAReasonAndWorkToEnd(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	_, err := d.Client.CancelWork(t.Context(), "no-such-work", api.CancelWork{Reason: "gone"})
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
	w := apitest.NewWorld(t, d)
	w.Ready("wo", "Drop the old plan")
	_, err = d.Client.CancelWork(t.Context(), "wo", api.CancelWork{})
	refusedWith(t, err, http.StatusBadRequest, api.CodeInvalid)
	if _, err := d.Client.CancelWork(t.Context(), "wo", api.CancelWork{Reason: "gone"}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Client.CancelWork(t.Context(), "wo", api.CancelWork{Reason: "gone"}); err != nil {
		t.Fatalf("a retry after a lost answer: %v", err)
	}
}

// expectRunning says an attempt changed nothing.
func expectRunning(t *testing.T, d *apitest.Daemon, id string) {
	t.Helper()
	row, err := d.Client.WorkOrder(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Running {
		t.Fatalf("%s reads %s", id, row.State)
	}
}

// The attacks that matter come from inside the fleet: everything a lane or an orchestrator can reach
// goes through its worker's credential, so that credential and each thing a worker may say must stop
// short of ending work. Only a member's session may.
func TestNoLaneOrOrchestratorCanCancelWork(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo", "Drop the old plan")
	path := apitest.Route("/v1/work-orders/wo/cancel")
	reason := api.CancelWork{Reason: "not mine to end"}

	anonymous := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer anonymous.Close()
	refusedWith(t, anonymous.Do(t.Context(), http.MethodPost, path, reason, nil), http.StatusUnauthorized, api.CodeSessionRequired)

	worker := anonymous.WithCredential(w.Credential())
	refusedWith(t, worker.Do(t.Context(), http.MethodPost, path, reason, nil), http.StatusUnauthorized, api.CodeSessionRequired)

	forged := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer forged.Close()
	forged.UseSession(w.Credential())
	if err := forged.Do(t.Context(), http.MethodPost, path, reason, nil); err == nil {
		t.Error("a worker's credential passed as a member's session")
	}
	expectRunning(t, d, "wo")

	// An orchestrator's acts are a closed list. A worker that names cancel names nothing.
	if _, err := d.Client.SetOrchestrator(t.Context(), apitest.WorldWorker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning}); err != nil {
		t.Fatal(err)
	}
	call := ledger.OrchestratorCall{Epoch: w.Epoch(), Lane: ledger.Orchestrator{Run: 1}.Lane(), WorkOrder: "wo", Reason: "stuck"}
	acted := worker.Do(t.Context(), http.MethodPost, "/v1/workers/"+apitest.WorldWorker+"/orchestrator/acts/cancel", call, nil)
	refusedWith(t, acted, http.StatusBadRequest, api.CodeInvalid)
	expectRunning(t, d, "wo")

	// A lane's words are events with a fence, and naming the edge moves nothing.
	lane.Upload("canceled", json.RawMessage(`{"trigger":"canceled"}`))
	lane.Upload(workorders.EventType(workorders.TriggerCanceled), json.RawMessage(`{"trigger":"canceled"}`))
	expectRunning(t, d, "wo")

	// A lane's end moves work by its outcome, and no outcome means canceled.
	refused := workorders.LaneResult{Report: workorders.Report{Outcome: "canceled", Reason: "not mine to end"}}
	raw, err := json.Marshal(refused)
	if err != nil {
		t.Fatal(err)
	}
	if rejected := lane.Refused(ledger.UploadLaneEnded, raw); len(rejected) != 1 {
		t.Errorf("an outcome of canceled was taken: %+v", rejected)
	}
	expectRunning(t, d, "wo")

	// The use case holds the same line for whoever reaches it, even with a store in hand.
	leases := control.NewLeases(d.Store)
	for _, actor := range []ledger.Actor{
		{Kind: ledger.ActorLane, ID: lane.ID}, {Kind: ledger.ActorOrchestrator, ID: apitest.WorldWorker}, {Kind: ledger.ActorWorker, ID: apitest.WorldWorker},
	} {
		if err := leases.Cancel(context.Background(), apitest.Scope, "wo", "not mine to end", actor); !errors.Is(err, workorders.ErrForbidden) {
			t.Errorf("%s: %v", actor.Kind, err)
		}
	}
	expectRunning(t, d, "wo")
}

func TestAWebPageCancelsThroughItsOwnRoute(t *testing.T) {
	t.Parallel()
	if !slices.ContainsFunc(api.WebRoutes(), func(route api.WebRoute) bool {
		return route.Method == http.MethodPost && route.Pattern == api.ProjectRoute("/v1/work-orders/{id}/cancel") && route.Command == "work cancel"
	}) {
		t.Error("the web listener has no route to cancel work")
	}
}

func TestStuckWorkOffersACancel(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo", "Stuck on a question")
	lane.End(workorders.OutcomeBlocked, "no_outcome")
	var stuck api.Stuck
	if err := d.Client.ReadView(t.Context(), api.ViewStuck, "", &stuck); err != nil {
		t.Fatal(err)
	}
	if len(stuck.Items) != 1 || !slices.Contains(stuck.Items[0].Actions, api.ActCancel) {
		t.Errorf("stuck %+v", stuck.Items)
	}
}

// A lane whose work ended can stop nowhere a person could act: its tripped breaker counts for nothing
// once its work is gone.
func TestALaneOfCanceledWorkIsNotStuck(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	lane := w.Running("wo", "Tripped and dropped")
	lane.Status(ledger.LaneStatus{ActualState: "tripped", Liveness: "dead", ModelReach: "reachable", LedgerReach: "reachable"})
	if _, err := d.Client.CancelWork(t.Context(), "wo", api.CancelWork{Reason: "dropped"}); err != nil {
		t.Fatal(err)
	}
	var stuck api.Stuck
	if err := d.Client.ReadView(t.Context(), api.ViewStuck, "", &stuck); err != nil {
		t.Fatal(err)
	}
	if len(stuck.Items) != 0 {
		t.Errorf("stuck %+v", stuck.Items)
	}
}
