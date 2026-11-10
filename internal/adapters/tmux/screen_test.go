package tmux_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

// The web pane shows a person the screen as their terminal would, so a read keeps the escape sequences
// for colour and emphasis and asks for nothing else.
func TestScreenAsksForTheVisibleScreenWithItsAttributes(t *testing.T) {
	runner := &answering{stdout: "\x1b[31mred\x1b[39m plain\n"}
	terminal := answeringTerminal(t, runner)
	got, err := terminal.Screen(t.Context(), "lane-g1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "\x1b[31mred\x1b[39m plain\n" {
		t.Errorf("screen %q", got)
	}
	args := runner.last().Args
	want := []string{"capture-pane", "-p", "-e", "-t", "=lane-g1:"}
	if got := args[slices.Index(args, "-S")+2:]; !slices.Equal(got, want) {
		t.Errorf("tmux was asked %v, want %v", got, want)
	}
}

func TestScreenOfAPaneTheServerDoesNotHoldIsNotFound(t *testing.T) {
	runner := &answering{stderr: "can't find session: lane-g1\n", code: 1}
	terminal := answeringTerminal(t, runner)
	if _, err := terminal.Screen(t.Context(), "lane-g1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v, want not found", err)
	}
}

// On a real server, a coloured word reaches the reader with its colour, and plain text stays plain. The
// pane's process prints a file and stays, because tmux can lose what a process wrote if it exits before
// the server reads it.
func TestScreenKeepsColourOnAPrivateServer(t *testing.T) {
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	ctx := t.Context()
	dir := t.TempDir()
	text := filepath.Join(dir, "screen.txt")
	if err := os.WriteFile(text, []byte("\x1b[31mred\x1b[0m plain\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := ports.PaneSpec{Name: "screen-g1", Dir: dir, Width: 80, Height: 10, Command: []string{"tail", "-n", "+1", "-f", text}}
	if _, err := terminal.Open(ctx, spec); err != nil {
		t.Fatal(err)
	}
	doubles.KillTmux(t, terminal, spec.Name)
	deadline := time.Now().Add(10 * time.Second)
	for {
		screen, err := terminal.Screen(ctx, spec.Name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(screen, "plain") {
			if !strings.Contains(screen, "\x1b[31mred") {
				t.Errorf("the colour is gone from %q", screen)
			}
			plain, err := terminal.Capture(ctx, spec.Name)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(plain, "\x1b[") {
				t.Errorf("the start check's read carries escapes: %q", plain)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed its text: %q", screen)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
