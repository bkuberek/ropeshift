package claudecode

import (
	"errors"
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
