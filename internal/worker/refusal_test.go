package worker_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// endsAtRest is a first turn that ends with no outcome.
func endsAtRest(*laneRig) [][]fake.DriverStep {
	return [][]fake.DriverStep{{{Bound: true}, completed(0.01)}}
}

// refusedLane is a lane whose first turn ends with no outcome while its session refuses the nudge, because it may
// wait on an answer the nudge's keys would give. It returns once the refusal is on record.
func refusedLane(t *testing.T, profile string, turns func(*laneRig) [][]fake.DriverStep) *laneRig {
	t.Helper()
	r := laneRigWith(t, profile)
	r.driver.Play(script(turns(r)...))
	r.driver.FailSend(ports.ErrAwaitingAnswer)
	r.start()
	key := lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "nudge").Key
	r.eventually("a refused nudge", func(ledger.LaneView) bool {
		effect, err := r.journal.Effect(t.Context(), key)
		return err == nil && effect.State == lanes.EffectFailed
	})
	return r
}

// runsOn moves the worker's clock on by d, a minute at a time, and fails if the work order stopped running.
func (r *laneRig) runsOn(t *testing.T, d time.Duration) {
	t.Helper()
	for range int(d / time.Minute) {
		r.clock.Advance(time.Minute)
		settle()
	}
	if state := r.row().State; state != workorders.Running {
		t.Fatalf("a lane whose nudge a wait refused was left %s with %+v", state, r.outcome())
	}
}

// endsPastTheBound moves the worker's clock on until the lane ends, and fails one that runs for long.
func (r *laneRig) endsPastTheBound(t *testing.T, state workorders.State, reason string) {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); r.row().State == workorders.Running; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(end) {
			t.Fatal("the lane was held running past max_live_wait")
		}
		r.clock.Advance(5 * time.Second)
	}
	r.awaitOrderState(t, state)
	if change := r.outcome(); change.Reason != reason {
		t.Errorf("outcome %+v, want %s", change, reason)
	}
}

// askBlocking is a session's blocking question, as its MCP tool asks it.
func (r *laneRig) askBlocking(t *testing.T, text string) {
	t.Helper()
	line, err := json.Marshal(lanes.Request{Version: lanes.SocketProtocol, Token: r.token(t), Ask: &lanes.AskRequest{Text: text, Blocking: true}})
	if err != nil {
		t.Fatal(err)
	}
	if reply := r.subject.AnswerLane(context.Background(), line); !reply.OK {
		t.Fatal(reply.Message)
	}
}

// A refused nudge is no end of the lane's work: the session may wait on a dialog of the turn after it. The lane
// waits for as long as it may wait live (max_live_wait), and only then ends blocked, as it did at once before.
func TestARefusedNudgeWaitsTheLiveWaitAndThenEndsTheLane(t *testing.T) {
	t.Parallel()
	r := refusedLane(t, "", endsAtRest)
	r.runsOn(t, lanes.MaxLiveWait(nil)-time.Minute)
	r.endsPastTheBound(t, workorders.Blocked, workorders.ReasonNoOutcome)
	if sent := r.sentText(); len(sent) != 0 {
		t.Errorf("sent %q to a session that waited on an answer", sent)
	}
}

// What the session does while the refused nudge waits holds the bound or ends the wait, so no lane at work and no
// request open is ended: a call at work holds it, and a call's end begins it again; an open request is left to
// approval_wait and a blocking question to ask_wait, which park the lane; a prompt begins a turn whose end decides,
// with the nudge still the lane's.
func TestWhatTheSessionDoesHoldsARefusedNudgesBound(t *testing.T) {
	t.Parallel()
	bound := lanes.MaxLiveWait(nil)
	t.Run("a call at work, then its end", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, "", endsAtRest)
		r.callAt(t, "PreToolUse", "tool-1", time.Now().UnixMilli())
		r.runsOn(t, 2*bound)
		r.callAt(t, "PostToolUse", "tool-1", time.Now().UnixMilli())
		r.runsOn(t, bound-time.Minute)
		r.endsPastTheBound(t, workorders.Blocked, workorders.ReasonNoOutcome)
	})
	t.Run("an open request", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, profileAt+"[lifecycle]\napproval_wait = \"20m\"\nhandoff_timeout = \"30ms\"\n", endsAtRest)
		if reply := r.look(t, gatedCall, "call-1", false); !reply.OK {
			t.Fatalf("reply %+v", reply)
		}
		r.runsOn(t, bound+time.Minute)
		r.runsOn(t, 20*time.Minute-bound-2*time.Minute)
		r.awaitParked(t)
		if parking := r.parkingOf(t); parking == nil || parking.Trigger != lanes.ParkApproval {
			t.Errorf("parking %+v, want the request's wait", parking)
		}
	})
	t.Run("a blocking question", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, profileAt+"[lifecycle]\nask_wait = \"10m\"\nhandoff_timeout = \"30ms\"\n", endsAtRest)
		r.askBlocking(t, "Which harbour comes first?")
		r.runsOn(t, bound+time.Minute)
		r.runsOn(t, 10*time.Minute-2*time.Minute)
		r.awaitParked(t)
		if parking := r.parkingOf(t); parking == nil || parking.Trigger != lanes.ParkBlockingQuestion {
			t.Errorf("parking %+v, want the question's wait", parking)
		}
	})
	t.Run("a prompt", func(t *testing.T) {
		t.Parallel()
		resumed := newGate()
		r := refusedLane(t, "", func(r *laneRig) [][]fake.DriverStep {
			return [][]fake.DriverStep{
				{{Bound: true}, completed(0.01), resumed.step(), completed(0.02)},
				{r.reports(workorders.OutcomeCompleted), completed(0.03)},
			}
		})
		r.gates(resumed)
		r.runsOn(t, bound/2)
		r.promptAt(t, time.Now().UnixMilli())
		r.runsOn(t, bound/2+time.Minute)
		resumed.free()
		r.awaitOrderState(t, workorders.Acceptance)
		if sent := r.sentText(); len(sent) != 1 || sent[0] != lanes.Nudge {
			t.Errorf("sent %q, want the nudge once the turn the prompt began ended", sent)
		}
	})
}

// A worker that takes the lane over reads the refusal in the journal, and bounds the wait again from its own start,
// unless a prompt or a call's end came after both the turn's end and the newest dialog: the session went on, and a
// wait that live activity ended is not begun again. It sends nothing to begin with either way.
func TestARefusedNudgesBoundAcrossARestart(t *testing.T) {
	t.Parallel()
	bound := lanes.MaxLiveWait(nil)
	t.Run("still waiting", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, "", endsAtRest)
		r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
		driver := r.takeOver(t, script())
		r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
		r.runsOn(t, bound-time.Minute)
		r.endsPastTheBound(t, workorders.Blocked, workorders.ReasonNoOutcome)
		if sent := driver.Sent(); len(sent) != 0 {
			t.Errorf("sent %+v after a restart", sent)
		}
	})
	t.Run("a call's end since", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, "", endsAtRest)
		r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
		journalHook(t, r, lanes.HookEvent{Event: "PermissionRequest", Subject: "s", Tool: "Bash", AtMs: time.Now().UnixMilli()})
		journalHook(t, r, lanes.HookEvent{Event: "PostToolUse", Subject: "tool-1", Tool: "Bash", AtMs: time.Now().UnixMilli() + 1})
		driver := r.takeOver(t, script())
		r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
		r.runsOn(t, 2*bound)
		if sent := driver.Sent(); len(sent) != 0 {
			t.Errorf("sent %+v after a restart", sent)
		}
	})
	t.Run("a dialog after the call's end", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, "", endsAtRest)
		r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
		journalHook(t, r, lanes.HookEvent{Event: "PostToolUse", Subject: "tool-1", Tool: "Bash", AtMs: time.Now().UnixMilli()})
		journalHook(t, r, lanes.HookEvent{Event: "Notification", Subject: "s", Detail: lanes.NotificationNeedsInput, AtMs: time.Now().UnixMilli() + 1})
		driver := r.takeOver(t, script())
		r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
		r.runsOn(t, bound-time.Minute)
		r.endsPastTheBound(t, workorders.Blocked, workorders.ReasonNoOutcome)
	})
	t.Run("a prompt since", func(t *testing.T) {
		t.Parallel()
		r := refusedLane(t, "", endsAtRest)
		r.eventually("a lane at rest", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Idle) })
		journalHook(t, r, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "session-1", SessionID: "session-1", AtMs: time.Now().UnixMilli()})
		driver := r.takeOver(t, script())
		r.eventually("a lane at work on the prompt", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
		r.runsOn(t, bound-time.Minute)
		if sent := r.awaitSentOn(t, driver, 1); len(sent) != 1 || sent[0].Text != lanes.Nudge {
			t.Fatalf("sent %+v, want the nudge the refusal left the lane", sent)
		}
		if state := r.row().State; state != workorders.Running {
			t.Errorf("a lane nudged after a restart was left %s", state)
		}
	})
}

// A refusal is of the turn's end it was refused at: a later turn that ends on background work rests on it, and
// that rest ends as a rest on background work does, with background_unreported, never with the old refusal's reason.
func TestAnOldRefusalDoesNotOutrankALaterRestOnBackgroundWork(t *testing.T) {
	t.Parallel()
	resumed := newGate()
	r := refusedLane(t, "", func(*laneRig) [][]fake.DriverStep {
		return [][]fake.DriverStep{{{Bound: true}, completed(0.01), resumed.step(), completed(0.02)}}
	})
	r.gates(resumed)
	at := time.Now().UnixMilli()
	r.promptAt(t, at)
	r.stopAt(t, at+1, 1)
	resumed.free()
	r.turnDecided(t, 0.02)
	r.restsOnBackground()
	r.driver.FailSend(ports.ErrAwaitingAnswer)
	r.endsPastTheBound(t, workorders.Blocked, workorders.ReasonBackgroundUnreported)
}

// The rest on background work has had its whole live wait when its nudge is due, so a nudge refused then ends the
// lane blocked at once, as it always did.
func TestARefusedBackgroundNudgeEndsTheLaneBlocked(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	stopped := newGate()
	r.gates(stopped)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, stopped.step(), completed(0.01)}))
	r.atGate(stopped)
	r.stopAt(t, time.Now().UnixMilli(), 1)
	stopped.free()
	r.restsOnBackground()
	r.driver.FailSend(ports.ErrAwaitingAnswer)
	r.clock.Advance(lanes.MaxLiveWait(nil))
	r.awaitOrderState(t, workorders.Blocked)
	if change := r.outcome(); change.Reason != workorders.ReasonBackgroundUnreported {
		t.Errorf("outcome %+v, want %s", change, workorders.ReasonBackgroundUnreported)
	}
}

// A refused nudge reached nobody, so it is not spent: the end of the next turn the session takes is nudged, and
// never ended with no nudge. The nudge that does go is sent once, and a worker that takes the lane over sends no other.
func TestARefusedNudgeIsNotSpentAndNotSentTwice(t *testing.T) {
	t.Parallel()
	resumed := newGate()
	r := refusedLane(t, "", func(*laneRig) [][]fake.DriverStep {
		return [][]fake.DriverStep{
			{{Bound: true}, completed(0.01), resumed.step(), completed(0.02)},
			{{Hold: true}},
		}
	})
	r.gates(resumed)
	r.promptAt(t, time.Now().UnixMilli())
	resumed.free()
	if sent := r.awaitSentOn(t, r.driver, 1); len(sent) != 1 || sent[0].Text != lanes.Nudge {
		t.Fatalf("sent %+v, want the nudge at the next turn's end", sent)
	}
	if state := r.row().State; state != workorders.Running {
		t.Fatalf("a lane whose refused nudge went at the next turn's end was left %s", state)
	}
	driver := r.takeOver(t, script())
	r.eventually("the session adopted", func(ledger.LaneView) bool { return len(driver.Adopted()) == 1 })
	r.runsOn(t, lanes.MaxLiveWait(nil)-time.Minute)
	if sent := driver.Sent(); len(sent) != 0 {
		t.Errorf("sent %+v again after a restart", sent)
	}
}
