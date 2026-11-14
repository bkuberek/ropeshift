package entry

import (
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/internal/workorders"
)

// TestTheDeliveryTickSharesEachProjectsHistory runs the daemon's delivery tick over an installation: a project
// whose estimate policy shares history puts its finished work in the pool on the tick, where another project
// reads it, and the project that does not share puts nothing there (ADR-0026, FR-EST-13).
func TestTheDeliveryTickSharesEachProjectsHistory(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	ctx := t.Context()
	state, home, err := sqlite.Upgrade(ctx, t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer home.Close()
	beta, _, err := state.CreateProject(ctx, ledger.Project{ID: "beta-id", Name: "beta"}, member)
	if err != nil {
		t.Fatal(err)
	}
	betaStore, err := state.Open(ctx, beta)
	if err != nil {
		t.Fatal(err)
	}
	finished := func(store ports.Store, scope ledger.WorkspaceID, id string) {
		t.Helper()
		record := delivery.Record{WorkOrder: id, Keys: delivery.Keys{Kind: "change", Size: "medium"}, Outcome: workorders.Done, Ended: 100, DeliveryMs: 60}
		err := store.Fold(ctx, scope, func(tx ports.FoldTx) error {
			return tx.Delivery().Save(delivery.Timeline{WorkOrder: id, Version: delivery.Version, Terminal: workorders.Done, End: 100}, &record)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	finished(betaStore, beta.ID, "beta-work")
	finished(home, soloScope, "home-work")
	draft := delivery.EstimatePolicyDraft{
		Body:   "[estimate_policy]\nversion = 2\nmin_samples = 10\nrecent_samples = 50\nforecast_trials = 2000\nshare_history = true\n",
		Policy: delivery.EstimatePolicy{Version: 2, MinSamples: 10, RecentSamples: 50, ForecastTrials: 2000, ShareHistory: true},
	}
	if _, err := control.NewEstimatePolicies(betaStore).PublishEstimatePolicy(ctx, beta.ID, draft, member); err != nil {
		t.Fatal(err)
	}
	ticks := soloTicks(state, soloScope, soloOptions{deliveryTick: time.Second}, fake.NewCheckouts(), questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "delivery" })
	if at < 0 {
		t.Fatal("no delivery tick")
	}
	if err := ticks[at].Run(ctx); err != nil {
		t.Fatal(err)
	}
	pool := func(store ports.Store, scope ledger.WorkspaceID) []delivery.Record {
		t.Helper()
		var out []delivery.Record
		if err := store.Read(ctx, scope, func(tx ports.ReadTx) error {
			var err error
			out, err = tx.Pool().Records()
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if shared := pool(home, soloScope); len(shared) != 1 || shared[0].WorkOrder != "" || shared[0].DeliveryMs != 60 {
		t.Errorf("after the tick, the first project reads %+v from the pool", shared)
	}
	if own := pool(betaStore, beta.ID); len(own) != 0 {
		t.Errorf("the first project, which does not share, put %+v in the pool", own)
	}
}
