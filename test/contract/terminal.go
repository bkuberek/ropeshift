package contract

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// TerminalRig is one terminal, and how a test sees what it holds. A scripted pane and a
// real process answer the same questions, so the fake cannot drift from tmux.
type TerminalRig struct {
	Terminal ports.Terminal
	// Spec is a pane that starts on the trust dialog and takes keys.
	Spec func(name string) ports.PaneSpec
	// Exits is a pane whose process ends by itself with code 7.
	Exits func(name string) ports.PaneSpec
	// Waits holds a test until a pane shows a piece of text.
	Waits func(t *testing.T, name, text string)
	// Attaches puts a person's terminal on a pane, the way this terminal does it, and returns once
	// that client is attached. Detach presses the detach keys and returns once the client has ended.
	Attaches func(t *testing.T, name string) (detach func())
}

// RunTerminal runs one contract on one terminal.
func RunTerminal(t *testing.T, fresh func(*testing.T) TerminalRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, TerminalRig)
	}{
		{"a pane shows what its process drew", checkPaneShows},
		{"a key reaches the process", checkKeyMovesFocus},
		{"typed text is not submitted", checkTypedText},
		{"a name is held once", checkNameHeldOnce},
		{"a process that ends leaves a dead pane with its code", checkDeadPane},
		{"a signal reaches the pane's process", checkPaneSignal},
		{"a closed pane is not found, and a second close is fine", checkClose},
		{"a pane that never was is not found", checkNoPane},
		{"a pane counts the clients attached to it", checkClients},
		{"a detached client leaves the pane running", checkDetachLeavesThePane},
		{"a closed pane ends the client attached to it", checkCloseEndsTheClient},
		{"a pane that never was has no clients to count or attach", checkNoClients},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			check.run(t, ctx, fresh(t))
		})
	}
}

func openPane(t *testing.T, ctx context.Context, rig TerminalRig, spec ports.PaneSpec) ports.Pane {
	t.Helper()
	pane, err := rig.Terminal.Open(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if pane.PID == 0 || pane.Name != spec.Name {
		t.Fatalf("pane %+v", pane)
	}
	t.Cleanup(func() { _ = rig.Terminal.Close(context.Background(), spec.Name) })
	return pane
}

func checkPaneShows(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("show"))
	rig.Waits(t, "show", "No, exit")
	text, err := rig.Terminal.Capture(ctx, "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Yes, I trust this folder") {
		t.Errorf("capture %q", text)
	}
}

func checkKeyMovesFocus(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("keys"))
	rig.Waits(t, "keys", "❯ No, exit")
	if err := rig.Terminal.Keys(ctx, "keys", "Down"); err != nil {
		t.Fatal(err)
	}
	rig.Waits(t, "keys", "❯ Yes, I trust this folder")
}

func checkTypedText(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("typed"))
	rig.Waits(t, "typed", "❯ No, exit")
	if err := rig.Terminal.Keys(ctx, "typed", "Down"); err != nil {
		t.Fatal(err)
	}
	rig.Waits(t, "typed", "❯ Yes, I trust this folder")
	if err := rig.Terminal.Type(ctx, "typed", "/compact"); err != nil {
		t.Fatal(err)
	}
	text, err := rig.Terminal.Capture(ctx, "typed")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Yes, I trust this folder") {
		t.Errorf("typed text answered a dialog: %q", text)
	}
}

func checkNameHeldOnce(t *testing.T, ctx context.Context, rig TerminalRig) {
	spec := rig.Spec("held")
	first := openPane(t, ctx, rig, spec)
	if _, err := rig.Terminal.Open(ctx, spec); err == nil {
		t.Fatal("a second pane took a held name")
	}
	again, err := rig.Terminal.Pane(ctx, "held")
	if err != nil {
		t.Fatal(err)
	}
	if again.PID != first.PID {
		t.Errorf("the pane changed from %d to %d", first.PID, again.PID)
	}
}

func waitDead(t *testing.T, ctx context.Context, rig TerminalRig, name string) ports.Pane {
	t.Helper()
	for ctx.Err() == nil {
		pane, err := rig.Terminal.Pane(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if pane.Dead {
			return pane
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the pane did not die")
	return ports.Pane{}
}

func checkDeadPane(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Exits("gone"))
	if pane := waitDead(t, ctx, rig, "gone"); pane.Exit.Code != 7 || pane.Exit.Signal != "" {
		t.Errorf("exit %+v", pane.Exit)
	}
}

func checkPaneSignal(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("sig"))
	rig.Waits(t, "sig", "No, exit")
	if err := rig.Terminal.Signal(ctx, "sig", ports.SignalKill); err != nil {
		t.Fatal(err)
	}
	if pane := waitDead(t, ctx, rig, "sig"); pane.Exit.Signal != ports.SignalKill {
		t.Errorf("exit %+v", pane.Exit)
	}
	if err := rig.Terminal.Signal(ctx, "sig", ports.SignalKill); err != nil {
		t.Errorf("a signal to a dead pane: %v", err)
	}
}

func checkClose(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("close"))
	if err := rig.Terminal.Close(ctx, "close"); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.Terminal.Pane(ctx, "close"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a closed pane: %v", err)
	}
	if err := rig.Terminal.Close(ctx, "close"); err != nil {
		t.Errorf("a second close: %v", err)
	}
}

func checkNoPane(t *testing.T, ctx context.Context, rig TerminalRig) {
	if _, err := rig.Terminal.Pane(ctx, "nothing"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a read of nothing: %v", err)
	}
	if err := rig.Terminal.Keys(ctx, "nothing", "Down"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("keys to nothing: %v", err)
	}
}

func clientsOf(t *testing.T, ctx context.Context, rig TerminalRig, name string) int {
	t.Helper()
	count, err := rig.Terminal.Clients(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func checkClients(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("seen"))
	if count := clientsOf(t, ctx, rig, "seen"); count != 0 {
		t.Fatalf("%d clients on a pane nobody attached to", count)
	}
	detach := rig.Attaches(t, "seen")
	if count := clientsOf(t, ctx, rig, "seen"); count != 1 {
		t.Errorf("%d clients on an attached pane, want 1", count)
	}
	detach()
	if count := clientsOf(t, ctx, rig, "seen"); count != 0 {
		t.Errorf("%d clients after the detach, want none", count)
	}
}

func checkDetachLeavesThePane(t *testing.T, ctx context.Context, rig TerminalRig) {
	opened := openPane(t, ctx, rig, rig.Spec("stays"))
	rig.Waits(t, "stays", "No, exit")
	rig.Attaches(t, "stays")()
	pane, err := rig.Terminal.Pane(ctx, "stays")
	if err != nil {
		t.Fatalf("the pane is gone after a detach: %v", err)
	}
	if pane.Dead || pane.PID != opened.PID {
		t.Errorf("pane %+v after the detach, opened as %+v", pane, opened)
	}
	// The same pane takes a second person, who sees what the first left.
	rig.Attaches(t, "stays")()
	if count := clientsOf(t, ctx, rig, "stays"); count != 0 {
		t.Errorf("%d clients after the second detach", count)
	}
}

func checkCloseEndsTheClient(t *testing.T, ctx context.Context, rig TerminalRig) {
	openPane(t, ctx, rig, rig.Spec("closed"))
	detach := rig.Attaches(t, "closed")
	if err := rig.Terminal.Close(ctx, "closed"); err != nil {
		t.Fatal(err)
	}
	detach()
	if _, err := rig.Terminal.Clients(ctx, "closed"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("clients of a closed pane: %v, want not found", err)
	}
}

func checkNoClients(t *testing.T, ctx context.Context, rig TerminalRig) {
	if _, err := rig.Terminal.Clients(ctx, "never"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("clients of a pane that never was: %v, want not found", err)
	}
}
