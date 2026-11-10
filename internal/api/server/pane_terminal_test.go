package server_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api/server"
	"ropeshift.com/ropeshift/internal/ports"
)

func generationPane(lane string, generation int64) string {
	return fmt.Sprintf("%s-g%d", lane, generation)
}

// openPane opens a pane on a fake terminal that shows a screen.
func openPane(t *testing.T, terminal *fake.Terminal, name, screen string) {
	t.Helper()
	terminal.Play(fake.PaneScript{Screen: screen})
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name}); err != nil {
		t.Fatal(err)
	}
}

// A lane's generation has a pane of its own, under the name the driver gave it, and the web pane reads
// that one and no other.
func TestTerminalPanesReadTheGenerationsOwnPane(t *testing.T) {
	terminal := fake.NewTerminal()
	openPane(t, terminal, "lane-1-g1", "generation one")
	openPane(t, terminal, "lane-1-g2", "generation two")
	panes := server.TerminalPanes{Terminal: terminal, Name: generationPane}
	got, err := panes.Screen(t.Context(), "lane-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got != "generation two" {
		t.Errorf("screen %q", got)
	}
	if _, err := panes.Screen(t.Context(), "lane-1", 3); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v, want not found", err)
	}
	if err := panes.Send(t.Context(), "lane-1", 3, server.PaneInput{Keys: []string{"Enter"}}); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v, want not found", err)
	}
	if terminal.Captures() != 0 {
		t.Errorf("the web pane's read counted as the start check's capture")
	}
}

// typing is a fake terminal that keeps the order of what it was sent, and when.
type typing struct {
	*fake.Terminal
	mu  sync.Mutex
	log []string
	at  []time.Time
}

func (t *typing) Type(ctx context.Context, name, text string) error {
	t.mu.Lock()
	t.log, t.at = append(t.log, "type "+text), append(t.at, time.Now())
	t.mu.Unlock()
	return t.Terminal.Type(ctx, name, text)
}

func (t *typing) Keys(ctx context.Context, name string, keys ...string) error {
	t.mu.Lock()
	t.log, t.at = append(t.log, fmt.Sprint("keys ", keys)), append(t.at, time.Now())
	t.mu.Unlock()
	return t.Terminal.Keys(ctx, name, keys...)
}

// Text goes in first and the keys after it, with a rest between, because an agent CLI that sees text
// and Enter arrive together can take the Enter as part of a paste and not as a submit.
func TestTerminalPanesTypeTextAndThenPressItsKeys(t *testing.T) {
	terminal := &typing{Terminal: fake.NewTerminal()}
	openPane(t, terminal.Terminal, "lane-1-g1", "a screen")
	settle := 100 * time.Millisecond
	panes := server.TerminalPanes{Terminal: terminal, Name: generationPane, Settle: settle}
	if err := panes.Send(t.Context(), "lane-1", 1, server.PaneInput{Text: "A planning tool", Keys: []string{"Enter"}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"type A planning tool", "keys [Enter]"}; !slices.Equal(terminal.log, want) {
		t.Fatalf("sent %v, want %v", terminal.log, want)
	}
	if gap := terminal.at[1].Sub(terminal.at[0]); gap < settle {
		t.Errorf("Enter followed the text after %v, want at least %v", gap, settle)
	}
	terminal.log, terminal.at = nil, nil
	start := time.Now()
	if err := panes.Send(t.Context(), "lane-1", 1, server.PaneInput{Keys: []string{"C-c"}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"keys [C-c]"}; !slices.Equal(terminal.log, want) {
		t.Errorf("sent %v, want %v", terminal.log, want)
	}
	if waited := time.Since(start); waited >= settle {
		t.Errorf("a key alone waited %v", waited)
	}
}

// A person's terminal attached with `lane attach` and the web pane see the same pane at once: the web
// pane reads and types while the client stays attached, and it never detaches it.
func TestTerminalPanesWorkWhileATerminalIsAttached(t *testing.T) {
	terminal := fake.NewTerminal()
	openPane(t, terminal, "lane-1-g1", "the conversation")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	attached := make(chan error, 1)
	go func() { attached <- terminal.Attach(ctx, "lane-1-g1", ports.Console{}) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		clients, err := terminal.Clients(t.Context(), "lane-1-g1")
		if err != nil {
			t.Fatal(err)
		}
		if clients == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the client never attached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	panes := server.TerminalPanes{Terminal: terminal, Name: generationPane, Settle: time.Millisecond}
	if got, err := panes.Screen(t.Context(), "lane-1", 1); err != nil || got != "the conversation" {
		t.Fatalf("screen %q, err %v", got, err)
	}
	if err := panes.Send(t.Context(), "lane-1", 1, server.PaneInput{Text: "yes", Keys: []string{"Enter"}}); err != nil {
		t.Fatal(err)
	}
	if clients, err := terminal.Clients(t.Context(), "lane-1-g1"); err != nil || clients != 1 {
		t.Errorf("%d clients after the web pane typed, err %v; the person's terminal must stay attached", clients, err)
	}
	if typed := terminal.TypedInto("lane-1-g1"); !slices.Equal(typed, []string{"yes"}) {
		t.Errorf("typed %v", typed)
	}
	select {
	case err := <-attached:
		t.Fatalf("the client ended: %v", err)
	default:
	}
}
