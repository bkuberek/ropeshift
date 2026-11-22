package claudecode

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// replayedPane is a session in an open pane, as a worker that restarted holds it, whose reads
// stopped at floor.
func replayedPane(t *testing.T, floor int64) *paneSession {
	t.Helper()
	terminal := fake.NewTerminal()
	spec := policySpec(t)
	spec.RunMode = ports.RunModeInteractive
	name := paneName(spec)
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, Options{Terminal: terminal}).newPaneSession(spec, name, ports.SessionHandle{ID: sessionHandleID(spec)}, lanes.CostMeter{})
	s.floor = floor
	return s
}

func hookAt(event string, atMs int64, detail string) lanes.HookRecord {
	return lanes.HookRecord{HookEvent: lanes.HookEvent{Lane: "lane-1", Generation: 1, Event: event, Subject: "s", Detail: detail}, FirstMs: atMs, LastMs: atMs, Count: 1}
}

// typesFreely says the pane takes a key, which it refuses to a session that waits for an answer.
func typesFreely(t *testing.T, s *paneSession) {
	t.Helper()
	_, err := s.typeInput(t.Context(), ports.Input{Text: "a nudge"})
	if errors.Is(err, ports.ErrAwaitingAnswer) {
		t.Fatal("the pane refused input as if an answer were waited on")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func refusesInput(t *testing.T, s *paneSession) {
	t.Helper()
	if _, err := s.typeInput(t.Context(), ports.Input{Text: "a nudge"}); !errors.Is(err, ports.ErrAwaitingAnswer) {
		t.Fatalf("the pane took input while an answer was waited on: %v", err)
	}
}

// A Stop the worker already read ends the wait a Notification began before it, however late it arrives.
func TestAStopAlreadyReadEndsTheWaitBeforeIt(t *testing.T) {
	t.Parallel()
	s := replayedPane(t, 120)
	s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: notificationNeedsInput, AtMs: 100})
	refusesInput(t, s)
	s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: 110})
	typesFreely(t, s)
}

// A repeat of an older Stop says nothing of a wait that began after it.
func TestAStopAlreadyReadLeavesALaterWait(t *testing.T) {
	t.Parallel()
	s := replayedPane(t, 120)
	s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: notificationNeedsInput, AtMs: 130})
	s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: 110})
	refusesInput(t, s)
}

// A journal merges each hook into one record with no call to name, so what a replay reads of waits
// cannot say which stand. It rebuilds none of them, and the open approvals are the ones that do.
func TestAReplayWaitsOnlyOnTheOpenApprovals(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		hooks []lanes.HookRecord
	}{
		{"a wait the turn's end closed", []lanes.HookRecord{
			hookAt(eventPermissionRequest, 100, ""), hookAt(hookNotification, 105, notificationNeedsInput), hookAt(hookStop, 110, ""),
		}},
		{"a wait nothing after it closed", []lanes.HookRecord{
			hookAt(eventPermissionRequest, 100, ""), hookAt(hookNotification, 105, notificationNeedsInput),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s := replayedPane(t, 110)
			s.replay(ports.AdoptFrom{Hooks: test.hooks}, 1)
			typesFreely(t, s)
		})
	}
}

func TestAReplayWaitsOnAnOpenApprovalUntilItsCallEnds(t *testing.T) {
	t.Parallel()
	s := replayedPane(t, 110)
	s.replay(ports.AdoptFrom{Hooks: []lanes.HookRecord{hookAt(hookStop, 110, "")}, Approvals: []string{"seen-1"}}, 1)
	refusesInput(t, s)
	s.takeHook(lanes.HookEvent{Event: hookPostToolUse, Digest: "seen-1", AtMs: 120})
	typesFreely(t, s)
}

// A PermissionRequest no turn's end followed may have left a dialog open, and its request may not be raised
// yet, so no key reaches the pane until that turn ends.
func TestAReplayHoldsTheKeysFromADialogWithNoRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		hooks []lanes.HookRecord
	}{
		{"a dialog in the turn under way", []lanes.HookRecord{hookAt(eventPermissionRequest, 120, "")}},
		{"a dialog after an end not yet read", []lanes.HookRecord{hookAt(hookStop, 115, ""), hookAt(eventPermissionRequest, 120, "")}},
		{"a dialog beside a call that ended", []lanes.HookRecord{hookAt(eventPermissionRequest, 120, ""), hookAt(hookPostToolUse, 125, "")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s := replayedPane(t, 110)
			s.replay(ports.AdoptFrom{Hooks: test.hooks}, 1)
			refusesInput(t, s)
			s.takeHook(lanes.HookEvent{Event: hookPostToolUse, AtMs: 128})
			refusesInput(t, s)
			s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: 130})
			typesFreely(t, s)
		})
	}
}

// What a worker could not read may be an open dialog.
func TestAReplayHoldsTheKeysFromACallItCouldNotRead(t *testing.T) {
	t.Parallel()
	s := replayedPane(t, 0)
	s.replay(ports.AdoptFrom{Approvals: []string{""}}, 1)
	refusesInput(t, s)
}

// A turn that ended asks nothing, whatever request the worker still holds of it, so its end draws a nudge
// the pane takes and the lane does not end blocked.
func TestAReplayRestoresNoApprovalOfATurnThatEnded(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		floor int64
		hooks []lanes.HookRecord
	}{
		{"a Stop not yet read", 50, []lanes.HookRecord{hookAt(eventPermissionRequest, 100, ""), hookAt(hookStop, 200, "")}},
		{"a Stop already read", 200, []lanes.HookRecord{hookAt(eventPermissionRequest, 100, ""), hookAt(hookStop, 200, "")}},
		{"a StopFailure not yet read", 50, []lanes.HookRecord{hookAt(eventPermissionRequest, 100, ""), hookAt(hookStopFailure, 200, "server_error")}},
		{"a clear", 50, []lanes.HookRecord{hookAt(eventPermissionRequest, 100, ""), hookAt(hookSessionStart, 200, lanes.SourceClear)}},
	}
	for _, test := range tests {
		for _, open := range [][]string{nil, {"seen-1"}, {""}} {
			t.Run(test.name+"/"+strings.Join(append([]string{"open"}, open...), "-"), func(t *testing.T) {
				t.Parallel()
				s := replayedPane(t, test.floor)
				s.replay(ports.AdoptFrom{Hooks: test.hooks, Approvals: open}, 1)
				typesFreely(t, s)
			})
		}
	}
}

// A Notification that asks for an answer is all that says a question stands, so one that nothing after it
// closed holds the keys across a restart.
func TestAReplayHoldsTheKeysFromAQuestionNothingClosed(t *testing.T) {
	t.Parallel()
	asked := hookAt(hookNotification, 120, notificationNeedsInput)
	tests := []struct {
		name  string
		floor int64
		hooks []lanes.HookRecord
		holds bool
	}{
		{"a question after the read Stop", 110, []lanes.HookRecord{hookAt(hookStop, 110, ""), asked}, true},
		{"a question after an unread Stop", 110, []lanes.HookRecord{hookAt(hookStop, 115, ""), asked}, true},
		{"a question after the prompt", 110, []lanes.HookRecord{hookAt(hookUserPromptSubmit, 115, ""), asked}, true},
		{"a question after a clear", 110, []lanes.HookRecord{hookAt(hookSessionStart, 115, lanes.SourceClear), asked}, true},
		{"a question a Stop closed", 110, []lanes.HookRecord{asked, hookAt(hookStop, 130, "")}, false},
		{"a question a StopFailure closed", 110, []lanes.HookRecord{asked, hookAt(hookStopFailure, 130, "server_error")}, false},
		{"a question before the floor", 125, []lanes.HookRecord{asked}, false},
		{"a question a prompt answered", 110, []lanes.HookRecord{asked, hookAt(hookUserPromptSubmit, 130, "")}, false},
		{"a question a clear closed", 110, []lanes.HookRecord{asked, hookAt(hookSessionStart, 130, lanes.SourceClear)}, false},
		{"a Notification that asks nothing", 110, []lanes.HookRecord{hookAt(hookNotification, 120, "idle_prompt")}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s := replayedPane(t, test.floor)
			s.replay(ports.AdoptFrom{Hooks: test.hooks}, 1)
			if !test.holds {
				typesFreely(t, s)
				return
			}
			refusesInput(t, s)
			s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: 140})
			typesFreely(t, s)
		})
	}
}

// An open approval names one call, and a later request can be a second dialog with no approval yet, so the
// first call's end frees no key.
func TestAReplayHoldsTheKeysFromASecondDialogBesideAnApproval(t *testing.T) {
	t.Parallel()
	s := replayedPane(t, 110)
	asked := hookAt(eventPermissionRequest, 120, "")
	asked.LastMs, asked.Count = 125, 2
	s.replay(ports.AdoptFrom{Hooks: []lanes.HookRecord{hookAt(hookStop, 110, ""), asked}, Approvals: []string{"seen-1"}}, 1)
	refusesInput(t, s)
	s.takeHook(lanes.HookEvent{Event: hookPostToolUse, Digest: "seen-1", AtMs: 130})
	refusesInput(t, s)
	s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: 140})
	typesFreely(t, s)
}
