package claudecode

import (
	"errors"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// dialogPane is a session in an open pane, at rest after a turn that stopped at 110, and the terminal that holds it.
func dialogPane(t *testing.T) (*paneSession, *fake.Terminal) {
	t.Helper()
	terminal := &takingTerminal{}
	return takingPane(t, terminal), terminal.Terminal
}

// noKeyReached fails if any key or text reached the pane.
func noKeyReached(t *testing.T, s *paneSession, terminal *fake.Terminal) {
	t.Helper()
	if keys, typed := terminal.KeysOf(s.name), terminal.TypedInto(s.name); len(keys) != 0 || len(typed) != 0 {
		t.Fatalf("a worker's keys reached a pane that may show a dialog: keys %q, text %q", keys, typed)
	}
}

// Every Notification that tells of a dialog holds every key a worker sends, a compaction's too, because a key answers
// the dialog.
func TestEveryDialogANotificationTellsOfHoldsTheKeys(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{lanes.NotificationPermission, lanes.NotificationElicitation, lanes.NotificationElicitationURL, lanes.NotificationNeedsInput} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			s, terminal := dialogPane(t)
			s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: kind, AtMs: 120})
			refusesInput(t, s)
			if err := s.compact(t.Context()); !errors.Is(err, ports.ErrAwaitingAnswer) {
				t.Fatalf("a compaction went to a pane that shows a dialog: %v", err)
			}
			noKeyReached(t, s, terminal)
		})
	}
}

// A Notification that tells of no dialog, such as one that the session has been idle, leaves a dialog standing.
func TestANotificationOfNoDialogLeavesADialogStanding(t *testing.T) {
	t.Parallel()
	s, terminal := dialogPane(t)
	s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationElicitation, AtMs: 120})
	s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: "idle_prompt", AtMs: 130})
	refusesInput(t, s)
	noKeyReached(t, s, terminal)
}

// An MCP server's dialog closes when its response goes back to the server, or its browser step completes.
func TestAnElicitationsCloseFreesTheKeys(t *testing.T) {
	t.Parallel()
	for _, closed := range []string{lanes.NotificationElicitationResponse, lanes.NotificationElicitationComplete} {
		t.Run(closed, func(t *testing.T) {
			t.Parallel()
			s, _ := dialogPane(t)
			s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationElicitation, AtMs: 120})
			s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: closed, AtMs: 115})
			refusesInput(t, s)
			s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: closed, AtMs: 130})
			typesFreely(t, s)
		})
	}
}

// A spool can deliver a dialog's hook after the end, or the prompt, that came after the dialog. That dialog is gone, so
// its hook holds no key.
func TestADialogsHookAfterTheNewerEndHoldsNoKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		after lanes.HookEvent
		late  lanes.HookEvent
	}{
		{"a Notification after its turn's Stop", lanes.HookEvent{Event: hookStop, AtMs: 130}, lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationNeedsInput, AtMs: 120}},
		{"a Notification after its turn's StopFailure", lanes.HookEvent{Event: hookStopFailure, Detail: "server_error", AtMs: 130}, lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationPermission, AtMs: 120}},
		{"a Notification after the prompt that answered it", lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: 130}, lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationNeedsInput, AtMs: 120}},
		{"a PermissionRequest after its turn's Stop", lanes.HookEvent{Event: hookStop, AtMs: 130}, lanes.HookEvent{Event: eventPermissionRequest, Digest: "seen-1", AtMs: 120}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			s, _ := dialogPane(t)
			s.takeHook(test.after)
			if test.after.Event == hookUserPromptSubmit {
				s.takeHook(lanes.HookEvent{Event: hookStop, AtMs: 140})
			}
			s.takeHook(test.late)
			typesFreely(t, s)
		})
	}
}

// A dialog no hook announces, such as a sandboxed command's network request, is told only by a Notification some
// seconds after it opens, so no key reaches a pane in the first seconds of a turn.
func TestTheStartOfATurnHoldsTheKeys(t *testing.T) {
	t.Parallel()
	s, terminal := dialogPane(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: now.UnixMilli()})
	refusesInput(t, s)
	noKeyReached(t, s, terminal)
	s.now = func() time.Time { return now.Add(dialogNotice) }
	typesFreely(t, s)
}

// A prompt's hook can reach a pane twice, from an adoption that kept it and from the session it adopted, and it began
// one turn.
func TestARepeatedPromptIsTakenOnce(t *testing.T) {
	t.Parallel()
	s := replayedPane(t, 0)
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: 120})
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: 120})
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: 115})
	s.mu.Lock()
	defer s.mu.Unlock()
	admitted := 0
	for _, event := range s.queue {
		if event.Kind == ports.EventInputAdmitted {
			admitted++
		}
	}
	if admitted != 1 {
		t.Errorf("one prompt was admitted %d times", admitted)
	}
}

// A prompt answers a dialog older than it, and a late prompt's hook leaves a newer dialog standing.
func TestALatePromptLeavesANewerDialog(t *testing.T) {
	t.Parallel()
	s, terminal := dialogPane(t)
	s.takeHook(lanes.HookEvent{Event: hookNotification, Detail: lanes.NotificationPermission, AtMs: 130})
	s.takeHook(lanes.HookEvent{Event: hookUserPromptSubmit, AtMs: 120})
	refusesInput(t, s)
	noKeyReached(t, s, terminal)
}
