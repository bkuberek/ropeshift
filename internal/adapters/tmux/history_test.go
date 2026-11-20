package tmux_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

// historyPane opens a pane five rows tall whose program prints lines and stays, and waits until the
// screen shows the last of them, so the lines before it have scrolled into the pane's history.
func historyPane(t *testing.T, lines int) (*tmux.Terminal, string) {
	t.Helper()
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	script := fmt.Sprintf("i=1; while [ $i -le %d ]; do echo line$i; i=$((i+1)); done; exec sleep 600", lines)
	spec := ports.PaneSpec{Name: "history-g1", Dir: t.TempDir(), Width: 40, Height: 5, Command: []string{"sh", "-c", script}}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	doubles.KillTmux(t, terminal, spec.Name)
	last := fmt.Sprintf("line%d", lines)
	deadline := time.Now().Add(10 * time.Second)
	for {
		screen, err := terminal.Capture(t.Context(), spec.Name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(screen, last) {
			return terminal, spec.Name
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pane never showed %s: %q", last, screen)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A read of the scrollback answers the newest lines that scrolled off the screen, oldest first, with how
// many lines the history holds and how many tmux keeps (https://man.openbsd.org/tmux.1#capture-pane).
// Five rows show line9 to line12 and the empty line under them, so line1 to line8 are the history.
func TestHistoryReadsTheNewestLinesThatScrolledOffTheScreen(t *testing.T) {
	terminal, name := historyPane(t, 12)
	got, err := terminal.History(t.Context(), name, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := ports.Scrollback{Lines: "line6\nline7\nline8\n", Size: 8, Limit: 2000}
	if got != want {
		t.Errorf("history %+v, want %+v", got, want)
	}
	all, err := terminal.History(t.Context(), name, 200)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(all.Lines, "line1\n") || !strings.HasSuffix(all.Lines, "line8\n") || all.Size != 8 {
		t.Errorf("all of the history %+v", all)
	}
}

// A pane whose lines all fit its screen has no history, and a read of it answers none. tmux would answer
// the first line of the screen for the range a read asks for, so the read checks the history's size.
func TestHistoryOfAPaneWithNoneIsEmpty(t *testing.T) {
	terminal, name := historyPane(t, 2)
	got, err := terminal.History(t.Context(), name, 200)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lines != "" || got.Size != 0 || got.Limit != 2000 {
		t.Errorf("history %+v, want none of the 2000 lines tmux keeps", got)
	}
}
