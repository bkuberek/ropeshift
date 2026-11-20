package server_test

import (
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

// readyWaits reads what the plans view says holds each ready work order from a claim.
func readyWaits(t *testing.T, d *apitest.Daemon) map[string]api.ReadyWait {
	t.Helper()
	var view api.Plans
	if err := d.Client.ReadView(t.Context(), api.ViewPlans, "", &view); err != nil {
		t.Fatal(err)
	}
	return view.ReadyWaits
}

func waitsFor(t *testing.T, d *apitest.Daemon, step string, want map[string]api.ReadyWait) {
	t.Helper()
	if got := readyWaits(t, d); !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: ready waits %+v, want %+v", step, got, want)
	}
}

// FR-UI-13: ready work that no worker's orchestrator will claim says why, from the state the claim reads: the fleet,
// the caps, each worker's level and run window, and whether a member wants its orchestrator running. Work a worker
// will claim says nothing, and so does work that is not ready.
func TestReadyWorkSaysWhyNoWorkerClaimsIt(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	ctx, worker, solo := t.Context(), apitest.WorldWorker, "solo"
	w.Ready("wo-1", "Write the guide")
	w.Recorded("wo-later", "Not ready yet")
	waitsFor(t, d, "no orchestrator", map[string]api.ReadyWait{
		"wo-1": {Reason: api.ReadyWaitsForOrchestrator, Worker: worker, WorkerName: solo},
	})
	if _, err := d.Client.SetOrchestrator(ctx, worker, api.SetOrchestrator{Desired: ledger.OrchestratorRunning}); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "an attended worker with no window", map[string]api.ReadyWait{
		"wo-1": {Reason: api.ReadyWaitsForRunWindow, Worker: worker, WorkerName: solo},
	})
	if _, err := d.Client.OpenWindow(ctx, worker, api.OpenWindow{ID: "w1", LengthMs: 60_000}); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "an open window", nil)
	autonomy := control.NewAutonomy(d.Store)
	if err := autonomy.SetLevel(ctx, apitest.Scope, worker, authority.LevelManual, talkMember); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "a manual worker", map[string]api.ReadyWait{
		"wo-1": {Reason: api.ReadyWaitsForManual, Worker: worker, WorkerName: solo},
	})
	if err := autonomy.SetLevel(ctx, apitest.Scope, worker, authority.LevelAttended, talkMember); err != nil {
		t.Fatal(err)
	}
	fleet := control.NewFleet(d.Store)
	if err := fleet.SetCaps(ctx, apitest.Scope, ledger.Caps{WorkerLimit: 1}, talkMember); err != nil {
		t.Fatal(err)
	}
	w.Ready("wo-held", "Hold the only place")
	if _, err := d.Client.Claim(ctx, "wo-held", api.ClaimRequest{ClaimID: "claim-held", WorkerID: worker}); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "a worker at its cap", map[string]api.ReadyWait{
		"wo-1": {Reason: api.ReadyWaitsForCap, Worker: worker, WorkerName: solo, Cap: api.CapLanesPerWorker},
	})
	if err := fleet.SetCaps(ctx, apitest.Scope, ledger.Caps{WIPLimit: 1}, talkMember); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "a workspace at its cap", map[string]api.ReadyWait{
		"wo-1": {Reason: api.ReadyWaitsForCap, Cap: api.CapWorkInProgress},
	})
	if err := fleet.SetCaps(ctx, apitest.Scope, ledger.Caps{}, talkMember); err != nil {
		t.Fatal(err)
	}
	if err := fleet.SetMode(ctx, apitest.Scope, ledger.FleetPause, talkMember); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "a paused fleet", map[string]api.ReadyWait{
		"wo-1": {Reason: api.ReadyWaitsForFleet, Fleet: ledger.FleetPause},
	})
}

// Work whose needs no worker meets waits for one that can run it, and with several workers a page names the one
// nearest to claiming: one a window would let claim before one with no orchestrator.
func TestReadyWorkNamesTheWorkerNearestToClaimingIt(t *testing.T) {
	t.Parallel()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	ctx := t.Context()
	w.Worker("worker-b", "duo", ledger.Manifest{})
	if _, err := d.Client.SetOrchestrator(ctx, "worker-b", api.SetOrchestrator{Desired: ledger.OrchestratorRunning}); err != nil {
		t.Fatal(err)
	}
	w.Ready("wo-1", "Write the guide")
	work := control.LedgerWork{
		Order: workorders.WorkOrder{
			ID: "wo-gpu", Kind: workorders.KindChange, Title: "Train the model", MaxAttempts: 1, RepositoryID: "repository-1",
			Needs: map[string]string{"gpu": "yes"},
		},
		Statement: "Train the model.", Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}
	if _, err := control.NewWorkOrders(d.Store).Record(ctx, apitest.Scope, work, talkMember); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewReviews(d.Store).Pass(ctx, apitest.Scope, "wo-gpu", talkMember); err != nil {
		t.Fatal(err)
	}
	waitsFor(t, d, "two workers", map[string]api.ReadyWait{
		"wo-1":   {Reason: api.ReadyWaitsForRunWindow, Worker: "worker-b", WorkerName: "duo"},
		"wo-gpu": {Reason: api.ReadyWaitsForWorker},
	})
}
