package worker_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// installationRig is one worker on the plane of an installation with two projects, each in its own
// database, and a fake driver.
type installationRig struct {
	t       *testing.T
	state   *sqlite.State
	plane   *control.Plane
	stores  map[ledger.WorkspaceID]ports.Store
	journal *fake.Journal
	driver  *fake.AgentDriver
	subject *worker.Worker
	profile *fake.ProfileSource
}

func newInstallationRig(t *testing.T) *installationRig {
	t.Helper()
	ctx := t.Context()
	state, err := sqlite.OpenState(ctx, t.TempDir(), sqlstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	r := &installationRig{t: t, state: state, stores: map[ledger.WorkspaceID]ports.Store{}}
	for _, p := range []ledger.Project{{ID: "pa", Name: "alpha"}, {ID: "pb", Name: "beta"}} {
		_, store, err := state.CreateProject(ctx, p, member)
		if err != nil {
			t.Fatal(err)
		}
		r.stores[p.ID] = store
	}
	r.plane = control.NewPlane(state, state.Installation, "pa", control.WorkerOptions{HeartbeatMs: 20, LeaseTTLMs: 60_000}, nil)
	if r.journal, err = fake.NewJournal(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.journal.Close() })
	if err := r.journal.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	r.driver = fake.NewAgentDriver()
	r.profile = fake.NewProfileSource()
	dir := t.TempDir()
	config := &worker.LaneRuntime{
		Driver: r.driver,
		Provisioner: worker.Provisioner{
			Profiles: r.profile, Worktrees: fake.NewWorktrees(), Hooks: fake.NewHookRunner(),
			Lookup: func(string) (string, bool) { return "", false },
		},
		Secrets:    fake.SecretResolver{},
		Repository: func(context.Context, string) (string, error) { return "/repository", nil },
		Socket:     "/run/worker.sock", Spool: "/state/hooks.spool", Binary: "/state/bin/ropeshift",
		StateDir: dir, RunDir: dir,
	}
	r.subject = worker.New(&fake.WorkerLink{Plane: r.plane}, r.journal, fake.NewClock(time.UnixMilli(1_000_000)), worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return r.plane.CreateJoinToken(ctx, member) },
		Lanes:     config,
		Spool:     filepath.Join(dir, "spool"),
	})
	if err := r.subject.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

// dispatch records the same work order ID in a project, claims it for the worker, and starts a lane on it.
func (r *installationRig) dispatch(project ledger.WorkspaceID, lane string) {
	r.t.Helper()
	ctx := r.t.Context()
	store := r.stores[project]
	_, err := control.NewWorkOrders(store).Record(ctx, project, control.LedgerWork{
		Order:    workorders.WorkOrder{ID: "wo", Kind: workorders.KindChange, Title: "T"},
		Criteria: []planning.Criterion{{ID: "c1", Text: "It works"}},
	}, member)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := control.NewReviews(store).Pass(ctx, project, "wo", member); err != nil {
		r.t.Fatal(err)
	}
	_, err = control.NewLeases(store).Claim(ctx, project, workorders.ClaimRequest{
		WorkOrder: "wo", ClaimID: "claim-" + string(project), Worker: r.subject.Auth().WorkerID, TTLMs: 60_000,
	}, member)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := control.NewLeases(store).Dispatch(ctx, project, "wo", lane, member); err != nil {
		r.t.Fatal(err)
	}
}

func (r *installationRig) row(project ledger.WorkspaceID) workorders.WorkOrder {
	r.t.Helper()
	var out workorders.WorkOrder
	err := r.stores[project].Read(r.t.Context(), project, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.WorkOrders().Get("wo")
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

// together holds a lane's turn until both lanes have a session, so the lanes can only finish if one worker
// runs them at the same time.
func (r *installationRig) together() ports.SessionSpec {
	deadline := time.Now().Add(10 * time.Second)
	for len(r.driver.Specs()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(r.driver.Specs()) < 2 {
		r.t.Error("the second project's lane did not start while the first ran")
	}
	return ports.SessionSpec{}
}

func (r *installationRig) laneScript() fake.DriverScript {
	return script([]fake.DriverStep{
		{Bound: true},
		{Do: func(ports.SessionSpec) { r.together() }},
		{Do: func(spec ports.SessionSpec) {
			raw, err := json.Marshal(workorders.Report{Outcome: workorders.OutcomeCompleted})
			if err == nil {
				_, err = r.journal.SaveReport(context.Background(), spec.Lane, raw)
			}
			if err != nil {
				r.t.Error(err)
			}
		}},
		completed(0.1),
	})
}

// TestOneWorkerRunsLanesOfTwoProjectsAtOnce runs a lane in each of two projects on one worker, each on a
// work order with the same ID: both run at the same time, each outcome lands in its own project, and the
// journal keeps each lane's project beside it (ADR-0021).
func TestOneWorkerRunsLanesOfTwoProjectsAtOnce(t *testing.T) {
	t.Parallel()
	r := newInstallationRig(t)
	r.dispatch("pa", "lane-a")
	r.dispatch("pb", "lane-b")
	r.driver.Play(r.laneScript())
	r.driver.PlayAfter(r.laneScript())
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, project := range []ledger.WorkspaceID{"pa", "pb"} {
		if row := r.row(project); row.State != workorders.Acceptance {
			t.Errorf("project %s: %+v", project, row)
		}
	}
	for lane, project := range map[string]string{"lane-a": "pa", "lane-b": "pb"} {
		record, err := r.journal.Lane(t.Context(), lane)
		if err != nil {
			t.Fatal(err)
		}
		if record.Project != project {
			t.Errorf("the journal keeps %s in project %q, want %s", lane, record.Project, project)
		}
	}
	state, err := r.journal.Desired(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Projects) != 2 || state.Revisions["pa"] == 0 || state.Revisions["pb"] == 0 {
		t.Errorf("desired state names %+v at %v", state.Projects, state.Revisions)
	}
}
