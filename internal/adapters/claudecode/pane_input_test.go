package claudecode

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ports"
)

// failingTerminal fails what it is told to, and passes the rest to a fake terminal.
type failingTerminal struct {
	*fake.Terminal
	typeErr, keysErr error
}

func (f failingTerminal) Type(ctx context.Context, name, text string) error {
	if f.typeErr != nil {
		return f.typeErr
	}
	return f.Terminal.Type(ctx, name, text)
}

func (f failingTerminal) Keys(ctx context.Context, name string, keys ...string) error {
	if f.keysErr != nil {
		return f.keysErr
	}
	return f.Terminal.Keys(ctx, name, keys...)
}

// An input typed into a pane is undelivered only when the pane is not there. A terminal that failed
// any other way may have passed the text on, so its error comes back as it is, and the caller does not
// take the input for one nobody got.
func TestTypedInputIsNoSessionOnlyWhenThePaneIsGone(t *testing.T) {
	tooLong := errors.New("tmux send-keys: command too long")
	gone := fmt.Errorf("%w: can't find session", ports.ErrNotFound)
	tests := []struct {
		name          string
		typeErr       error
		keysErr       error
		wantNoSession bool
		wantText      string
	}{
		{name: "the pane is gone when the text goes in", typeErr: gone, wantNoSession: true, wantText: "can't find session"},
		{name: "the pane is gone when Enter goes in", keysErr: gone, wantNoSession: true, wantText: "can't find session"},
		{name: "the terminal fails the text", typeErr: tooLong, wantText: "command too long"},
		{name: "the terminal fails Enter", keysErr: tooLong, wantText: "command too long"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminal := fake.NewTerminal()
			spec := policySpec(t)
			spec.RunMode = ports.RunModeInteractive
			name := paneName(spec)
			if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
				t.Fatal(err)
			}
			driver := New(nil, Options{Terminal: failingTerminal{Terminal: terminal, typeErr: test.typeErr, keysErr: test.keysErr}})
			s := driver.newPaneSession(spec, name, ports.SessionHandle{ID: sessionHandleID(spec)}, lanes.CostMeter{})
			_, err := s.typeInput(t.Context(), ports.Input{Text: "a brief"})
			if err == nil {
				t.Fatal("an input a terminal failed was reported as sent")
			}
			if got := errors.Is(err, ports.ErrNoSession); got != test.wantNoSession {
				t.Errorf("no session %v, want %v: %v", got, test.wantNoSession, err)
			}
			if !strings.Contains(err.Error(), test.wantText) {
				t.Errorf("the error %q lost the terminal's words %q", err, test.wantText)
			}
		})
	}
}
