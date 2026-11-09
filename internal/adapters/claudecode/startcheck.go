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
}

// readScreen sorts one capture. It holds to exact words, because every other dialog that
// offers `Yes` writes to a user's settings or grants something a lane never asked for, so
// anything that is not the one known dialog or the one known prompt is unknown.
func readScreen(text string) screen {
	lines := strings.Split(text, "\n")
	if focus, ok := trustFocus(lines); ok {
		return screen{kind: screenTrust, focus: focus}
	}
	for i := 1; i+1 < len(lines); i++ {
		if isPrompt(lines[i]) && isRule(lines[i-1]) && isRule(lines[i+1]) {
			return screen{kind: screenReady}
		}
	}
	return screen{kind: screenUnknown}
}

// trustFocus finds the dialog by its heading and its accept option, and the focus by its
// glyph. A dialog with no glyph on any line is still painting.
func trustFocus(lines []string) (focusLine, bool) {
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
		return "", false
	}
	switch {
	case focus == accept:
		return focusOnAccept, true
	case focus < accept:
		return focusAbove, true
	}
	return focusBelow, true
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

// startCheck is one checked sequence. Poll is the wait between reads, and Settle the wait
// after a key, so a key's own effect is what the next read sees.
type startCheck struct {
	mode    checkMode
	timeout time.Duration
	poll    time.Duration
	settle  time.Duration
}

func (c startCheck) failure() error {
	if c.mode == checkResume {
		return ports.ErrResumeNotReady
	}
	return ports.ErrUnknownScreen
}

// runStartCheck reads the pane until it holds a prompt, and is the driver's ready signal
// for the `starting` state. It answers the trust dialog by moving the focus and reading
// that it moved, so no Enter reaches a dialog on `No, exit`. Nothing goes to a screen it
// does not recognize, and a read that failed is not a screen.
func runStartCheck(ctx context.Context, terminal ports.Terminal, name string, check startCheck) error {
	deadline := time.Now().Add(check.timeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, err := terminal.Capture(ctx, name)
		if err != nil {
			if errors.Is(err, ports.ErrNotFound) {
				return fmt.Errorf("%w: the pane closed: %v", check.failure(), err)
			}
			pause(ctx, check.poll)
			continue
		}
		seen := readScreen(text)
		switch seen.kind {
		case screenReady:
			return nil
		case screenTrust:
			if err := terminal.Keys(ctx, name, answerKey(sabotagedFocus(seen.focus))); err != nil {
				return err
			}
			pause(ctx, check.settle)
		default:
			pause(ctx, check.poll)
		}
	}
	return check.failure()
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

// answerKey is the one key a dialog takes from where its focus stands.
func answerKey(focus focusLine) string {
	switch focus {
	case focusOnAccept:
		return "Enter"
	case focusBelow:
		return "Up"
	}
	return "Down"
}

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
