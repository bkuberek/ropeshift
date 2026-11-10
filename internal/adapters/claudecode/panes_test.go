package claudecode_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/claudecode"
	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
)

func TestAPaneIsNamedByItsLaneAndGeneration(t *testing.T) {
	tests := []struct {
		lane       string
		generation int64
		want       string
	}{
		{"lane-1", 1, "lane-1-g1"},
		{"lane-1", 12, "lane-1-g12"},
		// tmux reads a dot and a colon in a session name as separators.
		{"a.b:c", 2, "a_b_c-g2"},
	}
	for _, tt := range tests {
		if got := claudecode.PaneName(tt.lane, tt.generation); got != tt.want {
			t.Errorf("PaneName(%q, %d) = %q, want %q", tt.lane, tt.generation, got, tt.want)
		}
	}
}

func openPane(t *testing.T, terminal *fake.Terminal, lane string, generation int64) string {
	t.Helper()
	name := claudecode.PaneName(lane, generation)
	if _, err := terminal.Open(t.Context(), ports.PaneSpec{Name: name, Command: []string{"claude", "x"}}); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestPanesHaveOnlyALivePaneOfTheGeneration(t *testing.T) {
	terminal := fake.NewTerminal()
	panes := claudecode.Panes{Terminal: terminal}
	openPane(t, terminal, "lane-1", 2)
	dead := openPane(t, terminal, "lane-1", 3)
	terminal.Exit(dead, ports.ExitStatus{Code: 1})
	for _, tt := range []struct {
		generation int64
		want       bool
	}{{1, false}, {2, true}, {3, false}, {4, false}} {
		got, err := panes.Has(t.Context(), "lane-1", tt.generation)
		if err != nil || got != tt.want {
			t.Errorf("generation %d: %v, %v; want %v", tt.generation, got, err, tt.want)
		}
	}
	if got, err := panes.Has(t.Context(), "lane-2", 2); err != nil || got {
		t.Errorf("another lane's pane: %v, %v", got, err)
	}
}

func TestPanesHasReportsAReadThatDidNotFinish(t *testing.T) {
	terminal := fake.NewTerminal()
	openPane(t, terminal, "lane-1", 1)
	broken := errors.New("tmux did not answer")
	terminal.FailReads(broken)
	if _, err := (claudecode.Panes{Terminal: terminal}).Has(t.Context(), "lane-1", 1); !errors.Is(err, broken) {
		t.Errorf("err %v, want the read's own", err)
	}
}

func TestPanesAttachToTheGenerationsPane(t *testing.T) {
	terminal := fake.NewTerminal()
	openPane(t, terminal, "lane-1", 1)
	openPane(t, terminal, "lane-1", 2)
	panes := claudecode.Panes{Terminal: terminal}
	done := make(chan error, 1)
	go func() { done <- panes.Attach(context.Background(), "lane-1", 2) }()
	for len(terminal.Attached()) == 0 {
		time.Sleep(time.Millisecond)
	}
	terminal.Detach("lane-1-g2")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := terminal.Attached(); !slices.Equal(got, []string{"lane-1-g2"}) {
		t.Errorf("attached to %v, want only the generation asked for", got)
	}
}
