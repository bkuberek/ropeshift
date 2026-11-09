package worker_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

// notCarried is a narrative that says the next step needs the session.
const notCarried = `{"next_steps":["Fix the failing test"],"carries_next_step":false}`

// parkedAndWoken parks a lane on a person's word with a narrative, and wakes it.
func parkedAndWoken(t *testing.T, narrative string, before func(*handoffRig)) *handoffRig {
	t.Helper()
	var r *handoffRig
	r = atWork(t, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrative) }}, completed(0.02))
	if before != nil {
		before(r)
	}
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	return r
}

func (r *laneRig) wake(t *testing.T) {
	t.Helper()
	if _, err := control.NewWakes(r.store).ResumeLane(t.Context(), scope, "lane-1", member); err != nil {
		t.Fatal(err)
	}
}

// pass is one tick of a daemon: it reads desired state and acts on it, twice over, because a
// claim is what puts a resume in desired state.
func (r *laneRig) pass(t *testing.T) {
	t.Helper()
	for range 2 {
		if err := r.subject.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := r.subject.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *laneRig) resumed(t *testing.T) {
	t.Helper()
	r.pass(t)
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
}

func TestAWokenLaneContinuesItsSession(t *testing.T) {
	r := parkedAndWoken(t, notCarried, nil)
	r.resumed(t)
	got := r.driver.Resumed()
	if len(got) != 1 || got[0].SessionID != "session-1" {
		t.Fatalf("resumed %+v", got)
	}
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartResume || generations[1].HandoffIn != "" {
		t.Errorf("generations %+v", generations)
	}
	if row := r.row(); row.ResumeLaneID != "" {
		t.Errorf("row %+v", row)
	}
}

func TestAResumePassesEveryStartFlagAgain(t *testing.T) {
	r := parkedAndWoken(t, notCarried, nil)
	r.resumed(t)
	specs := r.driver.Specs()
	if len(specs) != 2 {
		t.Fatalf("%d sessions", len(specs))
	}
	first, second := specs[0], specs[1]
	if second.Generation != 2 {
		t.Errorf("generation %d", second.Generation)
	}
	if second.Model != first.Model || second.PermissionMode != first.PermissionMode || second.ConfigDir != first.ConfigDir ||
		second.Binary != first.Binary || second.Dir != first.Dir || second.RunMode != first.RunMode || second.Repository != first.Repository {
		t.Errorf("a resume changed what it starts with:\nfirst  %+v\nsecond %+v", first, second)
	}
	if len(second.Tools) != len(first.Tools) || len(second.Env) != len(first.Env) {
		t.Errorf("tools %v and %v, env %d and %d", first.Tools, second.Tools, len(first.Env), len(second.Env))
	}
}

func TestAWokenLaneWithAGoodHandoffPastTheCacheStartsFresh(t *testing.T) {
	r := parkedAndWoken(t, narrativeText, func(r *handoffRig) {
		r.driver.SetResumeCosts(lanes.ResumeCosts{})
	})
	r.resumed(t)
	if got := r.driver.Resumed(); len(got) != 0 {
		t.Fatalf("a fresh start resumed %+v", got)
	}
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff || generations[1].HandoffIn == "" {
		t.Errorf("generations %+v", generations)
	}
	brief := r.driver.Specs()[1].Brief
	if !strings.Contains(brief, "Fix the failing test") || !strings.Contains(brief, "which handed off") {
		t.Errorf("the brief holds no handoff: %q", brief)
	}
}

func TestAResumeChoosesByTheTable(t *testing.T) {
	tests := []struct {
		name      string
		narrative string
		costs     lanes.ResumeCosts
		context   int64
		native    bool
	}{
		{"a handoff that does not carry the next step", notCarried, lanes.ResumeCosts{}, 0, true},
		{"nothing past the cache and a small conversation", narrativeText, fake.DefaultResumeCosts(), 0, true},
		{
			"a conversation past the model's ratio", narrativeText,
			lanes.ResumeCosts{Lifetimes: fake.DefaultResumeCosts().Lifetimes, Ratios: map[string]int64{"resolved-model": 20}}, 1000000, false,
		},
		{
			"a model with no ratio skips that row", narrativeText,
			lanes.ResumeCosts{Lifetimes: fake.DefaultResumeCosts().Lifetimes, Ratios: map[string]int64{"another-model": 20}}, 1000000, true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := parkedAndWoken(t, tt.narrative, func(r *handoffRig) { r.driver.SetResumeCosts(tt.costs) })
			if tt.context != 0 {
				r.setContext(t, tt.context)
			}
			r.resumed(t)
			if got := len(r.driver.Resumed()) == 1; got != tt.native {
				t.Errorf("native %v, want %v", got, tt.native)
			}
		})
	}
}

// setContext stands for a lane whose last call held this much beyond its first.
func (r *laneRig) setContext(t *testing.T, tokens int64) {
	t.Helper()
	record, err := r.journal.Lane(t.Context(), "lane-1")
	if err != nil {
		t.Fatal(err)
	}
	progress := record.Progress
	progress.ContextTokens, progress.BaselineTokens = tokens+500, 500
	if _, err := r.journal.SaveProgress(t.Context(), "lane-1", record.Generation, progress); err != nil {
		t.Fatal(err)
	}
}

// stoppedMidTurn parks a lane in a turn that never finishes, so its instruction stays open.
func stoppedMidTurn(t *testing.T, before func(*handoffRig)) *handoffRig {
	t.Helper()
	r := &handoffRig{laneRig: laneRigWith(t, handoffProfile), release: make(chan struct{}), reached: make(chan struct{})}
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	if before != nil {
		before(r)
	}
	first := []fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) { close(r.reached) }}, {Hold: true}}
	r.driver.Play(script(first))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	<-r.reached
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	if err := r.subject.Park(t.Context(), "lane-1", lanes.ParkRequest{Trigger: lanes.ParkFleetDrain, Expr: lanes.HumanWake(), SkipNarrative: true}); err != nil {
		t.Fatal(err)
	}
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	return r
}

func TestAStoppedTurnIsStatedAgainOnBothPaths(t *testing.T) {
	const instruction = "When the work is done, or cannot go on"
	t.Run("native", func(t *testing.T) {
		r := stoppedMidTurn(t, nil)
		if checkpoint := r.checkpointOf(t, 1); !strings.Contains(checkpoint.PendingInstruction, instruction) {
			t.Fatalf("checkpoint %+v", checkpoint)
		}
		r.resumed(t)
		if len(r.driver.Resumed()) != 1 {
			t.Fatalf("resumed %+v", r.driver.Resumed())
		}
		if brief := r.driver.Specs()[1].Brief; !strings.Contains(brief, "still open") || !strings.Contains(brief, instruction) {
			t.Errorf("the resume prompt restates nothing: %q", brief)
		}
	})
	t.Run("fresh", func(t *testing.T) {
		r := stoppedMidTurn(t, func(r *handoffRig) { r.driver.SetNativeResume(false) })
		r.resumed(t)
		if len(r.driver.Resumed()) != 0 {
			t.Fatalf("resumed %+v", r.driver.Resumed())
		}
		if brief := r.driver.Specs()[1].Brief; !strings.Contains(brief, "still open") {
			t.Errorf("the fresh brief restates nothing: %q", brief)
		}
	})
}

func TestATurnThatFinishedLeavesNothingPending(t *testing.T) {
	r := parkedAndWoken(t, notCarried, nil)
	if checkpoint := r.checkpointOf(t, 1); checkpoint.PendingInstruction != "" {
		t.Errorf("checkpoint %+v", checkpoint)
	}
	r.resumed(t)
	if brief := r.driver.Specs()[1].Brief; strings.Contains(brief, "still open") {
		t.Errorf("the resume prompt invents pending work: %q", brief)
	}
}

// stalledResume is a tmux resume that never reached a ready state.
type stalledResume struct {
	*fake.AgentDriver
}

func (d stalledResume) Resume(context.Context, ports.SessionSpec, ports.ResumeRef) (ports.SessionHandle, error) {
	return ports.SessionHandle{}, fmt.Errorf("check: %w", ports.ErrResumeNotReady)
}

func TestAStalledResumeStartsFreshInTheSameGeneration(t *testing.T) {
	r := parkedAndWoken(t, notCarried, nil)
	r.config.Driver = stalledResume{AgentDriver: r.driver}
	r.resumed(t)
	specs := r.driver.Specs()
	if len(specs) != 2 || specs[1].Generation != 2 {
		t.Fatalf("specs %+v", specs)
	}
	if !strings.Contains(specs[1].Brief, "which handed off") {
		t.Errorf("a fresh start has no handoff: %q", specs[1].Brief)
	}
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff || generations[1].HandoffIn == "" {
		t.Errorf("a fallback reads as a resume: %+v", generations)
	}
}

func TestAWokenLaneWaitsForADrainingFleet(t *testing.T) {
	r := parkedAndWoken(t, notCarried, nil)
	r.drainFleet(t)
	r.pass(t)
	r.subject.Wait()
	if row := r.row(); row.State != workorders.Ready {
		t.Errorf("a drain let a lane back: %+v", row)
	}
	if len(r.driver.Specs()) != 1 {
		t.Errorf("sessions %d", len(r.driver.Specs()))
	}
}

func TestAResumeKeepsTheSessionsRunningTotal(t *testing.T) {
	r := parkedAndWoken(t, notCarried, nil)
	r.resumed(t)
	got := r.driver.Resumed()
	if len(got) != 1 || got[0].SessionTotalUSD != 0.02 {
		t.Errorf("resumed %+v", got)
	}
}

// TestAFenceOnTheLastClaimLeavesTheNextOneAlone is a beat that names the claim a lane parked
// under, after the lane resumed under another on the same work order. That fence is true, and it
// says nothing about the claim that runs now.
func TestAFenceOnTheLastClaimLeavesTheNextOneAlone(t *testing.T) {
	g := newGate()
	var r *handoffRig
	r = atWorkBefore(t, func(lane *laneRig) fake.DriverScript {
		return script([]fake.DriverStep{{Bound: true}, g.step(), lane.reports(workorders.OutcomeCompleted), completed(0.03)})
	}, fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(notCarried) }}, completed(0.02))
	r.park(t, humanPark())
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	r.pass(t)
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the resumed session never reached its gate")
	}
	stale := ledger.HeldLease{WorkOrder: r.grant.WorkOrder, Claim: r.grant.ClaimID, Fence: r.grant.Fence}
	if err := r.journal.SaveLeases(t.Context(), []ledger.HeldLease{stale}); err != nil {
		close(g.release)
		t.Fatal(err)
	}
	reply, err := r.subject.Beat(t.Context())
	close(g.release)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Fenced) != 1 {
		t.Fatalf("the beat fenced %+v", reply.Fenced)
	}
	r.awaitOrder(t, workorders.Acceptance)
	r.subject.Wait()
}

// restingProfile parks a lane soon after it comes to rest on a question.
const restingProfile = profileAt + "[lifecycle]\nask_wait = \"1s\"\nhandoff_timeout = \"3m\"\nmax_restarts = 0\n"

// sessionToken reads a session's own token, because each generation has one.
func sessionToken(t *testing.T, spec ports.SessionSpec) string {
	t.Helper()
	for _, entry := range spec.Env {
		if value, ok := strings.CutPrefix(entry, lanes.EnvLaneToken+"="); ok {
			return value
		}
	}
	t.Error("no token")
	return ""
}

// callAs is one line of the lane protocol from whichever generation a session belongs to.
func (r *laneRig) callAs(spec ports.SessionSpec, request lanes.Request) {
	r.t.Helper()
	request.Version, request.Token = lanes.SocketProtocol, sessionToken(r.t, spec)
	line, err := json.Marshal(request)
	if err != nil {
		r.t.Error(err)
		return
	}
	if reply := r.subject.AnswerLane(context.Background(), line); !reply.OK {
		r.t.Errorf("generation %d: %s", spec.Generation, reply.Message)
	}
}

// asksThenNarrates is a session that asks a blocking question and comes to rest, then answers
// the instruction that asks for its narrative.
func (r *laneRig) asksThenNarrates(question, narrative string) fake.DriverScript {
	ask := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		r.callAs(spec, lanes.Request{Ask: &lanes.AskRequest{Text: question, Blocking: true}})
	}}
	narrate := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		r.callAs(spec, lanes.Request{Handoff: json.RawMessage(narrative)})
	}}
	return script([]fake.DriverStep{{Bound: true}, ask, completed(0.01)}, []fake.DriverStep{narrate, completed(0.02)})
}

// awaitRest waits for a generation to come to rest, and lets its `ask_wait` pass.
func (r *laneRig) awaitRest(t *testing.T, generation int64) {
	t.Helper()
	r.eventually("generation to rest", func(view ledger.LaneView) bool {
		return view.Generation == generation && view.ActualState == string(lanes.Idle)
	})
	r.clock.Advance(2 * time.Second)
}

// TestANewGenerationSaysWhatTheLastOneSaid keeps a status that matches the one a worker sent before
// a lane parked: the ledger reads the new generation as provisioning until it hears otherwise, and
// a worker that thought it had said so already would leave it reading that for good.
func TestANewGenerationSaysWhatTheLastOneSaid(t *testing.T) {
	r := laneRigWith(t, restingProfile)
	r.driver.Play(r.asksThenNarrates("The first question?", notCarried))
	r.driver.PlayAfter(r.asksThenNarrates("The second question?", notCarried), r.toldToGoOn())
	r.start()
	r.awaitRest(t, 1)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	r.pass(t)
	deadline := time.Now().Add(20 * time.Second)
	for {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		if record.Generation == 2 && record.Actual == lanes.Idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lane reads %s in generation %d", record.Actual, record.Generation)
		}
		time.Sleep(time.Millisecond)
	}
	if view := r.read(); view.Generation != 2 || view.ActualState != string(lanes.Idle) {
		t.Errorf("the ledger reads %s in generation %d", view.ActualState, view.Generation)
	}
	r.clock.Advance(2 * time.Second)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
}

// TestAResumedLaneAsksAndParksAgain walks a lane through two parkings, which is what RS7 asks of
// a real session: the second question belongs to the generation that came back, and it must
// reach the ledger, hold the lane at rest, and park it with a wake of its own.
func TestAResumedLaneAsksAndParksAgain(t *testing.T) {
	r := laneRigWith(t, restingProfile)
	r.driver.Play(r.asksThenNarrates("The first question?", notCarried))
	r.driver.PlayAfter(r.asksThenNarrates("The second question?", notCarried), r.toldToGoOn())
	r.start()
	r.awaitRest(t, 1)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	r.pass(t)
	r.awaitRest(t, 2)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	if view := r.read(); view.ActualState != ledger.StateParked || view.Generation != 2 {
		t.Fatalf("the lane reads %s in generation %d", view.ActualState, view.Generation)
	}
	wake := r.wakeOf(t, 2)
	if !wake.Open() {
		t.Errorf("the second wake closed with nobody asked: %+v", wake)
	}
	if first := r.wakeOf(t, 1); first.Open() {
		t.Errorf("the first wake stayed open: %+v", first)
	}
	asked := r.questionList(t)
	if len(asked) != 2 {
		t.Fatalf("questions %+v", asked)
	}
	var second string
	for _, question := range asked {
		if question.Text == "The second question?" {
			second = question.ID
		}
	}
	if want := lanes.QuestionWake([]string{second}); wake.Expr != want {
		t.Errorf("the second wake waits on %s, want %s", wake.Expr, want)
	}
	r.wake(t)
	r.resumed(t)
	generations := r.generations(t)
	if len(generations) != 3 || generations[1].EndReason != workorders.EndParked {
		t.Fatalf("generations %+v", generations)
	}
	if got := r.driver.Resumed(); len(got) != 2 {
		t.Errorf("resumed %+v", got)
	}
}

// TestANativeResumeStaysInTheRunModeItStartedIn changes a repository's profile while its lane is
// parked. A session resumed in the other mode is untested, so a native resume keeps its mode, and
// a fresh session has no such history and takes what the profile says now.
func TestANativeResumeStaysInTheRunModeItStartedIn(t *testing.T) {
	const pane, headless = "run_mode = \"tmux\"\n", "run_mode = \"headless\"\n"
	tests := []struct {
		name   string
		native bool
		want   string
	}{
		{"a native resume keeps its mode", true, ports.RunModeInteractive},
		{"a fresh start reads the profile", false, ports.RunModeHeadless},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r *handoffRig
			r = atWorkOn(t, pane+handoffProfile, (*laneRig).toldToGoOn,
				fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(notCarried) }}, completed(0.02))
			r.driver.SetNativeResume(tt.native)
			r.park(t, humanPark())
			r.awaitOrder(t, workorders.Parked)
			r.subject.Wait()
			r.config.Provisioner.Profiles.(*fake.ProfileSource).Put("/repository", headless+handoffProfile)
			r.wake(t)
			r.resumed(t)
			specs := r.driver.Specs()
			if len(specs) != 2 || specs[0].RunMode != ports.RunModeInteractive {
				t.Fatalf("specs %+v", specs)
			}
			if specs[1].RunMode != tt.want {
				t.Errorf("run mode %q, want %q", specs[1].RunMode, tt.want)
			}
		})
	}
}

// killedAtResume stands for a kill right after a resume's journal write: the write stands, and
// nothing that follows it in the run happens.
type killedAtResume struct {
	ports.Journal
}

var errKilled = errors.New("killed")

func (j killedAtResume) BeginResume(ctx context.Context, lane lanes.Record, event ledger.UploadEvent) (ledger.UploadEvent, error) {
	stored, err := j.Journal.BeginResume(ctx, lane, event)
	if err != nil {
		return stored, err
	}
	return stored, errKilled
}

// TestALaneKilledInItsResumeStillComesToRest kills a worker after a resume's journal write, so
// the next process starts that generation from the record. A lane still reading `parked` takes
// no signal the table has an edge for: it never rests, never parks, and never hands off.
func TestALaneKilledInItsResumeStillComesToRest(t *testing.T) {
	r := laneRigWith(t, restingProfile)
	r.driver.Play(r.asksThenNarrates("The first question?", notCarried))
	r.start()
	r.awaitRest(t, 1)
	r.awaitOrder(t, workorders.Parked)
	r.subject.Wait()
	r.wake(t)
	killed := worker.New(r.link, killedAtResume{Journal: r.journal}, r.clock, worker.Config{
		Name:      "test",
		JoinToken: func(ctx context.Context) (string, error) { return r.plane.CreateJoinToken(ctx, member) },
		Lanes:     r.config,
		Report:    func(err error) { t.Log(err) },
	})
	if err := killed.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := killed.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := killed.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	killed.Wait()
	if len(r.driver.Resumed()) != 0 {
		t.Fatalf("the kill came after the session: %+v", r.driver.Resumed())
	}
	next := r.nextWorker(t)
	r.subject = next
	driver, ok := r.config.Driver.(*fake.AgentDriver)
	if !ok {
		t.Fatalf("driver %T", r.config.Driver)
	}
	r.driver = driver
	driver.Play(r.asksThenNarrates("The second question?", notCarried))
	t.Cleanup(driver.StopEverySession)
	if err := next.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.awaitRest(t, 2)
	r.awaitOrder(t, workorders.Parked)
	next.Wait()
	if view := r.read(); view.ActualState != ledger.StateParked || view.Generation != 2 {
		t.Errorf("the lane reads %s in generation %d", view.ActualState, view.Generation)
	}
}

// otherWorker is a second machine on the same control plane.
func (r *laneRig) otherWorker(t *testing.T, driver *fake.AgentDriver) (*worker.Worker, ports.Journal, *fake.Worktrees) {
	t.Helper()
	journal, err := fake.NewJournal()
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	worktrees := fake.NewWorktrees()
	config := *r.config
	config.Driver = driver
	config.Provisioner.Worktrees = worktrees
	config.StateDir, config.RunDir = t.TempDir(), t.TempDir()
	other := worker.New(r.link, journal, r.clock, worker.Config{
		Name:      "other",
		JoinToken: func(ctx context.Context) (string, error) { return r.plane.CreateJoinToken(ctx, member) },
		Lanes:     &config,
		Report:    func(err error) { t.Log(err) },
	})
	if err := other.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return other, journal, worktrees
}

func TestAnotherWorkerStartsFromWhatTheLedgerHolds(t *testing.T) {
	driver := fake.NewAgentDriver()
	r := parkedAndWoken(t, narrativeText, nil)
	other, journal, worktrees := r.otherWorker(t, driver)
	reports := fake.DriverStep{Do: func(spec ports.SessionSpec) {
		if _, err := journal.SaveReport(context.Background(), spec.Lane, []byte(`{"outcome":"completed"}`)); err != nil {
			r.t.Error(err)
		}
	}}
	driver.Play(script([]fake.DriverStep{{Bound: true}, reports, completed(0.03)}))
	r.driver.StopEverySession()
	if _, err := control.NewWakes(r.store).WakeTick(t.Context(), scope, member); err != nil {
		t.Fatal(err)
	}
	if err := other.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Ready {
		t.Fatalf("another worker went ahead of the delay: %+v", row)
	}
	r.shiftDatabase(t, control.AffinityDelayMs+1000)
	if _, err := control.NewWakes(r.store).WakeTick(t.Context(), scope, member); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := other.Sync(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err := other.Reconcile(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for r.row().State != workorders.Acceptance && time.Now().Before(deadline) {
		if err := other.Upload(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	other.Wait()
	if row := r.row(); row.State != workorders.Acceptance {
		t.Fatalf("row %+v", row)
	}
	specs := driver.Specs()
	if len(specs) != 1 || specs[0].Generation != 2 || !strings.Contains(specs[0].Brief, "Fix the failing test") {
		t.Fatalf("specs %+v", specs)
	}
	if got := driver.Resumed(); len(got) != 0 {
		t.Errorf("a machine with no session resumed one: %+v", got)
	}
	if fetched := worktrees.Fetched(); len(fetched) == 0 || fetched[0] != lanes.HeadsRef("lane/lane-1") {
		t.Errorf("fetched %v", fetched)
	}
	record, err := journal.Lane(t.Context(), "lane-1")
	if err != nil || record.Generation != 2 {
		t.Errorf("record %+v, %v", record, err)
	}
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff {
		t.Errorf("generations %+v", generations)
	}
}
