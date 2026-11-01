package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// laneRig is one worker with a fake driver and one dispatched work order.
type laneRig struct {
	*rig
	subject *worker.Worker
	driver  *fake.AgentDriver
	config  *worker.LaneRuntime
	id      string
	grant   workorders.Grant
	dir     string
	// stopWorker ends the context the lanes run under.
	stopWorker func()
}

func newLaneRig(t *testing.T) *laneRig {
	t.Helper()
	return newLaneRigFor(t, newRig(t))
}

// newLaneRigFor builds on a rig a test has already set up.
func newLaneRigFor(t *testing.T, base *rig) *laneRig {
	t.Helper()
	driver := fake.NewAgentDriver()
	dir := t.TempDir()
	config := &worker.LaneRuntime{
		Driver:      driver,
		Provisioner: worker.Provisioner{
			Profiles: fake.NewProfileSource(), Worktrees: fake.NewWorktrees(), Hooks: fake.NewHookRunner(),
			Lookup: func(string) (string, bool) { return "", false },
		},
		Secrets:     fake.SecretResolver{},
		Repository:  func(context.Context, string) (string, error) { return "/repository", nil },
		Socket:      "/run/worker.sock", Spool: "/state/hooks.spool", Binary: "/state/bin/ropeshift",
		StateDir: dir, RunDir: dir,
	}
	r := &laneRig{rig: base, driver: driver, config: config, dir: dir}
	r.subject = worker.New(base.link, base.journal, base.clock, worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return base.plane.CreateJoinToken(ctx, member) },
		Lanes:     config,
		Spool:     filepath.Join(dir, "spool"),
	})
	if err := r.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.id = "wo"
	r.grant = base.claimFor(r.id, r.subject.Auth().WorkerID)
	if _, err := control.NewLeases(base.store).Dispatch(t.Context(), scope, r.id, "lane-1", member); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *laneRig) row() workorders.WorkOrder {
	r.t.Helper()
	var out workorders.WorkOrder
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		out, err = tx.WorkOrders().Get(r.id)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return out
}

func (r *laneRig) outcome() workorders.Change {
	r.t.Helper()
	var last ledger.Event
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, e := range events {
			if e.Subject.ID == r.id {
				last = e
			}
		}
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	var change workorders.Change
	if err := json.Unmarshal(last.Data, &change); err != nil {
		r.t.Fatal(err)
	}
	return change
}

func (r *laneRig) reports(outcome workorders.Outcome) fake.DriverStep {
	return fake.DriverStep{Do: func(spec ports.SessionSpec) {
		raw, err := json.Marshal(workorders.Report{Outcome: outcome})
		if err != nil {
			r.t.Error(err)
			return
		}
		if _, err := r.journal.SaveReport(context.Background(), spec.Lane, raw); err != nil {
			r.t.Error(err)
		}
	}}
}

func completed(total float64) fake.DriverStep {
	return fake.DriverStep{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: total}}
}

// noRestarts turns a crash into a lane's end.
const noRestarts = "[lifecycle]\nmax_restarts = 0\n"

func script(turns ...[]fake.DriverStep) fake.DriverScript {
	return fake.DriverScript{SessionID: "session-1", Model: "resolved-model", Turns: turns}
}

func TestReportedLaneStopsAtTheEndOfItsTurn(t *testing.T) {
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Fatalf("row %+v", row)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	spec := specs[0]
	if spec.Model != "opus" || spec.PermissionMode != "default" || spec.Binary != "/state/bin/ropeshift" {
		t.Errorf("spec %+v", spec)
	}
	if !slices.Equal(spec.Tools, lanes.Tools) {
		t.Errorf("tools %v", spec.Tools)
	}
	if !strings.Contains(spec.Brief, lanes.ToolLaneReport) || !strings.Contains(spec.Brief, r.id) {
		t.Errorf("brief %q", spec.Brief)
	}
	var token string
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			token = value
		}
	}
	if token == "" {
		t.Fatalf("no token in %v", spec.Env)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != lanes.StateEnded || record.TokenDigest != "" {
		t.Errorf("the token outlived its process: %+v", record)
	}
	if _, err := r.journal.LaneByToken(t.Context(), lanes.TokenDigest(token)); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("an expired token still finds its lane: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "t", "lane-1")); !os.IsNotExist(err) {
		t.Errorf("the private directory remains: %v", err)
	}
	var result workorders.LaneResult
	if err := json.Unmarshal(r.outcome().Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.CostUSD != 0.25 {
		t.Errorf("result %+v", result)
	}
}

func TestAnIdleLaneIsNudgedOnce(t *testing.T) {
	r := newLaneRig(t)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{r.reports(workorders.OutcomeCompleted), completed(0.02)},
	))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Errorf("a nudged lane that reports: %+v", row)
	}
}

func TestALaneThatNeverReportsIsBlocked(t *testing.T) {
	r := newLaneRig(t)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
	))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	change := r.outcome()
	if r.row().State != workorders.Blocked || change.Reason != workorders.ReasonNoOutcome {
		t.Errorf("outcome %+v", change)
	}
}

func TestAFailedTurnIsAFailedLane(t *testing.T) {
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Turn: &fake.DriverTurn{Status: ports.TurnFailed, Reason: "error_max_budget_usd"}}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	change := r.outcome()
	if change.Outcome != workorders.OutcomeFailed || change.Reason != "error_max_budget_usd" {
		t.Errorf("outcome %+v", change)
	}
}

func TestACrashBeforeAReportIsAFailedLane(t *testing.T) {
	r := laneRigWith(t, noRestarts)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Crash: true}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if change := r.outcome(); change.Reason != workorders.ReasonCrashed {
		t.Errorf("outcome %+v", change)
	}
}

func TestACrashAfterAReportKeepsTheReport(t *testing.T) {
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), {Crash: true}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Errorf("row %+v", row)
	}
}

func TestLaneFailuresBeforeASession(t *testing.T) {
	tests := []struct {
		name   string
		change func(*laneRig)
		reason string
	}{
		{"no checkout of the repository", func(r *laneRig) {
			r.config.Repository = func(context.Context, string) (string, error) { return "", errors.New("no source") }
		}, workorders.ReasonNoRepository},
		{"a bad secret reference", func(r *laneRig) {
			r.config.Provisioner.Profiles = badProfile{}
		}, workorders.ReasonSpawnFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newLaneRig(t)
			tt.change(r)
			if err := r.subject.Converge(t.Context()); err != nil {
				t.Fatal(err)
			}
			if change := r.outcome(); change.Outcome != workorders.OutcomeFailed || change.Reason != tt.reason {
				t.Errorf("outcome %+v", change)
			}
			if len(r.driver.Specs()) != 0 {
				t.Error("a session started")
			}
		})
	}
}

type badProfile struct{}

func (badProfile) Load(context.Context, string) (lanes.Profile, error) {
	return lanes.Profile{}, lanes.ErrBadProfile
}

func TestARestartAccountsForALaneAndDoesNotStartItAgain(t *testing.T) {
	r := newLaneRig(t)
	claim, fence := r.grant.ClaimID, r.grant.Fence
	record := lanes.Record{ID: "lane-1", WorkOrder: r.id, ClaimID: claim, Fence: fence, Generation: 1, State: lanes.StateRunning}
	begun := ledger.UploadEvent{
		Epoch: r.subject.Session().Epoch, Type: ledger.UploadLaneStarted, WorkOrder: r.id, ClaimID: claim, Fence: fence,
		Lane: "lane-1", Generation: 1, OccurredAt: 1,
	}
	if _, err := r.journal.BeginLane(t.Context(), record, begun); err != nil {
		t.Fatal(err)
	}
	spawn := lanes.NewEffect(lanes.EffectSession, "lane-1", 1, "", "")
	if _, err := r.journal.BeginEffect(t.Context(), spawn); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.FinishEffect(t.Context(), spawn.Key, lanes.EffectCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.driver.Specs()) != 0 {
		t.Error("a restart spawned a lane it had already started")
	}
	if change := r.outcome(); change.Reason != workorders.ReasonWorkerRestarted {
		t.Errorf("outcome %+v", change)
	}
}
