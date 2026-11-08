package worker_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/messages"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/worker"
	"ropeshift.dev/ropeshift/internal/workorders"
)

// contextProfile sets a lane's thresholds where a script can reach them, with room for a baseline.
const contextProfile = handoffProfile + `
[context]
min_headroom_tokens = 40

[context.roles.writer]
warn_tokens = 120
compact_tokens = 150
handoff_tokens = 180
`

func tokens(n int64) fake.DriverStep { return fake.DriverStep{Context: &lanes.Usage{InputTokens: n}} }

// contextRig runs a lane under contextProfile, whose next generation finishes the work. Its session
// can play a step before it returns, so a step that reaches the rig needs contextLane and startOn.
func contextRig(t *testing.T, turns ...[]fake.DriverStep) *laneRig {
	t.Helper()
	return contextLane(t).startOn(turns...)
}

// contextLane is a lane under contextProfile with no session yet, so a step can name the rig
// before anything plays it.
func contextLane(t *testing.T) *laneRig {
	t.Helper()
	r := laneRigWith(t, contextProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	return r
}

// startOn starts the lane on these turns, and its next generation finishes the work.
func (r *laneRig) startOn(turns ...[]fake.DriverStep) *laneRig {
	r.t.Helper()
	r.driver.Play(script(turns...))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	return r
}

func (r *laneRig) awaitSent(t *testing.T, text string) ports.Input {
	t.Helper()
	var found ports.Input
	r.eventually("an input that says "+text, func(ledger.LaneView) bool {
		for _, input := range r.driver.Sent() {
			if strings.Contains(input.Text, text) {
				found = input
				return true
			}
		}
		return false
	})
	return found
}

func (r *laneRig) awaitOrderState(t *testing.T, state workorders.State) {
	t.Helper()
	r.awaitOrder(t, state)
	r.subject.Wait()
}

// reach waits for a session at its gate. A threshold crossed before it moves the lane on, so the
// lane's state says nothing here.
func (r *laneRig) reach(t *testing.T, g *gate) {
	t.Helper()
	g.letGoOnFailure(t)
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
}

// letGoOnFailure keeps a failed test from waiting on a session at its gate. It goes after the rig,
// because a rig's cleanup waits for the session.
func (g *gate) letGoOnFailure(t *testing.T) {
	t.Cleanup(func() {
		select {
		case <-g.release:
		default:
			if t.Failed() {
				close(g.release)
			}
		}
	})
}

// admit is a session's stream saying it took an input.
func (r *laneRig) admit(input ports.Input) { r.driver.AdmitInput(input.ID) }

// hookAdmits is a pane's hook saying it took an input, which carries only its first line.
func (r *laneRig) hookAdmits(t *testing.T, text string) {
	t.Helper()
	line, err := json.Marshal(lanes.Request{
		Version: lanes.SocketProtocol, Token: r.token(t),
		Hook: &lanes.HookEvent{Event: "UserPromptSubmit", Message: messages.ReadFirstLine(text), Subject: "s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(t.Context(), line); !reply.OK {
		t.Fatalf("reply %+v", reply)
	}
}

func TestContextUnderItsThresholdsDoesNothing(t *testing.T) {
	r := contextLane(t)
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), tokens(140), r.reports(workorders.OutcomeCompleted), completed(0.01)})
	r.awaitOrderState(t, workorders.Acceptance)
	if got := r.driver.Compacts(); got != 0 {
		t.Errorf("%d compactions under the threshold", got)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions", len(specs))
	}
}

// TestAThresholdDuringATurnSendsTheInstruction is the design's row for `working` with a channel:
// the instruction goes on the mid-turn path, the next tool call is denied until the session takes
// it, and the lane hands off to a new generation.
func TestAThresholdDuringATurnSendsTheInstruction(t *testing.T) {
	g := newGate()
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) {
		if reply := r.callHandoff(narrativeText); !reply.OK {
			t.Errorf("reply %+v", reply)
		}
	}}
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), tokens(185), g.step(), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)})
	r.reach(t, g)
	input := r.awaitSent(t, lanes.ToolLaneHandoff)
	if !input.MidTurn || input.Peer {
		t.Errorf("the instruction took another path: %+v", input)
	}
	if holds := r.driver.Holds(); len(holds) != 1 || !strings.Contains(holds[0], lanes.ToolLaneHandoff) {
		t.Errorf("holds %q", holds)
	}
	if id := messages.ReadFirstLine(input.Text); id == "" {
		t.Errorf("no first line a hook can read: %q", input.Text)
	} else if input.ID != messages.InputID(id) {
		t.Errorf("input %s, and its first line names %s", input.ID, id)
	}
	r.admit(input)
	r.eventually("the hold lifted", func(ledger.LaneView) bool { return len(r.driver.Holds()) == 2 })
	if holds := r.driver.Holds(); holds[1] != "" {
		t.Errorf("holds %q", holds)
	}
	close(g.release)
	r.awaitSecondGeneration(t)
	generations := r.generations(t)
	if len(generations) != 2 || generations[1].StartReason != ledger.StartHandoff {
		t.Errorf("generations %+v", generations)
	}
	_, doc := r.storedHandoff(t, 1)
	if doc.Reason != lanes.HandoffContextThreshold {
		t.Errorf("reason %q", doc.Reason)
	}
}

// A hook reports what a pane took, and a session with a stream reports it on the stream.
func TestAnInstructionAdmittedByAHookLiftsTheHold(t *testing.T) {
	g := newGate()
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), tokens(185), g.step(), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)})
	r.reach(t, g)
	input := r.awaitSent(t, lanes.ToolLaneHandoff)
	r.hookAdmits(t, input.Text)
	r.eventually("the hold lifted", func(ledger.LaneView) bool { return len(r.driver.Holds()) == 2 })
	close(g.release)
	r.awaitSecondGeneration(t)
}

func TestWithNoChannelTheNextToolCallIsDenied(t *testing.T) {
	g := newGate()
	var r *laneRig
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r = laneRigWith(t, contextProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.SetPeerChannel(false)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, tokens(30), tokens(185), g.step(), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)}))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.reach(t, g)
	input := r.awaitSent(t, lanes.ToolLaneHandoff)
	if input.MidTurn {
		t.Errorf("no channel, and the instruction took one: %+v", input)
	}
	if holds := r.driver.Holds(); len(holds) != 1 || holds[0] == "" {
		t.Errorf("holds %q", holds)
	}
	close(g.release)
	r.awaitSecondGeneration(t)
	if holds := r.driver.Holds(); len(holds) != 1 {
		t.Errorf("a hold nothing can answer was lifted: %q", holds)
	}
}

// A socket that takes nothing leaves the instruction to the end of the turn, and the hold in place.
func TestAnInstructionTheSocketRefusesWaitsForTheTurnToEnd(t *testing.T) {
	g := newGate()
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), tokens(100), g.step(), tokens(185), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)})
	g.letGoOnFailure(t)
	r.atGate(g)
	r.driver.SetMidTurnRefused(true)
	close(g.release)
	input := r.awaitSent(t, lanes.ToolLaneHandoff)
	if input.MidTurn {
		t.Errorf("sent %+v", input)
	}
	r.awaitSecondGeneration(t)
}

func TestAtRestAThresholdCompacts(t *testing.T) {
	r := contextLane(t)
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, completed(0.02)},
		[]fake.DriverStep{tokens(40), r.reports(workorders.OutcomeCompleted), completed(0.03)},
	)
	r.awaitOrderState(t, workorders.Acceptance)
	if got := r.driver.Compacts(); got != 1 {
		t.Errorf("%d compactions", got)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("a compaction that worked went on to %d sessions", len(specs))
	}
	if sent := r.driver.Sent(); len(sent) != 1 || sent[0].Text != lanes.AfterCompaction {
		t.Errorf("sent %+v", sent)
	}
}

// The first call after a compaction is what counts, and a compaction that left too much hands off.
func TestACompactionThatLeftTooMuchHandsOff(t *testing.T) {
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, completed(0.02)},
		[]fake.DriverStep{tokens(155), completed(0.03)},
		[]fake.DriverStep{answer, completed(0.04)},
	)
	r.awaitSecondGeneration(t)
	if got := r.driver.Compacts(); got != 1 {
		t.Errorf("%d compactions", got)
	}
	if sent := r.driver.Sent(); len(sent) != 2 || !sent[1].MidTurn {
		t.Errorf("the turn the session was told to go on with is a running turn: %+v", sent)
	}
}

// A compaction's own API call comes before its boundary, and it is no evidence of anything.
func TestAUsageBeforeTheBoundaryIsNoEvidence(t *testing.T) {
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{tokens(10), {Compaction: true}, completed(0.02)},
		[]fake.DriverStep{tokens(155), completed(0.03)},
		[]fake.DriverStep{answer, completed(0.04)},
	)
	r.awaitSecondGeneration(t)
}

// A turn that ended with no boundary did not compact, whatever its figures say.
func TestARunWithNoBoundaryDidNotCompact(t *testing.T) {
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
		[]fake.DriverStep{answer, completed(0.03)},
	)
	r.awaitSecondGeneration(t)
	if sent := r.driver.Sent(); len(sent) != 1 || !strings.Contains(sent[0].Text, lanes.ToolLaneHandoff) || sent[0].MidTurn {
		t.Errorf("sent %+v", sent)
	}
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("a lane at rest held its tools: %q", holds)
	}
}

func TestAnOversizedPackHoldsNoHandoff(t *testing.T) {
	g := newGate()
	r := contextRig(t, []fake.DriverStep{{Bound: true}, tokens(170), tokens(190), g.step(), completed(0.01)}, []fake.DriverStep{{Hold: true}})
	g.letGoOnFailure(t)
	r.atGate(g)
	r.eventually("the lane reads oversized", func(view ledger.LaneView) bool {
		return slices.Contains(view.Conditions, string(lanes.PackOversized))
	})
	if view := r.read(); view.ActualState != string(lanes.Working) {
		t.Errorf("a lane with no room moved to %s", view.ActualState)
	}
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("holds %q", holds)
	}
	close(g.release)
	r.eventually("a compaction at rest", func(ledger.LaneView) bool { return r.driver.Compacts() == 1 })
}

// A restart reads what a run left, so a compaction in flight is still judged by its next call, and
// the worker that took over tells the session to go on.
func TestARestartKeepsACompactionInFlight(t *testing.T) {
	r := contextRig(t,
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Hold: true}},
	)
	r.eventually("a compaction asked for", func(ledger.LaneView) bool { return r.driver.Compacts() == 1 })
	var progress lanes.Progress
	r.eventually("the journal holds it", func(ledger.LaneView) bool {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		if err != nil {
			t.Fatal(err)
		}
		progress = record.Progress
		return progress.CompactPending && record.Actual == lanes.Working
	})
	if progress.Compactions != 1 {
		t.Errorf("progress %+v", progress)
	}
	finish := fake.DriverStep{Do: func(ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(ports.SessionSpec{Lane: "lane-1"}) }}
	driver := r.takeOver(t, script([]fake.DriverStep{{Compaction: true}, completed(0.02)}, []fake.DriverStep{tokens(40), finish, completed(0.03)}))
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := driver.Sent(); len(sent) != 1 || sent[0].Text != lanes.AfterCompaction {
		t.Errorf("sent %+v", sent)
	}
	if specs := driver.Specs(); len(specs) != 0 {
		t.Errorf("a compaction in flight became a new session: %d", len(specs))
	}
}

// A boundary the last worker read is still a boundary, so the next figure is the verdict, and a lane
// past its threshold after that is in a running turn with its compaction behind it.
func TestARestartKeepsABoundaryItRead(t *testing.T) {
	r := contextRig(t,
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, completed(0.02)},
		[]fake.DriverStep{{Hold: true}},
	)
	r.eventually("the boundary in the journal", func(ledger.LaneView) bool {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		return err == nil && record.Progress.CompactSeen
	})
	driver := r.takeOver(t, script([]fake.DriverStep{{Bound: true}, tokens(40), tokens(185), {Hold: true}}))
	r.eventually("the instruction", func(ledger.LaneView) bool { return len(driver.Sent()) == 1 })
	if sent := driver.Sent(); !sent[0].MidTurn {
		t.Errorf("sent %+v", sent)
	}
}

// takeOver stands for a new worker on the same journal, with a session that outlived the last. The
// first run stays on that session's stream until the test is over.
func (r *laneRig) takeOver(t *testing.T, plays fake.DriverScript) *fake.AgentDriver {
	t.Helper()
	first := r.subject
	r.stopWorker()
	t.Cleanup(func() {
		r.driver.StopEverySession()
		first.Wait()
	})
	driver := fake.NewAgentDriver()
	driver.Leave("lane-1", 1, lanes.Session{Found: true, Liveness: lanes.LivenessAlive, Readable: true})
	driver.Play(plays)
	config := *r.config
	config.Driver = driver
	r.subject = worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config})
	t.Cleanup(func() {
		driver.StopEverySession()
		r.subject.Wait()
	})
	if err := r.subject.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	return driver
}

// A worker that took over reads what the driver offers again, because a run keeps none of it, and
// the baseline a restart reads is the one the session began with, or a lane near its threshold
// would read as one with no room to hand off.
func TestARunThatTookOverGoesOnByThePolicy(t *testing.T) {
	g := newGate()
	r := contextLane(t)
	finish := fake.DriverStep{Do: func(ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(ports.SessionSpec{Lane: "lane-1"}) }}
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), g.step(), completed(0.01)})
	r.reach(t, g)
	r.eventually("a baseline in the journal", func(ledger.LaneView) bool {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		return err == nil && record.Progress.BaselineTokens == 30
	})
	defer close(g.release)
	driver := r.takeOver(t, script(
		[]fake.DriverStep{{Bound: true}, tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, completed(0.02)},
		[]fake.DriverStep{tokens(40), finish, completed(0.03)},
	))
	r.awaitOrderState(t, workorders.Acceptance)
	if got := driver.Compacts(); got != 1 {
		t.Errorf("%d compactions", got)
	}
}

// Past the handoff line a lane with no room stays, so what a first call read must survive a restart.
func TestARestartKeepsWhatAGenerationBeganWith(t *testing.T) {
	g := newGate()
	r := contextRig(t, []fake.DriverStep{{Bound: true}, tokens(30), g.step(), completed(0.01)})
	r.reach(t, g)
	r.eventually("a baseline in the journal", func(ledger.LaneView) bool {
		record, err := r.journal.Lane(t.Context(), "lane-1")
		return err == nil && record.Progress.BaselineTokens == 30
	})
	defer close(g.release)
	driver := r.takeOver(t, script([]fake.DriverStep{{Bound: true}, tokens(185), {Hold: true}}))
	r.eventually("the instruction", func(ledger.LaneView) bool { return len(driver.Sent()) == 1 })
}

// A lane that reported is done, whatever its last calls of a turn say.
func TestALaneThatReportedIsNotToldToHandOff(t *testing.T) {
	r := contextLane(t)
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), r.reports(workorders.OutcomeCompleted), tokens(185), completed(0.01)})
	r.awaitOrderState(t, workorders.Acceptance)
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v", sent)
	}
	if holds := r.driver.Holds(); len(holds) != 0 {
		t.Errorf("holds %q", holds)
	}
}

// A driver with no path to a compaction goes straight to the next tier.
func TestWithNoCompactionTheCompactThresholdHandsOff(t *testing.T) {
	var r *laneRig
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r = laneRigWith(t, contextProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.SetCompact(false)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)}))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.awaitSecondGeneration(t)
	if got := r.driver.Compacts(); got != 0 {
		t.Errorf("%d compactions", got)
	}
}

// A pane that waits on an answer takes no keys, and that says nothing about a later compaction: the
// lane goes on as it would have, and the next time it rests it compacts.
func TestACompactionThatMustWaitForAnAnswerTriesAgain(t *testing.T) {
	var r *laneRig
	finish := fake.DriverStep{Do: func(spec ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(spec) }}
	r = laneRigWith(t, contextProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.FailCompact(ports.ErrAwaitingAnswer)
	answered := fake.DriverStep{Do: func(ports.SessionSpec) { r.driver.FailCompact(nil) }}
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{answered, completed(0.02)},
		[]fake.DriverStep{{Compaction: true}, completed(0.03)},
		[]fake.DriverStep{tokens(40), finish, completed(0.04)},
	))
	r.start()
	r.awaitOrderState(t, workorders.Acceptance)
	sent := r.driver.Sent()
	if len(sent) != 2 || sent[0].Text != lanes.Nudge || sent[1].Text != lanes.AfterCompaction {
		t.Errorf("sent %+v", sent)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions", len(specs))
	}
}

// What one generation's compaction came to says nothing of the next, which has a new session.
func TestANewGenerationMayCompact(t *testing.T) {
	var r *laneRig
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	finish := fake.DriverStep{Do: func(spec ports.SessionSpec) { r.reports(workorders.OutcomeCompleted).Do(spec) }}
	r = laneRigWith(t, contextProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{completed(0.02)},
		[]fake.DriverStep{answer, completed(0.03)},
	))
	r.driver.PlayAfter(script(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.04)},
		[]fake.DriverStep{{Compaction: true}, completed(0.05)},
		[]fake.DriverStep{tokens(40), finish, completed(0.06)},
	))
	r.start()
	r.awaitOrderState(t, workorders.Acceptance)
	if got := r.driver.Compacts(); got != 2 {
		t.Errorf("%d compactions", got)
	}
}

// A compaction that cannot start is one that did not run, and the lane has a next tier.
func TestACompactionThatCannotStartHandsOff(t *testing.T) {
	var r *laneRig
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r = laneRigWith(t, contextProfile)
	t.Cleanup(func() {
		r.driver.StopEverySession()
		r.subject.Wait()
	})
	r.driver.FailCompact(errors.New("the session took nothing"))
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)}))
	r.driver.PlayAfter(r.toldToGoOn())
	r.start()
	r.awaitSecondGeneration(t)
}

// A status line reports about 330 ms after a reply, so a short turn can end before its figure comes.
// That leaves a verdict to come and not a failed compaction: the lane goes on as a lane with no
// outcome does, and the next figure judges it.
func TestAFigureThatFollowsTheTurnStillJudges(t *testing.T) {
	r := contextLane(t)
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, completed(0.02)},
		[]fake.DriverStep{completed(0.03)},
		[]fake.DriverStep{tokens(40), r.reports(workorders.OutcomeCompleted), completed(0.04)},
	)
	r.awaitOrderState(t, workorders.Acceptance)
	sent := r.driver.Sent()
	if len(sent) != 2 || sent[0].Text != lanes.AfterCompaction || sent[1].Text != lanes.Nudge {
		t.Errorf("sent %+v", sent)
	}
	if specs := r.driver.Specs(); len(specs) != 1 {
		t.Errorf("%d sessions", len(specs))
	}
}

// A session that cannot be told to go on gives no next call, so nothing will ever judge it.
func TestACompactionThatCannotGoOnHandsOff(t *testing.T) {
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	refused := fake.DriverStep{Do: func(ports.SessionSpec) { r.driver.FailSend(errors.New("the session took nothing")) }}
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, refused, completed(0.02)},
		[]fake.DriverStep{answer, completed(0.03)},
	)
	r.awaitSecondGeneration(t)
	if sent := r.driver.Sent(); len(sent) != 1 || !strings.Contains(sent[0].Text, lanes.ToolLaneHandoff) {
		t.Errorf("sent %+v", sent)
	}
}

// One compaction working does not use the next one up: context that grows back compacts again.
func TestASecondCompactionHasItsOwnInputs(t *testing.T) {
	r := contextLane(t)
	r.startOn(
		[]fake.DriverStep{{Bound: true}, tokens(30), tokens(160), completed(0.01)},
		[]fake.DriverStep{{Compaction: true}, completed(0.02)},
		[]fake.DriverStep{tokens(40), tokens(160), completed(0.03)},
		[]fake.DriverStep{{Compaction: true}, completed(0.04)},
		[]fake.DriverStep{tokens(40), r.reports(workorders.OutcomeCompleted), completed(0.05)},
	)
	r.awaitOrderState(t, workorders.Acceptance)
	if got := r.driver.Compacts(); got != 2 {
		t.Errorf("%d compactions", got)
	}
	if sent := r.driver.Sent(); len(sent) != 2 {
		t.Errorf("sent %+v", sent)
	}
}

// A compaction nobody here asked for is not this policy's to judge, and its next call is no verdict.
func TestABoundaryNobodyAskedForChangesNothing(t *testing.T) {
	r := contextLane(t)
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), {Compaction: true}, tokens(40), r.reports(workorders.OutcomeCompleted), completed(0.01)})
	r.awaitOrderState(t, workorders.Acceptance)
	if got := r.driver.Compacts(); got != 0 {
		t.Errorf("%d compactions", got)
	}
	if sent := r.driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v", sent)
	}
}

// Both a stream and a hook say a session took its instruction, and the first is enough.
func TestAHoldLiftsOnce(t *testing.T) {
	g := newGate()
	r := contextLane(t)
	answer := fake.DriverStep{Do: func(ports.SessionSpec) { r.callHandoff(narrativeText) }}
	r.startOn([]fake.DriverStep{{Bound: true}, tokens(30), tokens(185), g.step(), completed(0.01)}, []fake.DriverStep{answer, completed(0.02)})
	r.reach(t, g)
	input := r.awaitSent(t, lanes.ToolLaneHandoff)
	r.hookAdmits(t, input.Text)
	r.admit(input)
	r.eventually("the hold lifted", func(ledger.LaneView) bool { return len(r.driver.Holds()) >= 2 })
	close(g.release)
	r.awaitSecondGeneration(t)
	if holds := r.driver.Holds(); len(holds) != 2 {
		t.Errorf("holds %q", holds)
	}
}
