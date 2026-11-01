package worker_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
)

const stopWorktree = "/lanes/lane-1"

func (r *laneRig) worktrees() *fake.Worktrees {
	return r.config.Provisioner.Worktrees.(*fake.Worktrees)
}

// dirtying is a session that leaves uncommitted work behind before it reports.
func (r *laneRig) dirtying(path string) fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) { r.worktrees().Dirty(path) }}
}

func (r *laneRig) committing(path string) fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) { r.worktrees().Commit(path) }}
}

// profileAt places a lane's worktree where a test names it.
const profileAt = `[worktree]
root = "/lanes"
`

func (r *laneRig) view() ledger.LaneView {
	r.t.Helper()
	view, err := r.plane.Lane(r.t.Context(), "lane-1")
	if err != nil {
		r.t.Fatal(err)
	}
	return view
}

func (r *laneRig) runWith(steps ...fake.DriverStep) {
	r.t.Helper()
	r.driver.Play(script(append(append([]fake.DriverStep{{Bound: true}}, steps...), r.reports(workorders.OutcomeCompleted), completed(0.1))))
	if err := r.subject.Converge(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
}

func TestAStopGuardsTheWorktree(t *testing.T) {
	checkpoint := lanes.CheckpointRef("lane-1", 1)
	branch := lanes.HeadsRef("lane/lane-1")
	tests := []struct {
		name     string
		remote   string
		refuse   bool
		steps    func(*laneRig) []fake.DriverStep
		pushed   []string
		unpushed bool
	}{
		{name: "clean, everything pushed", remote: "origin", steps: func(*laneRig) []fake.DriverStep { return nil }},
		{
			name: "commits not pushed", remote: "origin", pushed: []string{branch},
			steps: func(r *laneRig) []fake.DriverStep { return []fake.DriverStep{r.committing(stopWorktree)} },
		},
		{
			name: "uncommitted changes", remote: "origin", pushed: []string{checkpoint},
			steps: func(r *laneRig) []fake.DriverStep { return []fake.DriverStep{r.dirtying(stopWorktree)} },
		},
		{
			name: "a push that fails keeps the commits", remote: "origin", refuse: true, unpushed: true,
			steps: func(r *laneRig) []fake.DriverStep { return []fake.DriverStep{r.committing(stopWorktree)} },
		},
		{
			name: "a push that fails keeps the changes", remote: "origin", refuse: true, unpushed: true,
			steps: func(r *laneRig) []fake.DriverStep { return []fake.DriverStep{r.dirtying(stopWorktree)} },
		},
		{
			name: "no remote and a commit", unpushed: true,
			steps: func(r *laneRig) []fake.DriverStep { return []fake.DriverStep{r.committing(stopWorktree)} },
		},
		{
			name: "no remote and changes", unpushed: true,
			steps: func(r *laneRig) []fake.DriverStep { return []fake.DriverStep{r.dirtying(stopWorktree)} },
		},
		{name: "no remote and nothing of its own", steps: func(*laneRig) []fake.DriverStep { return nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := laneRigWith(t, profileAt)
			r.worktrees().UseRemote(tt.remote)
			r.worktrees().RefusePushes(tt.refuse)
			r.runWith(tt.steps(r)...)
			if row := r.row(); row.State != workorders.Acceptance {
				t.Fatalf("row %+v", row)
			}
			if _, err := r.worktrees().Inspect(t.Context(), stopWorktree); err != nil {
				t.Errorf("a stop took the worktree: %v", err)
			}
			if got := r.worktrees().Pushed(); !slices.Equal(uniq(got), tt.pushed) {
				t.Errorf("pushed %v, want %v", got, tt.pushed)
			}
			got := slices.Contains(r.view().Conditions, string(lanes.UnpushedWork))
			if got != tt.unpushed {
				t.Errorf("conditions %v, want unpushed=%v", r.view().Conditions, tt.unpushed)
			}
		})
	}
}

func uniq(values []string) []string {
	var out []string
	for _, value := range values {
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

// A session keeps working while a stop waits for it, so the last word is the reading after
// it is gone.
func TestWorkMadeDuringTheStopIsGuarded(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.worktrees().UseRemote("origin")
	r.driver.OnStop(func() { r.worktrees().Dirty(stopWorktree) })
	r.runWith()
	if refs := r.worktrees().Snapshots(); refs[lanes.CheckpointRef("lane-1", 1)] != stopWorktree {
		t.Errorf("work from the stop was not saved: %v", refs)
	}
}

func TestASnapshotThatSavesWorkIsOnRecord(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.worktrees().UseRemote("origin")
	r.runWith(r.dirtying(stopWorktree))
	artifacts := r.view().Artifacts
	if len(artifacts) != 1 || artifacts[0].Kind != ledger.ArtifactSalvage || artifacts[0].URI != lanes.CheckpointRef("lane-1", 1) {
		t.Errorf("artifacts %+v", artifacts)
	}
}

func TestACrashedLaneIsGuardedToo(t *testing.T) {
	r := laneRigWith(t, profileAt+noRestarts)
	r.worktrees().UseRemote("origin")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.dirtying(stopWorktree), {Crash: true}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if refs := r.worktrees().Snapshots(); refs[lanes.CheckpointRef("lane-1", 1)] != stopWorktree {
		t.Errorf("a crash lost uncommitted work: %v", refs)
	}
}

func TestAFailedReadNeverReadsAsClean(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.worktrees().FailSurvey(errors.New("git is gone"))
	r.runWith()
	if !slices.Contains(r.view().Conditions, string(lanes.UnpushedWork)) {
		t.Errorf("conditions %v", r.view().Conditions)
	}
}

func (r *laneRig) archive(forced bool) error {
	r.t.Helper()
	return control.NewLeases(r.store).ArchiveLane(r.t.Context(), scope, r.id, "lane-1", forced, member)
}

// settle runs what a heartbeat would: desired state, then one pass, then the upload.
func (r *laneRig) settle() {
	r.t.Helper()
	ctx := r.t.Context()
	if err := r.subject.Sync(ctx); err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(ctx); err != nil {
		r.t.Fatal(err)
	}
	r.subject.Wait()
	if err := r.subject.Upload(ctx); err != nil {
		r.t.Fatal(err)
	}
}

func TestRemovingUnpushedWorkNeedsAForcedCommand(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.runWith(r.dirtying(stopWorktree))
	if err := r.archive(false); !errors.Is(err, workorders.ErrUnpushedWork) {
		t.Fatalf("a plain removal: %v", err)
	}
	r.settle()
	if _, err := r.worktrees().Inspect(t.Context(), stopWorktree); err != nil {
		t.Fatalf("a refused removal took the worktree: %v", err)
	}
	if err := control.NewLeases(r.store).ArchiveLane(t.Context(), scope, r.id, "lane-1", true,
		ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "o"}); !errors.Is(err, workorders.ErrForbidden) {
		t.Fatalf("an orchestrator forced a removal: %v", err)
	}
	if err := r.archive(true); err != nil {
		t.Fatal(err)
	}
	r.settle()
	if _, err := r.worktrees().Inspect(t.Context(), stopWorktree); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a forced removal left the worktree: %v", err)
	}
	if slices.Contains(r.view().Conditions, string(lanes.UnpushedWork)) {
		t.Errorf("the condition outlived the worktree: %v", r.view().Conditions)
	}
	if refs := r.worktrees().Snapshots(); refs[lanes.CheckpointRef("lane-1", 1)] != stopWorktree {
		t.Errorf("a forced removal left no snapshot: %v", refs)
	}
}

func TestARemovalOfCleanWorkNeedsNoForce(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.worktrees().UseRemote("origin")
	r.runWith()
	if err := r.archive(false); err != nil {
		t.Fatal(err)
	}
	r.settle()
	if _, err := r.worktrees().Inspect(t.Context(), stopWorktree); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("the worktree stands: %v", err)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil || record.State != lanes.StateArchived {
		t.Errorf("record %+v: %v", record, err)
	}
}

// Work can appear after a stop's reading, so the removal reads again, and what it finds
// cancels a request nobody forced.
func TestARemovalReadsTheWorktreeAgain(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.worktrees().UseRemote("origin")
	r.runWith()
	if err := r.archive(false); err != nil {
		t.Fatal(err)
	}
	r.worktrees().Dirty(stopWorktree)
	r.worktrees().RefusePushes(true)
	r.settle()
	if _, err := r.worktrees().Inspect(t.Context(), stopWorktree); err != nil {
		t.Fatalf("a removal lost new work: %v", err)
	}
	if !slices.Contains(r.view().Conditions, string(lanes.UnpushedWork)) {
		t.Errorf("conditions %v", r.view().Conditions)
	}
	r.settle()
	if _, err := r.worktrees().Inspect(t.Context(), stopWorktree); err != nil {
		t.Errorf("the request came back: %v", err)
	}
}

func TestARunningLaneKeepsItsWorktree(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
	r.start()
	r.awaitGeneration(t, 1)
	r.eventuallyUp()
	if err := r.archive(true); !errors.Is(err, workorders.ErrIllegalMove) {
		t.Errorf("a running lane: %v", err)
	}
	r.driver.StopEverySession()
	r.subject.Wait()
}

// eventuallyUp waits for the ledger to hold the lane.
func (r *laneRig) eventuallyUp() {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := r.subject.Upload(r.t.Context()); err != nil {
			r.t.Fatal(err)
		}
		if _, err := r.plane.Lane(r.t.Context(), "lane-1"); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	r.t.Fatal("the ledger never held the lane")
}


func TestAFencedLaneStopsThroughTheGuard(t *testing.T) {
	r := laneRigWith(t, profileAt)
	r.worktrees().UseRemote("origin")
	wrote := make(chan struct{})
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, r.dirtying(stopWorktree), r.committing(stopWorktree),
		{Do: func(ports.SessionSpec) { close(wrote) }}, {Hold: true},
	}))
	r.start()
	<-wrote
	fence := r.grant.Fence
	r.loseLease(t)
	if _, err := r.subject.Beat(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	ref := lanes.SalvageRef("lane-1", fence)
	if refs := r.worktrees().Snapshots(); refs[ref] != stopWorktree {
		t.Errorf("snapshots %v", refs)
	}
	if got := r.worktrees().Pushed(); !slices.Contains(got, ref) || slices.Contains(got, lanes.HeadsRef("lane/lane-1")) {
		t.Errorf("pushed %v", got)
	}
	artifacts := r.view().Artifacts
	if len(artifacts) != 1 || artifacts[0].URI != ref {
		t.Errorf("the ledger holds no salvage: %+v", artifacts)
	}
	for _, event := range r.events() {
		if event.Type == ledger.EventLaneGuarded && !strings.Contains(string(event.Data), ref) {
			t.Errorf("event %s", event.Data)
		}
	}
}

// loseLease ends this worker's ownership, the way the ledger does when it answers a
// heartbeat with a fence that moved.
func (r *laneRig) loseLease(t *testing.T) {
	t.Helper()
	r.eventuallyUp()
	holder := workorders.Holder{Lease: r.grant.Lease, Lane: "lane-1", Generation: 1}
	result := workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeFailed, Reason: "taken"}}
	if err := control.NewLeases(r.store).EndLane(t.Context(), scope, holder, result, member); err != nil {
		t.Fatal(err)
	}
}

func TestAResourceBlockReachesTheLane(t *testing.T) {
	r := laneRigWith(t, "[resources]\nports_per_lane = 10\ncompose_project_prefix = \"rs\"\n")
	r.runWith()
	spec := r.driver.Specs()[0]
	if !slices.Contains(spec.Env, lanes.EnvPortBase+"=20000") || !slices.Contains(spec.Env, lanes.EnvComposeProject+"=rs-lane-1") {
		t.Errorf("environment %v", spec.Env)
	}
}

func TestAnEndedLaneGivesItsResourcesBack(t *testing.T) {
	r := newLaneRig(t)
	r.runWith()
	held, err := r.journal.Allocations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 {
		t.Fatalf("held before a pass: %+v", held)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if held, err = r.journal.Allocations(t.Context()); err != nil || len(held) != 0 {
		t.Errorf("held after a pass: %+v: %v", held, err)
	}
}

func TestARestartGetsTheSameBlock(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	r.start()
	r.awaitCharge(t)
	r.clock.Advance(30 * time.Second)
	r.subject.Wait()
	specs := r.driver.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d sessions", len(specs))
	}
	if portOf(specs[0]) == "" || portOf(specs[0]) != portOf(specs[1]) {
		t.Errorf("ports %q and %q", portOf(specs[0]), portOf(specs[1]))
	}
}

func (r *laneRig) awaitCharge(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		charges, err := r.journal.Restarts(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		if len(charges) != 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the crash was never charged")
}

func portOf(spec ports.SessionSpec) string {
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvPortBase+"="); ok {
			return value
		}
	}
	return ""
}

func TestAHookRunsOnceWithTheLanesEnvironment(t *testing.T) {
	r := laneRigWith(t, profileAt+"[hooks]\non_lane_created = [\"./scripts/setup.sh\", \"{lane.name}\"]\n")
	hooks := r.config.Provisioner.Hooks.(*fake.HookRunner)
	r.runWith()
	runs := hooks.Runs()
	if len(runs) != 1 {
		t.Fatalf("%d hook runs", len(runs))
	}
	got := runs[0]
	if !slices.Equal(got.Argv, []string{"./scripts/setup.sh", "lane-1"}) || got.Dir != stopWorktree {
		t.Errorf("hook %+v", got)
	}
	if !slices.Contains(got.Env, lanes.EnvComposeProject+"=rs-lane-1") {
		t.Errorf("hook environment %v", got.Env)
	}
	for _, entry := range got.Env {
		if strings.HasPrefix(entry, lanes.EnvLaneToken+"=") {
			t.Error("a hook holds the lane token")
		}
	}
}

func TestAHookThatFailsFailsTheLaneBeforeASession(t *testing.T) {
	r := laneRigWith(t, profileAt+"[hooks]\non_lane_created = [\"./scripts/setup.sh\"]\n")
	r.config.Provisioner.Hooks.(*fake.HookRunner).Fail(errors.New("no such setup"))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if change := r.outcome(); change.Reason != workorders.ReasonHookFailed {
		t.Errorf("outcome %+v", change)
	}
	if len(r.driver.Specs()) != 0 {
		t.Error("a session started")
	}
}

func TestAHookACrashLeftUnknownStopsTheLaneForAPerson(t *testing.T) {
	r := laneRigWith(t, profileAt+"[hooks]\non_lane_created = [\"./scripts/setup.sh\"]\n")
	effect := lanes.NewEffect(lanes.EffectHook, "lane-1", 1, stopWorktree, lanes.HookLaneCreated)
	if _, err := r.journal.BeginEffect(t.Context(), effect); err != nil {
		t.Fatal(err)
	}
	if _, err := r.journal.MarkUnknown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if change := r.outcome(); change.Outcome != workorders.OutcomeBlocked || change.Reason != workorders.ReasonHookUnknown {
		t.Errorf("outcome %+v", change)
	}
	if runs := r.config.Provisioner.Hooks.(*fake.HookRunner).Runs(); len(runs) != 0 {
		t.Errorf("a hook of unknown state ran again: %+v", runs)
	}
}
