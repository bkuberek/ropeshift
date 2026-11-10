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
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// gate holds a scripted session at one point, so a test reads a lane in a known state.
type gate struct {
	reached chan struct{}
	release chan struct{}
}

func newGate() *gate { return &gate{reached: make(chan struct{}), release: make(chan struct{})} }

func (g *gate) step() fake.DriverStep {
	return fake.DriverStep{Do: func(ports.SessionSpec) {
		close(g.reached)
		<-g.release
	}}
}

// atGate starts the lane, waits for the session to reach the gate and for the worker to
// catch up with it, and reads the lane the way a client does.
func (r *laneRig) atGate(g *gate) ledger.LaneView {
	r.t.Helper()
	if _, err := r.subject.Reconcile(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		r.t.Fatal("the session never reached its gate")
	}
	return r.eventually("a turn in progress", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Working)
	})
}

func (r *laneRig) read() ledger.LaneView {
	r.t.Helper()
	if err := r.subject.Publish(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	if err := r.subject.Upload(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	view, err := r.plane.Lane(r.t.Context(), "lane-1")
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		r.t.Fatal(err)
	}
	return view
}

// eventually reads until a condition holds. A scripted session runs ahead of the worker
// that follows it, so one read could land before the worker took an event.
func (r *laneRig) eventually(what string, done func(ledger.LaneView) bool) ledger.LaneView {
	r.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var view ledger.LaneView
	for time.Now().Before(deadline) {
		if view = r.read(); done(view) {
			return view
		}
		time.Sleep(time.Millisecond)
	}
	r.t.Fatalf("%s: the lane reads %+v", what, view)
	return view
}

func (r *laneRig) dispatched() *laneRig {
	r.t.Helper()
	if err := r.subject.Sync(r.t.Context()); err != nil {
		r.t.Fatal(err)
	}
	return r
}

func (r *laneRig) finishLater(g *gate) {
	r.t.Cleanup(func() {
		close(g.release)
		r.driver.StopEverySession()
		r.subject.Wait()
	})
}

// laneRigWith uses a repository layer, so a test can set a threshold.
func laneRigWith(t *testing.T, profile string) *laneRig {
	t.Helper()
	r := newLaneRig(t)
	if profile != "" {
		r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", profile)
	}
	return r.dispatched()
}

func TestStateDerivesFromDriverEvents(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Context: &lanes.Usage{InputTokens: 3}}, g.step()}))
	r.atGate(g)
	r.eventually("a turn in progress", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Working) && view.Liveness == string(lanes.LivenessAlive) &&
			view.ModelReach == string(lanes.Reachable)
	})
}

func TestAnEndedLaneReadsEnded(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	view, err := r.plane.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.ActualState != ledger.StateEnded || view.Freshness != ledger.FreshnessObserved {
		t.Errorf("view %+v", view)
	}
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.Actual != lanes.Ended {
		t.Errorf("the journal holds %s", record.Actual)
	}
}

// An idle session has no fresh evidence, even right after a turn that went well.
func TestAnIdleSessionReadsReachabilityUnknown(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	reached, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		close(release)
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	nudged := fake.DriverStep{Do: func(ports.SessionSpec) {
		close(reached)
		<-release
	}}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, completed(0.1)}, []fake.DriverStep{nudged}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	<-reached
	r.eventually("a turn in progress", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Working) && view.ModelReach == string(lanes.Reachable)
	})
	if got := lanes.ReadReach(lanes.Idle, lanes.ReachEvidence{Reach: lanes.Reachable, Seen: true}, time.Minute); got != lanes.ReachUnknown {
		t.Errorf("an idle lane reads %s", got)
	}
}

func TestStalledNeedsTheProfilesLimit(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "[lifecycle]\nstall_after = \"20m\"\n")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	if view := r.atGate(g); slices.Contains(view.Conditions, string(lanes.Stalled)) {
		t.Errorf("a new lane reads %v", view.Conditions)
	}
	r.clock.Advance(21 * time.Minute)
	if view := r.read(); !slices.Contains(view.Conditions, string(lanes.Stalled)) {
		t.Errorf("a quiet working lane reads %v", view.Conditions)
	}
}

func TestAToolInFlightIsAWaitAndNotAStall(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "[lifecycle]\nstall_after = \"20m\"\n")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "tool-1", Tool: "Bash"})
	r.clock.Advance(21 * time.Minute)
	view := r.read()
	if slices.Contains(view.Conditions, string(lanes.Stalled)) || !slices.Contains(view.Conditions, string(lanes.WaitingExternal)) {
		t.Errorf("conditions %v", view.Conditions)
	}
	r.hook(t, lanes.HookEvent{Event: "PostToolUse", Subject: "tool-1", Tool: "Bash"})
	r.clock.Advance(21 * time.Minute)
	if view := r.read(); !slices.Contains(view.Conditions, string(lanes.Stalled)) {
		t.Errorf("after the tool: %v", view.Conditions)
	}
}

// hook reaches the worker the way a session's hook does, with the token the session got.
func (r *laneRig) hook(t *testing.T, event lanes.HookEvent) {
	t.Helper()
	var token string
	for _, entry := range r.driver.Specs()[0].Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			token = value
		}
	}
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: token, Hook: &event})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(context.Background(), line); !reply.OK {
		t.Fatalf("the hook was refused: %+v", reply)
	}
}

func TestCompactionComesFromHooksAndReturns(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "PreCompact", Subject: "s", Detail: "auto"})
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Errorf("during a compaction: %s", view.ActualState)
	}
	r.hook(t, lanes.HookEvent{Event: "PostCompact", Subject: "s", Detail: "auto"})
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("after a compaction: %s", view.ActualState)
	}
}

func TestStopFailureSetsReachability(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "StopFailure", Subject: "s", Detail: "authentication_failed"})
	if view := r.read(); view.ModelReach != string(lanes.Unreachable) {
		t.Errorf("reach %s", view.ModelReach)
	}
}

func TestContextCrossingTheWarningIsNearBudget(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "[context.roles.writer]\nwarn_tokens = 100\n")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Context: &lanes.Usage{InputTokens: 101}}, g.step()}))
	r.atGate(g)
	r.eventually("context past the warning", func(view ledger.LaneView) bool {
		return slices.Contains(view.Conditions, string(lanes.NearBudget))
	})
}

func TestABriefWithNoEvidenceGoesIdleAndIsFlagged(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "[lifecycle]\nbrief_grace = \"2m\"\n")
	r.driver.Play(fake.DriverScript{SessionID: "session-1", Model: "resolved-model", Silent: true})
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.eventually("the brief handed over", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Briefing)
	})
	r.clock.Advance(3 * time.Minute)
	if err := r.subject.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	view := r.read()
	if !slices.Contains(view.Conditions, string(lanes.BriefUnconfirmed)) {
		t.Errorf("view %+v", view)
	}
}

func TestAnUnqualifiedVersionIsDegradedMode(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	r.driver.SetVersion("9.9.9", true)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	if view := r.atGate(g); !slices.Contains(view.Conditions, string(lanes.DegradedMode)) {
		t.Errorf("conditions %v", view.Conditions)
	}
}

// A status travels on change, so a heartbeat alone adds nothing to the ledger.
func TestAStatusIsPublishedOnlyOnChange(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.eventually("a turn in progress", func(view ledger.LaneView) bool {
		return view.ActualState == string(lanes.Working) && view.Liveness == string(lanes.LivenessAlive)
	})
	before, err := r.journal.Through(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	r.read()
	r.read()
	after, err := r.journal.Through(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("%d events with nothing changed", after-before)
	}
}

// TestAdoptionKeepsTheLanesState holds what a restart must not lose.
func TestAdoptionKeepsTheLanesState(t *testing.T) {
	t.Parallel()
	w := newWorldRig(t)
	w.record("lane-1", lanes.StateRunning)
	if err := w.journal.SaveActual(t.Context(), "lane-1", lanes.Idle, ""); err != nil {
		t.Fatal(err)
	}
	w.effect(lanes.EffectSession, "lane-1", lanes.EffectCompleted)
	w.driver.Leave("lane-1", 1, sessionAlive)
	w.driver.Play(script([]fake.DriverStep{{Hold: true}}))
	if _, err := w.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := w.lane("lane-1").Actual; got != lanes.Idle {
		t.Errorf("adoption moved the lane to %s", got)
	}
}

// TestPublishReadsWhileASessionStarts holds the read a heartbeat makes against the write
// a start makes, with no order between them. It has meaning under the race detector.
func TestPublishReadsWhileASessionStarts(t *testing.T) {
	t.Parallel()
	for range 40 {
		r := laneRigWith(t, "")
		r.driver.Play(script([]fake.DriverStep{{Bound: true}}))
		reading := make(chan struct{})
		stop := make(chan struct{})
		go func() {
			defer close(reading)
			for {
				select {
				case <-stop:
					return
				default:
					if err := r.subject.Publish(t.Context()); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
		r.driver.StopEverySession()
		r.subject.Wait()
		close(stop)
		<-reading
	}
}

// A hook that missed its ack spools with the worker alive, and a pane has no stream to say
// the same thing, so a live run must still hear it.
func TestASpooledHookReachesALiveRun(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	path := filepath.Join(r.dir, "spool")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	line := sealedLine(t, r.token(t), lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: "PreCompact", Subject: "s", AtMs: 1})
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.DrainSpool(t.Context()); err != nil {
		t.Fatal(err)
	}
	if view := r.read(); view.ActualState != string(lanes.Compacting) {
		t.Errorf("after a spooled hook: %s", view.ActualState)
	}
	if hooks := r.driver.Hooks(); len(hooks) != 1 || hooks[0].Event != "PreCompact" {
		t.Errorf("the driver took %+v", hooks)
	}
}

// A profile names `tmux`, and a driver names `interactive`.
func TestRunModeReachesTheDriver(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "run_mode = \"tmux\"\n[lifecycle]\nready_timeout = \"7s\"\n")
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	specs := r.driver.Specs()
	if len(specs) != 1 || specs[0].RunMode != ports.RunModeInteractive || specs[0].ReadyTimeout != 7*time.Second {
		t.Errorf("specs %+v", specs)
	}
}

func TestAnUnknownRunModeIsRefused(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "run_mode = \"container\"\n")
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 0 {
		t.Errorf("a session started: %+v", specs)
	}
}

// startRefusal is a driver whose start stops at the check.
type startRefusal struct {
	*fake.AgentDriver
	err error
}

func (d startRefusal) Start(context.Context, ports.SessionSpec) (ports.SessionHandle, error) {
	return ports.SessionHandle{}, d.err
}
