package worker_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

const generatedWorlds = 300

// worldRig is a worker with no dispatched work, so a test can lay the journal out as a
// crash would have left it.
type worldRig struct {
	*laneRig
	leases []ledger.DesiredLease
}

// newWorldRig stops what a test left running before its directories go.
func newWorldRig(t *testing.T) *worldRig {
	t.Helper()
	w := &worldRig{laneRig: newLaneRig(t)}
	t.Cleanup(func() {
		w.driver.StopEverySession()
		w.subject.Wait()
	})
	return w
}

func (w *worldRig) desire(lane string) {
	w.leases = append(w.leases, ledger.DesiredLease{
		WorkOrder: "wo-" + lane, ClaimID: "c-" + lane, Fence: 1, State: string(workorders.Claimed), Lane: lane,
	})
	if err := w.journal.SaveDesired(w.t.Context(), ledger.DesiredState{Rev: 1, Leases: w.leases}); err != nil {
		w.t.Fatal(err)
	}
}

func (w *worldRig) record(lane, state string) {
	err := w.journal.SaveLane(w.t.Context(), lanes.Record{
		ID: lane, WorkOrder: "wo-" + lane, ClaimID: "c-" + lane, Fence: 1, Generation: 1, State: state, Worktree: "/worktrees/" + lane,
	})
	if err != nil {
		w.t.Fatal(err)
	}
}

// effect lays down one effect as a crash leaves it.
func (w *worldRig) effect(kind lanes.EffectKind, lane string, state lanes.EffectState) lanes.Effect {
	w.t.Helper()
	ctx := w.t.Context()
	effect := lanes.NewEffect(kind, lane, 1, "/worktrees/"+lane, "")
	if kind == lanes.EffectInput {
		effect = lanes.NewEffect(kind, lane, 1, "", "nudge")
	}
	if _, err := w.journal.BeginEffect(ctx, effect); err != nil {
		w.t.Fatal(err)
	}
	switch state {
	case lanes.EffectUnknown:
		if _, err := w.journal.MarkUnknown(ctx); err != nil {
			w.t.Fatal(err)
		}
	case lanes.EffectStarted:
	default:
		if err := w.journal.FinishEffect(ctx, effect.Key, state, ""); err != nil {
			w.t.Fatal(err)
		}
	}
	return effect
}

func (w *worldRig) state(effect lanes.Effect) lanes.Effect {
	w.t.Helper()
	got, err := w.journal.Effect(w.t.Context(), effect.Key)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

func (w *worldRig) lane(id string) lanes.Record {
	w.t.Helper()
	got, err := w.journal.Lane(w.t.Context(), id)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

var (
	sessionAlive   = lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true}
	sessionDead    = lanes.Session{Found: true, Liveness: lanes.LivenessDead}
	sessionPending = lanes.Session{Found: true, Liveness: lanes.LivenessUnknown}
)

// generate lays out one world from a seed: lanes wanted, running, and ended, sessions in
// every state, and effects a crash could have left.
func (w *worldRig) generate(seed uint64) {
	rng := rand.New(rand.NewPCG(seed, 1))
	sessions := []lanes.Session{{}, sessionAlive, sessionDead, sessionPending, {Found: true, Liveness: lanes.LivenessDead, Readable: true}}
	states := []lanes.EffectState{lanes.EffectCompleted, lanes.EffectFailed, lanes.EffectUnknown}
	for i := range 1 + rng.IntN(4) {
		lane := fmt.Sprintf("generated-%d", i)
		if rng.IntN(2) == 0 {
			w.desire(lane)
		}
		switch rng.IntN(3) {
		case 0:
			continue
		case 1:
			w.record(lane, lanes.StateRunning)
		case 2:
			w.record(lane, lanes.StateEnded)
		}
		w.driver.Leave(lane, 1, sessions[rng.IntN(len(sessions))])
		for _, kind := range []lanes.EffectKind{lanes.EffectWorktree, lanes.EffectSession, lanes.EffectStop, lanes.EffectInput} {
			if rng.IntN(2) == 0 {
				w.effect(kind, lane, states[rng.IntN(len(states))])
			}
		}
	}
}

func (r *laneRig) holdEverySession() {
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Hold: true}}))
}

// TestReconcileTwiceNoActions is NFR-REL-3 over generated world states: a pass acts,
// and a second pass with no external change does nothing.
func TestReconcileTwiceNoActions(t *testing.T) {
	seen := map[lanes.ActionKind]int{}
	for seed := range uint64(generatedWorlds) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			w := newWorldRig(t)
			w.holdEverySession()
			w.generate(seed)
			first, err := w.subject.Reconcile(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range first {
				seen[action.Kind]++
			}
			second, err := w.subject.Reconcile(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(second) != 0 {
				t.Errorf("the first pass did %v, and the second did %v", first, second)
			}
		})
	}
	for _, kind := range []lanes.ActionKind{
		lanes.ActionStart, lanes.ActionConfirm, lanes.ActionRetry, lanes.ActionAbandon,
		lanes.ActionContinue, lanes.ActionAdopt, lanes.ActionEnd,
	} {
		if seen[kind] == 0 {
			t.Errorf("no generated world reached %s, so this test proved nothing about it", kind)
		}
	}
	t.Logf("actions over %d worlds: %v", generatedWorlds, seen)
}

// lanesRead runs a function once, after a pass reads the lanes and before it goes on, which
// is the moment a pass and a lane that is starting can cross.
type lanesRead struct {
	ports.Journal
	then atomic.Pointer[func()]
}

func (j *lanesRead) Lanes(ctx context.Context) ([]lanes.Record, error) {
	records, err := j.Journal.Lanes(ctx)
	if then := j.then.Swap(nil); then != nil {
		(*then)()
	}
	return records, err
}

// A lane that starts while a pass reads holds resources the pass finds with no record of
// the lane behind them. Those resources are a running lane's, and they are not free.
func TestAPassLeavesAStartingLanesResourcesAlone(t *testing.T) {
	base := newRig(t)
	read := &lanesRead{Journal: base.journal}
	base.workerJournal = read
	r := newLaneRigFor(t, base)
	r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", "[resources]\nports_per_lane = 10\n")
	r.dispatched()
	r.holdEverySession()
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	start := func() {
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Error(err)
		}
		r.awaitAllocation(t)
	}
	read.then.Store(&start)
	actions, err := r.subject.Reconcile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		if action.Kind == lanes.ActionRelease {
			t.Errorf("a pass released a running lane's resources: %+v", actions)
		}
	}
	allocations, err := r.journal.Allocations(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(allocations) != 1 {
		t.Errorf("allocations %+v", allocations)
	}
}

func (r *laneRig) awaitAllocation(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		allocations, err := r.journal.Allocations(t.Context())
		if err != nil {
			t.Error(err)
			return
		}
		if len(allocations) != 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("the lane never took its resources")
}

func TestAStartedEffectIsUnknownAfterACrash(t *testing.T) {
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	effect := w.effect(lanes.EffectSession, "lane-1", lanes.EffectStarted)
	w.driver.FailFind(errors.New("nothing to read yet"))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(effect).State; got != lanes.EffectUnknown {
		t.Errorf("state %s", got)
	}
}

// A spawn is not idempotent, so one that happened must not happen twice.
func TestAnEffectFoundIsNotRepeated(t *testing.T) {
	w := newWorldRig(t)
	w.desire("lane-1")
	w.record("lane-1", lanes.StateRunning)
	effect := w.effect(lanes.EffectSession, "lane-1", lanes.EffectStarted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", noRestarts)
	w.driver.Play(script([]fake.DriverStep{{Crash: true}}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if specs := w.driver.Specs(); len(specs) != 0 {
		t.Errorf("%d sessions started", len(specs))
	}
	if got := w.state(effect); got.State != lanes.EffectCompleted || got.Attempts != 1 {
		t.Errorf("effect %+v", got)
	}
	if len(w.driver.Adopted()) != 1 {
		t.Errorf("adoptions %v", w.driver.Adopted())
	}
}

func TestAnEffectNotFoundIsRetried(t *testing.T) {
	w := newWorldRig(t)
	w.desire("lane-1")
	w.record("lane-1", lanes.StateRunning)
	effect := w.effect(lanes.EffectSession, "lane-1", lanes.EffectStarted)
	w.driver.Play(script([]fake.DriverStep{w.reportsCompleted(), completed(0.25)}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if specs := w.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions started", len(specs))
	}
	if got := w.state(effect); got.State != lanes.EffectCompleted || got.Attempts != 2 {
		t.Errorf("effect %+v", got)
	}
}

// Nothing shows whether a session took an input, so a person decides, and a restart
// never sends it again.
func TestATargetWithNoObservationStaysUnknown(t *testing.T) {
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	effect := w.effect(lanes.EffectInput, "lane-1", lanes.EffectStarted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{completed(0.25)}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(effect); got.State != lanes.EffectUnknown || got.Attempts != 1 {
		t.Errorf("effect %+v", got)
	}
}

func TestAnIncompleteReadStartsAndEndsNothing(t *testing.T) {
	w := newWorldRig(t)
	w.desire("lane-1")
	w.record("lane-1", lanes.StateRunning)
	effect := w.effect(lanes.EffectSession, "lane-1", lanes.EffectStarted)
	w.driver.FailFind(errors.New("the read timed out"))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(w.driver.Specs()) != 0 {
		t.Error("an incomplete read started a session")
	}
	if got := w.state(effect).State; got != lanes.EffectUnknown {
		t.Errorf("state %s", got)
	}
	if got := w.lane("lane-1").State; got != lanes.StateRunning {
		t.Errorf("lane %s", got)
	}
	w.driver.FailFind(nil)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{w.reportsCompleted(), completed(0.25)}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.state(effect).State; got != lanes.EffectCompleted {
		t.Errorf("after a complete read, state %s", got)
	}
}

func TestAWorktreeMadeBeforeACrashIsNotMadeAgain(t *testing.T) {
	w := newWorldRig(t)
	w.desire("lane-1")
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectWorktree, "lane-1", lanes.EffectCompleted)
	profile, err := w.config.Provisioner.Profiles.Load(t.Context(), "/repository", "")
	if err != nil {
		t.Fatal(err)
	}
	place, err := lanes.Place(profile.Worktree, "/repository", "lane-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.config.Provisioner.Worktrees.Create(t.Context(), ports.WorktreeSpec{Repository: "/repository", Path: place.Path, Branch: place.Branch}); err != nil {
		t.Fatal(err)
	}
	w.driver.Play(script([]fake.DriverStep{w.reportsCompleted(), completed(0.25)}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := w.driver.Specs()
	if len(specs) != 1 || specs[0].Dir != place.Path {
		t.Errorf("sessions %+v", specs)
	}
}

// TestAdoptionContinuesFromTheJournal holds the counts across a restart: a generation's
// cost continues, and the stream is read from the last turn this worker finished with.
func TestAdoptionContinuesFromTheJournal(t *testing.T) {
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	progress := lanes.Progress{Cursor: 40, Cost: lanes.CostMeter{SessionTotalUSD: 0.5, CostUSD: 0.5}, Usage: lanes.Usage{OutputTokens: 7}}
	if _, err := w.journal.SaveProgress(t.Context(), "lane-1", 1, progress); err != nil {
		t.Fatal(err)
	}
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{w.reportsCompleted(), completed(0.75)}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	adopted := w.driver.Adopted()
	if len(adopted) != 1 || adopted[0].Cursor != 40 || adopted[0].Cost != progress.Cost {
		t.Errorf("adoptions %+v", adopted)
	}
	if got := w.lane("lane-1"); got.State != lanes.StateEnded {
		t.Errorf("lane %+v", got)
	}
}

// reportsCompleted ends a session's work, so a lane that adopted it has nothing to nudge.
func (w *worldRig) reportsCompleted() fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) {
		if _, err := w.journal.SaveReport(context.Background(), "lane-1", []byte(`{"outcome":"completed"}`)); err != nil {
			w.t.Error(err)
		}
	}}
}

func TestALaneThatReportedIsStoppedOnAdoption(t *testing.T) {
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	if _, err := w.journal.SaveReport(t.Context(), "lane-1", []byte(`{"outcome":"completed"}`)); err != nil {
		t.Fatal(err)
	}
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.lane("lane-1").State; got != lanes.StateEnded {
		t.Errorf("lane %s", got)
	}
}

func TestAGoneSessionThatRanEndsTheLane(t *testing.T) {
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	if err := w.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.lane("lane-1").State; got != lanes.StateEnded {
		t.Errorf("lane %s", got)
	}
	if len(w.driver.Specs()) != 0 {
		t.Error("a session started")
	}
}
