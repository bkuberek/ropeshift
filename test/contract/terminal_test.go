package contract

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/osexec"
	"ropeshift.com/ropeshift/internal/adapters/tmux"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
	"ropeshift.com/ropeshift/test/doubles"
)

func TestTmuxTerminalContract(t *testing.T) {
	doubles.RequireTmux(t)
	RunTerminal(t, func(t *testing.T) TerminalRig {
		terminal := doubles.PrivateTmux(t)
		terminalPath := doubles.InstallFakeClaude(t, "pane-terminal")
		exitPath := doubles.InstallFakeClaude(t, "pane-exit")
		spec := func(path string) func(string) ports.PaneSpec {
			return func(name string) ports.PaneSpec {
				return ports.PaneSpec{
					Name: name, Dir: t.TempDir(), Width: 130, Height: 45,
					Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"},
				}
			}
		}
		return TerminalRig{
			Terminal: terminal, Spec: spec(terminalPath), Exits: spec(exitPath), Attaches: attachesOnATerminal(terminal),
			Waits: func(t *testing.T, name, text string) {
				t.Helper()
				deadline := time.Now().Add(20 * time.Second)
				last := ""
				for time.Now().Before(deadline) {
					screen, err := terminal.Capture(context.Background(), name)
					if err == nil && strings.Contains(screen, text) {
						return
					}
					last = screen
					time.Sleep(5 * time.Millisecond)
				}
				t.Fatalf("%q never showed: %q", text, last)
			},
			// Each rig installs its own double, which holds one pane, so its log is that pane's.
			Took: func(t *testing.T, _ string, n int) {
				t.Helper()
				log := filepath.Join(doubles.BinDir(terminalPath), "keys.log")
				deadline := time.Now().Add(20 * time.Second)
				taken := 0
				for time.Now().Before(deadline) {
					taken = trustKeysTaken(log)
					if taken >= n {
						return
					}
					time.Sleep(time.Millisecond)
				}
				t.Fatalf("the pane took %d keys, want %d", taken, n)
			},
		}
	})
}

// trustKeysTaken counts the keys a double's trust dialog has read and drawn, from its log.
func trustKeysTaken(log string) int {
	raw, err := os.ReadFile(log)
	if err != nil {
		return 0
	}
	taken := 0
	for line := range strings.Lines(string(raw)) {
		// A line is the process ID, the dialog, and the key.
		if fields := strings.Fields(line); len(fields) == 3 && fields[1] == "trust" {
			taken++
		}
	}
	return taken
}

// attachesOnATerminal attaches a real tmux client on a pseudo-terminal, as a person's shell would, and
// detaches it with tmux's own binding.
func attachesOnATerminal(terminal *tmux.Terminal) func(*testing.T, string) func() {
	return func(t *testing.T, name string) func() {
		t.Helper()
		person := doubles.NewPTY(t)
		done := make(chan error, 1)
		go func() {
			done <- terminal.Attach(context.Background(), name, ports.Console{Stdin: person.Terminal, Stdout: person.Terminal, Stderr: person.Terminal})
		}()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if count, err := terminal.Clients(context.Background(), name); err == nil && count == 1 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return func() {
			t.Helper()
			select {
			case err := <-done:
				// The pane's end took the client with it, which is how it ended.
				_ = err
				return
			default:
			}
			person.Type(t, "\x02d")
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("the client ended badly after the detach keys: %v", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatalf("the client did not end after the detach keys: %q", person.Drawn())
			}
		}
	}
}

// A person who attaches to a pane the server does not hold, such as one a park just closed, learns
// that and not a failure of their terminal.
func TestTmuxAttachToAMissingPaneIsNotFound(t *testing.T) {
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	path := doubles.InstallFakeClaude(t, "pane-terminal")
	spec := ports.PaneSpec{Name: "here-g1", Dir: t.TempDir(), Width: 130, Height: 45, Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"}}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	person := doubles.NewPTY(t)
	console := ports.Console{Stdin: person.Terminal, Stdout: person.Terminal, Stderr: person.Terminal}
	if err := terminal.Attach(t.Context(), "here-g0", console); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("attach to a pane of an older generation: %v, want not found", err)
	}
}

// A worker's server must not read a person's configuration or leave their server alone
// by luck, so this reads the one socket the adapter was given.
func TestTmuxStaysOnItsOwnSocket(t *testing.T) {
	doubles.RequireTmux(t)
	dir := apitest.ShortDir(t)
	socket := filepath.Join(dir, "tmux.sock")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	terminal, err := tmux.New(osexec.Runner{}, tmux.Options{Socket: socket, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	path := doubles.InstallFakeClaude(t, "pane-terminal")
	doubles.KillTmux(t, terminal, "pane")
	spec := ports.PaneSpec{
		Name: "pane", Dir: dir, Width: 130, Height: 45, Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"},
	}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Errorf("no server on the given socket: %v", err)
	}
}

// recording starts every process on the real runner and keeps each spec it started.
type recording struct {
	mu    sync.Mutex
	specs []ports.ProcessSpec
}

func (r *recording) Start(ctx context.Context, spec ports.ProcessSpec) (ports.Process, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	r.mu.Unlock()
	return osexec.Runner{}.Start(ctx, spec)
}

func (r *recording) started() []ports.ProcessSpec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.specs)
}

// The call that opens a pane can start the private server, which holds every pane, so it must outlive
// a stop of the worker's service (D20). A call that only reads or sends to the server ends at once.
func TestTheTmuxServerOutlivesTheWorkersService(t *testing.T) {
	doubles.RequireTmux(t)
	dir := apitest.ShortDir(t)
	runner := &recording{}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir}
	terminal, err := tmux.New(runner, tmux.Options{Socket: filepath.Join(dir, "tmux.sock"), Env: env})
	if err != nil {
		t.Fatal(err)
	}
	path := doubles.InstallFakeClaude(t, "pane-terminal")
	// The server ends before the fake CLI's directory goes, so it comes after the directory is made.
	doubles.EndTmuxOn(t, filepath.Join(dir, "tmux.sock"))
	spec := ports.PaneSpec{Name: "pane", Dir: dir, Width: 130, Height: 45, Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"}}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Capture(t.Context(), "pane"); err != nil {
		t.Fatal(err)
	}
	if err := terminal.Close(t.Context(), "pane"); err != nil {
		t.Fatal(err)
	}
	started := runner.started()
	if len(started) != 3 {
		t.Fatalf("%d calls", len(started))
	}
	for i, call := range started {
		opens := slices.Contains(call.Args, "new-session")
		if opens != (i == 0) || call.Outlive != opens || !call.Detached {
			t.Errorf("call %d %v: outlive %v, detached %v", i, call.Args, call.Outlive, call.Detached)
		}
	}
}

func TestTmuxNameMatchesExactly(t *testing.T) {
	doubles.RequireTmux(t)
	terminal := doubles.PrivateTmux(t)
	path := doubles.InstallFakeClaude(t, "pane-terminal")
	spec := ports.PaneSpec{
		Name: "lane-10", Dir: t.TempDir(), Width: 130, Height: 45, Command: []string{filepath.Join(doubles.BinDir(path), "claude"), "--tui"},
	}
	if _, err := terminal.Open(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Pane(t.Context(), "lane-1"); err == nil {
		t.Error("a prefix of a name read another lane's pane")
	}
}

func TestFakeTerminalContract(t *testing.T) {
	RunTerminal(t, func(t *testing.T) TerminalRig {
		terminal := fake.NewTerminal()
		var opened atomic.Int64
		exits := map[string]bool{}
		cancel := "   \n ❯ No, exit\n   Yes, I trust this folder\n"
		accept := "   \n   No, exit\n ❯ Yes, I trust this folder\n"
		terminal.Play(fake.PaneScript{Screen: cancel, Moves: map[string]map[string]string{cancel: {"Down": accept}}})
		return TerminalRig{
			Terminal: terminal,
			Attaches: func(t *testing.T, name string) func() {
				t.Helper()
				done := make(chan error, 1)
				go func() { done <- terminal.Attach(context.Background(), name, ports.Console{}) }()
				for count := 0; count != 1; {
					count, _ = terminal.Clients(context.Background(), name)
					time.Sleep(time.Millisecond)
				}
				return func() {
					terminal.Detach(name)
					if err := <-done; err != nil {
						t.Errorf("the attach ended with %v", err)
					}
				}
			},
			Spec: func(name string) ports.PaneSpec {
				opened.Add(1)
				return ports.PaneSpec{Name: name, Command: []string{"claude", strconv.FormatInt(opened.Load(), 10)}}
			},
			Exits: func(name string) ports.PaneSpec {
				exits[name] = true
				go func() {
					for range 1000 {
						if _, err := terminal.Pane(context.Background(), name); err == nil {
							terminal.Exit(name, ports.ExitStatus{Code: 7})
							return
						}
						time.Sleep(time.Millisecond)
					}
				}()
				return ports.PaneSpec{Name: name, Command: []string{"claude", "exits"}}
			},
			Waits: func(t *testing.T, name, text string) {
				t.Helper()
				screen, err := terminal.Capture(context.Background(), name)
				if err != nil || !strings.Contains(screen, text) {
					t.Fatalf("%q is not on the screen: %q, %v", text, screen, err)
				}
			},
			Took: func(*testing.T, string, int) {},
		}
	})
}
