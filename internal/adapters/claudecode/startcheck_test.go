package claudecode

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
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
		{capture: "ready-dashed", want: screenReady},
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

func TestAPaneHoldsOnlyItsToolsWhenItSaysSo(t *testing.T) {
	spec := ports.SessionSpec{Lane: "lane-1", Generation: 1, Model: "opus", PermissionMode: "default", ConfigDir: "/state"}
	if slices.Contains(paneArgs(spec), "--tools") {
		t.Errorf("a lane lost its tools: %v", paneArgs(spec))
	}
	spec.OnlyTools = true
	got := paneArgs(spec)
	at := slices.Index(got, "--tools")
	if at < 0 || got[at+1] != "" {
		t.Errorf("args %v", got)
	}
}

func TestAPaneHoldsItsPack(t *testing.T) {
	spec := ports.SessionSpec{Lane: "lane-1", Generation: 1, Model: "opus", PermissionMode: "default", ConfigDir: "/state"}
	if slices.Contains(paneArgs(spec), "--append-system-prompt-file") {
		t.Errorf("a pane with no pack: %v", paneArgs(spec))
	}
	spec.SystemPromptFile = "/lanes/one/pack.md"
	got := paneArgs(spec)
	at := slices.Index(got, "--append-system-prompt-file")
	if at < 0 || got[at+1] != spec.SystemPromptFile {
		t.Errorf("args %v", got)
	}
}

func TestStartCheck(t *testing.T) {
	trust, accepted, ready := pane(t, "trust-focus-cancel"), pane(t, "trust-focus-accept"), pane(t, "ready")
	flipped := strings.Join([]string{" Accessing workspace:", "", "   Yes, I trust this folder", " ❯ No, exit", ""}, "\n")
	// repainted is a later frame of the same dialog, which a pane shows while the key it took is still
	// being answered.
	repainted := accepted + "\n Checking this folder.\n"
	// The dialog's focus wraps on an arrow, so a second Down takes it back to `No, exit`, and End does not move past
	// the last option.
	moves := map[string]map[string]string{
		trust:    {"Down": accepted, "End": accepted},
		accepted: {"Enter": ready, "Up": trust, "Down": trust},
	}
	flippedMoves := map[string]map[string]string{flipped: {"Up": accepted, "Home": accepted}, accepted: {"Enter": ready}}
	// middle is a dialog whose accept option is not at an edge, where a repeated key has no absolute target.
	middle := strings.Join([]string{" Accessing workspace:", "", " ❯ No, exit", "   Yes, I trust this folder", "   Tell me more", ""}, "\n")
	tests := []struct {
		name   string
		script fake.PaneScript
		mode   checkMode
		// timeout is how long the check runs, and is longer where a key's effect is late.
		timeout time.Duration
		// settle is how long a frame stands before a key, and resend how long before a navigation key is repeated. A
		// case that sets no resend never reaches one.
		settle, resend time.Duration
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
			name: "a dropped Down is followed by End, which cannot wrap", mode: checkFresh, timeout: 2 * time.Second, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: trust, Moves: moves, Refused: 1}, keys: []string{"Down", "End", "Enter"},
		},
		{
			name: "a dropped Up is followed by Home, which cannot wrap", mode: checkFresh, timeout: 2 * time.Second, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: flipped, Moves: flippedMoves, Refused: 1}, keys: []string{"Up", "Home", "Enter"},
		},
		{
			name: "every key dropped until the dialog listens is sent again", mode: checkFresh, timeout: 2 * time.Second, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: trust, Moves: moves, Refused: 3}, keys: []string{"Down", "End", "End", "End", "Enter"},
		},
		{
			name: "a key is not repeated where the accept option is not at an edge", mode: checkFresh, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: middle, Refused: 1}, keys: []string{"Down"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "a focus the dialog undoes as it settles takes no Enter until it stands", mode: checkFresh, timeout: 3 * time.Second,
			settle: 100 * time.Millisecond, script: fake.PaneScript{Screen: trust, Moves: moves, Undone: 3}, keys: []string{"Down", "End", "Enter"},
		},
		{
			name: "a key that moved to a screen the check does not know is not repeated", mode: checkFresh, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: trust, Moves: map[string]map[string]string{trust: {"Down": pane(t, "mode-acceptance")}}},
			keys:   []string{"Down"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "an Enter the dialog refused as it put the focus back is never sent again", mode: checkFresh, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: accepted, Moves: map[string]map[string]string{accepted: {"Enter": trust}, trust: {"Down": accepted, "End": accepted}}},
			keys:   []string{"Enter"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "an Enter that changes nothing is not sent again once keys repeat", mode: checkFresh, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: accepted}, keys: []string{"Enter"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "a late repaint is waited out before the next key", mode: checkFresh, timeout: 2 * time.Second,
			script: fake.PaneScript{Screen: trust, Moves: moves, Stale: 5}, keys: []string{"Down", "Enter"},
		},
		{
			name: "a late repaint of a focus below accept takes Up once", mode: checkFresh, timeout: 2 * time.Second,
			script: fake.PaneScript{Screen: flipped, Moves: map[string]map[string]string{flipped: {"Up": accepted}, accepted: {"Enter": ready}}, Stale: 5},
			keys:   []string{"Up", "Enter"},
		},
		{
			name: "an Enter that changes nothing is sent once", mode: checkFresh,
			script: fake.PaneScript{Screen: accepted}, keys: []string{"Enter"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "a dialog that repaints after Enter and still asks takes no second Enter", mode: checkFresh,
			script: fake.PaneScript{Screen: accepted, Moves: map[string]map[string]string{accepted: {"Enter": repainted}}},
			keys:   []string{"Enter"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "an Enter that never repaints times out a resume", mode: checkResume,
			script: fake.PaneScript{Screen: accepted}, keys: []string{"Enter"}, want: ports.ErrResumeNotReady,
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
			timeout := cmp.Or(tt.timeout, 80*time.Millisecond)
			err := runStartCheck(t.Context(), terminal, "p", startCheck{mode: tt.mode, timeout: timeout, poll: time.Millisecond, settle: cmp.Or(tt.settle, time.Millisecond), resend: cmp.Or(tt.resend, time.Minute)})
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

// repaintsOnEnd is a pane that takes every key at once and shows none of their effect until it took an End, as one
// does whose repaint is later than the resend.
type repaintsOnEnd struct {
	*fake.Terminal
	before string
}

func (p repaintsOnEnd) Capture(ctx context.Context, name string) (string, error) {
	if !slices.Contains(p.KeysOf(name), "End") {
		return p.before, nil
	}
	return p.Terminal.Capture(ctx, name)
}

// A slow Down and the End sent again for it can both land, and the focus then still stands on the accept option.
func TestASlowDownAndItsResentEndBothLandOnAccept(t *testing.T) {
	trust, accepted, ready := pane(t, "trust-focus-cancel"), pane(t, "trust-focus-accept"), pane(t, "ready")
	terminal := fake.NewTerminal()
	terminal.Play(fake.PaneScript{Screen: trust, Moves: map[string]map[string]string{
		trust:    {"Down": accepted, "End": accepted},
		accepted: {"Down": trust, "End": accepted, "Enter": ready},
	}})
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	slow := repaintsOnEnd{Terminal: terminal, before: trust}
	check := startCheck{mode: checkFresh, timeout: 2 * time.Second, poll: time.Millisecond, settle: time.Millisecond, resend: 20 * time.Millisecond}
	if err := runStartCheck(t.Context(), slow, "p", check); err != nil {
		t.Fatalf("check: %v", err)
	}
	if got, want := terminal.KeysOf("p"), []string{"Down", "End", "Enter"}; !slices.Equal(got, want) {
		t.Errorf("keys %v, want %v", got, want)
	}
}
