package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// keepRecords keeps finished work's delivery records in a project, as its fold would.
func keepRecords(t *testing.T, store ports.Store, scope ledger.WorkspaceID, records []delivery.Record) {
	t.Helper()
	err := store.Fold(t.Context(), scope, func(tx ports.FoldTx) error {
		for _, r := range records {
			if err := tx.Delivery().Save(delivery.Timeline{WorkOrder: r.WorkOrder, Version: delivery.Version, Terminal: r.Outcome, End: r.Ended}, &r); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// poolOf reads the pool as a project's transaction reads it.
func poolOf(t *testing.T, store ports.Store, scope ledger.WorkspaceID) []delivery.Record {
	t.Helper()
	var out []delivery.Record
	err := store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Pool().Records()
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// shareHistory publishes a version of a project's estimate policy that shares its history, or stops sharing it.
func shareHistory(t *testing.T, store ports.Store, scope ledger.WorkspaceID, version int64, share bool) {
	t.Helper()
	body := "[estimate_policy]\nversion = " + strconv.FormatInt(version, 10) + "\nmin_samples = 10\nrecent_samples = 50\nforecast_trials = 2000\nshare_history = false\n"
	if share {
		body = strings.Replace(body, "share_history = false", "share_history = true", 1)
	}
	draft := delivery.EstimatePolicyDraft{Body: body, Policy: delivery.EstimatePolicy{Version: version, MinSamples: 10, RecentSamples: 50, ForecastTrials: 2000, ShareHistory: share}}
	if _, err := control.NewEstimatePolicies(store).PublishEstimatePolicy(t.Context(), scope, draft, member); err != nil {
		t.Fatal(err)
	}
}

// currentEstimate reads a work order's current estimate.
func currentEstimate(t *testing.T, store ports.Store, scope ledger.WorkspaceID, order string) delivery.Estimate {
	t.Helper()
	var out delivery.Estimate
	err := store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.Estimates().Current(order)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestTheInstallationPoolHoldsOnlyWhatSharingProjectsShare holds the installation's pool (ADR-0026, FR-EST-13):
// a project's delivery records enter it only while its estimate policy shares history, stripped to kind, size,
// capability class, durations, cost, outcome, and end; only a project that shares history draws on it, and never
// on its own records twice; and a project that stops sharing takes its records out.
func TestTheInstallationPoolHoldsOnlyWhatSharingProjectsShare(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	beta, betaStore := createProject(t, state, "pb", "beta")
	ctx := t.Context()
	finished := []delivery.Record{
		{WorkOrder: "wo-a1", Keys: delivery.Keys{Kind: "change", Size: "medium", Agent: "implementer", CapabilityClass: "standard", Repository: "repo-a", MergeAuthority: "person_merges", Criteria: 2},
			Model: "a-model", Plan: "plan-a", Outcome: workorders.Done, Ended: 100, ElapsedMs: 90, DeliveryMs: 80, LaneMs: 50, PersonMs: 20, PersonWaits: []int64{20},
			Usage: delivery.Usage{InputTokens: 9, CostUSD: 1, CostLowerBound: true}},
		{WorkOrder: "wo-a2", Keys: delivery.Keys{Kind: "change", Size: "medium", Agent: "implementer", CapabilityClass: "standard", Repository: "repo-a", MergeAuthority: "person_merges", Criteria: 1},
			Outcome: workorders.Done, Ended: 200, ElapsedMs: 190, DeliveryMs: 180, LaneMs: 150, Usage: delivery.Usage{CostUSD: 2}},
		{WorkOrder: "wo-a3", Keys: delivery.Keys{Kind: "change", Size: "medium", CapabilityClass: "standard"}, Outcome: workorders.Failed, Ended: 300, ElapsedMs: 10},
	}
	keepRecords(t, alphaStore, alpha.ID, finished)
	pool := control.NewDeliveryPool(alphaStore)
	if changed, err := pool.Share(ctx, alpha.ID); err != nil || changed {
		t.Fatalf("a project that does not share history shared: %v, %v", changed, err)
	}
	if shared := poolOf(t, betaStore, beta.ID); len(shared) != 0 {
		t.Fatalf("the pool holds %+v before anyone shared", shared)
	}

	shareHistory(t, alphaStore, alpha.ID, 2, true)
	if changed, err := pool.Share(ctx, alpha.ID); err != nil || !changed {
		t.Fatalf("a project that shares history did not share: %v, %v", changed, err)
	}
	if changed, err := pool.Share(ctx, alpha.ID); err != nil || changed {
		t.Fatalf("sharing the same records again changed the pool: %v, %v", changed, err)
	}
	if shared := poolOf(t, betaStore, beta.ID); len(shared) != 2 || !reflect.DeepEqual(shared, delivery.Pooled(finished)) {
		t.Errorf("another project reads %+v, want %+v", shared, delivery.Pooled(finished))
	}
	if own := poolOf(t, alphaStore, alpha.ID); len(own) != 0 {
		t.Errorf("a project reads its own records from the pool: %+v", own)
	}
	db, err := sqlite.OpenDB(filepath.Join(dir, sqlite.InstallationFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if columns := columnsOf(t, db, "delivery_pool"); !slices.Equal(columns, []string{
		"capability_class", "cost_lower_bound", "cost_usd", "delivery_ms", "elapsed_ms", "ended_at", "kind", "lane_ms",
		"person_ms", "project_id", "seq", "size", "workspace_id",
	}) {
		t.Errorf("the pool's columns are %v", columns)
	}

	// A project that does not share history does not draw on the pool, and says so.
	if _, err := control.NewWorkOrders(betaStore).Create(ctx, beta.ID, workorders.WorkOrder{ID: "wo-b1", Kind: workorders.KindChange, Title: "Alone", Size: "medium", MaxAttempts: 1}, member); err != nil {
		t.Fatal(err)
	}
	if alone := currentEstimate(t, betaStore, beta.ID, "wo-b1"); alone.Status != delivery.NoHistory || !strings.Contains(alone.Explanation, "does not share history") {
		t.Errorf("a project that does not share history is estimated %+v", alone)
	}
	shareHistory(t, betaStore, beta.ID, 2, true)
	if _, err := control.NewWorkOrders(betaStore).Create(ctx, beta.ID, workorders.WorkOrder{ID: "wo-b2", Kind: workorders.KindChange, Title: "Pooled", Size: "medium", MaxAttempts: 1}, member); err != nil {
		t.Fatal(err)
	}
	pooled := currentEstimate(t, betaStore, beta.ID, "wo-b2")
	if pooled.Status != delivery.Estimated || pooled.Tier != 6 || pooled.PoolSamples != 2 || len(pooled.SampleIDs) != 0 || !pooled.CostAtLeast ||
		!strings.Contains(pooled.Explanation, "installation pool") || strings.Contains(pooled.Explanation, "wo-a") {
		t.Errorf("a project that shares history is estimated %+v", pooled)
	}

	// A project that stops sharing takes its records out as the member publishes it, with no tick.
	shareHistory(t, alphaStore, alpha.ID, 3, false)
	if shared := poolOf(t, betaStore, beta.ID); len(shared) != 0 {
		t.Errorf("after alpha stopped sharing, the pool holds %+v", shared)
	}
	if changed, err := pool.Share(ctx, alpha.ID); err != nil || changed {
		t.Errorf("a tick after alpha stopped sharing changed the pool: %v, %v", changed, err)
	}
	// A share read under the policy before cannot put the records back.
	if err := alphaStore.Installation(alpha.ID).SharePool(ctx, alpha.ID, 2, delivery.PoolDigest(delivery.Pooled(finished)), finished); err != nil {
		t.Fatal(err)
	}
	if shared := poolOf(t, betaStore, beta.ID); len(shared) != 0 {
		t.Errorf("a share under alpha's earlier policy put back %+v", shared)
	}
	// Beta's estimates stop drawing on alpha's records once its tick runs, and its earlier set keeps what it showed.
	if _, err := control.NewEstimates(betaStore).Refresh(ctx, beta.ID); err != nil {
		t.Fatal(err)
	}
	after := currentEstimate(t, betaStore, beta.ID, "wo-b2")
	if after.Status != delivery.NoHistory || !strings.Contains(after.Explanation, "or in the installation pool") || after.SetID == pooled.SetID {
		t.Errorf("after alpha stopped sharing, beta's work is estimated %+v", after)
	}
	var kept delivery.EstimateSet
	if err := betaStore.Read(ctx, beta.ID, func(tx ports.ReadTx) error {
		var err error
		kept, err = tx.Estimates().Set(pooled.SetID)
		return err
	}); err != nil || len(kept.Estimates) != 1 || kept.Estimates[0].Tier != 6 || kept.Estimates[0].Current {
		t.Errorf("beta's earlier set now reads %+v: %v", kept, err)
	}
}

// sharing makes a project in an installation whose finished change is shared in the pool, and returns it.
func sharing(t *testing.T, state *sqlite.State, id ledger.WorkspaceID, name string) (ledger.Project, ports.Store) {
	t.Helper()
	project, store := createProject(t, state, id, name)
	keepRecords(t, store, project.ID, []delivery.Record{{WorkOrder: "wo-" + string(id), Keys: delivery.Keys{Kind: "change", Size: "medium"}, Outcome: workorders.Done, Ended: 100, DeliveryMs: 60}})
	shareHistory(t, store, project.ID, 2, true)
	if changed, err := control.NewDeliveryPool(store).Share(t.Context(), project.ID); err != nil || !changed {
		t.Fatalf("%s did not share: %v, %v", name, changed, err)
	}
	return project, store
}

// TestARemovedOrArchivedProjectLeavesThePool takes a project's share out of the pool when it is removed or
// archived, because neither runs a tick to take it out later; a restored project shares again on its next tick
// while its policy still shares history.
func TestARemovedOrArchivedProjectLeavesThePool(t *testing.T) {
	dir := t.TempDir()
	state := openState(t, dir)
	reader, readerStore := createProject(t, state, "pr", "reader")
	ctx := t.Context()
	removed, _ := sharing(t, state, "pg", "gone")
	archived, archivedStore := sharing(t, state, "ph", "shelved")
	if shared := poolOf(t, readerStore, reader.ID); len(shared) != 2 {
		t.Fatalf("the pool holds %+v", shared)
	}
	if err := state.Remove(ctx, removed, member); err != nil {
		t.Fatal(err)
	}
	if shared := poolOf(t, readerStore, reader.ID); len(shared) != 1 {
		t.Errorf("after a project was removed, the pool holds %+v", shared)
	}
	if _, err := state.SetState(ctx, archived.ID, ledger.ProjectArchived, member); err != nil {
		t.Fatal(err)
	}
	if shared := poolOf(t, readerStore, reader.ID); len(shared) != 0 {
		t.Errorf("after a project was archived, the pool holds %+v", shared)
	}
	db, err := sqlite.OpenDB(filepath.Join(dir, sqlite.InstallationFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var shares int
	if err := db.QueryRow(`SELECT count(*) FROM delivery_pool_shares`).Scan(&shares); err != nil || shares != 0 {
		t.Errorf("%d shares are left: %v", shares, err)
	}
	// A share read before the archive cannot put the records back while the project stands archived.
	if err := archivedStore.Installation(archived.ID).SharePool(ctx, archived.ID, 2, "sha256:late", []delivery.Record{{Keys: delivery.Keys{Kind: "change", Size: "medium"}, Outcome: workorders.Done}}); err != nil {
		t.Fatal(err)
	}
	if shared := poolOf(t, readerStore, reader.ID); len(shared) != 0 {
		t.Errorf("a late share of an archived project put back %+v", shared)
	}
	if _, err := state.SetState(ctx, archived.ID, ledger.ProjectActive, member); err != nil {
		t.Fatal(err)
	}
	if changed, err := control.NewDeliveryPool(archivedStore).Share(ctx, archived.ID); err != nil || !changed {
		t.Fatalf("a restored project that shares history did not share: %v, %v", changed, err)
	}
	if shared := poolOf(t, readerStore, reader.ID); len(shared) != 1 {
		t.Errorf("after the project was restored, the pool holds %+v", shared)
	}
}

// failingFold is a store whose fold fails, as one with a corrupt delivery row would.
type failingFold struct{ ports.Store }

func (failingFold) Fold(context.Context, ledger.WorkspaceID, func(ports.FoldTx) error) error {
	return errors.New("the fold failed")
}

// TestTheDeliveryTickSharesWhenTheFoldFails runs a project's share even when its fold fails, so a project's choice
// to share or not never waits on its fold.
func TestTheDeliveryTickSharesWhenTheFoldFails(t *testing.T) {
	state := openState(t, t.TempDir())
	reader, readerStore := createProject(t, state, "pr", "reader")
	alpha, alphaStore := createProject(t, state, "pa", "alpha")
	keepRecords(t, alphaStore, alpha.ID, []delivery.Record{{WorkOrder: "wo-1", Keys: delivery.Keys{Kind: "change", Size: "medium"}, Outcome: workorders.Done, Ended: 100, DeliveryMs: 60}})
	shareHistory(t, alphaStore, alpha.ID, 2, true)
	if err := control.DeliveryTick(t.Context(), failingFold{alphaStore}, alpha.ID); err == nil {
		t.Error("a failed fold was not reported")
	}
	if shared := poolOf(t, readerStore, reader.ID); len(shared) != 1 {
		t.Errorf("with the fold failing, the pool holds %+v", shared)
	}
}

// TestAStoreWithNoInstallationKeepsNoPool shares nothing and searches nothing where no other project can be: a
// store with no installation database.
func TestAStoreWithNoInstallationKeepsNoPool(t *testing.T) {
	store, err := sqlite.OpenMemory(sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := t.Context()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(ctx, ledger.Workspace{ID: "solo", Name: "Solo"}, member); err != nil {
		t.Fatal(err)
	}
	keepRecords(t, store, "solo", []delivery.Record{{WorkOrder: "wo-1", Keys: delivery.Keys{Kind: "change", Size: "large"}, Outcome: workorders.Done, Ended: 100, DeliveryMs: 60}})
	shareHistory(t, store, "solo", 2, true)
	if changed, err := control.NewDeliveryPool(store).Share(ctx, "solo"); err != nil || changed {
		t.Errorf("a store with no installation shared: %v, %v", changed, err)
	}
	if _, err := control.NewWorkOrders(store).Create(ctx, "solo", workorders.WorkOrder{ID: "wo-2", Kind: workorders.KindChange, Title: "Alone", Size: "medium", MaxAttempts: 1}, member); err != nil {
		t.Fatal(err)
	}
	if e := currentEstimate(t, store, "solo", "wo-2"); e.Status != delivery.NoHistory || strings.Contains(e.Explanation, "pool") {
		t.Errorf("with no installation, the estimate reads %+v", e)
	}
}

// columnsOf lists a table's columns by name.
func columnsOf(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?1) ORDER BY name`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}
