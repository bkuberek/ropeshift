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
	"time"

	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/doubles"
)

const (
	pasteBegin = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// A long text with newlines and multibyte characters, as an instruction handed to a session is.
func longText() string {
	const line = "héllo wörld 日本語 🙂 - a line of an instruction\n"
	return strings.Repeat(line, 64*1024/len(line)+1)
}

// A tmux client's command, with its arguments, has to fit one message to the server, about 16 KiB, so
// a long text typed as one argument fails with "command too long". The pane here asks for bracketed
// paste, as Claude Code's does, so it receives the text as one paste, whole, with its newlines inside.
func TestTypeDeliversATextTooLongForOneTmuxCommandAsOnePaste(t *testing.T) {
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	ctx := t.Context()
	dir := t.TempDir()
	received := filepath.Join(dir, "received.txt")
	// The pane reads raw bytes, so the line discipline neither limits a line nor echoes it, and it
	// says when it is ready so no byte arrives before the mode is set.
	script := `stty raw -echo; printf '\033[?2004h'; echo ready; cat > ` + received
	spec := ports.PaneSpec{Name: "type-g1", Dir: dir, Width: 80, Height: 10, Command: []string{"sh", "-c", script}}
	if _, err := terminal.Open(ctx, spec); err != nil {
		t.Fatal(err)
	}
	doubles.KillTmux(t, terminal, spec.Name)
	waitFor(t, func() bool {
		screen, err := terminal.Capture(ctx, spec.Name)
		return err == nil && strings.Contains(screen, "ready")
	})

	text := longText()
	if err := terminal.Type(ctx, spec.Name, text); err != nil {
		t.Fatal(err)
	}
	// tmux ends a pasted line with a carriage return, which is what Enter sends.
	want := pasteBegin + strings.ReplaceAll(text, "\n", "\r") + pasteEnd
	waitFor(t, func() bool {
		got, err := os.ReadFile(received)
		return err == nil && len(got) >= len(want)
	})
	got, err := os.ReadFile(received)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("the pane received %d bytes that differ from the %d pasted", len(got), len(want))
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("the pane did not get there in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// recording is a tmux that keeps each call and what it was fed, and fails the calls of one command.
type recording struct {
	mu     sync.Mutex
	calls  [][]string
	fed    map[string]string
	failed string
}

func (r *recording) Start(_ context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	args := spec.Args[slices.Index(spec.Args, "-S")+2:]
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	if spec.Stdin != nil {
		in, _ := io.ReadAll(spec.Stdin)
		if r.fed == nil {
			r.fed = map[string]string{}
		}
		r.fed[args[slices.Index(args, "-b")+1]] = string(in)
	}
	code := 0
	if args[0] == r.failed {
		code = 1
		_, _ = io.WriteString(spec.Stderr, "lost the server\n")
	}
	return &finished{code: code}, nil
}

func (r *recording) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var names []string
	for _, call := range r.calls {
		names = append(names, call[0])
	}
	return names
}

func recordingTerminal(t *testing.T, runner *recording) *tmux.Terminal {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, tmux.Binary), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	terminal, err := tmux.New(runner, tmux.Options{Socket: filepath.Join(dir, "tmux.sock"), Env: []string{"PATH=" + dir}})
	if err != nil {
		t.Fatal(err)
	}
	return terminal
}

func TestTypeSendsAShortTextAsKeysInOneCall(t *testing.T) {
	runner := &recording{}
	terminal := recordingTerminal(t, runner)
	if err := terminal.Type(t.Context(), "lane-g1", "/compact"); err != nil {
		t.Fatal(err)
	}
	if got := runner.commands(); !slices.Equal(got, []string{"send-keys"}) {
		t.Errorf("tmux was asked %v", got)
	}
	last := runner.calls[0]
	if want := []string{"send-keys", "-t", "=lane-g1:", "-l", "--", "/compact"}; !slices.Equal(last, want) {
		t.Errorf("tmux was asked %v, want %v", last, want)
	}
}

func TestTypeFeedsALongTextToABufferAndPastesIt(t *testing.T) {
	runner := &recording{}
	terminal := recordingTerminal(t, runner)
	text := longText()
	if err := terminal.Type(t.Context(), "lane-g1", text); err != nil {
		t.Fatal(err)
	}
	if got := runner.commands(); !slices.Equal(got, []string{"load-buffer", "paste-buffer"}) {
		t.Fatalf("tmux was asked %v", got)
	}
	load, paste := runner.calls[0], runner.calls[1]
	buffer := load[slices.Index(load, "-b")+1]
	if runner.fed[buffer] != text {
		t.Errorf("the buffer was fed %d bytes, want the %d typed", len(runner.fed[buffer]), len(text))
	}
	if want := []string{"paste-buffer", "-p", "-d", "-b", buffer, "-t", "=lane-g1:"}; !slices.Equal(paste, want) {
		t.Errorf("tmux was asked %v, want %v", paste, want)
	}
}

func TestTypeLeavesNoBufferWhenTheLoadOrThePasteFails(t *testing.T) {
	for _, failed := range []string{"load-buffer", "paste-buffer"} {
		t.Run(failed, func(t *testing.T) {
			runner := &recording{failed: failed}
			terminal := recordingTerminal(t, runner)
			err := terminal.Type(t.Context(), "lane-g1", longText())
			if err == nil || errors.Is(err, ports.ErrNotFound) {
				t.Fatalf("err %v, want the failure", err)
			}
			commands := runner.commands()
			if commands[len(commands)-1] != "delete-buffer" {
				t.Errorf("tmux was asked %v, want the buffer deleted last", commands)
			}
		})
	}
}
