package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/ports"
)

func pane(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "panes", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestReadScreen(t *testing.T) {
	tests := []struct {
		capture string
		want    screenKind
		focus   focusLine
	}{
		{capture: "trust-focus-cancel", want: screenTrust, focus: focusAbove},
		{capture: "trust-focus-accept", want: screenTrust, focus: focusOnAccept},
		{capture: "trust-other-cancel-label", want: screenTrust, focus: focusAbove},
		{capture: "ready", want: screenReady},
		{capture: "ready-placeholder", want: screenReady},
		{capture: "ready-boxed", want: screenReady},
		{capture: "resume", want: screenReady},
		// Each of these holds a focused option or a prompt-like line, and none may read
		// as a screen the check can act on.
		{capture: "mode-acceptance", want: screenUnknown},
		{capture: "settings-trust", want: screenUnknown},
		{capture: "resume-dialog", want: screenUnknown},
		{capture: "conversation-mid-turn", want: screenUnknown},
		{capture: "unknown", want: screenUnknown},
		{capture: "empty", want: screenUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.capture, func(t *testing.T) {
			got := readScreen(pane(t, tt.capture))
			if got.kind != tt.want || got.focus != tt.focus {
				t.Errorf("read %+v, want %s with focus %s", got, tt.want, tt.focus)
			}
		})
	}
}

// A dialog with the accept option above the focus takes Up, so the direction comes from
// the screen and not from a fixed key.
func TestTrustFocusAboveAccept(t *testing.T) {
	flipped := strings.Join([]string{" Accessing workspace:", "", "   Yes, I trust this folder", " ❯ No, exit", ""}, "\n")
	if got := readScreen(flipped); got.kind != screenTrust || got.focus != focusBelow {
		t.Errorf("read %+v", got)
	}
}

func TestStartCheck(t *testing.T) {
	trust, accepted, ready := pane(t, "trust-focus-cancel"), pane(t, "trust-focus-accept"), pane(t, "ready")
	moves := map[string]map[string]string{
		trust:    {"Down": accepted},
		accepted: {"Enter": ready, "Up": trust},
	}
	tests := []struct {
		name   string
		script fake.PaneScript
		mode   checkMode
		// keys is every key the pane must take, in order.
		keys []string
		want error
	}{
		{name: "a ready screen takes no key", script: fake.PaneScript{Screen: ready}, mode: checkFresh},
		{name: "a resume takes no key", script: fake.PaneScript{Screen: pane(t, "resume")}, mode: checkResume},
		{
			name: "the trust dialog moves the focus and then confirms", script: fake.PaneScript{Screen: trust, Moves: moves},
			mode: checkFresh, keys: []string{"Down", "Enter"},
		},
		{
			name: "a dialog that refuses its first key is pressed again", mode: checkFresh,
			script: fake.PaneScript{Screen: trust, Moves: moves, Refused: 1}, keys: []string{"Down", "Down", "Enter"},
		},
		{
			name: "a focus already on accept only confirms", script: fake.PaneScript{Screen: accepted, Moves: moves},
			mode: checkFresh, keys: []string{"Enter"},
		},
		{name: "an unknown screen takes nothing and times out", script: fake.PaneScript{Screen: pane(t, "unknown")}, mode: checkFresh, want: ports.ErrUnknownScreen},
		{name: "an acceptance dialog is never answered", script: fake.PaneScript{Screen: pane(t, "mode-acceptance")}, mode: checkFresh, want: ports.ErrUnknownScreen},
		{name: "a resume past its timeout failed", script: fake.PaneScript{Screen: pane(t, "resume-dialog")}, mode: checkResume, want: ports.ErrResumeNotReady},
		{name: "an empty pane past its timeout is unknown", script: fake.PaneScript{Screen: ""}, mode: checkFresh, want: ports.ErrUnknownScreen},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal := fake.NewTerminal()
			terminal.Play(tt.script)
			if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
				t.Fatal(err)
			}
			err := runStartCheck(t.Context(), terminal, "p", startCheck{mode: tt.mode, timeout: 80 * time.Millisecond, poll: time.Millisecond, settle: time.Millisecond})
			if !errors.Is(err, tt.want) {
				t.Fatalf("check: %v, want %v", err, tt.want)
			}
			if got := terminal.KeysOf("p"); !slices.Equal(got, tt.keys) {
				t.Errorf("keys %v, want %v", got, tt.keys)
			}
			if got := terminal.TypedInto("p"); len(got) != 0 {
				t.Errorf("typed %v into a pane during the check", got)
			}
		})
	}
}

// Enter on the first dialog is the wrong answer, so the check must see the focus move
// before it confirms. Here the key never moves it, and no Enter may follow.
func TestAFocusThatNeverMovesIsNeverConfirmed(t *testing.T) {
	terminal := fake.NewTerminal()
	terminal.Play(fake.PaneScript{Screen: pane(t, "trust-focus-cancel")})
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	err := runStartCheck(t.Context(), terminal, "p", startCheck{mode: checkFresh, timeout: 40 * time.Millisecond, poll: time.Millisecond, settle: time.Millisecond})
	if !errors.Is(err, ports.ErrUnknownScreen) {
		t.Fatalf("check: %v", err)
	}
	if slices.Contains(terminal.KeysOf("p"), "Enter") {
		t.Errorf("Enter reached a dialog whose focus did not move: %v", terminal.KeysOf("p"))
	}
}

func TestAnIncompleteReadIsNotAScreen(t *testing.T) {
	terminal := fake.NewTerminal()
	terminal.Play(fake.PaneScript{Screen: pane(t, "ready")})
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	terminal.FailReads(errors.New("the server did not answer"))
	err := runStartCheck(t.Context(), terminal, "p", startCheck{mode: checkFresh, timeout: 30 * time.Millisecond, poll: time.Millisecond, settle: time.Millisecond})
	if !errors.Is(err, ports.ErrUnknownScreen) {
		t.Errorf("check: %v", err)
	}
	if len(terminal.KeysOf("p")) != 0 {
		t.Errorf("keys %v", terminal.KeysOf("p"))
	}
}

func TestACanceledCheckStops(t *testing.T) {
	terminal := fake.NewTerminal()
	terminal.Play(fake.PaneScript{Screen: pane(t, "unknown")})
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := runStartCheck(ctx, terminal, "p", startCheck{mode: checkFresh, timeout: time.Minute, poll: time.Millisecond, settle: time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("check: %v", err)
	}
}
