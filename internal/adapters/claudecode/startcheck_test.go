package claudecode

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
		// Claude Code 2.1.330 at the pane's width, whose prompt glyph a no-break space follows.
		{capture: "v2-1-330-trust-focus-cancel", want: screenTrust, focus: focusAbove},
		{capture: "v2-1-330-trust-focus-accept", want: screenTrust, focus: focusOnAccept},
		{capture: "v2-1-330-ready", want: screenReady},
		{capture: "v2-1-330-resume", want: screenReady},
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
	trust330, accepted330, ready330 := pane(t, "v2-1-330-trust-focus-cancel"), pane(t, "v2-1-330-trust-focus-accept"), pane(t, "v2-1-330-ready")
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
		// lost says the check ended on the trust dialog after its Enter, and says names the focus its error reports.
		lost bool
		says string
	}{
		{name: "a ready screen takes no key", script: fake.PaneScript{Screen: ready}, mode: checkFresh},
		{name: "a resume takes no key", script: fake.PaneScript{Screen: pane(t, "resume")}, mode: checkResume},
		{
			name: "the trust dialog moves the focus and then confirms", script: fake.PaneScript{Screen: trust, Moves: moves},
			mode: checkFresh, keys: []string{"Down", "Enter"},
		},
		{
			name: "the trust dialog of 2.1.330 moves the focus and then confirms", mode: checkFresh,
			script: fake.PaneScript{Screen: trust330, Moves: map[string]map[string]string{trust330: {"Down": accepted330}, accepted330: {"Enter": ready330}}},
			keys:   []string{"Down", "Enter"},
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
			keys:   []string{"Enter"}, want: ports.ErrUnknownScreen, lost: true, says: "moved its focus off",
		},
		{
			name: "a refused Enter takes no navigation either, whatever the focus calls for", mode: checkFresh, timeout: 300 * time.Millisecond,
			resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: trust, Moves: map[string]map[string]string{trust: {"Down": accepted, "End": accepted}, accepted: {"Enter": trust}}},
			keys:   []string{"Down", "Enter"}, want: ports.ErrUnknownScreen, lost: true, says: "moved its focus off",
		},
		{
			name: "a refused Enter on a resume times it out on the trust dialog", mode: checkResume, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: accepted, Moves: map[string]map[string]string{accepted: {"Enter": trust}}},
			keys:   []string{"Enter"}, want: ports.ErrResumeNotReady, lost: true, says: "moved its focus off",
		},
		{
			name: "an Enter that moved the dialog on to a screen the check does not know names no lost key", mode: checkFresh,
			script: fake.PaneScript{Screen: accepted, Moves: map[string]map[string]string{accepted: {"Enter": pane(t, "mode-acceptance")}}},
			keys:   []string{"Enter"}, want: ports.ErrUnknownScreen,
		},
		{
			name: "an Enter that changes nothing is not sent again once keys repeat", mode: checkFresh, resend: 5 * time.Millisecond,
			script: fake.PaneScript{Screen: accepted}, keys: []string{"Enter"}, want: ports.ErrUnknownScreen, lost: true, says: trustAccept,
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
			script: fake.PaneScript{Screen: accepted}, keys: []string{"Enter"}, want: ports.ErrUnknownScreen, lost: true, says: trustAccept,
		},
		{
			name: "a dialog that repaints after Enter and still asks takes no second Enter", mode: checkFresh,
			script: fake.PaneScript{Screen: accepted, Moves: map[string]map[string]string{accepted: {"Enter": repainted}}},
			keys:   []string{"Enter"}, want: ports.ErrUnknownScreen, lost: true, says: trustAccept,
		},
		{
			name: "an Enter that never repaints times out a resume", mode: checkResume,
			script: fake.PaneScript{Screen: accepted}, keys: []string{"Enter"}, want: ports.ErrResumeNotReady, lost: true, says: trustAccept,
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
			guard := &keyGuard{Terminal: terminal}
			err := runStartCheck(t.Context(), guard, "p", startCheck{mode: tt.mode, timeout: timeout, poll: time.Millisecond, settle: cmp.Or(tt.settle, time.Millisecond), resend: cmp.Or(tt.resend, time.Minute)})
			if !errors.Is(err, tt.want) {
				t.Fatalf("check: %v, want %v", err, tt.want)
			}
			if lost := errors.Is(err, ports.ErrTrustKeyLost); lost != tt.lost || (lost && !strings.Contains(err.Error(), tt.says)) {
				t.Errorf("check: %v, want a lost trust key %v naming %q", err, tt.lost, tt.says)
			}
			// A resume that stopped is no fresh start's unknown screen, whatever stopped it.
			if tt.mode == checkResume && errors.Is(err, ports.ErrUnknownScreen) {
				t.Errorf("a resume ended as an unknown screen: %v", err)
			}
			if got := terminal.KeysOf("p"); !slices.Equal(got, tt.keys) {
				t.Errorf("keys %v, want %v", got, tt.keys)
			}
			for _, wrong := range guard.wrong {
				t.Error(wrong)
			}
			if got := terminal.TypedInto("p"); len(got) != 0 {
				t.Errorf("typed %v into a pane during the check", got)
			}
		})
	}
}

// A trust dialog whose Enter did not take, and whose focus is anywhere but its accept option when the check ends, says it
// moved its focus off that option. It does not say the focus went back on `No, exit`, which only the dialog's first option
// is.
func TestATrustTimeoutWithTheFocusElsewhereSaysItLeftAccept(t *testing.T) {
	for _, focus := range []focusLine{focusAbove, focusBelow} {
		t.Run(string(focus), func(t *testing.T) {
			err := startCheck{mode: checkFresh}.timedOut(screen{kind: screenTrust, focus: focus}, true)
			if !errors.Is(err, ports.ErrTrustKeyLost) || !errors.Is(err, ports.ErrUnknownScreen) {
				t.Fatalf("the timeout is %v, want a lost trust key that wraps the start's failure", err)
			}
			if strings.Contains(err.Error(), "No, exit") {
				t.Errorf("a focus %s the accept option is reported as %q, which names an option the focus may not be on", focus, err)
			}
			if !strings.Contains(err.Error(), "moved its focus off `"+trustAccept+"`") {
				t.Errorf("the timeout says %q, want that the focus left the accept option", err)
			}
		})
	}
	if err := (startCheck{mode: checkFresh}).timedOut(screen{kind: screenTrust, focus: focusOnAccept}, true); !strings.Contains(err.Error(), "lost the key") {
		t.Errorf("a focus still on the accept option says %q, want that the key was lost", err)
	}
}

// keyGuard is a pane that holds each key against the screen it lands on, as the pane shows it then and not as the
// check last read it. The trust key is the only key that does not go through press and its keysHeld gate, so only
// the trust dialog takes a key, and only a focus on its accept option takes Enter.
type keyGuard struct {
	*fake.Terminal
	wrong []string
}

func (g *keyGuard) Keys(ctx context.Context, name string, keys ...string) error {
	shown, err := g.Screen(ctx, name)
	if err != nil {
		return err
	}
	seen := readScreen(shown)
	for _, key := range keys {
		switch {
		case seen.kind != screenTrust:
			g.wrong = append(g.wrong, fmt.Sprintf("%s reached a %s screen:\n%s", key, seen.kind, shown))
		case key == keyEnter && seen.focus != focusOnAccept:
			g.wrong = append(g.wrong, fmt.Sprintf("Enter reached a focus %s the accept option:\n%s", seen.focus, shown))
		}
	}
	return g.Terminal.Keys(ctx, name, keys...)
}

// Every screen that is not the trust dialog, the ready prompt among them, takes no key, however long it stands.
func TestNoScreenButTheTrustDialogTakesAKey(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "panes"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".txt")
		shown := pane(t, name)
		if readScreen(shown).kind == screenTrust {
			continue
		}
		for _, mode := range []checkMode{checkFresh, checkResume} {
			t.Run(name+"/"+string(mode), func(t *testing.T) {
				terminal := fake.NewTerminal()
				terminal.Play(fake.PaneScript{Screen: shown})
				if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
					t.Fatal(err)
				}
				guard := &keyGuard{Terminal: terminal}
				_ = runStartCheck(t.Context(), guard, "p", startCheck{mode: mode, timeout: 40 * time.Millisecond, poll: time.Millisecond, settle: time.Millisecond, resend: time.Millisecond})
				if keys := terminal.KeysOf("p"); len(keys) != 0 {
					t.Errorf("keys %v reached a screen that is not the trust dialog", keys)
				}
				for _, wrong := range guard.wrong {
					t.Error(wrong)
				}
			})
		}
	}
}

// The trust key is the one key a worker sends that does not go through press, which holds every key while a dialog
// may be open, so no other code in the driver sends a key.
func TestOnlyTheStartCheckAndPressSendKeys(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	var senders []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				// A terminal's Keys takes a context and a pane before its keys, and maps.Keys takes one map.
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Keys" && len(call.Args) > 1 {
					senders = append(senders, path+":"+fn.Name.Name)
				}
				return true
			})
		}
	}
	slices.Sort(senders)
	if want := []string{"pane.go:press", "startcheck.go:runStartCheck"}; !slices.Equal(senders, want) {
		t.Errorf("keys are sent from %v, want only %v", senders, want)
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
	if !errors.Is(err, ports.ErrUnknownScreen) || errors.Is(err, ports.ErrTrustKeyLost) {
		t.Fatalf("check: %v, want an unknown screen, and no lost Enter, since none went", err)
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

// failsAfterKeys is a pane whose reads fail from the moment a key reaches it.
type failsAfterKeys struct {
	*fake.Terminal
	err error
}

func (f *failsAfterKeys) Keys(ctx context.Context, name string, keys ...string) error {
	if err := f.Terminal.Keys(ctx, name, keys...); err != nil {
		return err
	}
	f.FailReads(f.err)
	return nil
}

// A read that failed shows nothing, so a check that times out after only failed reads names no screen: it cannot say a
// trust dialog it saw before them is still there, or that its Enter was lost, and its error is the mode's failure alone.
func TestAFailedReadForgetsTheLastScreen(t *testing.T) {
	terminal := fake.NewTerminal()
	terminal.Play(fake.PaneScript{Screen: pane(t, "trust-focus-accept")})
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	failing := &failsAfterKeys{Terminal: terminal, err: errors.New("the server did not answer")}
	err := runStartCheck(t.Context(), failing, "p", startCheck{mode: checkFresh, timeout: 60 * time.Millisecond, poll: time.Millisecond, settle: time.Millisecond})
	if !errors.Is(err, ports.ErrUnknownScreen) {
		t.Fatalf("check: %v, want the start's unknown screen", err)
	}
	if errors.Is(err, ports.ErrTrustKeyLost) {
		t.Errorf("check: %v, names a trust dialog that no read after the Enter showed", err)
	}
	if got := terminal.KeysOf("p"); !slices.Equal(got, []string{"Enter"}) {
		t.Errorf("keys %v, want the one Enter", got)
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
