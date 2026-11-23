package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
)

// This file is the interactive start check, and the only place that reads a screen on a
// qualified version. From `briefing` onward, state and input go through hooks and the
// messaging socket, except `/compact`, which only keystrokes can deliver.

// The words the check matches. All of them are undocumented UI, so each is listed in the
// qualification matrix, and a screen that holds none of them is unknown.
const (
	trustHeading = "Accessing workspace:"
	trustAccept  = "Yes, I trust this folder"
	focusGlyph   = "❯"
	promptGlyph  = ">"
)

// A rule above and below the prompt separates the input from a conversation, which shows
// each earlier prompt with the same glyph.
const minRuleWidth = 10

// screenKind is what a capture shows, as far as the check can act on it.
type screenKind string

const (
	// screenUnknown takes no input. A screen still painting reads this way too.
	screenUnknown screenKind = "unknown"
	// screenTrust is the trust dialog, whose accept option the check can reach.
	screenTrust screenKind = "trust"
	// screenReady is a prompt waiting for input.
	screenReady screenKind = "ready"
)

// focusLine says where the trust dialog's focus stands against its accept option.
type focusLine string

const (
	// focusAbove means the accept option is below the focus.
	focusAbove focusLine = "above"
	// focusBelow means the accept option is above the focus.
	focusBelow focusLine = "below"
	// focusOnAccept means the next confirmation accepts.
	focusOnAccept focusLine = "accept"
)

type screen struct {
	kind  screenKind
	focus focusLine
	// edge says the trust dialog's accept option is the last of its options with the focus above it, or the first
	// with the focus below it, so Home or End reaches it from anywhere and cannot move past it.
	edge bool
}

// readScreen sorts one capture. It holds to exact words, because every other dialog that
// offers `Yes` writes to a user's settings or grants something a lane never asked for, so
// anything that is not the one known dialog or the one known prompt is unknown.
func readScreen(text string) screen {
	lines := strings.Split(text, "\n")
	if focus, edge, ok := trustFocus(lines); ok {
		return screen{kind: screenTrust, focus: focus, edge: edge}
	}
	for i := 1; i+1 < len(lines); i++ {
		if isPrompt(lines[i]) && isRule(lines[i-1]) && isRule(lines[i+1]) {
			return screen{kind: screenReady}
		}
	}
	return screen{kind: screenUnknown}
}

// trustFocus finds the dialog by its heading and its accept option, and the focus by its
// glyph. A dialog with no glyph on any line is still painting. An option is at the edge
// when a blank line follows the last one, or comes before the first.
func trustFocus(lines []string) (where focusLine, edge, ok bool) {
	heading, accept, focus := false, -1, -1
	for i, line := range lines {
		text := strings.TrimSpace(line)
		option, focused := strings.CutPrefix(text, focusGlyph)
		option = strings.TrimSpace(option)
		switch {
		case text == trustHeading:
			heading = true
		case option == trustAccept:
			accept = i
		}
		if focused {
			focus = i
		}
	}
	if !heading || accept < 0 || focus < 0 {
		return "", false, false
	}
	blank := func(i int) bool { return i < 0 || i >= len(lines) || strings.TrimSpace(lines[i]) == "" }
	switch {
	case focus == accept:
		return focusOnAccept, false, true
	case focus < accept:
		return focusAbove, blank(accept + 1), true
	}
	return focusBelow, blank(accept - 1), true
}

// isPrompt reads an input line, in a box or between rules. A numbered line is an option
// of a dialog.
func isPrompt(line string) bool {
	text := strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│"))
	rest, ok := strings.CutPrefix(text, focusGlyph)
	if !ok {
		rest, ok = strings.CutPrefix(text, promptGlyph)
	}
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	return rest == "" || rest[0] < '0' || rest[0] > '9'
}

func isRule(line string) bool {
	text := strings.TrimSpace(line)
	width := 0
	for _, r := range text {
		switch r {
		case '─', '━', '╌', '┄', '╭', '╮', '╰', '╯':
			width++
		default:
			return false
		}
	}
	return width >= minRuleWidth
}

// checkMode says what a check that runs out of time means. A fresh start needs a person,
// and a resume falls back to a new generation.
type checkMode string

const (
	checkFresh  checkMode = "fresh"
	checkResume checkMode = "resume"
)

// startCheck is one checked sequence. Poll is the wait between reads. Settle is the least time a
// frame stands unchanged before a key goes to it, and the least wait after a key before the next
// read. Resend is how long the frame a navigation key answered stands before that navigation goes
// again. Neither decides that a key took effect: the screen changing does, within the check's timeout.
type startCheck struct {
	mode    checkMode
	timeout time.Duration
	poll    time.Duration
	settle  time.Duration
	resend  time.Duration
}

func (c startCheck) failure() error {
	if c.mode == checkResume {
		return ports.ErrResumeNotReady
	}
	return ports.ErrUnknownScreen
}

// timedOut is the failure of a check whose time ran out on last, the screen of its last read. A trust dialog that
// still asks after the Enter did not take it: a focus still on the accept option says the key was lost, and a focus
// back on `No, exit` says the dialog refused it. The error names that and wraps the mode's failure, so every caller
// reads the start as it did.
func (c startCheck) timedOut(last screen, confirmed bool) error {
	if !confirmed || last.kind != screenTrust {
		return c.failure()
	}
	why := "it refused the key and put its focus back on `No, exit`"
	if last.focus == focusOnAccept {
		why = "it lost the key, and its focus stayed on `" + trustAccept + "`"
	}
	return fmt.Errorf("%w: %s: %w", ports.ErrTrustKeyLost, why, c.failure())
}

// runStartCheck reads the pane until it holds a prompt, and is the driver's ready signal
// for the `starting` state. It answers the trust dialog by moving the focus and reading
// that it moved, so no Enter reaches a dialog on `No, exit`. Nothing goes to a screen it
// does not recognize, and a read that failed is not a screen.
//
// A dialog that has just painted is not yet listening: Claude Code 2.1.329 moves the focus on a
// key and puts it back on `No, exit` some 150 ms after the first paint, and refuses an Enter until
// then. So a key goes only to a frame that stood unchanged for settle, and an Enter only to a focus
// that stood on the accept option.
//
// A key's effect is a different frame, never a later time, so the same frame after a key says
// either that the key was dropped or that the pane repaints slowly, and the check cannot tell which.
// What it sends again is therefore safe in both cases. Only navigation goes again, only to the
// trust dialog with its focus off the accept option, and only as Home or End: an arrow wraps, so a
// second one that lands beside the first would put the focus back on `No, exit` under a later
// Enter, and Home or End leaves it on the accept option however many land.
//
// Enter goes once. The check reads the ready prompt after it and nothing else, so an Enter that
// was lost or refused ends the check at its timeout, for a person to take over, and no second one
// can answer a later dialog with its default. A check that ends on the trust dialog after its Enter
// says so (timedOut).
func runStartCheck(ctx context.Context, terminal ports.Terminal, name string, check startCheck) error {
	deadline := time.Now().Add(check.timeout)
	// shown is the frame the pane shows and since when. answered is the frame the last key answered, empty once
	// a different frame was read. last is what the last read that answered showed.
	var shown, answered string
	var since, keyed time.Time
	var last screen
	moved, confirmed := false, false
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, err := terminal.Capture(ctx, name)
		if err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				return fmt.Errorf("%w: the pane closed: %v", check.failure(), err)
			}
			// Nothing says what showed meanwhile, so what stood starts again.
			shown = ""
			pause(ctx, check.poll)
			continue
		}
		now := time.Now()
		if text != shown {
			shown, since = text, now
		}
		if text != answered {
			answered = ""
		}
		seen := readScreen(text)
		if seen.kind == screenReady {
			return nil
		}
		last = seen
		stands := now.Sub(since) >= check.settle
		waits := answered != "" && now.Sub(keyed) < check.resend
		if seen.kind == screenTrust && !confirmed && stands && !waits {
			seen.focus = sabotagedFocus(seen.focus)
			if key := answerKey(seen, moved); key != "" {
				if err := terminal.Keys(ctx, name, key); err != nil {
					return err
				}
				answered, keyed = text, time.Now()
				moved, confirmed = true, key == keyEnter
				pause(ctx, check.settle)
				continue
			}
		}
		// A screen the check does not know, a frame that has not stood, a key's effect still due, or the dialog
		// after its Enter: only time passes.
		pause(ctx, check.poll)
	}
	return check.timedOut(last, confirmed)
}

// keepScreen saves what a failed start stopped on, for a person, beside the generated
// files. A screen can show account details, so it stays in that private directory, and
// nothing reports its text.
func keepScreen(ctx context.Context, terminal ports.Terminal, name string, spec ports.SessionSpec) error {
	text, err := terminal.Capture(ctx, name)
	if err != nil {
		return nil
	}
	return os.WriteFile(filepath.Join(spec.ConfigDir, fmt.Sprintf("screen-%d.txt", spec.Generation)), []byte(text), 0o600)
}

// answerKey is the one key a dialog takes from where its focus stands. The first navigation is an arrow.
// A later one is Home or End, and none where that key has no sure target, so no second arrow follows the first.
func answerKey(seen screen, moved bool) string {
	switch {
	case seen.focus == focusOnAccept:
		return keyEnter
	case !moved && seen.focus == focusBelow:
		return "Up"
	case !moved:
		return "Down"
	case !seen.edge:
		return ""
	case seen.focus == focusBelow:
		return "Home"
	}
	return "End"
}

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// startCheckScreen reads the pane's screen for the probe's evidence; the start check is where a screen is read.
func startCheckScreen(ctx context.Context, terminal ports.Terminal, name string) (string, error) {
	return terminal.Capture(ctx, name)
}
