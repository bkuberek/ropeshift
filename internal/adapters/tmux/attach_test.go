package tmux_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/ports"
)

// answering is a tmux that answers each call from a script, and keeps what it was asked.
type answering struct {
	mu      sync.Mutex
	specs   []ports.ProcessSpec
	stdout  string
	stderr  string
	code    int
	release chan struct{}
}

func (a *answering) Start(_ context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	a.mu.Lock()
	a.specs = append(a.specs, spec)
	a.mu.Unlock()
	if spec.Stdout != nil {
		_, _ = io.WriteString(spec.Stdout, a.stdout)
	}
	if spec.Stderr != nil {
		_, _ = io.WriteString(spec.Stderr, a.stderr)
	}
	return &finished{code: a.code, release: a.release}, nil
}

func (a *answering) last() ports.ProcessSpec {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.specs[len(a.specs)-1]
}

// finished is a client that ends with a code, once released if it is given a gate.
type finished struct {
	code    int
	release chan struct{}
	signals []ports.Signal
}

func (f *finished) PID() int { return 1 }

func (f *finished) Signal(signal ports.Signal) error {
	f.signals = append(f.signals, signal)
	if f.release != nil {
		select {
		case <-f.release:
		default:
			close(f.release)
		}
	}
	return nil
}

func (f *finished) Wait() (ports.ExitStatus, error) {
	if f.release != nil {
		<-f.release
	}
	return ports.ExitStatus{Code: f.code}, nil
}

func answeringTerminal(t *testing.T, runner *answering, env ...string) *tmux.Terminal {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tmux.Binary), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	terminal, err := tmux.New(runner, tmux.Options{Socket: filepath.Join(dir, "tmux.sock"), Env: append([]string{"PATH=" + dir}, env...)})
	if err != nil {
		t.Fatal(err)
	}
	return terminal
}

func TestClientsCountsTheClientsOfOnePane(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   int
	}{
		{"none attached", "", 0},
		{"one attached", "/dev/ttys004\n", 1},
		{"two attached", "/dev/ttys004\n/dev/ttys007\n", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &answering{stdout: tt.output}
			terminal := answeringTerminal(t, runner)
			got, err := terminal.Clients(t.Context(), "lane-g1")
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("%d clients, want %d", got, tt.want)
			}
			args := runner.last().Args
			want := []string{"list-clients", "-t", "=lane-g1:", "-F", "#{client_name}"}
			if !slices.Equal(args[slices.Index(args, "-S")+2:], want) {
				t.Errorf("tmux was asked %v, want %v", args, want)
			}
		})
	}
}

func TestClientsOfAPaneTheServerDoesNotHoldIsNotFound(t *testing.T) {
	runner := &answering{stderr: "can't find session: lane-g1\n", code: 1}
	terminal := answeringTerminal(t, runner)
	if _, err := terminal.Clients(t.Context(), "lane-g1"); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v, want not found", err)
	}
}

// An attach hands tmux the person's own terminal, because the client reads the keyboard and draws on
// the screen itself, and it is the only tmux call that stays in the person's session and group.
func TestAttachRunsTheClientOnThePersonsTerminal(t *testing.T) {
	runner := &answering{}
	terminal := answeringTerminal(t, runner, "TERM=xterm-256color")
	in, out, errs := strings.NewReader(""), new(strings.Builder), new(strings.Builder)
	if err := terminal.Attach(t.Context(), "lane-g1", ports.Console{Stdin: in, Stdout: out, Stderr: errs}); err != nil {
		t.Fatal(err)
	}
	spec := runner.last()
	want := []string{"attach-session", "-t", "=lane-g1:"}
	if got := spec.Args[slices.Index(spec.Args, "-S")+2:]; !slices.Equal(got, want) {
		t.Errorf("tmux was asked %v, want %v", got, want)
	}
	if !slices.Contains(spec.Args, "-u") || spec.Args[slices.Index(spec.Args, "-f")+1] != os.DevNull {
		t.Errorf("the client reads a configuration or the locale: %v", spec.Args)
	}
	if spec.Detached || spec.Outlive {
		t.Errorf("the client left the person's session: detached %v, outlive %v", spec.Detached, spec.Outlive)
	}
	if spec.Stdin != io.Reader(in) || spec.Stdout != io.Writer(out) {
		t.Errorf("the client did not get the person's terminal: %+v", spec)
	}
	if !slices.Contains(spec.Env, "TERM=xterm-256color") {
		t.Errorf("the client has no terminal type: %v", spec.Env)
	}
}

func TestAttachToAPaneTheServerDoesNotHoldIsNotFound(t *testing.T) {
	runner := &answering{stderr: "can't find session: lane-g1\n", code: 1}
	terminal := answeringTerminal(t, runner)
	err := terminal.Attach(t.Context(), "lane-g1", ports.Console{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	if !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v, want not found", err)
	}
}

func TestAttachThatFailsOtherwiseSaysSo(t *testing.T) {
	runner := &answering{stderr: "open terminal failed: not a terminal\n", code: 1}
	terminal := answeringTerminal(t, runner)
	err := terminal.Attach(t.Context(), "lane-g1", ports.Console{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || errors.Is(err, ports.ErrNotFound) {
		t.Errorf("err %v, want a failure that is not a missing pane", err)
	}
}

// A person who cancels the command ends the client, which is a detach and leaves the pane alone.
func TestACanceledAttachEndsTheClient(t *testing.T) {
	release := make(chan struct{})
	runner := &answering{release: release}
	terminal := answeringTerminal(t, runner)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- terminal.Attach(ctx, "lane-g1", ports.Console{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("err %v, want the cancellation", err)
	}
}
