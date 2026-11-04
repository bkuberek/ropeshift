package worker_test

import (
	"slices"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/ledger"
)

const stallProfile = profileAt + "[lifecycle]\nstall_after = \"20m\"\napproval_wait = \"5m\"\nhandoff_timeout = \"30ms\"\n"

// reads holds a lane to what a quiet stretch must show: stalled, or waiting on a tool.
func reads(t *testing.T, view ledger.LaneView, stalled bool) {
	t.Helper()
	has := func(condition lanes.Condition) bool { return slices.Contains(view.Conditions, string(condition)) }
	if has(lanes.Stalled) != stalled || has(lanes.WaitingExternal) == stalled {
		t.Errorf("%s with %v, want stalled %v", view.ActualState, view.Conditions, stalled)
	}
}

// A call that ends with no `PostToolUse` leaves nothing at work, or a lane that stopped moving reads
// as one waiting on a tool for the rest of its turn.
func TestACallThatEndedHidesNoStall(t *testing.T) {
	tests := []struct {
		name   string
		events []lanes.HookEvent
	}{
		{"denied by a hook", []lanes.HookEvent{{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", Detail: lanes.DetailDenied}}},
		{"failed", []lanes.HookEvent{
			{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash"},
			{Event: "PostToolUseFailure", Subject: "toolu_1", Tool: "Bash"},
		}},
		{"denied by auto mode", []lanes.HookEvent{
			{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash"},
			{Event: "PermissionDenied", Subject: "toolu_1", Tool: "Bash"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := laneRigWith(t, stallProfile)
			g := newGate()
			r.finishLater(g)
			r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
			r.atGate(g)
			for _, event := range tt.events {
				r.hook(t, event)
			}
			r.clock.Advance(21 * time.Minute)
			reads(t, r.read(), true)
		})
	}
}

// A refused call is still a session that went on, which is all a compaction's failure needs to show.
func TestARefusedCallIsStillActivity(t *testing.T) {
	r := laneRigWith(t, stallProfile)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.clock.Advance(19 * time.Minute)
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", Detail: lanes.DetailDenied})
	r.clock.Advance(2 * time.Minute)
	if view := r.read(); slices.Contains(view.Conditions, string(lanes.Stalled)) {
		t.Errorf("a lane that just called a tool reads %v", view.Conditions)
	}
}

// Neither hook names the other call, so ending one must leave one still at work alone.
func TestAnEndedCallLeavesTheOtherAtWork(t *testing.T) {
	r := laneRigWith(t, stallProfile)
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash"})
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_2", Tool: "Bash", Detail: lanes.DetailDenied})
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_3", Tool: "Bash"})
	r.hook(t, lanes.HookEvent{Event: "PostToolUseFailure", Subject: "toolu_3", Tool: "Bash"})
	r.clock.Advance(21 * time.Minute)
	reads(t, r.read(), false)
}

// A cancelled call and a deferred one report no end at all, and a turn's end closes what is left of
// it, or the next turn inherits a tool nothing runs.
func TestATurnsEndClosesItsCalls(t *testing.T) {
	first, second := newGate(), newGate()
	r := laneRigWith(t, stallProfile)
	r.finishLater(first)
	r.finishLater(second)
	r.driver.Play(script(
		[]fake.DriverStep{{Bound: true}, first.step(), completed(0.01)},
		[]fake.DriverStep{second.step()},
	))
	r.atGate(first)
	r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash"})
	first.release <- struct{}{}
	select {
	case <-second.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached the next turn")
	}
	r.eventually("the next turn", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	r.clock.Advance(21 * time.Minute)
	reads(t, r.read(), true)
}

// A member's refusal reaches a call a `PermissionRequest` held, which names no call, so only its
// digest ties it to the start it ends. It has to end that call alone.
func TestARefusedRequestEndsItsCallAlone(t *testing.T) {
	tests := []struct {
		name    string
		other   string
		stalled bool
	}{
		{"with nothing else at work", "", true},
		{"with another call at work", changedCall, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := aLaneThatWaitsUnder(t, stallProfile)
			r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", Digest: digestOf(t, gatedCall)})
			if tt.other != "" {
				r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_2", Tool: "Bash", Digest: digestOf(t, tt.other)})
			}
			r.look(t, gatedCall, "call-1", false)
			r.memberSays(t, r.onlyRequest(t), false)
			if got := decisionIn(t, r.look(t, gatedCall, "call-1", true)); got != lanes.ApprovalDeny {
				t.Fatalf("the call reads %s", got)
			}
			r.eventually("a turn again", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
			r.clock.Advance(21 * time.Minute)
			reads(t, r.read(), tt.stalled)
		})
	}
}

// adoptedRead reads a lane a new process took over with its session alive, from what the journal
// holds of the hooks.
func adoptedRead(t *testing.T, r *laneRig, g *gate) ledger.LaneView {
	t.Helper()
	g.release <- struct{}{}
	// What an adopted session admits counts as progress, and it can land after the clock moved, so this
	// one says nothing.
	plays := script([]fake.DriverStep{{Hold: true}})
	plays.Silent = true
	driver := r.takeOver(t, plays)
	// The hooks are read before the session is adopted, and reading one counts as progress, so a clock
	// that moved ahead of that would measure nothing.
	deadline := time.Now().Add(20 * time.Second)
	for len(driver.Adopted()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the session was never adopted")
		}
		time.Sleep(time.Millisecond)
	}
	r.clock.Advance(21 * time.Minute)
	return r.eventually("a condition", func(view ledger.LaneView) bool { return len(view.Conditions) != 0 })
}

// aTurnNoEndReaches is a lane in a turn that says no more, so what a new process reads comes from
// the journal alone.
func aTurnNoEndReaches(t *testing.T) (*laneRig, *gate) {
	t.Helper()
	g := newGate()
	r := laneRigWith(t, stallProfile)
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, g.step()}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	return r, g
}

// A new worker reads what the hooks said from the journal, so a refusal has to survive there, or a
// restart brings the same tool back to life.
func TestARestartKeepsACallEnded(t *testing.T) {
	t.Run("denied by a hook", func(t *testing.T) {
		r, g := aTurnNoEndReaches(t)
		r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", Detail: lanes.DetailDenied, AtMs: 1})
		reads(t, adoptedRead(t, r, g), true)
	})
	t.Run("refused by a member", func(t *testing.T) {
		r, g := aTurnNoEndReaches(t)
		r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", Digest: digestOf(t, gatedCall), AtMs: 1})
		r.look(t, gatedCall, "call-1", false)
		r.memberSays(t, r.onlyRequest(t), false)
		decisionIn(t, r.look(t, gatedCall, "call-1", true))
		reads(t, adoptedRead(t, r, g), true)
	})
	t.Run("ended in the millisecond it started", func(t *testing.T) {
		r, g := aTurnNoEndReaches(t)
		r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", AtMs: 1})
		r.hook(t, lanes.HookEvent{Event: "PostToolUse", Subject: "toolu_1", Tool: "Bash", AtMs: 1})
		reads(t, adoptedRead(t, r, g), true)
	})
	t.Run("still at work", func(t *testing.T) {
		r, g := aTurnNoEndReaches(t)
		r.hook(t, lanes.HookEvent{Event: "PreToolUse", Subject: "toolu_1", Tool: "Bash", AtMs: 1})
		reads(t, adoptedRead(t, r, g), false)
	})
}
