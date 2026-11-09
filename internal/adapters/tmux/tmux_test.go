package tmux_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/ports"
)

// scriptedTmux answers each `list-panes` with the next answer of its script, and with the last one
// from then on, and keeps every call's tmux arguments.
type scriptedTmux struct {
	mu      sync.Mutex
	answers []string
	calls   [][]string
}

func (s *scriptedTmux) Start(_ context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	args := spec.Args[slices.Index(spec.Args, "-S")+2:]
	s.calls = append(s.calls, args)
	if args[0] == "list-panes" {
		answer := s.answers[0]
		if len(s.answers) > 1 {
			s.answers = s.answers[1:]
		}
		_, _ = io.WriteString(spec.Stdout, answer+"\n")
	}
	return ended{}, nil
}

func (s *scriptedTmux) reaped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.calls, func(args []string) bool { return args[0] == "run-shell" })
}

// ended is a tmux client that has already exited 0.
type ended struct{}

func (ended) PID() int                        { return 1 }
func (ended) Signal(ports.Signal) error       { return nil }
func (ended) Wait() (ports.ExitStatus, error) { return ports.ExitStatus{}, nil }

func scriptedTerminal(t *testing.T, answers ...string) (*tmux.Terminal, *scriptedTmux) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tmux.Binary), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &scriptedTmux{answers: answers}
	terminal, err := tmux.New(runner, tmux.Options{Socket: filepath.Join(dir, "tmux.sock"), Env: []string{"PATH=" + dir}})
	if err != nil {
		t.Fatal(err)
	}
	return terminal, runner
}

// tmux marks a pane dead when its terminal closes, and learns how the process ended only when it
// reaps the process on SIGCHLD. A tmux built with libutempter, as Debian and Ubuntu build it, ignores
// SIGCHLD while it records a pane's start or end, so a process that ends then is not reaped until
// another child of the server ends. A dead pane read before that has an empty status and signal,
// which is no exit at all, and a lane that read it as code 0 would see a crash end cleanly.
func TestADeadPaneReadsHowItsProcessEnded(t *testing.T) {
	tests := []struct {
		name    string
		answers []string
		exit    ports.ExitStatus
		reaps   bool
	}{
		{name: "a code tmux knows at once", answers: []string{"42|1|7|"}, exit: ports.ExitStatus{Code: 7}},
		{name: "a clean end tmux knows at once", answers: []string{"42|1|0|"}, exit: ports.ExitStatus{}},
		{name: "a signal named the C library's way", answers: []string{"42|1||kill"}, exit: ports.ExitStatus{Signal: ports.SignalKill}},
		{
			name: "a code tmux learns once it reaps", answers: []string{"42|1||", "42|1||", "42|1|7|"},
			exit: ports.ExitStatus{Code: 7}, reaps: true,
		},
		{
			name: "a signal tmux learns once it reaps, as a number", answers: []string{"42|1||", "42|1||9"},
			exit: ports.ExitStatus{Signal: ports.SignalKill}, reaps: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terminal, runner := scriptedTerminal(t, tt.answers...)
			pane, err := terminal.Pane(t.Context(), "lane")
			if err != nil {
				t.Fatal(err)
			}
			if !pane.Dead || pane.Exit != tt.exit {
				t.Errorf("pane %+v, want dead with %+v", pane, tt.exit)
			}
			if runner.reaped() != tt.reaps {
				t.Errorf("reaped %v, want %v", runner.reaped(), tt.reaps)
			}
		})
	}
}

// A dead pane whose status tmux never learns is a read that did not finish, never a clean end.
func TestADeadPaneWithNoStatusIsNoAnswer(t *testing.T) {
	terminal, _ := scriptedTerminal(t, "42|1||")
	pane, err := terminal.Pane(t.Context(), "lane")
	if err == nil {
		t.Errorf("a dead pane with no status read as %+v", pane)
	}
}
